# Service access through the MCP adapter

Research/design only, 2026-11-14. Inspected the latest fetched upstream
`discobox-ai/discobox` main at
[`c6df6a65`](https://github.com/discobox-ai/discobox/tree/c6df6a65be0c58a9d544f5fa879ee3660ac38255).
The adapter still pins `d6c392a3` in `go.mod`; this is a source-level design,
not a dependency update. The inspected service/HTTP authorization and listening-port
fields also exist at that pin; runtime support must still be tested against the
installed server/pool versions. No live forwarding, new
service, or backend permission change was attempted for this investigation.

## Recommendation

Ship service discovery and lifecycle tools, then opt-in forwarding from the
**host-running MCPB adapter**. Give each selected service a local listener and a
small Services widget with status, logs, Start/Stop/Restart, Open in browser,
and Close forwarding. Keep service execution separate from forwarding: opening
a listener does not start a declared service, and closing a view does not kill it.

Use the upstream framed TCP tunnel for host forwarding. It preserves ordinary
web application behavior and works for loopback-only listeners. The current
sandbox-running `--local` adapter can use the HTTP proxy for permitted children,
but cannot provide general TCP forwarding. Do not promise arbitrary applications
inside canvas in the first iteration; browser opening is the reliable first UX.

## What Discobox exposes today

| Purpose | Control-plane route | Sandbox-role access | Authorized user access |
| --- | --- | --- | --- |
| Reported listening/declared ports | `GET /api/projects/{project}/sandboxes/{box}` → `runtime.agentStatus.ports` | Existing readable catalog scope | Project membership |
| Declared service list/details/logs | `GET .../services`, `.../services/{id}`, `.../services/{id}/logs` | Led children; backend checks apply | Project membership |
| Service lifecycle | `POST .../services/{id}/start`, `/stop`, `/restart` | Led children within caller's egress authority | Project membership |
| HTTP requests, including upgrades | Any method `/projects/{project}/sandboxes/{box}/http/{port}/...` | Led children within caller's authority | Project membership |
| Raw TCP | `GET /api/projects/{project}/sandboxes/{box}/tcp/attach?host=localhost&port=N` | Explicitly denied | Project membership |
| UDP datagrams | `GET /api/projects/{project}/sandboxes/{box}/udp/attach?host=127.0.0.1&port=N` | Not in role; denied | Project membership |

The HTTP route is registered under `/projects/`, without `/api` in the inspected
server. The service and tunnel routes above use `/api/projects/`. Preserve these
actual route shapes rather than deriving all paths from one assumed prefix.

The tunnel is WebSocket **plus Discobox framing**, not a raw WebSocket byte pipe.
TCP writes use `Input`, reads use `Stdout`, and `CloseInput`/`CloseOutput` preserve
half-closes. UDP uses one payload frame per datagram. The existing desktop client
already implements a specialized TCP dialer; generalize/reuse it carefully.
`cli/internal/portforward` is a Go internal package and cannot be imported by this
external adapter. Ask upstream to export it or implement the small listener
manager around public `pkg/execstream/frame`; do not import private CLI code.

`discobox proxy BOX --port 3000` already provides host-side forwarding manually.
Without `--port`, it follows reported ports. It defaults to loopback, chooses the
nearest available local port, keeps bindings stable across a temporary remote
restart, and stops when the command ends. The desktop TUI shares this mechanism.
The adapter can follow these semantics without launching a CLI subprocess.

## Connection authority and runtime differences

A host process is not automatically a user principal just because it uses a Unix
socket. With `authRequired=false`, the current server's fallback authenticates
requests as its configured default user. With `authRequired=true`, that fallback
is absent on every listener, including Unix sockets. An enrolled Iroh peer is
checked each request, acts as the default user with `ScopeAll`, and then passes
project membership authorization. The tunnel handler mints the downstream
`tcp:connect`/`udp:connect` lease; the adapter does not mint one or send a scope
parameter. Use an authorized user connection and report 401/403 without bypass.

A sandbox-forwarded API connection remains a sandbox principal even when a
human uses its MCP widget. API-access approval enables its fixed sandbox role;
it does not add TCP tunnels. Parent adoption establishes which boxes it leads,
but does not grant missing routes. There is no current TCP-access approval
request or per-widget toggle. Supporting narrowly delegated sandbox tunnels
would require an upstream policy/API change, not another API-access request.

The HTTP proxy is useful but has material limits:

- Docker runtime dials the container IP, so the service must listen on a reachable
  interface such as `0.0.0.0`; it cannot reach `127.0.0.1`-only services.
- Latest machine runtime exposes only the sandbox-agent port through
  `SandboxDialer`; arbitrary `/http/{port}` requests are unsupported there.
- TCP tunnels instead dial from inside the sandbox's network namespace, including
  loopback. The host-running adapter can therefore support both runtime types.
- The HTTP proxy uses an HTTP target, not arbitrary HTTPS/TCP/UDP. The TCP tunnel
  is protocol-transparent. Forwarding HTTPS preserves its certificate behavior;
  it does not make a remote certificate valid for localhost.

## Starting services

Services are files under `.discobox/services/` or built-in image declarations.
The API lists and controls declarations; it has no service create/delete route.
Declarations are re-read on every listing. A file added during a session appears
immediately but is not automatically launched; a permitted lifecycle API call
can start it explicitly. A declaration-only YAML or `start: never` entry advertises
ports without a runnable command. Failed/exited services are not auto-restarted.

For an existing project, launch a box, list its services, start the chosen
service, then open its port. For newly developed code, have the harness create
an executable service declaration and use the lifecycle API, or run the process
normally and discover its listening port. Do not introduce a model-facing tool
that accepts arbitrary host commands. A declared port is not proof of a listener
or HTTP readiness; show distinct `declared`, `starting`, `listening`, and
`reachable` states where evidence supports them.

Discovery includes `port`, `protocol`, `addresses`, `serviceId`, and `serviceName`.
Use service names, not internal box names. Service and discovered-port lists
must both be shown: a runnable service may have no port, and a port may have no
service. Resolve the destination from fresh metadata. Wildcard/loopback TCP binds
use `localhost` (IPv4/IPv6 fallback); specific binds use their actual address.
Do not accept an arbitrary caller-supplied tunnel host in the first release.

## URLs, privacy, and canvas

A raw TCP forward running in the **user's host MCPB process** can return, for
example, `http://127.0.0.1:43017/` after the listener is allocated. That URL means
that machine only. A loopback listener created in our sandbox or a remote hosted
adapter is not automatically reachable on the user's loopback; window-managed
Discobox forwarding is separate and must not be assumed. For a hosted adapter,
a browser-accessible authenticated gateway is a different deployment feature.

Keep upstream credentials on the adapter's outer connection. Never append the
Discobox credential or MCP bearer to service URLs, cookies, HTML, or model output.
Our trusted Services widget uses app-only tools; private view handles and log
streams go in `_meta`. The model gets service identity, lifecycle/readiness, and
forwarding status. Logs or response bodies enter context only via an explicit
read/share tool. A nonsecret loopback address may be returned as useful tool
metadata; private launch/session handles remain out of context.

For Open in browser, send the address through the host's link-opening API. Use
separate local ports/origins for untrusted applications; never serve their HTML
at the adapter's `/` or inline it into our trusted MCP App. Different localhost ports provide distinct web origins but do not isolate
cookies by port. Avoid privileged cookies on the adapter origin; applications
may need distinct local hostnames or cookie names to prevent collisions. A
loopback listener also is not service authentication: preserve application auth
and validate expected Host/Origin where appropriate. Raw TCP forwarding
preserves root-relative assets, application cookies, redirects, WebSocket/HMR,
and SSE without path rewriting. Absolute URLs or hard-coded remote origins may
still need the application's dev-server/public-origin configuration. Service
Origin/Host allowlists and auth remain the application's own requirements.

A path reverse proxy such as `/service/{id}/` is less compatible: `/assets/...`,
absolute `Location` headers, cookie Domain/Path, WebSocket URL construction, and
framework base paths need handling. The upstream HTTP proxy strips outer
Authorization before the box port, so application bearer authentication cannot
be assumed to pass through that API. Raw TCP preserves service-level auth bytes.
Do not solve these issues with general HTML rewriting or blanket header removal.

A nested service iframe is an optional second stage. MCP Apps supports
`_meta.ui.csp.frameDomains`, but the host must allow the exact local origin and
may still restrict localhost/mixed content. Service `frame-ancestors` and
`X-Frame-Options`, third-party/SameSite cookies, and host sandbox restrictions
can prevent embedding. Test Claude and Codex individually. Keep a browser
fallback; do not strip service framing protections automatically. Never give
untrusted service content the trusted widget's MCP bridge or approval handles.
Tool-only relaying of arbitrary HTML is not a substitute for a browser origin:
its scripts, imports, fetches, redirects, and sockets would all need a browser
transport. Existing private terminal/desktop frame relays solve a narrower case.

## Proposed staged surface and lifecycle

1. `list_services(boxId)` returns declared services plus discovered ports;
   the widget presents names, state, logs, and supported access methods.
   `control_service(boxId, serviceId, action)` handles explicit start/stop/restart;
   an explicit bounded log read is available for model diagnostics.
2. `open_service(boxId, serviceId, port?)` requests a forward for a fresh advertised
   TCP port and opens the trusted Services widget. The host adapter binds only
   `127.0.0.1`, allocates an actual free local port, and probes the tunnel/readiness
   with bounded timeouts. A bound listener is reported separately from a healthy
   service. App-only `service_open/read/close` handles UI leases and logs; a
   `close_service_forward` tool can close explicitly without stopping the service.
3. Preserve a forward across service restart; cap forward count and simultaneous
   connections, impose handshake deadlines and backpressure, and expire inactive
   leases. Renew from the widget or explicit use. Closing the widget releases its
   lease; it must not silently tear down another viewer's lease. Adapter exit,
   lost authorization/parenthood, archive/deletion, or explicit close tears down
   the listener and tunnels. Established tunnel revocation behavior needs a live
   check; do not claim enrollment's per-request check closes an existing stream.
4. Add a nested canvas preview only after each host's origin/CSP/frame behavior
   is verified. Keep arbitrary services separate from terminal ownership: opening
   a web view must not claim terminal input control. UDP/database tooling can
   follow once TCP web services are dependable.

Acceptance checks: list vs declared-only ports; loopback IPv4/IPv6; Docker and
machine runtime; conflict port selection; HTTP, WS/HMR, SSE, cookies, redirect,
HTTPS failure reporting; service restart; stopped/archived boxes; 401/403 and
mid-session revocation; no credentials in target requests/logs/model context;
untrusted-origin isolation; bounded cleanup after widget/adapter exit. Verify
that an advertised URL actually opens on the user's machine, not just here.

## Source map

Paths below refer to upstream commit `c6df6a65be0c58a9d544f5fa879ee3660ac38255`:

- `server/internal/server/sandbox_http_proxy.go`: HTTP route and `SandboxAction("http")`.
- `server/internal/server/sandbox_tunnel_proxy.go`: TCP/UDP routes and lease scopes.
- `server/internal/auth/authorizers.go`: `sandboxRole` service/HTTP allowlist,
  `ProjectAuthorizer` membership, `SandboxRoleAuthorizer` fixed-role refusal.
- `server/internal/auth/sandbox_role_internal_test.go:230-244`: service/HTTP
  child access and explicit TCP tunnel denial.
- `server/internal/auth/authenticators.go:196-250`, `server/internal/server/router.go:274-290`:
  Iroh identity, default-user fallback, and `authRequired` behavior.
- `pool-agent/server/sandbox_proxy.go:108-145,329-360`: HTTP target, runtime dial,
  upgrade forwarding and authorization stripping.
- `pool-agent/sandboxruntime/runtime.go:2705-2743`: Docker container-IP dialing.
- `pool-agent/sandboxruntime/machine.go:1221-1250`: machine agent-only dialing.
- `sandbox-agent/services/manager.go`, `sandbox-agent/server/services.go`:
  declaration discovery, current run state and lifecycle; no create API.
- `cli/internal/cli/proxy.go`, `listening_ports.go`, `port_tunnel.go`:
  actual port-forward UX, destination resolution and framed WebSocket net.Conn.
- `cli/internal/portforward/`: listener allocation, stable binding, half-close,
  UDP flow lifetime; internal-package boundary prevents direct external import.
- Adapter `internal/discobox/desktop.go`: existing constrained tunnel implementation.
- Bundled SDK `node_modules/@modelcontextprotocol/ext-apps/dist/src/spec.types.d.ts:465-505`:
  `connectDomains`, `resourceDomains`, `frameDomains` (version pinned in package.json).
