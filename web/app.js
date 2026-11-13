import { FilesView } from "./files-view.js";
import { DesktopView } from "./desktop-view.js";
import { ApprovalsView } from "./approvals-view.js";
import { StreamReconnect, canReconnect } from "./stream-reconnect.js";
import { FocusControl } from "./focus-control.js";
import { fittedDimensions, widgetHeight } from "./terminal-layout.js";
import { installTerminalClipboard, checkedPaste } from "./terminal-clipboard.js";
import { viewTool, viewNavigation } from "./view-navigation.js";
import { ContextSync } from "./context.js";
import { sendToChat } from "./chat-share.js";
import { App } from "@modelcontextprotocol/ext-apps";
import { Terminal, FitAddon, Ghostty } from "ghostty-web";
import wasmBytes from "ghostty-web/ghostty-vt.wasm";
const byId = (id) => document.getElementById(id);
if(window.parent!==window)document.documentElement.style.setProperty('--app-max-height','520px');
const status = byId('status');
const notice = byId('notice');
const control = byId('control');
const reconnect = byId('reconnect');
const share = byId('share');
let app, connection, terminal, fit, socket, owned = false, connected = false;
let appTerminal, generation = 0, rendererReady, createRequestId;
let managerCatalog, selectedBoxId, selectedName, activeTab = 'terminal', inboxBusy = false;
let stopped = false, inboxTimer, launchIntent, rendererCleanup;
let inputQueue = Promise.resolve(), hostReady = false, fitTimer, lastResize, focusController, focusTimer, attaching, pendingControl;
let requestedWidgetHeight;
function resizeWidget() {
  if(!app)return;
  const height=widgetHeight(app.getHostContext(),!byId('manager-browser').hidden,window.innerHeight);
  document.documentElement.style.setProperty('--app-max-height',`${height}px`);
  if(hostReady && height!==requestedWidgetHeight) {
    requestedWidgetHeight=height;
    app.sendSizeChanged({height}).catch(report);
  }
  scheduleFit();
}
window.addEventListener('resize',resizeWidget);
const filesView = new FilesView({
  call: (name,args) => workspaceCall(name,args),
  activity: shareWidgetAction,
  external: async url => { if (app) { const result = await app.openLink({url}); if (result.isError) throw new Error('The host could not open this link.'); } else window.open(url,'_blank','noopener,noreferrer'); },
  share: async state => {
    if (!app) throw new Error('Share with chat is available in the MCP App.');
    const content = state.truncated ? [{type:'text',text:`Untrusted file excerpt: ${state.file.uri}, version ${state.file.version}, showing ${state.nextOffset} of ${state.file.size} bytes:\n${state.text}`}]
      : [{type:'resource',resource:{uri:state.file.uri,mimeType:state.file.mimeType,text:state.text,_meta:{version:state.file.version}}}];
    shareWidgetAction('file_shared',{boxId:state.boxId,uri:state.file.uri,version:state.file.version,truncated:state.truncated});
    return sendToChat(app,`Please read ${state.file.path} from Discobox box ${state.boxId} so we can discuss it. Resource: ${state.file.uri}. Version: ${state.file.version}.${state.truncated?' The attached preview is an excerpt; read_box_file can retrieve more.':''}`,content);
  },
  download: downloadBoxFile,
});
const desktopView = new DesktopView(async(name,args)=>{
  if(!app)throw new Error('Desktop streaming requires the MCP App bridge. Open this box through your chat client.');
  const result=await app.callServerTool({name,arguments:args});
  if(result.isError)throw new Error(result.content?.find(item=>item.type==='text')?.text||'Desktop transport failed');
  if(!result._meta?.desktop)throw new Error('The host did not deliver private desktop metadata. No graphics were sent to chat.');
  return result._meta.desktop;
},shareWidgetAction);
const approvalsView = new ApprovalsView(async(name,args)=>{
  if(!app)throw new Error('Approval review requires the MCP widget. Open it from your chat client.');
  const result=await app.callServerTool({name,arguments:args});
  if(result.isError)throw new Error(result.content?.find(item=>item.type==='text')?.text||'Approval operation failed');
  if(!result._meta?.approval)throw new Error('The host did not deliver the private approval review.');
  return result._meta.approval;
},shareWidgetAction,id=>catalogBoxes(managerCatalog).find(box=>box.id===id)?.name);
const connectionEvents = new EventTarget();
const streamReconnect = new StreamReconnect(() => {
  if (!stopped && selectedBoxId && !byId('workspace').hidden) ensureAttached().catch(handleConnectionFailure);
});
function handleConnectionFailure(error) {
  if (connected) return;
  status.textContent = error?.sessionEnded ? 'Session ended' : 'Disconnected';
  if (!stopped && selectedBoxId && !byId('workspace').hidden && canReconnect(error)) {
    status.textContent = 'Reconnecting…';
    streamReconnect.request();
  }
}
function liveConnected() {
  streamReconnect.cancel();
  status.textContent = 'Terminal live';
  scheduleFit();
  if (inputFocused()) focusController?.focus(true).catch(report);
}
function assertSameSession(session) {
  if (session?.box !== connection.session.box || session?.exec !== connection.session.exec) {
    const error = new Error('This box’s terminal session changed. Reopen the box to connect to its new session. No input was replayed.');
    error.sessionChanged = true; throw error;
  }
}
const contextSync = new ContextSync(
  params => app.updateModelContext(params, { timeout: 2500 }),
  message => { byId('context-status').textContent = message; }
);
function shareWidgetAction(action, details = {}) {
  if (!app || !hostReady || stopped) return;
  contextSync.update({ activeBoxId:selectedBoxId || null, activeView:byId('manager-browser').hidden ? activeTab : 'catalog', humanHasTerminalControl:owned, humanHasDesktopControl:desktopView.hasControl },action,details);
}
function catalogBoxes(data) { return data?.boxes || data?.managers || []; }


