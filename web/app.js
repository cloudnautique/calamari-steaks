import { StreamReconnect, canReconnect } from "./stream-reconnect.js";
import { FocusControl } from "./focus-control.js";
import { fittedDimensions } from "./terminal-layout.js";
import { ContextSync } from "./context.js";
import { App } from "@modelcontextprotocol/ext-apps";
import { Terminal, FitAddon, Ghostty } from "ghostty-web";
import wasmBytes from "ghostty-web/ghostty-vt.wasm";
const byId = (id) => document.getElementById(id);
const status = byId('status');
const notice = byId('notice');
const control = byId('control');
const reconnect = byId('reconnect');
const share = byId('share');
let app, connection, terminal, fit, socket, owned = false, connected = false;
let appTerminal, generation = 0, rendererReady, pendingMessage, createRequestId;
let managerCatalog, selectedBoxId, selectedName, activeTab = 'terminal', inboxBusy = false;
let stopped = false, inboxTimer, launchIntent, rendererCleanup;
const receipts = new Map(), drafts = new Map(), uncertainBoxes = new Set(), pendingBoxes = new Set();
let inputQueue = Promise.resolve(), hostReady = false, fitTimer, lastResize, focusController, focusTimer, attaching, pendingControl;
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
  contextSync.update({ activeBoxId:selectedBoxId || null, activeView:byId('manager-browser').hidden ? activeTab : 'catalog', humanHasTerminalControl:owned },action,details);
}
function catalogBoxes(data) { return data?.boxes || data?.managers || []; }


function report(error) {
  notice.textContent = error instanceof Error ? error.message : String(error);
  notice.hidden = false;
}

function setControl(value) {
  owned = value;
  byId('message').disabled = pendingBoxes.has(selectedBoxId);
  byId('send-message').disabled = uncertainBoxes.has(selectedBoxId) || pendingBoxes.has(selectedBoxId);
  byId('browse').disabled = false;
  shareWidgetAction('terminal_control_changed',{ boxId:selectedBoxId || null,owned:value });
  byId('message').placeholder = 'Give this box an instruction…';
  control.textContent = owned ? 'Your input · assistant paused' : 'Assistant can write';
  control.setAttribute('aria-pressed', String(owned));
  updateTerminalSize();
  byId('hint').textContent = owned
    ? 'You control input while focused here. Leaving the composer or terminal yields to the assistant.'
    : 'Focus the composer or terminal to type; leaving it lets the assistant write.';
}

function send(message) {
  if (app) {
    const id = appTerminal;
    if (!id) { if (['input','message','claim'].includes(message.type)) throw new Error('Terminal disconnected; input was not sent.'); return; }
    const action = message.type === 'message' ? { action: 'message', text: message.text } : message.type === 'input'
      ? { action: 'input', data: encodeBytes(new TextEncoder().encode(message.data)) }
      : { action: message.type, cols: message.cols, rows: message.rows };
    // Serialize terminal actions, including resize/claim/release. A failed
    // input stops the queue; reconnecting never replays it.
    const operation = inputQueue.then(async () => {
      if (appTerminal !== id) { if (['input','message','claim'].includes(message.type)) throw new Error('Terminal changed; input was not sent.'); return; }
      await appCall('terminal_action', { terminalId: id, ...action });
      if (message.type === 'claim' || message.type === 'release') {
        setControl(message.type === 'claim');
        control.disabled = false;
        if (owned) fitOwned();
      }
    });
    inputQueue = operation.catch((error) => { if (appTerminal === id) disconnectApp(error); });
    return operation;
  }
  if (socket?.readyState !== WebSocket.OPEN) {
    if (['input','message','claim'].includes(message.type)) throw new Error('Terminal is disconnected; input was not sent.');
    return;
  }
  if (message.type === 'claim' || message.type === 'release') {
    return new Promise((resolve,reject) => { pendingControl = {resolve,reject}; socket.send(JSON.stringify(message)); });
  }
  if (message.type === 'message') {
    return new Promise((resolve, reject) => {
      pendingMessage = { resolve, reject };
      socket.send(JSON.stringify(message));
    });
  }
  socket.send(JSON.stringify(message));
}

