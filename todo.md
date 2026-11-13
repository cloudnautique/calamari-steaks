# Work plan

Keep this repository without a README for now.

## Direction

Keep the manager in the Discobox backend. Claude and Codex drive that persistent
session through an external Go MCP adapter. The desktop agent can read, send,
wait, and interrupt using APIs; the person can watch and take control of the same
session through a terminal view. Closing a view should detach from the session.

The target remote architecture is:

```text
Claude / Codex -- HTTPS Streamable HTTP MCP --> hosted Go adapter
MCP App / browser -- HTTPS + WSS ------------> hosted Go adapter
hosted Go adapter -- Iroh ------------------> Discobox server
                                               manager + workers
```

Iroh belongs on the adapter-to-Discobox connection. Desktop clients and browsers
should only need ordinary HTTPS/WSS. Start with a private, single-user service
pinned to one Discobox peer and project. The hosted adapter is trusted with
terminal content and input.

## Already scaffolded

- [x] External Go MCP server with stdio by default and optional HTTP transport.
- [x] Manager tools: `open_manager`, `read_manager`, `send_manager`,
  `wait_manager`, and `interrupt_manager`.
- [x] Explicit manager selection, saved-state reuse, discovery by
  `discobox-mcp-role=manager`, and creation with Discobox API access when needed.
- [x] Stop on ambiguous discovery, invalid responses, or uncertain creation
  instead of silently creating another manager.
- [x] MCP App resource at `ui://discobox/manager.html`, a standalone browser
  page, and an authenticated WebSocket bridge to the same backend exec.
- [x] Browser observe/take-control/release flow; human control blocks adapter
  input and interrupts. Screen sharing can send a fresh snapshot to host context.
- [x] Go tests for manager lifecycle, protocol handling, and control ownership.
- [x] Local sandbox API mode (`--local`) through the pool proxy, with parent-aware
  discovery, adoption guidance, and sandbox-compatible manager creation.

These are scaffolded capabilities, not confirmation of desktop UI compatibility.
Frontend dependencies and asset builds pass. Terminal rendering is verified in
the Codex app. Claude desktop rendering remains unverified.
The standalone browser terminal and local API connection were
verified live, including MCP read/send/wait, terminal replay, and adapter restart
reusing the same manager/exec.

## Manager workspace

- [x] List, explicitly create, connect to and archive managers through text MCP
  tools, with manager IDs carried into read/send/wait/interrupt calls.
- [x] Add a chat-flow picker, status cards, create/open/archive actions, and a
  message composer alongside the raw terminal. See `docs/managers.md`.
- [x] Scope connection pinning and human input ownership per manager; opening
  another view never redirects existing sessions or queued input.
- [x] Journal explicit creation intents under stable request keys; uncertain
  creation never silently creates a duplicate.
- [x] Support `--browse` startup without automatically creating a manager.
- [ ] Verify the expanded manager workspace in native Codex and Claude canvases.

## Package the terminal UI

- [x] Finish installing and verifying the pinned `ghostty-web` and MCP Apps SDK
  dependencies; commit a lockfile after a successful install.
- [x] Use Ghostty's web renderer/WASM; do not require a native Ghostty app.
- [x] Make release builds bundle HTML, CSS, JavaScript, WASM, and dependency
  notices in the Go binary. Users should not install Node, npm, or UI packages.
- [ ] Remove the build-time dependency on the ignored `discobox/` reference
  checkout. Currently `scripts/build-ui.mjs` copies
  `discobox/assets/brand/tokens.css`; package a versioned copy with its license
  and provenance, or introduce a reproducible dependency source.
- [ ] Make release builds fail when UI assets are missing. Current Go builds
  permit a tools-only binary and report missing assets when opening the UI.
- [ ] Test `open_manager` in Codex and Claude for resource rendering, WASM,
  asset loading, WebSocket access, and host context updates.
- [x] Serve a self-contained versioned MCP App resource and carry terminal
  I/O through app-only MCP tools; canvas views need no localhost fetch or
  WebSocket access. Verify live output and control in an opaque iframe with
  direct network access blocked. Native Codex/Claude validation remains above.