function report(error) {
  notice.textContent = error instanceof Error ? error.message : String(error);
  notice.hidden = false;
}

function setControl(value) {
  owned = value;
  byId('browse').disabled = false;
  shareWidgetAction('terminal_control_changed',{ boxId:selectedBoxId || null,owned:value });
  control.textContent = owned ? 'Your input · assistant paused' : 'Assistant can write';
  control.setAttribute('aria-pressed', String(owned));
  updateTerminalSize();
  byId('hint').textContent = activeTab === 'files' ? 'Read a file, share it with chat, or return to the live terminal.' : owned
    ? 'You control input while focused here. Leaving this input yields control to the assistant.'
    : 'Click the terminal to type; leaving it lets the assistant write.';
}

function send(message) {
  if (app) {
    const id = appTerminal;
    if (!id) { if (['input','claim'].includes(message.type)) throw new Error('Terminal disconnected; input was not sent.'); return; }
    const action = message.type === 'input'
      ? { action: 'input', data: encodeBytes(new TextEncoder().encode(message.data)) }
      : { action: message.type, cols: message.cols, rows: message.rows };
    // Serialize terminal actions, including resize/claim/release. A failed
    // input stops the queue; reconnecting never replays it.
    const operation = inputQueue.then(async () => {
      if (appTerminal !== id) { if (['input','claim'].includes(message.type)) throw new Error('Terminal changed; input was not sent.'); return; }
      const result=await appCall('terminal_action', { terminalId: id, ...action });
      if(message.type==='resize' && appTerminal===id) {
        if(result.resized) { terminal.resize(message.cols,message.rows); updateTerminalSize(); }
        else { lastResize=undefined; byId('hint').textContent='Another view controls the terminal; its dimensions are preserved.'; }
      }
      if (message.type === 'claim' || message.type === 'release') {
        setControl(message.type === 'claim');
        control.disabled = false;
        if (owned) fitTerminal();
      }
    });
    inputQueue = operation.catch((error) => { if (appTerminal === id) disconnectApp(error); });
    return operation;
  }
  if (socket?.readyState !== WebSocket.OPEN) {
    if (['input','claim'].includes(message.type)) throw new Error('Terminal is disconnected; input was not sent.');
    return;
  }
  if (message.type === 'claim' || message.type === 'release') {
    return new Promise((resolve,reject) => { pendingControl = {resolve,reject}; socket.send(JSON.stringify(message)); });
  }
  socket.send(JSON.stringify(message));
}

function updateTerminalSize() {
  if (!terminal) return;
  byId('terminal-size').textContent = `${terminal.cols} × ${terminal.rows}`;
  byId('terminal-size').title=`${terminal.cols} columns × ${terminal.rows} rows`;
  byId('fit-terminal').disabled = !connected;
  byId('fit-terminal').title = 'Fit this session to the visible pane';
}
function fitTerminal() {
  clearTimeout(fitTimer);
  if (!terminal) return;
  const viewport = byId('viewport');
  const visible = !document.hidden && activeTab === 'terminal' && !viewport.hidden && !byId('workspace').hidden;
  const dimensions = fittedDimensions({ connected,visible,width:viewport.clientWidth,height:viewport.clientHeight,proposal:visible ? fit.proposeDimensions() : undefined });
  if (!dimensions) return;
  const { cols, rows } = dimensions, key = `${cols}:${rows}`;
  if (key === lastResize) return;
  lastResize = key;
  send({ type:'resize',cols,rows })?.catch(report);
}
function scheduleFit() {
  clearTimeout(fitTimer);
  fitTimer = setTimeout(fitTerminal,100);
}