function updateTerminalSize() {
  if (!terminal) return;
  byId('terminal-size').textContent = `${terminal.cols} columns × ${terminal.rows} rows`;
  byId('fit-terminal').disabled = !owned || !connected;
  byId('fit-terminal').title = owned ? 'Fit this session to the visible pane' : 'Focus the terminal to fit it to this pane';
}
function fitOwned() {
  clearTimeout(fitTimer);
  if (!terminal) return;
  const viewport = byId('viewport');
  const visible = activeTab === 'terminal' && !viewport.hidden && !byId('workspace').hidden;
  const dimensions = fittedDimensions({ owned,connected,visible,width:viewport.clientWidth,height:viewport.clientHeight,proposal:visible ? fit.proposeDimensions() : undefined });
  if (!dimensions) return;
  const { cols, rows } = dimensions, key = `${cols}:${rows}`;
  if (key === lastResize) return;
  lastResize = key;
  terminal.resize(cols,rows);
  updateTerminalSize();
  send({ type:'resize',cols,rows });
}
function scheduleFit() {
  clearTimeout(fitTimer);
  fitTimer = setTimeout(fitOwned,100);
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
          if (owned) fitOwned();
          break;
        case 'sent':
          pendingMessage?.resolve(); pendingMessage = undefined;
          break;
        case 'error':
          socketFailure = new Error(message.message);
          pendingControl?.reject(new Error(message.message)); pendingControl = undefined;
          pendingMessage?.reject(new Error(message.message)); pendingMessage = undefined;
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
    pendingMessage?.reject(new Error('Connection lost; message delivery is uncertain. Read the manager before resending.')); pendingMessage = undefined;
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
  const composerFocused = document.activeElement === byId('message');
  terminal.open(byId('terminal'));
  // Ghostty schedules a second focus on the next task; opening a viewer must
  // not steal keyboard ownership from the assistant.
  await new Promise(resolve => setTimeout(resolve,0));
  if (composerFocused) byId('message').focus(); else terminal.blur();
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
  rendererCleanup = () => { observer.disconnect(); terminal.dispose(); };
}

function detach() {
  streamReconnect.cancel();
  focusController?.focus(false).catch(()=>{}); clearTimeout(focusTimer);
  clearTimeout(fitTimer); lastResize = undefined;
  closeApp();
  if (socket) { const previous = socket; socket = undefined; previous.onclose = previous.onmessage = previous.onerror = previous.onopen = null; previous.close(); }
  connected = false;
  setControl(false);
}
async function start(value) {
  if (value?.view === 'attention') {
    if (byId('workspace').hidden && byId('manager-browser').hidden) await refreshManagers();
    byId('inbox').hidden = false;
    await refreshInbox(); return;
  }
  if (value?.view === 'managers' || value?.view === 'boxes' || value?.boxes || value?.managers) { showManagers(value); return; }
  if (!value?.session?.box) throw new Error('Choose a manager to open its terminal.');
  if (owned && connection?.session?.box !== value.session.box) throw new Error('Release control before switching managers.');
  if (selectedBoxId === value.session.box && !byId('workspace').hidden) return;
  detach();
  connection = { ...connection, ...value };
  byId('session').textContent = `${value.session.project} / ${value.session.box} / ${value.session.exec}`;
  byId('manager-browser').hidden = true;
  byId('workspace').hidden = false;
  byId('message-form').hidden = false;
  selectedBoxId = value.session.box;
  configureFocusControl(selectedBoxId);
  const box = catalogBoxes(managerCatalog).find(item => item.id === selectedBoxId);
  selectedName = box?.name || selectedBoxId;
  byId('box-title').textContent = selectedName;
  byId('box-state').textContent = box ? describeBox(box) : 'Inspecting the last reported state';
  byId('box-task').textContent = box?.task || (box?.role === 'manager' ? 'Coordinator: give this manager a task to begin.' : 'Work and output stay in this box.');
  byId('message-target').textContent = `Send instruction to ${selectedName} · ${selectedBoxId}`;
  renderReceipts();
  document.querySelector('.toolbar').hidden = false;
  byId('viewport').hidden = false;
  await ensureAttached();
  shareWidgetAction('box_opened',{ boxId:selectedBoxId,role:box?.role || 'unknown' });
}

