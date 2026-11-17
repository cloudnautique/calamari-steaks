# Changelog

Dated design and verification notes from earlier releases remain in `docs/`.
`docs/contract.md` describes the current surface.

## 0.12.0 (2026-11-17)

Follows the design review in `DESIGN-REVIEW.md` (findings F1–F15).

### Model contract
- `wait_box` no longer requires a cursor; new `until` conditions (`settled`,
  `turn_end`, `needs_input`, `quiet`) use upstream hook events; at most 45 s
  per call. `launch_box` returns a `resumeAfter`.
- `launch_box` returns within about 50 s, reporting `terminal:"starting"` for a
  box still provisioning; `open_box` works for such a box.
- `list_boxes` and `list_attention` return bounded summaries with a short text
  table; archived boxes hidden by default; paging and filters.
- `open_box(boxId, view?, filePath?)` replaces `show_box_view`,
  `open_box_file` and `open_box_desktop`. Manager-named aliases and
  `inspect_box` are served only with `--legacy-manager-tools` (default on for
  `--box`/manager launchers).
- New `control_box` (start, stop, restart, restore).
- `send_box` refused by human focus returns `human_has_control` with a time.
- Server name is now `discobox`; instructions shrank from 4.6 KB to 1.5 KB;
  `resources/list` lists one workspace and one setup resource.

### Sessions and views
- Reopening a box adopts a replaced primary session (never under a person's
  input); stale sessions return an actionable error.
- At the 8-view cap, an older view of the same box (else the least recently
  used idle view) is paused instead of refusing the new one.
- Widgets attach and poll only while visible, read one shared catalog cache
  (invalidated by the project event stream), and push model context only
  after the person interacts.

### Connection
- Saving a profile applies it without a restart; open views keep their
  connection. Tests show each layer of the connection with hints.
- `connection_status` records real backend successes and failures.
- "Show my boxes" hands off to chat after setup.

### Services, files, terminal
- Forwards: no per-connection catalog reads, 32 connections plus a short
  queue, no idle cut on quiet WebSockets, stable local ports.
- Uploads can replace an existing file at a confirmed version; New folder.
- Option to send a bare Ctrl+V to the program on Windows and Linux.
- Inline embeds give the terminal more room.

### Packaging and maintenance
- The standalone browser UI is opt-in with `--browser-ui`.
- Brand tokens come from the pinned Go module; CI no longer checks out Discobox.
- Catalog listings accept up to 32 MiB (large projects exceeded 2 MiB).
- Surface snapshot, catalog size, lifecycle, forwarding and connection
  generation tests.
