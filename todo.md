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

These are scaffolded capabilities, not confirmation of desktop UI compatibility.
Frontend dependency installation and actual embedded rendering remain unverified.

## Package the terminal UI

- [ ] Finish installing and verifying the pinned `ghostty-web` and MCP Apps SDK
  dependencies; commit a lockfile after a successful install.
- [ ] Use Ghostty's web renderer/WASM; do not require a native Ghostty app.
- [ ] Make release builds bundle HTML, CSS, JavaScript, WASM, and dependency
  notices in the Go binary. Users should not install Node, npm, or UI packages.
- [ ] Remove the build-time dependency on the ignored `discobox/` reference
  checkout. Currently `scripts/build-ui.mjs` copies
  `discobox/assets/brand/tokens.css`; package a versioned copy with its license
  and provenance, or introduce a reproducible dependency source.
- [ ] Make release builds fail when UI assets are missing. Current Go builds
  permit a tools-only binary and report missing assets when opening the UI.
- [ ] Test `open_manager` in Codex and Claude for resource rendering, WASM,
  asset loading, WebSocket access, and host context updates.
- [ ] Keep the standalone page as a fallback. Test reconnecting to the same
  session and reopening the view without replacing the manager.
- [ ] Verify how each desktop host handles persistent panes. MCP cannot force
  a stationary view or keep a host iframe alive.
- [ ] Test multiple observers, resize behavior, input ownership, disconnects,
  and the absence of automatic input replay after an uncertain send.

## Add Iroh upstream transport

- [ ] Reuse Discobox's endpoint transport where practical. Inspect
  `discobox/pkg/endpoint/endpoint.go` and `iroh_transport.go`; decide whether
  importing that package or extracting a small shared module is appropriate.
- [ ] Support `discobox://<peer-id>` alongside the existing HTTP(S)/Unix socket
  endpoints. Carry both normal API requests and terminal WebSocket upgrades
  through the transport.
- [ ] Match Discobox's ALPN/protocol (`discobox/http/1`, with the certificate
  variant where applicable); do not substitute an unrelated Iroh bridge.
- [ ] Persist the adapter's peer identity across restarts and enroll it with
  the target Discobox server. Confirm current peer enrollment commands against
  the reference checkout before documenting operational steps.
- [ ] Check the Go version, native library packaging, extraction paths, and
  target platform requirements of the chosen Iroh dependency.
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
npm install
npm run build
npm run check
go test -race ./...
go vet ./...
go build -o bin/discobox-mcp ./cmd/discobox-mcp
```

The existing Go tests, race checks, vet, and build passed during scaffolding.
Frontend dependency fetching was blocked by DNS/network restrictions in the
development environment; a complete UI build and live host rendering have not
yet been validated. Remote hosting and Iroh integration are not implemented.

Local client configuration examples are under `examples/`. Omitting `--box`
reuses saved state, discovers one tagged manager, or creates one if none exists.
Discobox must already be running and reachable.