// No automatic reconnect or input replay: a failed write may already have run.
function attach() {
  if (app) { attachApp().catch(disconnectApp); return; }
  if (!connection || socket && socket.readyState < WebSocket.CLOSING) return;
  notice.hidden = true;
  reconnect.disabled = true;
  control.disabled = true;
  status.textContent = 'Attaching / waking…';
  lastResize = undefined; terminal.reset();
  const url = new URL('/terminal', connection.origin);
  url.searchParams.set('managerId', connection.session.box);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  socket = new WebSocket(url);
  socket.binaryType = 'arraybuffer';
  socket.onopen = () => send({ type: 'auth', token: connection.token });
  let socketFailure;
  socket.onmessage = ({ data }) => {
    if (data instanceof ArrayBuffer) { terminal.write(new Uint8Array(data)); return; }
    try {
      const message = JSON.parse(data);
      switch (message.type) {
        case 'resized':
          if(message.resized){terminal.resize(message.cols,message.rows);updateTerminalSize()}
          else {lastResize=undefined;byId('hint').textContent='Another view controls the terminal; its dimensions are preserved.'}
          break;
        case 'connected':
          connected = true;
          assertSameSession(message.session);
          if (message.cols && message.rows) terminal.resize(message.cols, message.rows);
          control.disabled = false;
          status.textContent = 'Terminal live';
          setControl(false); liveConnected(); connectionEvents.dispatchEvent(new Event('connected'));
          break;
        case 'control':
          pendingControl?.resolve(); pendingControl = undefined;
          setControl(message.owned);
          control.disabled = false;
          if (owned) fitTerminal();
          break;
        case 'error':
          socketFailure = new Error(message.message);
          pendingControl?.reject(new Error(message.message)); pendingControl = undefined;
          control.disabled = !connected;
          report(message.message);
          break;
        case 'exit':
          streamReconnect.cancel();
          report('The session ended. Reopening the box never creates a replacement automatically.');
          socket.onclose = null; socket.close(); connected = false; setControl(false); status.textContent = 'Session ended';
          break;
      }
    } catch (error) { report(error); if (error.sessionChanged) { socketFailure=error; socket.close(); } }
  };
  socket.onerror = () => report('Terminal connection failed. Check the server, the host’s local-network policy, and the configured UI origin.');
  socket.onclose = () => {
    if (focusController) focusController.held = false;
    pendingControl?.reject(new Error('Terminal disconnected')); pendingControl = undefined;
    connectionEvents.dispatchEvent(new CustomEvent('disconnected',{detail:socketFailure || new Error('Terminal disconnected')}));
    connected = false;
    setControl(false);
    control.disabled = true;
    reconnect.disabled = false;
    status.textContent = 'Disconnected';
    byId('hint').textContent = 'Restoring live output. Input is never replayed automatically.';
    handleConnectionFailure(socketFailure || new Error('Terminal disconnected'));
  };
}

function encodeBytes(bytes) {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}
async function appCall(name, args) {
  const result = await app.callServerTool({ name, arguments: args });
  if (result.isError) throw new Error(result.content?.find((item) => item.type === 'text')?.text || `${name} failed`);
  return result.structuredContent || JSON.parse(result.content.find((item) => item.type === 'text').text);
}
function closeApp() {
  clearTimeout(fitTimer); lastResize = undefined;
  generation++;
  const id = appTerminal;
  appTerminal = undefined;
  if (id) appCall('terminal_action', { terminalId: id, action: 'close' }).catch(() => {});
}
function disconnectApp(error) {
  if (focusController) focusController.held = false;
  closeApp();
  connected = false;
  setControl(false);
  control.disabled = true;
  reconnect.disabled = false;
  status.textContent = 'Disconnected';
  report(error);
  handleConnectionFailure(error);
}
async function attachApp() {
  if (appTerminal) return;
  const current = ++generation;
  try {
    notice.hidden = true;
    reconnect.disabled = true;
    control.disabled = true;
    status.textContent = 'Attaching / waking…';
    const opened = await appCall('terminal_open', { managerId: connection.session.box });
    if (current !== generation) {
      await appCall('terminal_action', { terminalId: opened.terminalId, action: 'close' });
      return;
    }
    try { assertSameSession(opened.session); }
    catch (error) { await appCall('terminal_action',{terminalId:opened.terminalId,action:'close'}); throw error; }
    appTerminal = opened.terminalId;
    inputQueue = Promise.resolve();
    terminal.reset();
    if (opened.cols && opened.rows) terminal.resize(opened.cols, opened.rows);
    connected = true;
    setControl(false);
    control.disabled = false;
    liveConnected();
    (async () => {
    let cursor = opened.cursor;
    while (current === generation) {
      const output = await appCall('terminal_read', { terminalId: opened.terminalId, after: cursor });
      if (current !== generation) return;
      for (const chunk of output.chunks) terminal.write(Uint8Array.from(atob(chunk), (character) => character.charCodeAt(0)));
      cursor = output.cursor;
      if (output.closed) { const error = new Error('The terminal stream ended. Reopen the box if its session is still available.'); error.sessionEnded = !!output.ended; throw error; }
    }
    })().catch(error => { if (current === generation) disconnectApp(error); });
  } catch (error) {
    if (current === generation) disconnectApp(error);
    throw error;
  }
}

async function initializeRenderer() {
  const { instance } = await WebAssembly.instantiate(wasmBytes, { env: { log() {} } });
  const ghostty = new Ghostty(instance);
  const styles = getComputedStyle(byId('viewport'));
  const color = (name) => styles.getPropertyValue(name).trim();
  terminal = new Terminal({
    ghostty, cols: 100, rows: 30, fontSize: 14, scrollback: 5000,
    fontFamily: color('--font-mono'),
    theme: { background: color('--ink-sunken'), foreground: color('--text'), cursor: color('--gold'), selectionBackground: color('--selection') },
  });
  const terminalElement=byId('terminal'),wasInert=terminalElement.inert;
  // Ghostty focuses on open and again on the next task. Suppress both while
  // mounting; selecting a view must not take keyboard focus from chat.
  terminalElement.inert=true;
  try{terminal.open(terminalElement);await new Promise(resolve=>setTimeout(resolve,0))}
  finally{terminalElement.inert=wasInert}
  fit = new FitAddon();
  terminal.loadAddon(fit);
  terminal.onData((data) => {
    const controller = focusController, id = selectedBoxId;
    if (!controller || !inputFocused()) return;
    controller.run(async () => { if (id !== selectedBoxId) throw new Error('Box changed; input was not sent.'); await send({ type:'input',data }); }).catch(report);
  });
  terminal.onResize(updateTerminalSize);
  const observer = new ResizeObserver(scheduleFit);
  observer.observe(byId('viewport'));
  const clipboardCleanup=installTerminalClipboard(byId('terminal'),terminal,report);
  rendererCleanup = () => { clipboardCleanup();observer.disconnect(); terminal.dispose(); };
}