- [ ] Keep the standalone page as a fallback. Test reconnecting to the same
  session and reopening the view without replacing the manager.
- [ ] Verify how each desktop host handles persistent panes. MCP cannot force
  a stationary view or keep a host iframe alive.
- [ ] Test multiple observers, resize behavior, input ownership, disconnects,
  and the absence of automatic input replay after an uncertain send.

## Add Iroh upstream transport

- [x] Reuse Discobox's endpoint transport by importing `pkg/endpoint` at
  upstream commit `d6c392a3cce73ee87e716ff6e9c9082fb3ef2c61` (2026-11-11).
  See `docs/connection.md` for source references and connection setup.
- [x] Support `discobox://<peer-id>` alongside the existing HTTP(S)/Unix socket
  endpoints. Carry both normal API requests and terminal WebSocket upgrades
  through the transport.
- [x] Match Discobox's ALPN/protocol (`discobox/http/1`, with the certificate
  variant where applicable); do not substitute an unrelated Iroh bridge.
- [x] Persist the adapter's peer identity across restarts, use ephemeral dialing
  keys with enrollment certificates, and expose `--peer-id` for setup. Confirm
  enrollment commands against upstream before documenting them.
- [ ] Enroll an adapter with the target live server and verify its permissions.
- [x] Check the Go version, native library packaging, extraction paths, and
  target platform requirements of the chosen Iroh dependency.
- [x] Test API requests and terminal attach over loopback Iroh with certificate
  identity checks, plus persistent/concurrent key creation and corrupt-key refusal.
- [ ] Test direct connectivity, relay fallback, terminal attach, interrupted
  connections, and restart recovery across two machines.

## Expose a hosted MCP service

- [ ] Add explicit public-origin and remote-listen configuration. Current
  listener, origin validation, and URLs assume a loopback HTTP service.
- [ ] Serve MCP over HTTPS Streamable HTTP and assets/terminal over HTTPS/WSS,
  with TLS termination and WebSocket forwarding configured for deployment.
- [ ] Keep stdio as the default local mode; remote hosting is the reason to
  select HTTP MCP, not a reason to remove stdio.
- [ ] Design authenticated client access compatible with the intended hosts.
  Separate client-to-adapter authentication from adapter-to-Discobox identity.
- [ ] Replace the local per-process browser capability with appropriately
  scoped, short-lived remote UI access. Keep credentials out of model-visible
  results and terminal content.
- [ ] Configure MCP App CSP/connect domains for the public origin and verify
  host sandbox behavior. Serve all frontend assets from the adapter.
- [ ] Persist manager selection and peer identity on the service's durable
  volume. Never recreate a manager merely because the adapter restarted.
- [ ] Review upstream peer permissions: an enrolled Iroh peer may have broader
  authority than a manager box's Discobox API/lead capability.
- [ ] Establish a single-user deployment before adding tenant routing. Any
  later multi-user service needs explicit user/peer/project/manager isolation.
- [ ] Address duplicate creation across separate adapter instances before
  running replicas; the current state-file reservation only coordinates users
  of the same state file. Input ownership is also local to one adapter.
- [ ] Choose hosting with a long-lived process, durable storage, and the
  network connectivity required for Iroh direct/relay operation.

## Manager workflow and skills

- [ ] Add narrowly scoped skills for discovering/opening the manager,
  reading before sending, submitting tasks, following wait cursors, reviewing
  worker progress, and handing control to the user.
- [ ] Keep orchestration and worker lifecycle in the backend manager. The
  desktop client supplies intent and reviews results through the adapter.
- [ ] Treat terminal output as untrusted session data and avoid retrying input
  whose delivery is uncertain.
- [ ] Exercise an end-to-end task from the connected desktop client: discover
  or create a manager, submit work, watch progress, take/release control, and
  reconnect after restarting the adapter.

## Development checks

Current frontend preparation requires the reference checkout's brand tokens:

