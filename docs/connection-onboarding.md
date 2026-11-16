# MCPB connection onboarding

Implementation follow-up: the first widget/bootstrap pass is now described in
[connection-setup-widget.md](connection-setup-widget.md). The findings below capture
the earlier source inspection and proposal; native and real remote validation remain pending.

Research only, 2026-11-14. No runtime changes, enrollment, backend settings,
commits, or pushes were performed. Sources inspected:

- `cloudnautique/discobox-ui` at [`e13d962`](https://github.com/cloudnautique/discobox-ui/tree/e13d962ceb03cb6db5e021e2da1f1393a9d3c4e9).
- Latest fetched `discobox-ai/discobox` at [`936af33`](https://github.com/discobox-ai/discobox/tree/936af3323d1b65f7bee68178d82d823988d0fb52).
- This adapter's manifest, startup, transport, identity, and connection documents.
  Its Go dependency remains pinned to `d6c392a3`; research does not upgrade it.

## Recommendation

Copy the GUI's simple interaction: **This computer** by default; **Remote
computer** with a server address, a copyable adapter peer ID/enrollment command,
and **Test connection**. Do not invent a cloud account, pairing code, or approval
inbox for a protocol that currently has none. Keep the adapter initialized and
its setup tools/widget available while Discobox is missing or unreachable.

The extension process runs on the machine where the user installs the MCPB.
“This computer” means that machine. If an adapter is instead deployed on a cloud
host, its Unix socket and private identity belong to that host, not the user's
laptop. MCPB installation and remote MCP hosting are separate deployment choices.

## What the reference GUI actually does

The default local profile is `unix://`. The Go companion resolves that shorthand
with upstream `pkg/endpoint.DefaultEndpoint()`, including the explicit
`DISCOBOX_SERVER_ADDRESS` override. Otherwise upstream uses
`$XDG_RUNTIME_DIR/discobox/server.sock`, or
`$TMPDIR/discobox-<uid>/discobox/server.sock` through `os.TempDir()` when XDG is
unset. Paths are URL-escaped. Do not hardcode `/tmp` on macOS: its user launchd
TMPDIR matters, and terminal/dev-shell environments can differ from a desktop
app. The GUI also normalizes saved copies of the default path back to the
shorthand, while retaining custom paths.

The GUI does not silently select a remote server from the CLI's saved connection
list. It starts with This computer; remote profiles are explicit and independently
saved. A specific custom socket remains pinned instead of following the default
address override. Connection testing uses a separate client, does not create a
box, and does not switch an existing workspace. Users can save an offline remote
profile and finish enrollment later.

For Remote computer, the GUI exposes **Authorize this computer → Show My Peer
ID → Copy Enrollment Command**. It owns a separate Keychain identity, passes its
seed to a Go helper over stdin, and uses ephemeral transport keys certified by
that stable issuer. The helper presents an owner-only local proxy socket; our Go
adapter already talks Iroh directly and does not need that Swift/helper layout.

Sources: [GUI profiles/resolver](https://github.com/cloudnautique/discobox-ui/blob/e13d962ceb03cb6db5e021e2da1f1393a9d3c4e9/Sources/DiscoboxCore/ConnectionProfile.swift),
[Go companion resolver](https://github.com/cloudnautique/discobox-ui/blob/e13d962ceb03cb6db5e021e2da1f1393a9d3c4e9/Companion/main.go),
[settings UI](https://github.com/cloudnautique/discobox-ui/blob/e13d962ceb03cb6db5e021e2da1f1393a9d3c4e9/Sources/DiscoboxDesktop/ConnectionSettings.swift),
[remote walkthrough](https://github.com/cloudnautique/discobox-ui/blob/e13d962ceb03cb6db5e021e2da1f1393a9d3c4e9/docs/REMOTE.md),
[upstream default resolver](https://github.com/discobox-ai/discobox/blob/936af3323d1b65f7bee68178d82d823988d0fb52/pkg/endpoint/default.go),
[Unix default](https://github.com/discobox-ai/discobox/blob/936af3323d1b65f7bee68178d82d823988d0fb52/pkg/endpoint/default_unix.go).

## Socket access and authentication are different

Opening a filesystem socket requires local OS access, but does not itself prove
an API user identity. With `authRequired=false`, the inspected server's fallback
authenticator admits otherwise-unclaimed requests as its configured default user.
With `authRequired=true`, that fallback is absent on Unix as well as HTTP.
An accessible socket can therefore still answer 401. The GUI does not add a
client authentication assertion to make that work.

An enrolled Iroh client authenticates as the server's default user with
`ScopeAll`, then passes route/project authorization. Its enrollment is not
restricted to the manifest's selected project or one box. Explain that scope
before inviting an operator to enroll the adapter. Sandbox API access is a
separate principal and fixed role; it cannot enroll peers or turn itself into a
user connection. The optional current `DISCOBOX_MCP_TOKEN` is not proof that the
inspected server accepts generic bearer-token login; deployments may differ.

Never borrow the GUI's Keychain item, the CLI's key, or a server private key for
convenience. Our adapter already has its own stable issuer identity, which is the
identity an operator must enroll.

Sources: [authenticator chain](https://github.com/discobox-ai/discobox/blob/936af3323d1b65f7bee68178d82d823988d0fb52/server/internal/server/router.go),
[Iroh/default-user authentication](https://github.com/discobox-ai/discobox/blob/936af3323d1b65f7bee68178d82d823988d0fb52/server/internal/auth/authenticators.go),
[GUI identity](https://github.com/cloudnautique/discobox-ui/blob/e13d962ceb03cb6db5e021e2da1f1393a9d3c4e9/Sources/DiscoboxDesktop/IrohIdentity.swift).

## Remote enrollment flow that works with today's server

1. An operator enables the remote server's Iroh listener. The GUI guide uses
   `listen: [unix://, iroh://]`; the CLI's automatic local launcher forces a
   Unix-only listener, so a real server launch/service configuration is needed.
   Do not restart or reconfigure the server from extension setup implicitly.
2. The operator shares that server's `discobox://d1-…` address. On the intended
   server, `discobox --server unix:// admin id` distinguishes the client ID from
   the server ID. `admin server status` also reports listener readiness. A server
   peer ID alone does not establish that it has an active/reachable Iroh listener.
3. The extension loads or creates **its own** identity and shows its public ID.
   This public ID may appear in model/tool output; the private key must not.
4. Show a copyable command for someone who already reaches the target server:

   ```sh
   discobox --server unix:// admin peer add <adapter-client-peer-id> --name 'Discobox MCP on My Mac'
   ```

   Run it on the server host, or replace the endpoint with one an already-enrolled
   operator client can reach. The server ID is the dial target; the adapter ID is
   the value being enrolled. Shell-quote the user-supplied label.
5. Back in the extension, **Test connection** makes a bounded authenticated read
   of the selected project. On success, show Connected and the normal box list.
   Listing boxes is explicit; onboarding must never create/discover a manager as
   a startup side effect.

The actual peer APIs are authenticated `GET /api/peers`, `POST /api/peers`, and
`DELETE /api/peers/{peerId}`. The new peer cannot call them to authorize itself.
There is no inspected unauthenticated enrollment request, pairing code exchange,
or pending/denied peer-request record. “Waiting for authorization” would be a
local UX state with instructions, not an inbox request sent to the remote host.
A smoother operator approval flow would require new upstream protocol support.

`POST` returns 409 when already enrolled. Treat that as “already registered;
test access,” never as a reason to replace the key. A refusal cannot reliably
say whether a peer was never enrolled or was revoked; avoid pretending to know
without an authorized server-side lookup. Revocation is checked again per HTTP
request; it does not imply every existing terminal/tunnel stream is torn down.
The file-based `authorized_ids` layer is separate from API-managed enrollment
and does not appear in `peer ls` or disappear through `peer rm`.

Sources: [peer CLI](https://github.com/discobox-ai/discobox/blob/936af3323d1b65f7bee68178d82d823988d0fb52/cli/internal/cli/peer.go),
[identity CLI](https://github.com/discobox-ai/discobox/blob/936af3323d1b65f7bee68178d82d823988d0fb52/cli/internal/cli/identity.go),
[peer resource](https://github.com/discobox-ai/discobox/blob/936af3323d1b65f7bee68178d82d823988d0fb52/server/internal/resources/peers/service.go),
[server identity handler](https://github.com/discobox-ai/discobox/blob/936af3323d1b65f7bee68178d82d823988d0fb52/server/internal/handlers/server_peer.go).

## Proposed extension experience

| State | Widget and raw-text behavior |
| --- | --- |
| Default local | “Connecting to Discobox on this computer”; resolve/probe the shared default socket without scanning arbitrary paths or starting a server. |
| No socket / access denied | Show the resolved path and distinguish missing server from OS permission failure. Offer Custom socket and Remote computer. Do not chmod/chown or silently start a server. |
| Local API 401 | Explain that socket discovery succeeded but API authentication is required; offer an authorized peer connection or deployment-specific supported auth. |
| Remote setup | Server address, project, public adapter peer ID, scope explanation, Copy enrollment command, Test connection. Allow saved setup while offline. |
| Explicit refusal | “This adapter is not authorized, or its enrollment was removed.” Show the stable ID and operator steps. Stop automatic retries until the user requests a new test. |
| Timeout / offline | “Could not reach the server”; show listener/network/relay diagnostics. Do not label a timeout as enrollment denial or rotate the key. |
| Connected | Show verified endpoint/project and the ordinary box list. Connection success is not proof of Desktop/service tunnel capability. |
| Identity invalid | Actionable local error and key path; retain the file. Recovery/reset is explicit and warns that all affected servers require re-enrollment. |

Keep the initial package settings small: an optional Connection URL defaulting to
`unix://`, project defaulting to `default`, and advanced relay/custom socket
settings. Do not require every local user to know a socket path. The optional
current API-token field must not be presented as the normal Iroh authorization
flow. The manifest cannot perform a dynamic enrollment wizard; the setup widget
and ordinary text tool results provide those steps after MCP initialization.

A minimal public `connection_status` tool can report configured mode, connection
state, selected project, public adapter ID, and sanitized diagnostic code. A
public `open_connection_setup` tool renders setup; the same essential instructions
must be readable in plain text for clients without apps. Private app tools can
probe/retry and load setup details, but endpoint changes/enrollment are not an
unrestricted model-facing HTTP client or automatic privilege grant.

For the first release, edit the extension's install settings then restart its
adapter for a different endpoint/project/relay. Keep one profile per process.
Our current `ConfigurePeer` installs process-wide transport configuration;
do not promise hot swapping endpoints or identities. Both Claude MCPB and a
Codex stdio entry can reuse this bootstrap/status/setup behavior. An extracted
binary should not depend on Claude's install directory or settings storage.

## Changes needed in this repository

1. Make bootstrap independent of a usable Discobox client. Currently `main.go`
   exits when endpoint is absent and can fail before MCP starts on identity or
   transport setup; the required manifest field reinforces that problem.
   Initialize MCP/setup resources first; backend operations return a structured
   setup-required/unreachable error until the connection is ready. Keep
   `--browse` semantics: no manager creation on startup.
2. Support `unix://` through the same upstream resolver as the GUI. The current
   adapter's pre-validation rejects empty Unix paths before upstream Parse can
   resolve them. Resolve first, validate the resulting local endpoint, and avoid
   cloning a hardcoded path algorithm. Explicit CLI/env endpoints must retain
   their precedence; don't import the UI's remote profiles or unrelated keys.
3. Wrap existing `PeerIdentity`/`ConfigurePeer` in a connection state service.
   Expose public identity/status without a network dependency. Do not initialize
   Iroh repeatedly on every test. Bound test deadlines separately from 75-second
   ordinary backend requests; keep user retry distinct from mutating API retry.
4. Add a small setup panel and text fallback. Update manifest endpoint default,
   docs, and app cache/resource version when implementation lands. Relay URLs
   are currently repeatable CLI flags but not MCPB user settings. The GUI also
   supports direct-only mode; this adapter currently does not, so either omit it
   from the first UX or implement and test it explicitly.
5. Keep the current key outside the package: `os.UserConfigDir()/discobox-mcp/iroh/id_ed25519`
   (macOS normally `~/Library/Application Support/discobox-mcp/iroh/id_ed25519`).
   Preserve it across upgrades/reinstalls when that directory remains; retain
   corrupt keys instead of replacing them. Forgetting an endpoint does not
   revoke enrollment. Explicit reset makes a new identity and requires enrollment
   on every server where the old identity was used. A future Keychain migration
   must import the existing key, not silently mint a new one.

Current adapter sources: [main.go](../cmd/discobox-mcp/main.go),
[manifest](../packaging/mcpb/manifest.json), [client validation](../internal/discobox/client.go),
[identity and peer transport](../internal/discobox/peer.go).

## Verification before shipping

Use fixtures for missing/custom/default socket and desktop-vs-shell environment;
local 401; malformed server ID; bounded timeout; explicit enrollment refusal;
409 already enrolled; server without Iroh listener; default/custom relay; key
reuse across two processes/restart/upgrade; corrupt key retention; and protocol
initialization before any backend is configured. Native Claude/Codex checks must
prove the setup widget is reachable, clipboard copy works, changed package
settings restart the adapter, and cached resource aliases restore correctly.

A real two-host enrolled Iroh test is still required for NAT/relay/revocation
behavior. Nothing in this research establishes native MCPB onboarding or remote
connectivity works today. Smooth QR/pairing-link authorization and project-scoped
client enrollment remain upstream design work, not hidden capabilities to expose.