function detach() {
  desktopView.close();
  filesView.setActive(false);
  streamReconnect.cancel();
  focusController?.focus(false).catch(()=>{}); clearTimeout(focusTimer);
  clearTimeout(fitTimer); lastResize = undefined;
  closeApp();
  if (socket) { const previous = socket; socket = undefined; previous.onclose = previous.onmessage = previous.onerror = previous.onopen = null; previous.close(); }
  connected = false;
  setControl(false);
}
async function start(value) {
  if(value?.view==='approvals') {
    if(byId('workspace').hidden&&byId('manager-browser').hidden)await refreshManagers();
    await approvalsView.open(value.requestId,value.summary?.status||value.status||'pending');return;
  }
  if (value?.view === 'desktop') { await openFileWorkspace(value); return; }
  if (value?.view === 'files') { await openFileWorkspace(value); return; }
  if (value?.view === 'attention') {
    if (byId('workspace').hidden && byId('manager-browser').hidden) await refreshManagers();
    byId('inbox').hidden = false;
    await refreshInbox(); return;
  }
  if (value?.view === 'managers' || value?.view === 'boxes' || value?.boxes || value?.managers) { showManagers(value); return; }
  if (!value?.session?.box) throw new Error('Choose a manager to open its terminal.');
  if (owned && connection?.session?.box !== value.session.box) throw new Error('Release control before switching managers.');
  if (selectedBoxId === value.session.box && !byId('workspace').hidden) { connection = {...connection,...value}; await selectWorkspaceTab('terminal'); return; }
  detach();
  connection = { ...connection, ...value };
  byId('session').textContent = `${value.session.project} / ${value.session.box} / ${value.session.exec}`;
  byId('manager-browser').hidden = true;
  byId('workspace').hidden = false;
  resizeWidget();
  selectedBoxId = value.session.box;
  configureFocusControl(selectedBoxId);
  const box = catalogBoxes(managerCatalog).find(item => item.id === selectedBoxId);
  selectedName = box?.name || selectedBoxId;
  byId('box-title').textContent = selectedName;
  byId('box-state').textContent = box ? describeBox(box) : 'Inspecting the last reported state';
  byId('box-task').textContent = box?.task || (box?.role === 'manager' ? 'Coordinator: give this manager a task to begin.' : 'Work and output stay in this box.');
  await selectWorkspaceTab('terminal');
  shareWidgetAction('box_opened',{ boxId:selectedBoxId,role:box?.role || 'unknown' });
}

