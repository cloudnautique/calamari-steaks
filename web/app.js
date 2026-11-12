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
let managerCatalog;
let inputQueue = Promise.resolve();

function report(error) {
  notice.textContent = error instanceof Error ? error.message : String(error);
  notice.hidden = false;
}

function setControl(value) {
  owned = value;
  byId('message').disabled = !value;
  byId('send-message').disabled = !value;
  byId('browse').disabled = value;
  byId('message').placeholder = value ? 'Message this manager…' : 'Take control to message this manager';
  control.textContent = owned ? 'Release control' : 'Take control';
  control.setAttribute('aria-pressed', String(owned));
  byId('hint').textContent = owned
    ? 'You are typing. Release control to let chat drive the manager.'
    : 'Observing. Chat can drive the manager; take control to type here.';
}

function send(message) {
  if (app) {
    const id = appTerminal;
    if (!id) return;
    const action = message.type === 'message' ? { action: 'message', text: message.text } : message.type === 'input'
      ? { action: 'input', data: encodeBytes(new TextEncoder().encode(message.data)) }
      : { action: message.type, cols: message.cols, rows: message.rows };
    // Serialize terminal actions, including resize/claim/release. A failed
    // input stops the queue; reconnecting never replays it.
    const operation = inputQueue.then(async () => {
      if (appTerminal !== id) return;
      await appCall('terminal_action', { terminalId: id, ...action });
      if (message.type === 'claim' || message.type === 'release') {
        setControl(message.type === 'claim');
        control.disabled = false;
        if (owned) { fitOwned(); terminal.focus(); }
      }
    });
    inputQueue = operation.catch((error) => { if (appTerminal === id) disconnectApp(error); });
    return operation;
  }
  if (socket?.readyState !== WebSocket.OPEN) {
    if (message.type === 'message') throw new Error('Terminal is disconnected; message was not sent.');
    return;
  }
  if (message.type === 'message') {
    return new Promise((resolve, reject) => {
      pendingMessage = { resolve, reject };
      socket.send(JSON.stringify(message));
    });
  }
  socket.send(JSON.stringify(message));
}

function fitOwned() {
  if (!owned || !terminal) return;
  fit.fit();
  const cols = Math.max(2, Math.min(500, terminal.cols));
  const rows = Math.max(2, Math.min(200, terminal.rows));
  terminal.resize(cols, rows);
  send({ type: 'resize', cols, rows });
}

