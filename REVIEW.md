# Review notes

Checks for later reviews of this repository, left by earlier reviews and fixes.

- Distinguish an uncertain create outcome from a definite rejection in every
  creation journal (`internal/discobox/manager.go`). Clear the intent only for a
  rejection that upstream returns before its commit (a 4xx other than 408, 409
  or 429); keep it for transport errors, 5xx and unparseable responses. A
  journal that treats every error as uncertain wedges its requestId forever, and
  the widget then has nothing it can submit; one that treats an uncertain error
  as a rejection creates a second box.
- Keep the widget's frozen creation intent escapable (`web/app.js`): after a
  failed launch the person needs an explicit, warned way to discard it and edit
  the settings, not only a retry of the same intent.
- Never hold a per-upload lock (`fileUpload.mu`) across network I/O in
  `internal/bridge/file_upload.go`. Lock order is `Server.uploadMu`, then
  `fileUpload.mu`; detach an upload from the map before committing it. A
  cancel sent after a host times out `finish` otherwise holds `uploadMu` for the
  whole commit, stalling every upload, expiry timer and shutdown cleanup.
- Match the no-backend guard in `Server.Handler` to the real routes. It once
  checked a nonexistent `/ws` while the terminal WebSocket is `GET /terminal`,
  so setup mode let a terminal request through to a nil client. A new route
  that dereferences `s.Client` must be gated there and covered by a nil-client
  test.
- Launcher overrides replace, not merge, the parts of the saved connection
  profile they own (`cmd/discobox-mcp/main.go`, `applyLauncherOverrides`). An
  endpoint override drops saved relays, because an override also makes the
  setup widget read-only and it cannot repair a profile that fails validation.
- Bump `uiResource` in `internal/bridge/tools.go` whenever the bundled widget
  (`web/`) changes, and keep every previously published URI as an alias, so
  hosts drop a cached widget without breaking restored sessions. Update the
  docs that name the current resource (`docs/connection.md`,
  `docs/managers.md`) in the same change.
- Keep desktop input ownership enforced in the bridge
  (`internal/bridge/rfb_filter.go`), not only by the widget's noVNC view-only
  mode: a caller that bypasses the widget otherwise types into a desktop
  another view controls, which the docs promise cannot happen. A new RFB client
  message or encoding noVNC starts sending must be framed there, with input
  messages dropped for views without control; one the filter does not know
  closes the desktop, so test a noVNC upgrade against it. Never let a
  pseudo-encoding through that changes a client message's length based on
  server state, as ExtendedMouseButtons does.
- In generated shell (for example the mcpjam wrapper in
  `.github/workflows/mcpjam.yml`), expand runner paths when the file is written.
  MCP stdio clients often launch servers with a reduced environment, so
  variables like `RUNNER_TEMP` may be unset when the wrapper runs.