async function workspaceCall(name, args = {}) {
  if (app) return appCall(name, args);
  const fileAction = {list_box_files:'list',read_box_file:'read',file_read:'read',file_stat:'stat'}[name];
  if (fileAction) {
    const response = await fetch(`/api/files/${fileAction}`,{method:'POST',headers:{Authorization:`Bearer ${connection.token}`,'Content-Type':'application/json'},body:JSON.stringify(args)});
    const result = await response.json(); if(!response.ok) throw new Error(result.error || 'File request failed'); return result;
  }
  const action = { list_managers: 'list', list_boxes: 'boxes', launch_box: 'launch', list_attention: 'attention', inspect_box: 'inspect', send_box:'message', send_manager: 'message', open_box:'connect', connect_manager: 'connect', create_manager: 'create', archive_box:'archive', archive_manager: 'archive' }[name];
  if (!action) throw new Error('Unknown workspace operation');
  const response = await fetch(`/api/managers/${action}`, { method: 'POST', headers: { Authorization: `Bearer ${connection.token}`, 'Content-Type': 'application/json' }, body: JSON.stringify(args) });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || 'Manager operation failed');
  return result;
}
async function refreshManagers() {
  const data = await workspaceCall('list_boxes');
  showManagers(data);
}
function showManagers(data) {
  if (owned) { report('Release control before browsing managers.'); return; }
  detach();
  managerCatalog = data;
  notice.hidden = true;
  byId('manager-browser').hidden = false;
  byId('workspace').hidden = true;
  resizeWidget();
  selectedBoxId = undefined;
  document.querySelector('.toolbar').hidden = true;
  byId('viewport').hidden = true;
  status.textContent = 'Boxes';
  shareWidgetAction('catalog_opened',{ boxCount:catalogBoxes(data).length, boxes:catalogBoxes(data).slice(0,50).map(box => ({ boxId:box.id,role:box.role,archived:box.archived })) });
  byId('hint').textContent = `Project ${data.project || connection?.session?.project || 'default'} · Closing a view leaves its boxes in Discobox. Opening a box connects live and may wake it.`;
  const list = byId('manager-list'); list.replaceChildren();
  const visible = catalogBoxes(data).filter((manager) => !manager.archived || byId('show-archived').checked);
  if (!visible.length) { const empty = document.createElement('p'); empty.textContent = 'No active boxes yet. Start a task above, or show archived boxes.'; list.append(empty); }
  for (const manager of visible) {
    const card = document.createElement('article'); card.className = 'manager-card';
    const details = document.createElement('div');
    const name = document.createElement('strong'); name.textContent = manager.name;
    if (manager.role === 'manager') { const badge = document.createElement('span'); badge.className = 'role-badge'; badge.textContent = 'Manager'; name.append(badge); }
    const id = document.createElement('code'); id.textContent = manager.id;
    const state = document.createElement('span'); state.className = 'manager-state';
    state.textContent = describeBox(manager) + (manager.accessible ? '' : ' · Requires adoption approval');
    if (['blocked','error','done'].includes(manager.program?.state)) card.classList.add('needs-attention');
    details.append(name, id, state);
    const actions = document.createElement('div'); actions.className = 'actions';
    const open = document.createElement('button'); open.textContent = 'Open workspace'; open.disabled = !manager.accessible || manager.archived;
    open.onclick = async () => { open.disabled = true; try { await yieldInput(); await start({ ...await workspaceCall('open_box', { boxId: manager.id }), view: 'workspace' }); } catch (error) { report(error); open.disabled = false; } };
    actions.append(open);
    if (manager.accessible && !manager.archived) {
      const archive = document.createElement('button'); archive.textContent = 'Archive';
      archive.onclick = async () => {
        if (archive.dataset.confirm !== 'yes') { archive.dataset.confirm = 'yes'; archive.textContent = 'Confirm archive'; return; }
        archive.disabled = true;
        try { await workspaceCall('archive_box', { boxId: manager.id }); shareWidgetAction('archive_requested',{ boxId:manager.id }); await refreshManagers(); } catch (error) { report(error); archive.disabled = false; }
      };
      actions.append(archive);
    }
    card.append(details, actions); list.append(card);
  }
}
byId('browse').onclick = () => yieldInput().then(refreshManagers).catch(report);
byId('show-archived').onchange = () => { if (managerCatalog) showManagers(managerCatalog); };
byId('refresh-managers').onclick = () => refreshManagers().catch(report);
byId('create-button').onclick = async (event) => {
  event.preventDefault();
  const button = byId('create-button'), name = byId('manager-name'), task = byId('task');
  if (!name.checkValidity() || !task.value.trim() || new TextEncoder().encode(task.value).length > 65536) { report('Add a task of at most 65536 bytes and use at most 64 letters, digits, underscores or hyphens for the optional name.'); return; }
  createRequestId ||= 'ui-' + Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, '0')).join('');
  launchIntent ||= { requestId: createRequestId, name: name.value.trim(), task: task.value, harness: byId('harness').value.trim(), model: byId('model').value.trim(), pool: byId('pool').value.trim() };
  // Freeze the exact intent until its outcome is known. An error does not imply
  // that creation failed, and changing settings must never create another box.
  for (const id of ['manager-name','task','harness','model','pool']) byId(id).disabled = true;
  byId('creation-intent').hidden = false;
  byId('creation-intent').textContent = `Creation reference: ${createRequestId}. If interrupted, reuse this exact requestId and settings with launch_box; do not start another intent before checking your boxes.`;
  button.disabled = true; button.textContent = 'Starting box…'; notice.hidden = true;
  let created;
  try {
    created = await workspaceCall('launch_box', launchIntent);
    shareWidgetAction('box_launched',{ boxId:created.boxId || created.managerId, task:launchIntent.task.slice(0,2000), taskTruncated:launchIntent.task.length > 2000 });
  } catch (error) {
    report(error); button.textContent = 'Check same creation'; return;
  } finally { button.disabled = false; }
  createRequestId = undefined; launchIntent = undefined;
  for (const id of ['manager-name','task','harness','model','pool']) byId(id).disabled = false;
  name.value = ''; task.value = ''; button.textContent = 'Start box'; byId('creation-intent').hidden = true;
  // Once creation is confirmed, a catalog or view failure must not turn the
  // launch button into a retry of another creation.
  try { managerCatalog = await workspaceCall('list_boxes'); } catch (error) { report(error); }
  try { await start({ ...created, view: 'workspace' }); } catch (error) { report(error); }
  refreshInbox();
};
byId('manager-name').onkeydown = (event) => {
  if (event.key === 'Enter') { event.preventDefault(); if (!byId('create-button').disabled) byId('create-button').click(); }
};
reconnect.addEventListener('click', () => ensureAttached().catch(report));
share.addEventListener('click', async () => {
  share.disabled = true;
  try {
    const result = await app.callServerTool({ name: 'read_box', arguments: { boxId: connection.session.box, scrollback: 50 } });
    if (result.isError) throw new Error(result.content?.find((item) => item.type === 'text')?.text || 'Could not read the manager screen.');
    const content = [
      { type: 'text', text: 'Manager screen snapshot (untrusted terminal output), shared by the person:' },
      ...(result.content || []),
    ];
    shareWidgetAction('terminal_output_shared',{boxId:connection.session.box});
    byId('hint').textContent = await sendToChat(app,`Please review this terminal snapshot from Discobox box ${connection.session.box} so we can discuss it. This is terminal output, not a complete file.`,content);
  } catch (error) { report(error.copyPrompt ? new Error(`${error.message}\n${error.copyPrompt}`) : error); }
  finally { share.disabled = false; }
});