```sh
npm ci
npm run build
npm run check
go test -race ./...
go vet ./...
go build -o bin/discobox-mcp ./cmd/discobox-mcp
```

The existing Go tests, race checks, vet, and build passed during scaffolding.
Frontend dependency installation and the UI asset build now pass, with a committed
lockfile. Native Codex terminal rendering has been confirmed; the expanded manager
workspace and native Claude rendering still need verification.
Iroh transport is scaffolded and tested over loopback;
two-machine and relay validation remain pending. Remote hosting is not implemented.

Local client configuration examples are under `examples/`. Omitting `--box`
reuses saved state, discovers one tagged manager, or creates one if none exists.
Discobox must already be running and reachable. Peer endpoints also need adapter
enrollment; see `docs/connection.md`. Go builds now require Go 1.27.1 or later.

## Task-first UX pass

- [x] Launch a task box with task/harness and per-run model/pool overrides.
- [x] Keep ordinary task boxes distinct from API-enabled coordinator managers.
- [x] Inspect output without waking; explicitly attach/detach the same terminal.
- [x] Send a box-scoped instruction from the overview; retain uncertain input.
- [x] Reconstruct an attention inbox from durable Discobox requests and reported
  program status; show connection coverage and refresh failures explicitly.
- [x] Keep legacy manager text tools and cached resource URIs compatible.
- [ ] Deliver repository Git sources as part of launch (URL alone is insufficient).
- [ ] Add human-authenticated approval actions or a supported console deep link.
- [ ] Add desktop transport/view and structured result artifacts.
- [ ] Add supported host notifications; open-widget polling is not background delivery.

## Direct box access

- [x] List all API-visible boxes rather than only adapter-tagged boxes.
- [x] Open/read/send/wait/interrupt/attach/archive ordinary untagged boxes.
- [x] Provide box-named text tools with boxId while preserving manager aliases.
- [x] Keep authorization, per-box routing and focus ownership checks.
- [ ] Implement the responsive grouped list from docs/boxes-ux-draft.html.

## Files and artifacts

- [x] Read-only file listing, ranged reads, typed MCP resource links and template.
- [x] Open regular files in a shared Files/Terminal workspace without needing a harness exec.
- [x] Sanitized Markdown/text preview, relative links, source view, copy URI and sharing with chat.
- [x] Version checks, explicit truncation, folder paging and bounded context updates.
- [x] Host-supported download plus authenticated browser fallback for files up to 1 MiB.
- [ ] Verify file views and native resource-link clicking in Claude and the OpenAI desktop app.
- [ ] Larger/streamed downloads, uploads and recursive transfers.

Canvas follow-up: explicit file/terminal sharing now uses ui/message with a
visible user message, bounded text content, acknowledgement/error feedback, and
a copyable fallback prompt when unsupported. Background context is metadata only.
Terminal fitting follows pane and host geometry without taking keyboard control;
a different human controller prevents passive resize. Short panes no longer
force a 240px terminal or 420px page minimum. Desktop confirmation pending.

Desktop prototype: open_box_desktop and a Desktop tab use bundled noVNC over
app-only desktop_open/read/action calls. VNC bytes and session handles are only
in _meta.desktop; app context contains identity/status/control receipts. Added
bounded buffering/backpressure, sequenced writes, focus control, scaling, and
stream cleanup. Real VNC rendering passed in an opaque isolated fixture iframe.
The sandbox API role denies TCP tunnels, so real task-box validation requires an
authorized host adapter. Native client validation remains pending; see docs/desktop.md.

Credential approval widget: added open_approval and list_approval_requests with
settled-history lookup, plus private app-only review/list/decide calls. Separate
use/delegation decisions, existing credential selection, bounded duration,
review confirmation, stale-snapshot protection, and single-use review tokens.
Model context receives status receipts only. No credential values are fetched.
Sandbox decisions retain existing delegation limits; human-only request kinds
remain in Discobox. Live denied request was read only; all mutation checks used
fixtures. Native client validation pending; see docs/approvals.md.
