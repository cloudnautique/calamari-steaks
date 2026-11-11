// Replaced by the Go resource renderer with a JSON-encoded, fixed local URL.
const assetBase = __ASSET_BASE__;
const byId = (id) => document.getElementById(id);
const status = byId('status');
const notice = byId('notice');
const control = byId('control');
const reconnect = byId('reconnect');
const share = byId('share');
let app, connection, terminal, fit, socket, owned = false, connected = false;

function report(error) {
  notice.textContent = error instanceof Error ? error.message : String(error);
  notice.hidden = false;
}

function setControl(value) {
  owned = value;
  control.textContent = owned ? 'Release control' : 'Take control';
  control.setAttribute('aria-pressed', String(owned));
  byId('hint').textContent = owned
    ? 'You are typing. Release control to let chat drive the manager.'
    : 'Observing. Chat can drive the manager; take control to type here.';
}

function send(message) {
  if (socket?.readyState !== WebSocket.OPEN) return;
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
  if (!connection || socket && socket.readyState < WebSocket.CLOSING) return;
  notice.hidden = true;
  reconnect.disabled = true;
  control.disabled = true;
  status.textContent = 'Connecting…';
  terminal.reset();
  const url = new URL('/terminal', connection.origin);
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
        case 'error':
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
    connected = false;
    setControl(false);
    control.disabled = true;
    reconnect.disabled = false;
    status.textContent = 'Disconnected';
    byId('hint').textContent = 'Detached. Reconnect to inspect the session before sending more input.';
  };
}

async function start(value) {
  if (connection) return;
  if (!value || value.origin !== new URL(assetBase).origin || !value.token || !value.session) {
    throw new Error('Missing or unexpected manager connection. Reopen the manager tool.');
  }
  connection = value;
  byId('session').textContent = `${value.session.project} / ${value.session.box} / ${value.session.exec}`;
  const { Terminal, FitAddon, Ghostty } = await import(`${assetBase}/ghostty-web.js`);
  const ghostty = await Ghostty.load(`${assetBase}/ghostty-vt.wasm`);
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
  const cleanup = () => { observer.disconnect(); socket?.close(); terminal.dispose(); };
  window.addEventListener('pagehide', cleanup, { once: true });
  if (app) app.onteardown = async () => { cleanup(); return {}; };
  attach();
}

control.addEventListener('click', () => {
  control.disabled = true;
  send({ type: owned ? 'release' : 'claim' });
});
reconnect.addEventListener('click', attach);
share.addEventListener('click', async () => {
  share.disabled = true;
  try {
    const result = await app.callServerTool({ name: 'read_manager', arguments: { scrollback: 50 } });
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
    const { App } = await import(`${assetBase}/mcp-app.js`);
    app = new App({ name: 'Discobox manager', version: '0.1.0' }, {}, { autoResize: false });
    app.ontoolresult = (result) => {
      if (result._meta?.connection) start(result._meta.connection).catch(report);
    };
    await app.connect();
    share.hidden = false;
    await app.sendSizeChanged({ height: 600 });
  } else {
    const token = new URLSearchParams(location.hash.slice(1)).get('token');
    history.replaceState(null, '', location.pathname);
    if (!token) throw new Error('Open the private terminal link printed by the MCP server.');
    const response = await fetch('/api/session', { headers: { Authorization: `Bearer ${token}` } });
    if (!response.ok) throw new Error('This terminal link has expired. Use the current link from the server.');
    await start(await response.json());
  }
} catch (error) { status.textContent = 'Unavailable'; report(error); }