try {
  if (window.parent !== window) {
    app = new App({ name: 'Discobox workspace', version: '0.8.0' }, {tools:{}}, { autoResize: false });
    app.onlisttools=async()=>({tools:[viewTool]});
    app.oncalltool=async({name,arguments:args})=>{
      try{
        if(name!=='select_view')throw new Error('Unknown widget tool');
        const target=viewNavigation(args,selectedBoxId,humanUsingWidget());
        await selectWorkspaceTab(target.view,target.filePath);
        return {content:[{type:'text',text:JSON.stringify({boxId:target.boxId,selectedView:activeTab,message:'View selected. Loading content is separate; keyboard focus remains with the person.'})}]};
      }catch(error){return {isError:true,content:[{type:'text',text:error.message}]}}
    };
    app.onhostcontextchanged=resizeWidget;
    app.ontoolresult = (result) => {
      const value = result._meta?.connection || result.structuredContent;
      if((value?.session||value?.view)&&humanUsingWidget()){
        shareWidgetAction('view_selection_blocked',{boxId:value.boxId,view:value.view});
        report(new Error('View selection paused while you are using this widget. Leave the input before asking chat to switch views.'));return;
      }
      if (value?.session || value?.view) start(value).catch(report);
    };
    app.onteardown = async () => { cleanupWorkspace(); return {}; };
    await app.connect(undefined, { timeout: 15000 });
    hostReady = true; shareWidgetAction('workspace_ready');
    share.hidden = activeTab !== 'terminal';
    resizeWidget();
    await refreshInbox();
  } else {
    const token = new URLSearchParams(location.hash.slice(1)).get('token');
    history.replaceState(null, '', location.pathname);
    if (!token) throw new Error('Open the private terminal link printed by the MCP server.');
    const response = await fetch('/api/session', { headers: { Authorization: `Bearer ${token}` } });
    if (!response.ok) throw new Error('This terminal link has expired. Use the current link from the server.');
    connection = await response.json();
    if (connection.session?.box) await start({ ...connection, view:'workspace' }); else await refreshManagers();
    await refreshInbox();
  }
} catch (error) { status.textContent = 'Unavailable'; report(error); }

function describeBox(box) {
  const labels = { blocked: 'Needs you', working: 'Working', done: 'Ready for review', idle: 'Waiting for a task', error: 'Agent error' };
  const parts = [box.state, box.harness || 'Project harness'];
  if (box.program?.state) parts.push(labels[box.program.state] || box.program.state);
  if (box.program?.msg) parts.push(box.program.msg);
  if (box.observedAt) parts.push(`Last report ${new Date(box.observedAt).toLocaleString()}`);

  return parts.join(' · ');
}
async function refreshInbox() {
  if (inboxBusy || stopped) return;
  clearTimeout(inboxTimer); inboxBusy = true;
  try {
    const { inbox } = await workspaceCall('list_attention');
    if (inbox.boxes) {
      managerCatalog = { ...managerCatalog,boxes:inbox.boxes };
      if (selectedBoxId) {
        const current = inbox.boxes.find(box => box.id === selectedBoxId);
        if (current) {
          selectedName = current.name;
          byId('box-title').textContent = selectedName;
                  byId('box-state').textContent = describeBox(current);
        }
      }
    }
    byId('attention-count').textContent = inbox.items.length;
    byId('inbox-coverage').textContent = `${inbox.coverage}. Updated ${new Date().toLocaleTimeString()}. ${inbox.warning || ''}`;
    byId('inbox-items').replaceChildren();
    if (!inbox.items.length) { const p = document.createElement('p'); p.textContent = 'No attention items within this connection’s coverage.'; byId('inbox-items').append(p); }
    for (const item of inbox.items) {
      const card = document.createElement('article'); card.className = 'attention-card';
      const title = document.createElement('h3'); title.textContent = item.title;
      const box = document.createElement('p'); box.textContent = `${item.boxName} · ${item.boxId}`;
      const reason = document.createElement('p'); reason.textContent = item.reason || 'Open the box or console to inspect the request.';
      card.append(title,box,reason);
      if (item.ttlSeconds) { const duration = document.createElement('p'); duration.textContent = `Requested duration: ${item.ttlSeconds} seconds`; card.append(duration); }
      if (item.scope && Object.keys(item.scope).length) { const scope = document.createElement('pre'); scope.textContent = JSON.stringify(item.scope,null,2); card.append(scope); }
      if (item.observedAt || item.createdAt) { const when = document.createElement('p'); when.className = 'muted'; when.textContent = `Last report: ${new Date(item.observedAt || item.createdAt).toLocaleString()}`; card.append(when); }
      if(item.kind==='credential') {
        const review=document.createElement('button');review.textContent='Review request';review.onclick=()=>approvalsView.open(item.id).catch(report);card.append(review);
      }else if (item.action === 'console') { const text = document.createElement('p'); text.textContent = `Open Discobox console → approval inbox. Request ${item.id}. This widget uses the MCP server’s API identity.`; card.append(text); }
      const open = document.createElement('button'); open.textContent = 'Inspect box';
      open.onclick = async () => {
        await yieldInput();
        open.disabled = true;
        try { const value = await workspaceCall('open_box',{ boxId:item.boxId }); byId('inbox').hidden = true; await start({ ...value,view:'workspace' }); } catch (error) { report(error); } finally { open.disabled = false; }
      };
      card.append(open); byId('inbox-items').append(card);
    }
  } catch (error) {
    byId('attention-count').textContent = '?';
    byId('inbox-coverage').textContent = `Inbox refresh failed: ${error.message}. Existing items may be stale. Check the Discobox console.`;
  } finally {
    inboxBusy = false;
    if (!stopped) inboxTimer = setTimeout(refreshInbox,15000);
  }
}
byId('fit-terminal').onclick = fitTerminal;
byId('inbox-button').onclick = () => { byId('inbox').hidden = !byId('inbox').hidden; if (!byId('inbox').hidden) refreshInbox(); };
byId('close-inbox').onclick = () => { byId('inbox').hidden = true; approvalsView.close(); };
byId('refresh-inbox').onclick = () => approvalsView.active?approvalsView.refresh():refreshInbox();
byId('approval-history').onclick=()=>approvalsView.open(undefined,'all').catch(report);
function cleanupWorkspace() {
  if (stopped) return;
  stopped = true; clearTimeout(inboxTimer); clearTimeout(fitTimer); streamReconnect.cancel();
  socket?.close(); closeApp(); filesView.close(); desktopView.close(); approvalsView.close(); rendererCleanup?.();
}
window.addEventListener('pagehide',cleanupWorkspace,{ once:true });
byId('detach-terminal').onclick = () => {
  detach(); control.disabled = true; reconnect.disabled = false;
  status.textContent = 'Detached';
  byId('hint').textContent = 'Terminal detached. The box remains in Discobox and can sleep under its normal idle policy.';
};