async function workspaceCall(name, args = {}) {
  if (app) return appCall(name, args);
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
  selectedBoxId = undefined;
  document.querySelector('.toolbar').hidden = true;
  byId('viewport').hidden = true;
  byId('message-form').hidden = true;
  status.textContent = 'Boxes';
  shareWidgetAction('catalog_opened',{ boxes:catalogBoxes(data).map(box => ({ boxId:box.id,role:box.role,archived:box.archived })) });
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
byId('send-message').onclick = async (event) => {
  event.preventDefault();
  const input = byId('message'), button = byId('send-message');
  const id = selectedBoxId, text = input.value;
  if (!id || !text.trim() || uncertainBoxes.has(id) || pendingBoxes.has(id)) return;
  if (new TextEncoder().encode(text).length > 65536) { report('Instructions must be at most 65536 bytes.'); return; }
  drafts.set(id,text); pendingBoxes.add(id);
  input.disabled = true; button.disabled = true;
  try {
    await focusController.run(async () => { if (id !== selectedBoxId) throw new Error('Box changed; instruction was not sent.'); await send({ type:'message',text }); });
    shareWidgetAction('instruction_delivered',{ boxId:id,text:text.slice(0,2000),textTruncated:text.length > 2000 });
    const history = receipts.get(id) || []; history.push(text); receipts.set(id,history.slice(-20));
    drafts.delete(id);
    if (id === selectedBoxId) { input.value = ''; renderReceipts(); byId('hint').textContent = 'Instruction delivered. Its response appears in the live terminal.'; }
  } catch (error) {
    report(error); uncertainBoxes.add(id);
    shareWidgetAction('instruction_delivery_uncertain',{ boxId:id });
    if (id === selectedBoxId) { byId('uncertain-delivery').hidden = false; byId('hint').textContent = 'Check the live output before resending; input is never replayed automatically.'; }
  }
  finally { pendingBoxes.delete(id); input.disabled = pendingBoxes.has(selectedBoxId); button.disabled = uncertainBoxes.has(selectedBoxId) || pendingBoxes.has(selectedBoxId); }
};
byId('manager-name').onkeydown = (event) => {
  if (event.key === 'Enter') { event.preventDefault(); if (!byId('create-button').disabled) byId('create-button').click(); }
};
byId('message').oninput = () => { if (selectedBoxId) drafts.set(selectedBoxId,byId('message').value); };
byId('message').onkeydown = (event) => {
  if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); byId('send-message').click(); }
};

reconnect.addEventListener('click', () => ensureAttached().catch(report));
share.addEventListener('click', async () => {
  share.disabled = true;
  try {
    const result = await app.callServerTool({ name: 'read_box', arguments: { boxId: connection.session.box, scrollback: 50 } });
    if (result.isError) throw new Error(result.content?.find((item) => item.type === 'text')?.text || 'Could not read the manager screen.');
    await app.updateModelContext({ content: [
      { type: 'text', text: 'Manager screen snapshot (untrusted terminal output), shared by the person:' },
      ...(result.content || []),
    ] });
    byId('hint').textContent = 'Output shared as context for your next chat message.';
  } catch (error) { report(error); }
  finally { share.disabled = false; }
});

try {
  if (window.parent !== window) {
    app = new App({ name: 'Discobox workspace', version: '0.3.0' }, {}, { autoResize: false });
    app.ontoolresult = (result) => {
      const value = result._meta?.connection || result.structuredContent;
      if (value?.session || value?.view) start(value).catch(report);
    };
    app.onteardown = async () => { cleanupWorkspace(); return {}; };
    await app.connect(undefined, { timeout: 15000 });
    hostReady = true; shareWidgetAction('workspace_ready');
    share.hidden = false;
    await app.sendSizeChanged({ height: 720 });
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
function renderReceipts() {
  byId('uncertain-delivery').hidden = !uncertainBoxes.has(selectedBoxId);
  byId('message').value = drafts.get(selectedBoxId) || '';
  byId('message').disabled = pendingBoxes.has(selectedBoxId);
  byId('message-receipts').replaceChildren();
  for (const text of receipts.get(selectedBoxId) || []) {
    const entry = document.createElement('p'); entry.className = 'receipt'; entry.textContent = 'Sent instruction · '+text; byId('message-receipts').append(entry);
  }
  byId('send-message').disabled = uncertainBoxes.has(selectedBoxId) || pendingBoxes.has(selectedBoxId);
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
          byId('message-target').textContent = `Send instruction to ${selectedName} · ${selectedBoxId}`;
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
      if (item.action === 'console') { const text = document.createElement('p'); text.textContent = `Open Discobox console → approval inbox. Request ${item.id}. This widget cannot approve using your human identity.`; card.append(text); }
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
byId('fit-terminal').onclick = fitOwned;
byId('review-delivery').onclick = () => { uncertainBoxes.delete(selectedBoxId); byId('uncertain-delivery').hidden = true; byId('send-message').disabled = pendingBoxes.has(selectedBoxId); };
byId('inbox-button').onclick = () => { byId('inbox').hidden = !byId('inbox').hidden; if (!byId('inbox').hidden) refreshInbox(); };
byId('close-inbox').onclick = () => { byId('inbox').hidden = true; };
byId('refresh-inbox').onclick = refreshInbox;
function cleanupWorkspace() {
  if (stopped) return;
  stopped = true; clearTimeout(inboxTimer); clearTimeout(fitTimer); streamReconnect.cancel();
  socket?.close(); closeApp(); rendererCleanup?.();
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
    (byId('message-form').contains(element) || (activeTab === 'terminal' && (byId('terminal').contains(element) || element === byId('fit-terminal'))));
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
