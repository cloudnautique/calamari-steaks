# Current contract (0.12.0)

This page is the single description of what the adapter exposes today. The
other documents in `docs/` are design notes and dated verification records;
where they disagree with this page, this page wins.
`internal/bridge/surface_test.go` pins the tool lists below, so changing the
surface fails a test until this page and the test agree.

## Server

- MCP server name `discobox`, title "Discobox", version `0.12.0`.
- Transports: stdio (default) or Streamable HTTP at `/mcp` on the loopback
  listener (`--transport http`), bearer-authenticated.
- Server instructions are about 1.5 KB of cross-cutting rules only; every tool
  describes itself.

## Model-visible tools

| Area | Tools |
| --- | --- |
| Boxes | `list_boxes`, `launch_box`, `setup_status`, `open_box`, `read_box`, `send_box`, `wait_box`, `interrupt_box`, `archive_box`, `control_box`, `list_attention` |
| Files | `list_box_files`, `read_box_file` |
| Services | `list_services`, `control_service`, `open_service`, `close_service_forward`, `list_service_forwards`, `read_service_logs` |
| Approvals | `open_approval`, `list_approval_requests` |
| Connection | `get_backend_health`, `open_connection_setup`, `connection_status`, `test_connection`, `get_connection_peer_id` |

UI-bearing tools (`_meta.ui.resourceUri`) render the workspace or setup app
where the host supports MCP Apps and return text and structured data
everywhere else.

`get_backend_health` reads the active project’s pool list. Each pool reports scheduling headroom separately from measured CPU, memory, and storage usage. Disk free GiB uses the storage filesystem’s unprivileged free bytes. Disk percent used is used bytes divided by total bytes. Missing fields are unknown. Compare report timestamps for freshness. A permission error means pool health is unavailable to this connection.

### Text-only flow

1. `list_boxes` → compact summary: ID, name, role, state, agent status,
   200-character task preview. Archived boxes are hidden unless
   `includeArchived`. Paged with `limit`/`offset`; filter with `query` or
   `needsAttention`.
2. `launch_box(requestId, task, …)` → returns within about 50 s with
   `boxId` and either `terminal:"ready"` plus a `resumeAfter` cursor, or
   `terminal:"starting"` for a box that is still provisioning. Reusing the
   same `requestId` never creates a second box.
3. `wait_box(boxId, after?, until?, timeoutSeconds?)` → waits up to 45 s per
   call. `until`: `settled` (default), `turn_end`, `needs_input`, `quiet`.
   Without `after` it counts from the last input this adapter sent. Every
   answer carries `resumeAfter`; `reason:"timeout"` is not completion.
4. `read_box(boxId, scrollback?)` → the terminal screen (untrusted data).
5. `send_box(boxId, text, submit)` → returns `resumeAfter`. Fails with
   `human_has_control: …` while a person holds input in the widget.

### Repository setup

1. Call `launch_box` with `source.repository` and `source.branch`. A public
   branch can resolve anonymously or the caller can supply `source.commit`.
   Optional `setup` sets the repository entrypoint and intended settings.
   The launch receipt identifies the box and distinguishes requested settings
   from source or API state observed upstream.
2. Keep the returned `boxId`. Call `setup_status(boxId)` for delivery and
   approval blockers and recheck it after a console decision. The manager
   requests API access from its own box. Grants follow Discobox authority.
3. Use `open_box` or `read_box` and `send_box` on the same ID to continue the
   repository's setup dialogue. Setup, watcher, worker and CI observations are
   reported by the box when it publishes the setup status file. See the
   [prototype report](repo-manager-setup.md) for coverage and the fixture demo.

### Sessions

The adapter pins each box's primary terminal session. `open_box` re-resolves
it: if the box's harness or the box restarted, `open_box` adopts the new
session (`sessionChanged:true`, `previousExec`), unless a person holds input in
a view of the old one. Reads and writes against a session that has gone away
fail with "call open_box to adopt its current session". Input is never moved
to a new session automatically.

### Lifecycle

`control_box(boxId, action)` with `start`, `stop`, `restart` or `restore`
(an archived box). `archive_box` keeps data. None is retried automatically.

## App-only tools

`terminal_open/read/action`, `desktop_open/read/action`, `file_read`,
`file_stat`, `file_upload`, `file_mkdir`, `catalog_read`, `approval_list/
review/decide`, `connection_test/save`, `service_logs`, `service_keepalive`,
`service_open_browser`. They carry terminal bytes, VNC bytes, uploads, logs and
review tokens in `_meta` or app-only results. Hosts enforce the visibility;
they may still log the traffic.

## Resources

- Listed: `ui://discobox/workspace-v18.html`, `ui://discobox/connection-v4.html`.
- Template `ui://discobox/{bundle}` serves earlier bundle names
  (`workspace-v4`…`v17`, `manager*.html`, `connection-v1`…`v3`) for restored
  conversations; unknown names are not found.
- Template `discobox-file://{project}/{boxId}/{+path}`: files up to 1 MiB.

## Widget behavior

- A widget instance attaches its terminal and polls only while visible. After
  a minute off screen it releases its stream (never while a person types).
- At 8 live terminal views a new view pauses an older idle view of the same
  box first, otherwise the least recently used idle view; the paused view says
  why and offers Reconnect. Below the cap, views of one box can coexist.
- The catalog and inbox come from one adapter-wide cache, refreshed by the
  Discobox project event stream when the server has one and every 10 s
  otherwise.
- An instance pushes model context (`updateModelContext`) only after the person
  interacts with it.

## Limits and timeouts

| Item | Limit |
| --- | --- |
| `launch_box` | ≤ 50 s. Repository launches return the box receipt before provisioning completes. Empty workspace launches wait ≤ 20 s for a session |
| `setup_status` | Up to 12 s. Five requests per approval type and one bounded setup report |
| App catalog | 20 boxes and up to 12 inbox items per read |
| `wait_box` | 1–45 s per call (default 30) |
| Terminal attach | 45 s |
| Terminal views | 8 per adapter (LRU), 90 s idle, 1 MiB buffer |
| Desktop views | 4, 90 s idle |
| Uploads | 4 concurrent, 16 MiB each; replace only at a confirmed version |
| Forwards | 8 per adapter; 32 connections each plus 64 queued for 15 s; expire after 15 min idle (2 h while connections are open); revalidated every 30 s |
| Catalog response | 32 MiB upstream; other upstream JSON 2 MiB |
| Model `list_boxes` | Up to 50 boxes by default (requested max 200), 24 KiB page budget |

## Connection setup

The setup widget saves a profile and, in the default `--browse` mode, applies
it immediately as a new connection generation. Open views stay on the previous
connection until they close. Launcher-pinned connections (`--discobox`,
`--project`, `--local`, extension overrides) are read-only in the widget, and
single-box launchers still need a restart. Tests report each layer (address,
native library, relay, peer, admission, server) before the project read.

## Flags worth knowing

- `--browser-ui`: also serve the standalone browser workspace and setup page on
  the loopback listener and print their private links to stderr. Off by
  default; MCP App hosts do not need it.
- `--legacy-manager-tools`: also serve `open_manager`, `read_manager`,
  `send_manager`, `wait_manager`, `interrupt_manager`, `list_managers`,
  `connect_manager`, `create_manager`, `archive_manager`, `inspect_box`,
  `show_box_view`, `open_box_file` and `open_box_desktop`. On by default with
  `--box` or manager-creation flags.