function inputFocused() {
  const element = document.activeElement;
  return document.hasFocus() && !byId('workspace').hidden &&
    activeTab === 'terminal' && byId('terminal').contains(element);
}
function configureFocusControl(id) {
  focusController = new FocusControl(async () => {
    if (id !== selectedBoxId || stopped) throw new Error('Box changed; no input sent.');
    await ensureAttached();
    if (id !== selectedBoxId || !connected || stopped) throw new Error('Terminal unavailable; no input sent.');
    await send({type:'claim'});
  },async () => {
    if (id === selectedBoxId && connected && owned) await send({type:'release'});
  });
}
async function yieldInput() { clearTimeout(focusTimer); await focusController?.focus(false); }
async function ensureAttached() {
  if (connected) return;
  if (attaching) return attaching;
  const id = selectedBoxId;
  attaching = (async () => {
    rendererReady ||= initializeRenderer(); await rendererReady;
    if (id !== selectedBoxId || stopped) throw new Error('Box changed; terminal was not attached.');
    if (app) { await attachApp(); if (!connected) throw new Error('Terminal did not connect.'); return; }
    await new Promise((resolve,reject) => {
      const done = error => { clearTimeout(timer); connectionEvents.removeEventListener('connected',ready); connectionEvents.removeEventListener('disconnected',failed); error ? reject(error) : resolve(); };
      const ready = () => done(), failed = event => done(event.detail || new Error('Terminal connection failed.'));
      const timer = setTimeout(() => done(new Error('Terminal connection timed out.')),50000);
      connectionEvents.addEventListener('connected',ready); connectionEvents.addEventListener('disconnected',failed); attach();
    });
  })();
  try { await attaching; } finally { attaching = undefined; }
}
function syncInputFocus() {
  clearTimeout(focusTimer);
  focusTimer = setTimeout(() => { focusController?.focus(inputFocused()).catch(report); },100);
}
document.addEventListener('focusin',syncInputFocus);
document.addEventListener('focusout',syncInputFocus);
window.addEventListener('blur',() => { yieldInput().catch(report); });
window.addEventListener('focus',syncInputFocus);
document.addEventListener('visibilitychange',() => { if (document.hidden) yieldInput().catch(report); else syncInputFocus(); });

