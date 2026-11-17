# Design review: Discobox MCP adapter (calamari-steaks)

| | |
| --- | --- |
| Reviewed commit | `b70f8ff5b8ac1b37c795cf572e36928c5ee82b23` (`main`, "Keep upload permission checks portable on Windows"); matched `origin/main` at review time |
| Adapter version | Go module `github.com/cloudnautique/discobox-mcp-ui`, server/MCPB `0.11.0`, UI resource `ui://discobox/workspace-v14.html` |
| Upstream checked | Pinned `github.com/discobox-ai/discobox v0.19.2-0.20261111144542-d6c392a3cce7` from the Go module cache: `api/openapi/server.yaml`, `pkg/endpoint`, `docs/adr`. Bundled `@modelcontextprotocol/ext-apps 2.0.4` type definitions. Go MCP SDK `v1.8.0`. |
| Kind of review | Design and product UX, findings only. This review changes no implementation files. Correctness and security have their own reviews. |
| Date | 2026-11-17 |

## How the review was done

- I read every design document (`docs/*.md`, `todo.md`) and every implementation file under `cmd/`, `internal/`, `web/`, `scripts/` and `packaging/`, plus the CI workflows.
- I built the UI and binary locally in the gitignored `node_modules/`, `web/vendor/` and `bin/` directories. The build-time brand tokens came from the pinned module cache, and I deleted these build outputs afterwards. I then ran a stdio MCP probe (`initialize`, `tools/list`, `resources/list`, `resources/templates/list`, `connection_status`) with no backend. Measured results are in [Appendix A](#appendix-a-measured-surface).
- `go test ./...` and `npm test` (47 tests) both passed at this commit.
- Upstream capability claims below come from the pinned OpenAPI spec and `pkg/endpoint` source, not from memory. I did not run a live Discobox server, a native Claude/Codex host, a real Iroh peer, or a real VNC/service target (see [Uncertainties](#uncertainties-and-what-was-not-validated)).

Each finding is labelled with its origin:

- **[Adapter]**: a defect or design choice in this repository that it can fix.
- **[Platform]**: a limitation of the MCP Apps host or client. The adapter can only work around it.
- **[Upstream]**: a Discobox API or policy gap.

---

## 1. Executive summary

The adapter is carefully built where it counts:

- It never replays uncertain input or creation.
- Creation intents are journalled.
- Human focus ownership is enforced on the server.
- App-private traffic (pixels, logs, upload bytes, review tokens) stays in `_meta` or app-only tools.
- It keeps working when no backend is configured.

These invariants are sound. Keep them.

The weaknesses sit where the product meets real hosts and long sessions. Several flagship journeys work in a fixture iframe but are likely to break in a long chat with a native host:

1. **Launch → follow has no text path.** `launch_box` returns no `resumeAfter`, and `wait_box` refuses a missing cursor. The pinned upstream API accepts a wait with no cursor and supports `fromLastInput`. Text-only clients (Codex CLI, Claude Code) can only poll `read_box` after launching a task. (F1)
2. **Several tool calls are as long as, or longer than, the hosts' ~60 s budget.** These are `launch_box` (up to 3 min), `wait_box` (up to 60 s plus network) and terminal attach (45 s). The host gives up first, and the person sees a generic timeout instead of a useful state. (F2)
3. **Model-visible catalog payloads are unbounded and sent three times.** Each `list_boxes` / `list_attention` result carries every box, including archived ones, with its full task prompt (up to 64 KiB each). The same data appears in `structuredContent`, again in the SDK's JSON text fallback, and again in `_meta.connection`. Every widget instance also polls `list_attention` every 15 s. (F3)
4. **A box's terminal session is cached for the whole adapter process.** If a box's primary session is replaced, the UI says "Reopen the box". But reopening returns the same stale exec, so the box stays unreachable until the adapter restarts. (F4)
5. **Every rendered widget instance is a full client.** Each one auto-attaches (which can wake a box), takes one of 8 global terminal slots, polls the inbox, and overwrites model context on mount. A long conversation, or one that is reopened, will exhaust slots and wake old boxes. (F5)
6. **Changing the connection requires restarting the adapter.** That means disabling and re-enabling the extension. Upstream already provides a per-instance `IrohEndpoint` with `RoundTripper` and a layered `Diagnose`, so the adapter could apply the change in place. (F6)
7. **Service forwarding breaks ordinary web apps under load.** Each forward accepts at most 8 concurrent TCP connections and closes the 9th immediately. Every connection repeats two API reads plus a tunnel handshake. Quiet WebSocket/SSE (HMR) streams are cut after 5 minutes. Local ports change on every reopen. (F7)
8. **The surface is sprawling.** There are 53 tools (36 visible to the model, about 25 KB of schemas), a 4.6 KB instruction string, 10 legacy manager aliases, 16 listed copies of one UI resource, a second HTTP/WebSocket implementation of most features, and 16 narrative docs with stale facts. This cost falls on model tool selection and on maintenance. (F8, F9, F12, F13)

None of this calls for a rewrite. Phase 0 is a set of small, local fixes: F1, F2, the compact-payload part of F3, the re-resolve part of F4, and copy and doc fixes. Phase 1 adds a per-box view lease, one shared upstream event subscription, and a connection "generation" object. Those three resolve most of the remaining lifecycle issues. See [§6](#6-phased-recommendation).

---

## 2. Architecture overview

```text
 Claude Desktop / Codex / other MCP host
 ├─ model ── tools/call (36 model-visible tools) ─────────────┐
 └─ MCP App iframe(s) (one per UI-bearing tool result)       │   stdio (default) or loopback
      ui://discobox/workspace-v14.html  (~2.2 MB inline JS)   │   Streamable HTTP /mcp
      ui://discobox/connection-v2.html                        │
      ── app.callServerTool (17 app-only + public tools) ─────┤
      ── ui/update-model-context, ui/message, openLink ──> host
                                                              ▼
                                    discobox-mcp (Go, one process)
                                    ├─ connection.Controller   (profile, identity, probe subprocess)
                                    ├─ bridge.Server
                                    │   ├─ managers map[boxID]*managerConnection  (Client+exec pin, focus owner)
                                    │   ├─ appTerminals (≤8, 90 s idle)  desktops (≤4)  uploads (≤4)
                                    │   ├─ serviceForwards (≤8 × 8 conns, 127.0.0.1 listeners)
                                    │   └─ approvalReviews (single-use tokens)
                                    ├─ loopback HTTP listener (always on): /, /setup, /api/*, /terminal WS, [/mcp]
                                    └─ discobox.Client (untyped JSON over pkg/endpoint transport)
                                                              │ unix:// | npipe:// | http(s) | discobox://<peer> (Iroh)
                                                              ▼
                                            Discobox server  →  pool  →  boxes (primary exec, files, services, VNC)
```

### Traffic planes

| Plane | Carrier | What travels | Visible to the model? |
| --- | --- | --- | --- |
| Model tools | `tools/call` from the model | Catalog, screen text, files on request, service metadata, setup status | Yes. Text content plus `structuredContent`. Some UI tools also duplicate their payload into `_meta.connection`. |
| App-private | `callServerTool` to tools with `_meta.ui.visibility=["app"]` | Terminal bytes (base64 in `structuredContent`), VNC bytes, logs, upload chunks and handles, review tokens (all in `_meta`) | Not by contract. Hosts enforce visibility and may still log this traffic. |
| App → model | `ui/update-model-context` (last write wins, deferred to the next turn), `ui/message` (visible user turn) | Action receipts and active box ID. Explicit shares of file or terminal content. | Yes, by design. |
| Standalone | Bearer-authenticated loopback HTTP and WebSocket | A parallel implementation of catalog, files, services, terminal and setup | No |

### Ownership

- **Durable state upstream:** boxes, approvals, files and services all live in Discobox.
- **Durable state in the adapter:** the connection profile, the Iroh identity and the creation journals, all under `os.UserConfigDir()/discobox-mcp`.
- **Process-lifetime state:** connection pins, focus ownership, views, forwards and uploads. All of it is lost when the adapter restarts.
- The widget holds UI state in about 25 module-level variables in `web/app.js` and keeps nothing between mounts.

---

## 3. Principal user journeys (traced)

Each journey follows the code path. ⚠ marks a mismatch between the intended experience and the actual state transitions.

### J1. First launch from the MCPB, local Discobox

1. The manifest launches `--browse --transport stdio` (`packaging/mcpb/manifest.json`). With no profile, `cfg.Endpoint = unix://` / `npipe://`. `NewCatalog` does not dial, so startup succeeds even if Discobox is not running yet (`cmd/discobox-mcp/main.go:95-181`). Good: if Discobox starts later, the next call works without a restart.
2. The person says "connect to Discobox". The host renders `open_connection_setup`. **Test** runs `connection_test` in a subprocess (`cmd/discobox-mcp/onboarding.go:37-68`), which maps the result to one of 7 coarse states.
3. ⚠ If the test succeeds, the widget says "Ask chat to **list my Discobox boxes**" (`web/onboarding.html`). The person has to retype a request. The widget could offer a button that sends that message or lists the boxes itself (F6).
4. ⚠ `connection_status` still reports `not_tested` after real traffic has succeeded, because the status only reflects explicit tests (`internal/connection/connection.go:156-161`) (F14).
5. ⚠ The private standalone link and setup link are printed only to stderr, which ends up in host logs. An MCPB user cannot realistically reach the "authenticated browser fallback" that the docs mention for downloads and setup (F9).

### J2. Remote Iroh onboarding

1. **Remote**: the person enters `discobox://<server-id>`, then **Show my peer ID** loads or creates the identity. They copy `discobox --server unix:// admin peer add <id> --name discobox-mcp` (`connection.go:178-180`).
2. The operator enrolls the ID. The person clicks **Test**, which probes in a subprocess because Iroh is configured process-wide. They click **Save**.
3. ⚠ The status becomes `restart_required`. Applying the profile means disabling and re-enabling the extension (`connection.go:218-240`, `docs/connection-setup-widget.md`). Until then, every backend tool fails with "Connection setup is required … restart this adapter" (`internal/bridge/onboarding.go:95-118`). The restart is not forced by upstream: `pkg/endpoint` exposes `NewIrohEndpoint(cfg)`, `(*IrohEndpoint).RoundTripper(id, base, direct...)` and `(*IrohEndpoint).Diagnose(...)` (`pkg/endpoint/iroh_transport.go:170,601`, `diagnose.go:218,263`). Only the process-global `ConfigureIroh` refuses a second configuration (`iroh_transport.go:529-541`) (F6).
4. ⚠ A failed test collapses to `unreachable` / `unauthorized` / `forbidden`. Upstream `Diagnose` distinguishes native library, relay, peer reachability, admission and API layers. Those are exactly the distinctions this journey needs: "timeout ≠ denial", "server has no Iroh listener" (F6).
5. [Upstream] Enrollment grants default-user `ScopeAll`. There is no pairing or approval request. The docs disclose this correctly.

### J3. Launch a task and follow it (widget and chat)

1. Widget **Start box** → `launch_box`. On the server, `CreateManager` lists the full catalog as a reachability check, journals the intent, POSTs the create, then **waits for the primary exec** for up to 3 minutes (`internal/bridge/managers.go:78-100`, `internal/discobox/manager.go:219-226`). The widget's `callServerTool` uses the ext-apps default 60 s timeout.
2. ⚠ On a cold pool or image pull, the widget times out at 60 s while the server keeps going. The button turns into "Check same creation", which works because the journal is keyed. But the person watched an opaque "Starting box…" for a minute and then got an error, even though the create had probably succeeded (F2).
3. If launch returns in time, the widget calls `list_boxes` again and `start()` attaches the terminal. Good.
4. ⚠ From chat, the model has the `boxId` but no `resumeAfter`, so `wait_box` cannot be called (`internal/discobox/client.go:243-246`). Upstream `/execs/{id}/wait` treats a missing `after` as "from the start of the wait" and also supports `fromLastInput`, `quietSeconds` and `hookEvents` (`server.yaml:1380-1410`, `12776-12822`). The model is pushed into polling `read_box` (F1).

### J4. Open an existing box from chat; text-only clients

- `open_box` → `connectManager` → `s.manager()`. On a cache miss this reads the box and resolves the primary exec, then caches it (`managers.go:33-62`). ⚠ A box whose harness has not started its primary exec yet fails `open_box` outright (`errNoPrimary`). Files and Desktop do not need an exec. The workspace could open in a "starting" state instead (F2/F4).
- In text-only clients, `open_box` still returns the full structured payload as text, so it works. But the model sees about 25 KB of tool schemas and 4.6 KB of instructions, including 10 manager aliases and 16 "SHOW UI" tools that do nothing visible there (F8).

### J5. Person takes the terminal, then hands back to the assistant

- Focusing the terminal claims ownership through `terminal_action claim`. Blur, window blur or hiding releases it, after queued input (`web/focus-control.js`). Model writes are refused while a person holds control (`internal/bridge/server.go:95-109`). Desktop focus shares the same owner (`app_desktop.go:128-136,161-168`). This is well designed, and the server-side enforcement is right.
- ⚠ The release happens on blur, but the assistant only learns about it through `updateModelContext`, which is deferred to the next user turn. If the person clicks into the chat composer and types "continue", the model gets the latest receipt, so this works. If the model is mid-turn and calls `send_box` while the person is typing, it gets an error with no retry hint. A `holder`/`retryAfter` field in that error would let the model wait instead of failing the turn (F14, minor).
- [Platform] On Windows and Linux, Ctrl+V is always intercepted as paste (`web/terminal-clipboard.js:5`), so remote programs never receive a literal Ctrl+V. This is a common browser-terminal trade-off. Document it, or offer Ctrl+Shift+V-only paste as an option.

### J6. Files: browse, preview, share, upload, download

- Browse and preview go through `list_box_files` (public) and app-only `file_read`/`file_stat`. Markdown is sanitized. The open file's version is checked every 5 s while visible. Explicit sharing uses a visible `ui/message`, bounded to 64 KiB. This is clean, and the private/public split is right.
- ⚠ Uploads refuse existing files and cannot create folders. The docs say "choose a different name" (`docs/files.md:117-121`). Upstream `PUT files/content` supports `ifExists=replace`, `ifVersion` (a version-checked replace) and `parents`, and the API also has `files/mkdir|move|copy|remove|archive` (`server.yaml:13347-13603`). So this limit is in the adapter, not upstream (F11).
- ⚠ In the host, download goes through `app.downloadFile(resource_link)`, so the host then reads a `discobox-file://` resource capped at 1 MiB. Folder export via `files/archive` is not available (F11).

### J7. Desktop

- `open_box_desktop` → app-only `desktop_open/read/action` with noVNC. Bytes stay in `_meta.desktop`, reads long-poll for up to 12 s, and `Adapt resolution` applies only while this view holds input. The design is consistent with the terminal.
- [Upstream] The sandbox role denies TCP tunnels, so `--local` cannot show desktops. The docs state this.
- [Platform] Every framebuffer update is a JSON-RPC tool result relayed by the host. Throughput and latency depend on the host bridge, and hosts may log these payloads (unverified; see [Uncertainties](#uncertainties-and-what-was-not-validated)).

### J8. Services, forwarding and browser access

- `list_services` → **Forward port** → `open_service` binds `127.0.0.1:0` and probes the tunnel. **Open via chat app** or **Open in default browser** (`service_open_browser`) opens it.
- ⚠ The browser loads a typical dev-server page. Each accepted TCP connection does a fresh `Services()` call (two API GETs: box, then services) plus a new framed tunnel (`internal/discobox/services.go:196-219`). More than 8 concurrent connections per forward are **closed immediately** (`internal/bridge/services.go:239-243`). Browsers routinely open 6 or more parallel connections, plus HMR WebSocket, SSE and prefetch. Expect sporadic resets and failed asset loads, especially over Iroh.
- ⚠ Connections with no traffic for 5 minutes are cut (`services.go:262-268`). HMR WebSockets are often silent that long, so the page reconnects or reloads.
- ⚠ Each reopen and each adapter restart gets a new random port (`services.go:185`). Bookmarks, OAuth redirect URIs and localhost cookie jars break. The upstream CLI `discobox proxy` uses the nearest free port and stable bindings (`docs/service-forwarding.md:492-497`) (F7).

### J9. Needs you / approvals

- The inbox is rebuilt from upstream requests and program state, and each widget refreshes it every 15 s (`web/app.js:561-607`). The approval decision uses explicit confirmation and a single-use token. Coverage gaps are stated honestly.
- ⚠ Every widget instance in the conversation polls separately (F5). Upstream offers `GET /api/projects/{p}/events`, an SSE stream of sandbox create, update and delete events (power, display and program state, tags) that sandboxes may read and that resumes by cursor (`server.yaml:8752-8795`, ADR 26-10-29-316). One adapter-level subscription could feed every view and remove most catalog polling (F3/F5).
- [Platform] No background notification is possible. The docs state this.

### J10. Session lifecycle: sleep, wake, restart, archive, adapter restart

- **Detach or close**: the view is released and the box keeps running. Correct.
- ⚠ **Box archive**: one-way in this UI. Archived boxes are listed (with "Show archived") but have no Restore action, although upstream has `POST …/unarchive`. There are no Start/Stop/Restart box controls either (`server.yaml:11164,11359,11513,11555`) (F10).
- ⚠ **Primary session replaced** (harness exit/restart, box restart/repair/upgrade): the old exec stays pinned in `s.managers` until archive or adapter exit. That entry is deleted only in `archiveManager` (`managers.go:121-123`). "Reopen" returns the same entry (F4).
- **Adapter restart**: forwards, views and ownership are lost. Creation journals and the profile survive. The documentation is accurate.

---

## 4. Findings

Priorities:

- **P1**: breaks or badly degrades a principal journey in realistic use.
- **P2**: a significant UX or maintainability cost.
- **P3**: polish.

Each finding gives the origin, trigger, impact, evidence, proposal, trade-offs, compatibility impact and acceptance criteria.

### F1 (P1) [Adapter]: Launch-and-follow has no cursor; the wait contract is narrower than upstream

- **Trigger and current behavior.** `launch_box`, `open_box` and `create_manager` return `{view, session, managerId, boxId, transport}` with no `resumeAfter` (`internal/bridge/managers.go:68-74`). `wait_box`/`wait_manager` require a non-empty `after` (`internal/discobox/client.go:243-246`). The only conditions they wait for are `exit` plus program states `idle|done|blocked|error` (`client.go:250-253`).
- **Impact.** "Start a task and tell me when it's done" cannot be done with the text tools unless the model sends something first. Text-only hosts (Codex CLI, Claude Code) depend entirely on these tools. The model ends up polling `read_box` and guessing.
- **Upstream support.** `SandboxExecWaitUntil.after` is optional ("Absent, and without fromLastInput, the start of the wait"). The wait also accepts `fromLastInput`, `quietSeconds` and `hookEvents` such as `Stop`/`Notification`, matched across harnesses (`server.yaml:1380-1410`). Every wait answer carries `resumeAfter` (`server.yaml:12778`).
- **Proposal.**
  - Make `after` optional, and default to `fromLastInput` when a send is known.
  - Return `resumeAfter` from `launch_box`/`open_box` by performing a 1 s wait or by reading the current cursor.
  - Add an optional `until` enum: `turn_end` (`hookEvents:["Stop"]` plus done/idle), `needs_input` (blocked/Notification), `quiet:N`.
- **Trade-offs.** Waiting without a cursor can miss events between calls. Always return `resumeAfter` and document "pass it back".
- **Compatibility.** Additive. Existing callers that pass `after` are unchanged.
- **Acceptance criteria.**
  - `launch_box` followed by `wait_box(boxId)` with no `after` returns when the first turn ends.
  - A fixture test covers absent `after`, `fromLastInput` and a hook event.

### F2 (P1) [Adapter + Platform]: Tool calls are as long as or longer than host budgets

- **Trigger.**
  - `launch_box` waits up to 3 minutes for the primary exec (`managers.go:79`, `manager.go:219-226`).
  - `wait_box` allows 60 s server-side (`client.go:247`) plus transport latency.
  - `terminal_open` waits 45 s (`app_terminal.go:23`).
  - Per-request HTTP timeout is 75 s (`client.go:116`).
  - Ext-apps `callServerTool` defaults to 60 000 ms. Codex defaults `tool_timeout_sec` to 60 (the repository example raises it to 90 in `examples/codex-autocreate.toml`).
- **Impact.** The host's deadline fires first. The model or widget sees a generic timeout instead of `timedOut:true` or a "provisioning" state. On launch, the person sees an error after a likely successful create.
- **Proposal.**
  - (a) `launch_box` returns as soon as the create is committed and the box ID is journalled: `{boxId, state:"provisioning"}`. The widget shows a provisioning workspace and attaches when the primary exec appears. That trigger can come from F3's event stream or a bounded app-side poll. `open_box` likewise succeeds without an exec and reports `terminal:"starting"`.
  - (b) Clamp `wait_box` to about 45 s server-side, or accept the caller's budget, and say so in the schema.
  - (c) Pass explicit `{timeout}` options to `callServerTool` where an app call is intentionally long, such as long-poll reads.
- **Trade-offs.** Two-phase launch adds a visible "starting" state, which is more honest. Never retry creation; the existing journal already guarantees that.
- **Compatibility.** Behavior change for `launch_box` callers expecting a ready session. Keep `session` in the response when it is already known.
- **Acceptance criteria.**
  - With a 90 s simulated provisioning delay, launch returns in under 10 s, the widget shows "starting", and it attaches automatically.
  - No path issues a second create.
  - `wait_box` never exceeds 50 s wall-clock.

### F3 (P1) [Adapter]: Model-visible catalog payloads are unbounded and duplicated; inbox polling multiplies them

- **Trigger and current behavior.**
  - `listBoxes` includes every non-deleted box, including archived ones. For non-manager boxes, `Task` is the full concatenated prompt, up to 64 KiB per box (`internal/discobox/catalog.go:113-123`).
  - `workspaceResult` puts the same map in `_meta.connection` and in `structuredContent` (`managers.go:148-150`). Because `Content` is nil, go-sdk also serializes it as a text block (`go-sdk server.go:477-481`).
  - `Attention` embeds the whole box list again as `inbox.boxes` (`internal/discobox/attention.go:39`).
  - Each widget calls `list_attention` every 15 s (`web/app.js:606`).
- **Impact.** In a project with 100 boxes and moderate prompts, one `list_boxes` can put hundreds of KB into model context. This crowds out the conversation, raises cost, and makes the model likelier to pick the wrong box. Polling repeats the upstream list on every tick for every widget.
- **Proposal.**
  - The model-visible result becomes a compact summary: id, name, role, state, program state, `observedAt`, a task preview of at most 200 characters, archived boxes excluded unless `includeArchived`, a `limit`/`cursor`, and an optional `state`/`needsAttention` filter.
  - Text content becomes a short table instead of a JSON dump.
  - The full catalog for the widget moves behind an app-only `catalog_read` that returns it in `_meta`. Remove the duplicate `_meta.connection` on the model path, where the widget only needs routing fields.
  - Replace per-widget polling with one adapter-level subscription to `GET /api/projects/{p}/events` (`server.yaml:8752-8795`). Keep a cached catalog and inbox in the adapter, and have widgets read the cache cheaply.
- **Trade-offs.** The model needs `read_box`/`inspect_box` to see a full task. The SSE subscription is one long-lived stream per adapter, and its reconnect and reset semantics are specified upstream.
- **Compatibility.** Structured field names stay the same; the model sees fewer of them. Older widget versions reading `inbox.boxes` need the app-only replacement, so bump the resource version.
- **Acceptance criteria.**
  - A model-visible `list_boxes` stays under 8 KB for 100 boxes with 64 KiB prompts.
  - Two open widgets cause at most one upstream catalog request per interval, or none with SSE.
  - A snapshot test asserts the size bound.

### F4 (P1) [Adapter]: The terminal session pin never refreshes; "Reopen" cannot recover a replaced session

- **Trigger.** `s.manager(id)` returns any cached `managerConnection` (`managers.go:40-45`). The entry is removed only on archive (`managers.go:121-123`). `ConnectManager` resolves `primary` once (`catalog.go:167-180`, `client.go:189-219`). When a stream reports `Exit`, the widget says "This box's terminal session changed. Reopen the box…" (`web/app.js:92-96, 311`). Reopening goes through the same cache and gets the same exec. `canReconnect` stops retrying on `sessionEnded`/`sessionChanged` (`web/stream-reconnect.js:11-13`).
- **Upstream evidence that replacement happens.** Exec listings are "live and ended alike". `displayName` follows "the newest primary session on the row" (ADR 26-10-31-618). Restart, repair and upgrade endpoints exist. I have not established whether sleep/wake preserves exec IDs (see Uncertainties).
- **Impact.** After a harness exit or box restart, text tools, the terminal and send all target a dead exec until the adapter (that is, the extension) restarts. No workaround exists in the UI.
- **Proposal.** Treat the pin as a *generation*.
  - On an explicit `open_box`, Reconnect, or `terminal_open` after `ended`, call `ResolvePrimary` again.
  - If the exec differs and no human holds control and no write is in flight, swap atomically. Return `{previousExec, exec, sessionChanged:true}` and send a `session_changed` context receipt.
  - Never move queued input. In-flight writes fail as they do today.
  - `read_box`/`send_box` with a stale exec (404/409/ended) return a typed error, "session replaced; call open_box to adopt the new session", and do not swap silently.
- **Trade-offs.** It keeps the "no silent retarget" principle, because the swap only happens on an explicit reopen.
- **Compatibility.** Additive.
- **Acceptance criteria.** In a fixture, the primary exec changes. Then `open_box` reports the new exec, the terminal attaches, `send_box` succeeds, and no input queued before the change was sent to either exec.

### F5 (P1) [Adapter, constrained by Platform]: Every widget instance is a full, auto-attaching client

- **Trigger.** Every UI-bearing tool result (16 tools) creates an independent App instance. On mount, the instance does all of the following:
  - pushes `workspace_ready` context (`app.js:536`);
  - starts the 15 s inbox loop (`app.js:539, 606`);
  - for terminal views, attaches immediately. Attaching can wake the box (`app.js:392, 704`, `docs/managers.md:143-150`) and takes one of **8 global** terminal slots (`app_terminal.go:92`).

  Old instances keep long-polling, which keeps resetting their 90 s idle timer, so they never expire while mounted. `show_box_view` explicitly "may open a new widget" (`views.go:18`).
- **Impact.** Whether this happens depends on the host:
  - In a conversation where the person opened boxes 8 times, or reopens a saved conversation that re-renders old widgets, new opens fail with "too many terminal views". Old boxes are woken. The adapter carries N inbox pollers.
  - Each instance's `updateModelContext` overwrites "last state". Whether hosts keep one context per instance or per app is unspecified, so the model may receive stale `activeBoxId`s.
- **Proposal.**
  1. **Attach only when visible.** Gate attach and polling on `IntersectionObserver` plus `document.visibilityState` (and the host `displayMode` when available). Detach after a grace period when hidden.
  2. **One lease per box per adapter.** A newer instance that attaches the same box takes over the stream. The older instance shows "Open in a newer view · Resume here". Leases go through a server-side `view_claim` on the existing app tools.
  3. **LRU eviction instead of refusal** once the 8-view cap is hit: evict the least recently interacted *observer* view, never one holding control.
  4. Push model context only on user action, not on mount.
  5. Serve the inbox from the shared adapter cache (F3).
- **Trade-offs.** More server-side view bookkeeping. LRU eviction can surprise someone watching two boxes side by side, so show the reason in the evicted view.
- **Platform notes.** MCP has no way to address an existing widget. `select_view` only works where hosts expose app-registered tools (`app.js:516`). Old widgets mounting again on conversation reload is host behavior.
- **Acceptance criteria.**
  - In an opaque-frame harness with 12 widget instances of 5 boxes, at most the visible instances attach.
  - Opening a 9th distinct box succeeds.
  - Reloading a conversation with old widgets wakes no box until it is scrolled into view.
  - Exactly one inbox fetch happens per interval.

### F6 (P1) [Adapter]: Connection changes require a restart; diagnostics are coarse; onboarding ends at a dead end

- **Trigger and current behavior.** `ConfigurePeer` installs the process-global Iroh transport (`internal/discobox/peer.go:122-138`). A saved profile becomes `pending`, and the status asks for a restart (`connection.go:218-240`). Probes run in a child process (`cmd/discobox-mcp/onboarding.go:37-68`) and map errors to seven states (`connection.go:244-266`).
- **Impact.** For MCPB users, "restart" means Settings → Extensions → disable and re-enable. The person also has to know to do it, and every backend tool fails until they do. Remote onboarding, the hardest journey, has the most friction.
- **Upstream capability (verified).** `NewIrohEndpoint(cfg)` creates an endpoint instance. `(*IrohEndpoint).RoundTripper(id, base, direct...)` builds a transport bound to that instance. `(*IrohEndpoint).Diagnose(ctx, raw, opts)` and package-level `Diagnose` report each layer: address, native library, relay, peer connect, admission, health (`pkg/endpoint/iroh_transport.go:170,601`, `diagnose.go:1-60,218-263`).
- **Proposal.**
  - Introduce a `ConnectionGeneration {cfg, irohEndpoint, client}` held by an atomic pointer on `bridge.Server`.
  - **Apply** builds a new generation in-process, probes it, swaps it in, and leaves existing views pinned to their old generation until they close. Prompt only if live sessions exist.
  - Run probes in-process with `Diagnose` and show its steps in the widget, including relay warnings and "peer reachable but not admitted".
  - After a successful apply, offer **Show my boxes**. It sends `ui/message` "Show my Discobox boxes" where `message` is supported, and otherwise displays copyable text.
  - Keep launcher-pinned (managed) connections read-only, as today.
- **Trade-offs.** Each generation holds an Iroh endpoint, so close old ones when their last view ends. It removes the subprocess isolation, which is acceptable once no global state is involved.
- **Compatibility.** The profile format is unchanged. `restartRequired` stays for managed or pinned cases.
- **Acceptance criteria.**
  - Save a remote profile and test it, then `list_boxes` succeeds with no restart.
  - An open terminal on the previous generation keeps working until it is closed.
  - The widget shows per-layer diagnosis for a missing relay and for a non-admitted peer.

### F7 (P1) [Adapter]: Service forwarding is fragile for real web apps

- **Trigger and evidence.**
  - Each local connection calls `DialServicePort`, which does `Services()` (box GET plus services GET) and then a tunnel dial (`internal/discobox/services.go:80-152, 196-219`).
  - Above 8 concurrent connections, new ones are accepted and closed at once (`internal/bridge/services.go:239-243`).
  - Each connection has a 5-minute read/write deadline that only traffic renews (`services.go:262-268`).
  - The port is ephemeral (`services.go:185`).
- **Impact.** Slow first paint, resets under a normal browser's parallelism, HMR drops after 5 quiet minutes, and broken callback URLs and bookmarks after a reopen or restart.
- **Proposal.**
  - Validate the destination when the forward opens and in the existing 30 s `watchForward`. Do not validate per connection. Keep the per-connection identity check only when the cached validation is older than, say, 30 s.
  - Raise the per-forward limit to about 32 and *queue* briefly instead of resetting.
  - Drop per-connection idle deadlines for established streams. Rely on the forward-level idle expiry, plus TCP keepalive on the local side.
  - Prefer binding the remote port number locally when free, then the nearest free port (matching upstream CLI `discobox proxy`). Remember the mapping per (box, port) for the process lifetime.
- **Trade-offs.** Fewer authorization rechecks per connection. Revocation still closes the forward within 30 s, the same window as today's watch loop.
- **Acceptance criteria.**
  - A fixture HTTP server with 20 parallel asset requests plus a WebSocket idle for 10 minutes: no resets.
  - Reopening the same forward returns the same address.
  - Revoking box access closes the forward within 35 s.

### F8 (P2) [Adapter]: Tool and resource surface sprawl blurs model tool choice

- **Evidence (measured, Appendix A).**
  - 53 tools: 36 visible to the model (about 25 KB of schemas), 17 app-only.
  - 16 UI-bearing tools.
  - A 4,663-character server instruction string, about 695 words, covering every subsystem (`internal/bridge/tools.go:31`).
  - Ten manager-named aliases duplicate box tools. The aliases' descriptions point at each other ("use open_box or open_manager").
  - `resources/list` returns 16 entries: 14 aliases of the same workspace HTML and 2 setup aliases (`tools.go:91`, `onboarding.go:84`).
  - Every visible tool declares an output schema, but most are permissive `object` schemas from `map[string]any`.
- **Impact.** Token overhead on every turn. The model has to choose between near-synonyms. In text-only clients, 16 "SHOW UI" tools do nothing visible.
- **Proposal.**
  - Canonical box vocabulary only: `list_boxes, launch_box, open_box, read_box, send_box, wait_box, interrupt_box, archive_box`.
  - Put the `*_manager` aliases behind `--legacy-manager-tools`, off by default for new MCPB installs. Keep it on for configured legacy launchers for one release.
  - Merge `inspect_box` into `read_box`.
  - Fold `open_manager`/`connect_manager`/`show_box_view` into `open_box(boxId, view?, filePath?)`.
  - Serve historical `ui://` URIs through one *unlisted* handler, for example a resource template, so restored sessions still read them but `resources/list` shows one entry.
  - Move subsystem guidance into tool descriptions. Keep instructions to about 1 KB of cross-cutting rules: untrusted output, no retries, focus ownership.
  - Add a snapshot test of tool names, schemas and visibility.
- **Compatibility.** Restored conversations that call a removed alias get "tool not found". Keep the flag, and keep the aliases served but unlisted if the host allows that. Hosts cache descriptors per session.
- **Acceptance criteria.**
  - Model-visible schemas plus instructions are at most 10 KB.
  - `resources/list` returns at most 2 UI resources.
  - All old resource URIs still resolve with `resources/read`.

### F9 (P2) [Adapter]: The standalone HTTP/WebSocket path duplicates the app bridge and is always on

- **Evidence.**
  - The loopback listener always starts, even in stdio/MCPB mode (`main.go:162`).
  - It serves `/api/managers/*`, `/api/files/*`, `/api/services/*`, `/api/connection/*` and the `/terminal` WebSocket (`server.go:149-206`, `managers.go:180-265`, `files.go:181-232`, `terminal.go`).
  - `web/app.js` keeps parallel branches for app and standalone in `send`, `attach`, `workspaceCall` and the services/files callers (`app.js:126-159, 186-251, 396-415`).
  - Desktop and approvals do not exist on the standalone path.
  - MCPB users only see the private link in stderr logs.
- **Impact.** Every feature is implemented and tested twice, with subtle divergence. There is an extra local attack surface (bearer, Host and Origin protected; not assessed here). The fallback is effectively unreachable for the main install path.
- **Proposal.** Either:
  - (a) Make standalone opt-in (`--browser-ui`) and drop it from the MCPB manifest; or
  - (b) Reimplement it as a small in-page `AppBridge` shim that calls the same MCP tools over the existing Streamable HTTP `/mcp` handler, mounted on the loopback listener with the same bearer. Then delete `/api/*` and `/terminal`.

  (b) keeps one dispatch path and gives standalone the desktop and approvals for free.
- **Acceptance criteria.**
  - No `/api/*` feature handlers remain, or they are off by default.
  - `app.js` has a single transport abstraction.
  - The standalone page passes the same UI test suite.

### F10 (P2) [Adapter]: Lifecycle actions are incomplete, and error copy targets the wrong surface

- **Archive is one-way.** Archived boxes are shown but have no Restore action, though `POST …/unarchive` exists (`server.yaml:11164`). There is no box Start/Stop/Restart (`server.yaml:11359,11513,11555`). The archive "Confirm archive" state never resets on that card (`app.js:453-457`).
- **CLI flag advice reaches the widget and model.** Errors such as "reconnect with --box ID" and "use --box %s --exec %s" (`internal/discobox/manager.go:110,212,225`), and "could not attach to manager" (`internal/discobox/stream.go:56`), appear in widget and model output, where flags mean nothing.
- **Proposal.** Add `restore_box` and box power actions (explicit, never retried). Make errors typed (`code`, `boxId`, `requestId`, `nextAction`) and render copy per surface. Reset confirm state after a timeout.
- **Acceptance criteria.** Archive → Show archived → Restore → Open works. No user-visible string mentions CLI flags outside CLI startup.

### F11 (P2) [Adapter]: File transfer stops short of what upstream supports

- **Current behavior.** Uploads cannot replace a file or create a folder. Downloads in the host are capped at 1 MiB. There is no folder export (`docs/files.md:51-56,117-121`).
- **Upstream.** `PUT files/content` takes `ifExists=replace|fail`, `ifVersion` and `parents`. The API also has `files/mkdir|move|copy|remove` and a streaming `files/archive` (`server.yaml:13136-13603`).
- **Proposal.**
  - Near term: **Replace…**, which shows the current version and size and requires confirmation, then writes with `ifVersion`. Add **New folder**, or upload with `parents=true` into a typed path.
  - Longer term: chunked downloads to a host download API where one exists, and folder export via `files/archive`.
- **Acceptance criteria.** Replacing a file changed elsewhere fails with 412 and an explanation. Uploading into a new folder succeeds.

### F12 (P2) [Adapter]: Maintainability hot spots

- **`web/app.js` (798 lines).** About 25 mutable module-level variables drive an implicit state machine: catalog/workspace/tab, attaching/connected, owned/claim, generation counters. That state machine is untested; Node tests cover only the helper modules (`web/*.test.js`). Most lifecycle bugs above live here. Extract a pure `workspaceState` reducer with explicit transitions: open, switch tab, attach, ended, session changed, lease lost, evicted. Unit-test it.
- **Untyped upstream client.** `internal/discobox` parses `map[string]any` by hand everywhere. The pinned module ships generated OpenAPI types and a client (`api/gen`, `api/model`). Adopting at least the model types for the endpoints in use would turn upstream drift into compile errors. *Trade-off:* a larger dependency surface and `ogen` client ergonomics. Partial adoption (types only) is fine.
- **Mixed terminology in code.** `Manager` is the catalog type for all boxes (`catalog.go:15-17`), and `managerConnection`/`managerId` appear throughout. Rename internally once F8 settles the external vocabulary.
- **Build dependency.** It is still on the ignored `discobox/` checkout for `tokens.css` (`scripts/build-ui.mjs:21-22`), as already noted in `todo.md`. The same file is available in the pinned Go module (`assets/brand/tokens.css`). Copy it from the module cache path resolved by `go list -m -f '{{.Dir}}' github.com/discobox-ai/discobox`, so the reference checkout is no longer needed.
- **Asset size.** The bundled workspace HTML is about 2.2 MB of inline JS (Ghostty WASM, noVNC, marked, DOMPurify, SDK). It is re-sent on every `resources/read`. That is acceptable, but noVNC could load lazily into the Desktop tab as a second resource only if hosts allow it; they generally do not. Keep it inline, and track the size in CI.

### F13 (P2) [Adapter]: Documentation sprawl and drift

- **Evidence.** 16 docs written as dated narratives, each with "Verification" paragraphs. Stale facts include:
  - `docs/connection.md:78` says the canvas is `manager-v2.html`.
  - `docs/managers.md` names `managers-v3`/`workspace-v4`; `docs/mcpjam-evaluation.md` names `v12`. The current resource is `v14`.
  - `examples/codex-autocreate.toml` says omitting `--box` "creates one", but browse is now the default.
  - The `--box` flag help also describes auto-create (`main.go:45`), and `--browse` help says "manager picker".
  - The footer default reads "Desktop and service views coming later" (`web/index.html:43`).
- **Proposal.**
  - One current-contract document: tools, visibility, limits, timeouts, resource URIs. Generate it from, or check it with, the F8 snapshot test.
  - Short ADRs for decisions such as "no input replay" and "app-private `_meta`".
  - Move the dated verification narratives to `CHANGELOG.md`.
  - Fix the stale strings above.

### F14 (P3) [Adapter]: Status and error affordances for the assistant

- `connection_status` reports only explicit tests. Also record passive health: the last successful or failed backend call and its time.
- `send_box` refused by human focus should return a structured `{code:"human_has_control", since}`, so the model can wait and re-check instead of failing the turn (`server.go:101-104`).
- `openForward`'s busy error says "connection tests are busy" (`services.go:157`). It should name forwarding.

### F15 (P3) Polish

- [Adapter] The inline workspace is capped at 520 px. The header, tabs and toolbar leave about 257 px of terminal, roughly 14 rows (`docs/managers.md:301-307`). Collapse the header into the tab row inline, or default task details to hidden.
- [Adapter] The MCP server name `discobox-manager` (`tools.go:30`) differs from the MCPB `discobox-mcp`/"Discobox". Align it, since clients show it in tool attribution.
- [Platform/Adapter] On Windows and Linux, Ctrl+V can never reach the remote (`terminal-clipboard.js:5`). Document it, and consider a setting.

---

## 5. Client and platform limitations (not adapter defects)

| Limitation | Effect | Current handling | Recommendation |
| --- | --- | --- | --- |
| MCP cannot target or reuse an existing widget; app-registered tools (`select_view`) depend on host support | View changes from chat may create new instances | `show_box_view` fallback; receipts | Design for multiplicity (F5) |
| `updateModelContext` is last-write-wins and deferred to the next user turn | No live push to the model; stale context from old instances | Bounded receipts | Push on user action only; include an instance timestamp |
| Host tool-call budgets (~60 s) and iframe lifecycle (unmount/suspend off-screen) vary | Long calls fail opaquely; old widgets may remount | 45 s attach bound | F2, F5 |
| App-only visibility is host-enforced; hosts relay and may log app traffic | Terminal bytes, pixels and uploads cross the host bridge | Documented | Validate per host; consider a "private transport" note in setup |
| Text-only clients (Codex CLI, Claude Code) render no UI | UI tools only return data | Text fallbacks exist | F1, F8 make the text path complete and lean |
| Sandbox role denies TCP/UDP tunnels [Upstream] | No desktop or forwarding in `--local` | Clear errors | Upstream ask: a delegated tunnel scope |
| Peer enrollment grants default-user `ScopeAll`; no pairing or approval request [Upstream] | A heavyweight trust grant for a desktop extension | Disclosed in the widget | Upstream ask: project-scoped enrollment plus a pairing request |
| `localhost` URLs belong to the adapter machine | Hosted adapters cannot offer browser links | Documented | Hosted gateway (long term) |

---

## 6. Phased recommendation

### Phase 0: small, local fixes (days)

1. F1: optional `after`, `fromLastInput`, `until` options; return `resumeAfter` from launch and open.
2. F2(b): clamp `wait_box` to 45 s or less. F2(c): explicit app-call timeouts.
3. F3, model-path part: compact `list_boxes`/`list_attention` text, task preview truncation, archived boxes excluded by default, `_meta.connection` reduced to routing fields.
4. F4: re-resolve primary on explicit reopen, plus a typed stale-session error.
5. F10 copy fixes, F13 stale strings and examples, F14 messages, F15 server name.

**Exit criteria:** text-only "launch → wait → read" works end to end against a fixture; the payload-size snapshot test passes.

### Phase 1: lifecycle and onboarding (1–3 weeks)

1. F3/F5: one adapter-level `/events` SSE subscription feeding a cached catalog and inbox; visible-only attach; per-box view lease; LRU eviction of observer views; context pushed only on user action.
2. F2(a): two-phase launch with a "starting" workspace.
3. F6: connection generations with in-process apply, `Diagnose`-based setup diagnostics, and a "Show my boxes" handoff.
4. F7: forwarding validation caching, queueing, idle semantics, stable ports.
5. F8: canonical vocabulary, legacy flag, unlisted UI aliases, slimmer instructions, surface snapshot test.
6. F10: restore and power actions.

**Exit criteria:** an opaque-frame multi-instance test passes, a remote profile applies without a restart, and the forwarding fixture passes. Native Claude Desktop (macOS) and Codex desktop runs of J1–J5 and J8 pass.

### Phase 2: structural (as needed)

1. F9: unify standalone onto MCP (or make it opt-in), then delete the `/api/*` duplicates.
2. F12: workspace state-machine extraction with tests; typed upstream models; brand tokens from the module cache.
3. F11: version-checked replace, folders, larger and folder downloads.
4. Items already on `todo.md`:
   - Hosted HTTPS service with real client authentication.
   - Source delivery for `launch_box`; upstream has `sources/*/pull` and `complete-source-push`.
   - Nested service previews after per-host frame and CSP validation.
5. Upstream asks: project-scoped or pairing-based peer enrollment; a delegated tunnel scope for sandbox principals.

---

## Uncertainties and what was not validated

- **Native hosts.** I ran no Claude Desktop, Codex desktop or Codex CLI session. F5's severity depends on whether hosts keep off-screen widget iframes mounted and on how they key `updateModelContext` (per instance or per app). I could not confirm either. I also could not confirm that Claude or Codex expose app-registered tools (`select_view`) to the model, or what their actual tool-call timeouts are. 60 s is the ext-apps and TS SDK default, and Codex's default `tool_timeout_sec`.
- **Exec replacement (F4).** Upstream documents multiple, newest primary sessions per box and exposes restart, repair and upgrade. I did not confirm which events actually change the primary exec ID; in particular, sleep/wake and harness crash-restart are unverified. The cache-never-invalidates behavior itself is certain from the code.
- **Forwarding (F7).** These are code-level conclusions. I did not measure real browser connection counts or Iroh latency.
- **Host logging of app-private payloads.** Whether Claude Desktop's MCP logs record full tool results (VNC frames, terminal bytes, upload chunks) is unverified. It is worth checking before claiming privacy beyond "not in model context".
- **macOS install.** The MCPB is ad-hoc signed, and Iroh's native library is extracted to the user cache on first use. Gatekeeper and quarantine behavior for a downloaded bundle and the extracted dylib is untested here. The repository says the same.
- **Upstream version skew.** I used the OpenAPI spec of the pinned pseudo-version. A user's server may be older or newer. The events stream, wait options and file preconditions should be feature-detected (fall back on 404/400).
- **discobox-ui.** The `cloudnautique/discobox-ui` sources cited in `docs/connection-onboarding.md` were not available offline. Statements about the reference GUI rely on that document.
- **Out of scope.** I did not assess security properties such as the loopback bearer/Origin model or approval token handling, beyond noting surface area. That belongs to the separate security review.

---

## Appendix A: Measured surface

Measured from a stdio probe of the built binary at `b70f8ff`, with no backend configured:

| Metric | Value |
| --- | --- |
| Tools total / model-visible / app-only | 53 / 36 / 17 |
| UI-bearing tools (`_meta.ui.resourceUri`) | 16 |
| Serialized tool list (all / model-visible) | 33,411 B / 25,211 B |
| Server `instructions` | 4,663 chars, 695 words |
| `resources/list` entries | 16 (14 workspace aliases + 2 setup) |
| Resource templates | `discobox-file://{project}/{boxId}/{+path}` |
| Bundled `web/vendor/app.js` (inlined into the workspace HTML) | 2,213,516 B |
| Bundled `web/vendor/onboarding.js` | 669,400 B |
| `go test ./...`, `npm test` | pass, pass (47) |

App-only tools: `approval_decide, approval_list, approval_review, connection_save, connection_test, desktop_action, desktop_open, desktop_read, file_read, file_stat, file_upload, service_keepalive, service_logs, service_open_browser, terminal_action, terminal_open, terminal_read`.

## Appendix B: Resource limits in force

| Resource | Limit | Where |
| --- | --- | --- |
| Terminal app views | 8 per adapter, 90 s idle, 1 MiB buffer | `internal/bridge/app_terminal.go:18-19,92` |
| Desktop views | 4, 90 s idle, 2 MiB buffer, 128 KiB per read | `internal/bridge/app_desktop.go:17-20,73` |
| Uploads | 4 concurrent, 16 MiB each, 192 KiB chunks, 5 min idle | `internal/bridge/file_upload.go:17-18,71,88` |
| Forwards | 8 per adapter, 8 connections each, 15 min idle, 5 min per-connection deadline | `internal/bridge/services.go:19-20,240,267` |
| File read/resource | 256 KiB per range, 1 MiB per resource | `docs/files.md`, `internal/bridge/files.go:147` |
| Upstream JSON response | 2 MiB; HTTP client timeout 75 s | `internal/discobox/client.go:116,155` |
| Launch wait / attach / wait_box | 3 min / 45 s / 60 s | `managers.go:79`, `app_terminal.go:23`, `client.go:247` |
| Widget inbox poll | every 15 s per instance | `web/app.js:606` |

---

## Appendix C: Resolution status on branch `design-review-fixes`

Added after the review, when the findings were implemented. Commit messages
name the finding each change addresses; `docs/contract.md` describes the
resulting surface.

| Finding | Status |
| --- | --- |
| F1 wait contract | Done: optional `after` (defaults to `fromLastInput`), `until` = settled/turn_end/needs_input/quiet with hook events, `resumeAfter` from `launch_box`, fallback for servers that reject the new fields. |
| F2 host budgets | Done: `launch_box` ≤ 50 s with a `starting` state and widget auto-attach, `wait_box` ≤ 45 s, explicit app-call timeouts, `open_box` works before a session exists. |
| F3 catalog payloads | Done: bounded model summaries with paging/filters and a text table; widget catalog in `_meta`/`catalog_read`; one adapter cache invalidated by the project event stream. Also raised the catalog response cap to 32 MiB after a 100-box fixture exceeded 2 MiB. |
| F4 session pin | Done: explicit reopen re-resolves and adopts a replaced session unless a person holds input; typed stale-session error. Command-line `--box` pins are unchanged. |
| F5 widget multiplicity | Done: visible-only attach and polling, release after a minute hidden, one live view per box, LRU eviction at 8, context pushed only after interaction. Behavior in native hosts is still unverified. |
| F6 connection changes | Done: per-generation `discobox.Connect` with its own Iroh endpoint, apply on save in `--browse` mode, layered `Diagnose` in tests and the widget, "Show my boxes" handoff. Old generations' Iroh endpoints cannot be closed (no upstream API) and live until exit. |
| F7 forwarding | Done: cached validation (30 s), 32 connections + queue, no per-connection idle cut, keepalive, stable ports. |
| F8 surface | Mostly done: 24 model-visible tools (from 36), legacy aliases behind `--legacy-manager-tools`, one listed UI resource, 1.5 KB instructions, snapshot test. Schemas plus instructions are about 17 KB, above the 10 KB target. |
| F9 standalone path | Partly done: option (a), the browser UI is opt-in with `--browser-ui`. The duplicate `/api/*` implementation remains for that mode. |
| F10 lifecycle | Done: `control_box` start/stop/restart/restore, widget Restore and Power actions, self-resetting confirmations, chat-facing copy without CLI flags. |
| F11 files | Near-term done: version-checked Replace and New folder. Chunked/large downloads and folder export are not done. |
| F12 maintainability | Partly done: brand tokens from the module cache. Not done: `app.js` state-machine extraction, typed upstream models, internal `Manager` rename. |
| F13 docs | Done: `docs/contract.md`, ADRs, `CHANGELOG.md`, stale facts corrected. Older narrative docs were not consolidated. |
| F14 status | Done: passive health in `connection_status`, `human_has_control` code with a time, forwarding busy message. |
| F15 polish | Done: compact inline layout, server name `discobox`, optional bare Ctrl+V to the program. |