// No automatic reconnect or input replay: a failed write may already have run.
function attach() {
  if (app) { attachApp().catch(disconnectApp); return; }
  if (!connection || socket && socket.readyState < WebSocket.CLOSING) return;
  notice.hidden = true;
  reconnect.disabled = true;
  control.disabled = true;
  status.textContent = 'Connecting…';
  terminal.reset();
  const url = new URL('/terminal', connection.origin);
  url.searchParams.set('managerId', connection.session.box);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  socket = new WebSocket(url);
  socket.binaryType = 'arraybuffer';
  socket.onopen = () => send({ type: 'auth', token: connection.token });
  socket.onmessage = ({ data }) => {
    if (data instanceof ArrayBuffer) { terminal.write(new Uint8Array(data)); return; }
    try {
      const message = JSON.parse(data);
      switch (message.type) {
        case 'connected':
          connected = true;
          if (message.cols && message.rows) terminal.resize(message.cols, message.rows);
          control.disabled = false;
          status.textContent = 'Live';
          setControl(false);
          break;
        case 'control':
          setControl(message.owned);
          control.disabled = false;
          if (owned) { fitOwned(); terminal.focus(); }
          break;
        case 'sent':
          pendingMessage?.resolve(); pendingMessage = undefined;
          break;
        case 'error':
          pendingMessage?.reject(new Error(message.message)); pendingMessage = undefined;
          control.disabled = !connected;
          report(message.message);
          break;
        case 'exit':
          report('The manager session exited. Reconnecting does not start a replacement.');
          break;
      }
    } catch (error) { report(error); }
  };
  socket.onerror = () => report('Terminal connection failed. Check the server, the host’s local-network policy, and the configured UI origin.');
  socket.onclose = () => {
    pendingMessage?.reject(new Error('Connection lost; message delivery is uncertain. Read the manager before resending.')); pendingMessage = undefined;
    connected = false;
    setControl(false);
    control.disabled = true;
    reconnect.disabled = false;
    status.textContent = 'Disconnected';
    byId('hint').textContent = 'Detached. Reconnect to inspect the session before sending more input.';
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
  generation++;
  const id = appTerminal;
  appTerminal = undefined;
  if (id) appCall('terminal_action', { terminalId: id, action: 'close' }).catch(() => {});
}
function disconnectApp(error) {
  closeApp();
  connected = false;
  setControl(false);
  control.disabled = true;
  reconnect.disabled = false;
  status.textContent = 'Disconnected';
  report(error);
}
async function attachApp() {
  if (appTerminal) return;
  const current = ++generation;
  try {
    notice.hidden = true;
    reconnect.disabled = true;
    control.disabled = true;
    status.textContent = 'Connecting…';
    const opened = await appCall('terminal_open', { managerId: connection.session.box });
    if (current !== generation) {
      await appCall('terminal_action', { terminalId: opened.terminalId, action: 'close' });
      return;
    }
    appTerminal = opened.terminalId;
    inputQueue = Promise.resolve();
    terminal.reset();
    if (opened.cols && opened.rows) terminal.resize(opened.cols, opened.rows);
    connected = true;
    setControl(false);
    control.disabled = false;
    status.textContent = 'Live';
    let cursor = opened.cursor;
    while (current === generation) {
      const output = await appCall('terminal_read', { terminalId: opened.terminalId, after: cursor });
      if (current !== generation) return;
      for (const chunk of output.chunks) terminal.write(Uint8Array.from(atob(chunk), (character) => character.charCodeAt(0)));
      cursor = output.cursor;
      if (output.closed) throw new Error('The terminal detached. Reconnect to inspect the same manager.');
    }
  } catch (error) {
    if (current === generation) disconnectApp(error);
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
  terminal.open(byId('terminal'));
  fit = new FitAddon();
  terminal.loadAddon(fit);
  terminal.onData((data) => { if (owned) send({ type: 'input', data }); });
  const observer = new ResizeObserver(fitOwned);
  observer.observe(byId('viewport'));
  const cleanup = () => { observer.disconnect(); socket?.close(); closeApp(); terminal.dispose(); };
  window.addEventListener('pagehide', cleanup, { once: true });
  if (app) app.onteardown = async () => { cleanup(); return {}; };
}

function detach() {
  closeApp();
  if (socket) { const previous = socket; socket = undefined; previous.onclose = previous.onmessage = previous.onerror = previous.onopen = null; previous.close(); }
  connected = false;
  setControl(false);
}
async function start(value) {
  if (value?.view === 'managers' || value?.managers) { showManagers(value); return; }
  if (!value?.session?.box) throw new Error('Choose a manager to open its terminal.');
  if (owned && connection?.session?.box !== value.session.box) throw new Error('Release control before switching managers.');
  if (connected && connection?.session?.box === value.session.box) return;
  detach();
  connection = { ...connection, ...value };
  byId('session').textContent = `${value.session.project} / ${value.session.box} / ${value.session.exec}`;
  byId('manager-browser').hidden = true;
  document.querySelector('.toolbar').hidden = false;
  byId('viewport').hidden = false;
  byId('message-form').hidden = false;
  rendererReady ||= initializeRenderer();
  await rendererReady;
  attach();
}

async function workspaceCall(name, args = {}) {
  if (app) return appCall(name, args);
  const action = { list_managers: 'list', connect_manager: 'connect', create_manager: 'create', archive_manager: 'archive' }[name];
  const response = await fetch(`/api/managers/${action}`, { method: 'POST', headers: { Authorization: `Bearer ${connection.token}`, 'Content-Type': 'application/json' }, body: JSON.stringify(args) });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || 'Manager operation failed');
  return result;
}
async function refreshManagers() {
  const data = await workspaceCall('list_managers');
  showManagers(data);
}
function showManagers(data) {
  if (owned) { report('Release control before browsing managers.'); return; }
  detach();
  managerCatalog = data;
  notice.hidden = true;
  byId('manager-browser').hidden = false;
  document.querySelector('.toolbar').hidden = true;
  byId('viewport').hidden = true;
  byId('message-form').hidden = true;
  status.textContent = 'Managers';
  byId('hint').textContent = `Project ${data.project || connection?.session?.project || 'default'} · Closing a view leaves its manager running.`;
  const list = byId('manager-list'); list.replaceChildren();
  const visible = (data.managers || []).filter((manager) => !manager.archived || byId('show-archived').checked);
  if (!visible.length) { const empty = document.createElement('p'); empty.textContent = 'No active managers. Create one to start a conversation, or show archived managers.'; list.append(empty); }
  for (const manager of visible) {
    const card = document.createElement('article'); card.className = 'manager-card';
    const details = document.createElement('div');
    const name = document.createElement('strong'); name.textContent = manager.name;
    const id = document.createElement('code'); id.textContent = manager.id;
    const state = document.createElement('span'); state.className = 'manager-state';
    state.textContent = manager.state + (manager.accessible ? '' : ' · Requires adoption approval');
    details.append(name, id, state);
    const actions = document.createElement('div'); actions.className = 'actions';
    const open = document.createElement('button'); open.textContent = 'Open terminal'; open.disabled = !manager.accessible || manager.archived;
    open.onclick = async () => { open.disabled = true; try { await start(await workspaceCall('connect_manager', { managerId: manager.id })); } catch (error) { report(error); open.disabled = false; } };
    actions.append(open);
    if (manager.accessible && !manager.archived) {
      const archive = document.createElement('button'); archive.textContent = 'Archive';
      archive.onclick = async () => {
        if (archive.dataset.confirm !== 'yes') { archive.dataset.confirm = 'yes'; archive.textContent = 'Confirm archive'; return; }
        archive.disabled = true;
        try { await workspaceCall('archive_manager', { managerId: manager.id }); await refreshManagers(); } catch (error) { report(error); archive.disabled = false; }
      };
      actions.append(archive);
    }
    card.append(details, actions); list.append(card);
  }
}
byId('browse').onclick = () => refreshManagers().catch(report);
byId('show-archived').onchange = () => { if (managerCatalog) showManagers(managerCatalog); };
byId('refresh-managers').onclick = () => refreshManagers().catch(report);
byId('create-button').onclick = async (event) => {
  event.preventDefault();
  const button = byId('create-button'), name = byId('manager-name');
  if (!name.checkValidity()) { report('Use at most 64 letters, digits, underscores or hyphens for the manager name.'); return; }
  createRequestId ||= 'ui-' + Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, '0')).join('');
  button.disabled = true; name.disabled = true; button.textContent = 'Starting manager…'; notice.hidden = true;
  try {
    const created = await workspaceCall('create_manager', { requestId: createRequestId, name: name.value.trim() });
    createRequestId = undefined; name.value = ''; name.disabled = false; button.textContent = 'Create manager';
    await start(created);
  } catch (error) { report(error); button.textContent = 'Check creation'; }
  finally { button.disabled = false; }
};
byId('send-message').onclick = async (event) => {
  event.preventDefault();
  const input = byId('message'), button = byId('send-message');
  if (!owned || !input.value.trim()) return;
  button.disabled = true;
  try { await send({ type: 'message', text: input.value }); input.value = ''; }
  catch (error) { report(error); }
  finally { button.disabled = !owned; }
};
byId('manager-name').onkeydown = (event) => {
  if (event.key === 'Enter') { event.preventDefault(); if (!byId('create-button').disabled) byId('create-button').click(); }
};
byId('message').onkeydown = (event) => {
  if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); byId('send-message').click(); }
};