async function selectWorkspaceTab(tab,filePath) {
  const id = selectedBoxId;
  if (!id) return;
  await yieldInput();
  if(tab!=='desktop')await desktopView.close();
  activeTab = tab;
  byId('files-pane').hidden = tab !== 'files';
  byId('desktop-pane').hidden = tab !== 'desktop';
  byId('viewport').hidden = tab !== 'terminal';
  document.querySelector('.toolbar').hidden = tab !== 'terminal';
  byId('task-details').hidden = tab !== 'terminal';
  share.hidden = !app || tab !== 'terminal';
  byId('terminal-tab').setAttribute('aria-selected',String(tab === 'terminal'));
  byId('files-tab').setAttribute('aria-selected',String(tab === 'files'));
  byId('desktop-tab').setAttribute('aria-selected',String(tab === 'desktop'));
  filesView.setActive(tab === 'files');
  if(tab==='desktop') {
    byId('hint').textContent='Desktop graphics stay in this widget, outside model context. Leaving this tab disconnects the desktop.';
    status.textContent='Desktop';
    await desktopView.open(id);
  } else if (tab === 'files') {
    byId('hint').textContent='Read a file, share it with chat, or return to the live terminal.';
    if (!connected) status.textContent = 'Files';
    await filesView.open(id,filePath);
  } else {
    if (!connection.session?.exec) {
      const value = await workspaceCall('open_box',{boxId:id});
      if (id !== selectedBoxId || activeTab !== 'terminal') return;
      connection={...connection,...value};
      byId('session').textContent = `${connection.session.project} / ${id} / ${connection.session.exec}`;
    }
    if (id !== selectedBoxId || activeTab !== 'terminal') return;
    await ensureAttached(); fitTerminal();
  }
  if(id===selectedBoxId&&activeTab===tab)shareWidgetAction('view_selected',{boxId:id,view:tab});
}
async function openFileWorkspace(value) {
  const desktop=value.view==='desktop';
  if (!value.boxId || (!desktop&&!value.file?.path&&!value.directory)) throw new Error('View needs a box ID and file or directory path.');
  await yieldInput();
  if (selectedBoxId !== value.boxId) {
    detach();
    connection={...connection,session:{project:value.project,box:value.boxId,exec:''}};
    selectedBoxId=value.boxId;configureFocusControl(selectedBoxId);
  }
  byId('manager-browser').hidden=true;byId('workspace').hidden=false;
  resizeWidget();
  const box=catalogBoxes(managerCatalog).find(box=>box.id===selectedBoxId);
  selectedName=box?.name||selectedBoxId;
  byId('box-title').textContent=selectedName;byId('box-state').textContent=box?describeBox(box):(desktop?'Desktop view':'File view');
  byId('session').textContent=`${value.project} / ${selectedBoxId}`;
  byId('box-task').textContent=box?.task||'Work and files stay in this box.';
  await selectWorkspaceTab(desktop?'desktop':'files',value.file?.path);
}
async function downloadBoxFile(file) {
  if (app) { const result=await app.downloadFile({contents:[{type:'resource_link',uri:file.uri,name:file.name,mimeType:file.mimeType,size:file.size}]}); if(result.isError)throw new Error('Download cancelled or unavailable in this host.');return; }
  if(file.size>1048576)throw new Error('Browser download limit is 1 MiB; use the file API for larger files.');
  const boxId=selectedBoxId,parts=[];let offset=0,version;
  do {const data=await workspaceCall('read_box_file',{boxId,path:file.path,offset,length:262144});if(version&&version!==data.file.version)throw new Error('File changed during download; try again.');version=data.file.version;parts.push(typeof data.text==='string'?new TextEncoder().encode(data.text):Uint8Array.from(atob(data.blob),c=>c.charCodeAt(0)));offset=data.nextOffset;if(!data.truncated)break;}while(offset<1048576);
  const url=URL.createObjectURL(new Blob(parts,{type:file.mimeType}));const link=document.createElement('a');link.href=url;link.download=file.name;link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
}
byId('files-tab').onclick=()=>selectWorkspaceTab('files').catch(report);
byId('terminal-tab').onclick=()=>selectWorkspaceTab('terminal').catch(report);
byId('desktop-tab').onclick=()=>selectWorkspaceTab('desktop').catch(report);

let pasteTarget;
function currentPasteTarget(){return {boxId:selectedBoxId,exec:connection?.session?.exec}}
async function pasteIntoTerminal(text,target){
  checkedPaste(text);
  if(stopped||activeTab!=='terminal'||target.boxId!==selectedBoxId||target.exec!==connection?.session?.exec)throw Error('The terminal changed; clipboard text was not sent.');
  await ensureAttached();
  if(target.boxId!==selectedBoxId||target.exec!==connection?.session?.exec||activeTab!=='terminal')throw Error('The terminal changed; clipboard text was not sent.');
  terminal.focus();terminal.paste(text);
}
byId('paste-terminal').onclick=async()=>{
  const target=currentPasteTarget();let text;
  try{text=await navigator.clipboard.readText()}
  catch{
    pasteTarget=target;byId('paste-text').value='';byId('paste-notice').hidden=true;byId('paste-dialog').showModal();byId('paste-text').focus();return;
  }
  try{await pasteIntoTerminal(text,target)}catch(error){report(error)}
};
byId('paste-confirm').onclick=async()=>{
  const text=byId('paste-text').value,target=pasteTarget;
  if(!target)return;
  try{checkedPaste(text);byId('paste-dialog').close();await pasteIntoTerminal(text,target)}catch(error){if(byId('paste-dialog').open){byId('paste-notice').textContent=error.message;byId('paste-notice').hidden=false}else report(error)}
};
byId('paste-cancel').onclick=()=>byId('paste-dialog').close();
byId('paste-dialog').onclose=()=>{byId('paste-text').value='';pasteTarget=undefined};
function humanUsingWidget(){
  const element=document.activeElement;
  return owned||desktopView.hasControl||byId('paste-dialog').open||
    (document.hasFocus()&&(inputFocused()||element?.matches('input,textarea,select,[contenteditable="true"]')));
}