control.addEventListener('click', () => {
  control.disabled = true;
  send({ type: owned ? 'release' : 'claim' });
});
reconnect.addEventListener('click', attach);
share.addEventListener('click', async () => {
  share.disabled = true;
  try {
    const result = await app.callServerTool({ name: 'read_manager', arguments: { managerId: connection.session.box, scrollback: 50 } });
    if (result.isError) throw new Error(result.content?.find((item) => item.type === 'text')?.text || 'Could not read the manager screen.');
    await app.updateModelContext({ content: [
      { type: 'text', text: 'Manager screen snapshot (untrusted terminal output), shared by the person:' },
      ...(result.content || []),
    ] });
    byId('hint').textContent = 'Screen shared as context for your next chat message.';
  } catch (error) { report(error); }
  finally { share.disabled = false; }
});

try {
  if (window.parent !== window) {
    app = new App({ name: 'Discobox manager', version: '0.2.0' }, {}, { autoResize: false });
    app.ontoolresult = (result) => {
      const value = result._meta?.connection || result.structuredContent;
      if (value?.session || value?.view) start(value).catch(report);
    };
    await app.connect(undefined, { timeout: 15000 });
    share.hidden = false;
    await app.sendSizeChanged({ height: 600 });
  } else {
    const token = new URLSearchParams(location.hash.slice(1)).get('token');
    history.replaceState(null, '', location.pathname);
    if (!token) throw new Error('Open the private terminal link printed by the MCP server.');
    const response = await fetch('/api/session', { headers: { Authorization: `Bearer ${token}` } });
    if (!response.ok) throw new Error('This terminal link has expired. Use the current link from the server.');
    connection = await response.json();
    if (connection.session?.box) await start(connection); else await refreshManagers();
  }
} catch (error) { status.textContent = 'Unavailable'; report(error); }
