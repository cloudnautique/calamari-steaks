# Security review: cloudnautique/calamari-steaks

| | |
|---|---|
| Reviewed commit | `b70f8ff5b8ac1b37c795cf572e36928c5ee82b23` (`main` = `origin/main`, clean tree) |
| Go module | `github.com/cloudnautique/discobox-mcp-ui` |
| Upstream checked | `github.com/discobox-ai/discobox@v0.19.2-0.20261111144542-d6c392a3cce7`, `modelcontextprotocol/go-sdk@v1.8.0`, ghostty-web 0.4.0, noVNC 1.7.0, ext-apps 2.0.4 |
| Review date | 2026-11-17 |
| Type | Findings only. No implementation files changed, nothing committed, pushed or published. |

## Remediation status (branch `security-review-fixes`)

The findings below describe commit `b70f8ff`. They were addressed afterwards on branch `security-review-fixes`:

| Item | Status | Change | Regression test |
|---|---|---|---|
| D1 | Fixed | Explicit Markdown URI allow-list (`MARKDOWN_URI_PATTERN`). `href`s that `resolveFileLink` rejects are stripped at render time. Middle-click (`auxclick`) goes through the same handler. | `web/files-model.test.js` |
| D2 | Fixed | Per-view RFB client-message filter (`internal/bridge/desktop_rfb.go`). Key, pointer, clipboard, desktop-size, XVP and QEMU key messages are delivered only while the view holds the claim, checked under the claim lock. Unsupported handshakes or messages close the view. | `desktop_rfb_test.go`, `app_desktop_test.go` |
| H1 | Fixed | `DISCOBOX_MCP_TOKEN` is sent only to an endpoint pinned by `--discobox`/`DISCOBOX_MCP_ENDPOINT`. | `cmd/discobox-mcp/token_binding_test.go`: fails on `b70f8ff`, passes now |
| H2 | Fixed (file tokens) | A token from `--access-token-file` is replaced by a placeholder in every stderr line. Per-process tokens are still printed, because those private links are the only way to open the standalone UI. | `TestAccessTokenFromFileIsNotLogged` |
| H3 | Fixed | `terminal_open`/`terminal_read`/`terminal_action` return data only in `_meta.terminal`, with constant text content. | `app_terminal_test.go` |
| H4 | Fixed | `connection_save` over MCP needs a single-use save token, delivered only in `_meta.setup`. The authenticated HTTP setup page is unchanged. | `onboarding_test.go` |
| H5 | Fixed | Ctrl/Cmd-clicks on the terminal are always swallowed, so ghostty-web never opens non-HTTP schemes. Plain-click HTTP(S) opening through the host is unchanged. | `web/terminal-links.test.js` |
| H6 | Fixed | The approval duration defaults to `min(requested, 1 h)`. | `web/approval-decision.test.js` |
| H7 | Fixed | `/` and `/setup` use a `script-src` SHA-256 hash of their inlined bundle instead of `'unsafe-inline'`. Checked in headless Chromium: both pages run with zero CSP violations. | `internal/bridge/csp_test.go` |
| H8 | Not changed | Loopback forwards are raw TCP splices for any declared protocol. Authenticating them is a design change (e.g. an HTTP-only proxy mode), left for a follow-up. | n/a |
| H9 | Fixed (warning) | Warns when the access-token file or Iroh identity is accessible to other users (Unix). | `TestSharedSecretWarning` |
| H10 | Fixed | At startup, removes regular `discobox-upload-*` staging files untouched for over an hour. Symlinks and other files are never touched. | `TestSweepStaleUploads` |
| H11 | Fixed | The MCPJam tools install with `npm ci` from the committed `ci/mcpjam/package-lock.json`. | CI (`workflow_dispatch`) |

## 1. Native workflow used, and its limitation

1. **The native command ran.** Claude Code's built-in `/security-review` skill was invoked through the Skill tool.
2. **It reviewed nothing on its own.** It reviews only the pending branch diff: `git diff origin/HEAD...` plus the commit log. The first call failed because `origin/HEAD` was missing. After that was repaired, the call succeeded but returned an empty diff and no commits, because `main` equals `origin/main`. An empty diff is not a repository review.
3. **Its method was applied to the whole repository.** The skill's three-step method was kept, with the scope widened from the diff to every tracked implementation file:
   1. **Identify candidates.** Three sub-tasks ran in parallel, each given the skill's full instructions and one area: (a) server, auth, onboarding, connection, packaging and CI; (b) files, uploads, services, forwarding and browser launch; (c) terminal, desktop, approvals and the web UI.
   2. **Filter false positives.** Each candidate got its own sub-task with the skill's false-positive rules. These ran in parallel.
   3. **Drop low confidence.** Candidates scoring below 8/10 were removed from the vulnerability list.
4. **Two changes from the skill.**
   - At the user's request, candidates that were filtered out are not hidden. They are reported in §4–§5 as confirmed defects or hardening items, with their confidence scores.
   - The lead reviewer re-read the key code paths and re-ran the regex reproduction to check the sub-tasks' citations. One wrong line reference was corrected: the token-withholding probe is at `cmd/discobox-mcp/onboarding.go:45-53`, not lines 438-447.

## 2. Summary

**No HIGH or MEDIUM vulnerability survived verification.** No candidate reached the skill's ≥ 8/10 bar.

Everything that was found falls into three groups:

- **Confirmed defects with no demonstrated exploit path:**
  - D1: hyphenated URI schemes get through the Markdown sanitizer.
  - D2: desktop input bytes skip the input-ownership check.
- **Hardening suggestions:** the token is not bound to the endpoint it was configured for, the bearer is printed to stderr, the terminal handle reaches model-visible content, and others listed in §5.
- **Client/upstream limitations:** the most important is that "app-only" tools are enforced only by the host.

**Trust model these conclusions rely on.** The adapter has one principal: the MCP connection. For stdio that is the launching client; for HTTP it is whoever holds the 256-bit loopback bearer.
- The widget, the model and the standalone browser page all act with that same principal's authority.
- The adapter cannot tell a model-initiated tool call from an app-initiated one.
- Several findings below are low severity because no second principal exists to exploit them. If the deployment ever exposes the adapter to more than one principal (a non-loopback listener, shared tokens, or a host that does not enforce MCP Apps visibility), re-evaluate D2, H1, H2 and H3 first.

## 3. Vulnerabilities (confidence ≥ 8/10)

None.

## 4. Confirmed defects (real behavior, no exploit path; below the bar)

### D1: Markdown sanitizer allows hyphenated URI schemes (Low, confidence 3/10)

- **Evidence:**
  - `web/files-view.js:80`: `ALLOWED_URI_REGEXP: /^(?:(?:https?|discobox-file):|[^a-z]|[a-z+.-]+(?:[^a-z+.-:]|$))/i`.
  - In the negated class `[^a-z+.-:]`, the `.-:` is a range from `.` to `:`. That range covers `./0-9:` and does not include `-`.
  - So `[a-z+.-]+` consumes the leading letters, and a following `-` satisfies the negated class.
- **Preconditions and attacker:**
  - The attacker controls a `.md` file in a box the user previews in the Files view. That could be the box's own agent, or repository content in the box.
  - The user then has to middle-click, use "Open link in new tab" from the context menu, or drag the link.
  - The browser or OS still shows its own external-protocol prompt.
- **Impact:**
  - An `<a href="search-ms:…">`, `ms-settings:` or `ms-officecmd:` link survives sanitization.
  - Script schemes are still rejected: `javascript:`, `vbscript:`, `data:` and `file:` all fail the regex. So there is no XSS.
  - A plain left click is intercepted by `web/files-view.js:42-44` (`preventDefault`) and routed through `resolveFileLink` (`web/files-model.js:3-13`), which allows only `http(s)` and same-box `discobox-file`.
- **Safe reproduction (pure regex, no browser):**
  ```sh
  node -e 'const r=/^(?:(?:https?|discobox-file):|[^a-z]|[a-z+.-]+(?:[^a-z+.-:]|$))/i;for(const u of ["ms-settings:x","search-ms:q","javascript:x","file:///x"])console.log(u,r.test(u))'
  # ms-settings:x true / search-ms:q true / javascript:x false / file:///x false
  ```
- **Fix:**
  - Replace the regex with an explicit allow-list, e.g. `/^(?:https?:|discobox-file:|#|\/(?!\/)|\.{0,2}\/)/i`. Minimum fix: escape the hyphen, `[^a-z+.\-:]`.
  - Also handle `auxclick`, or strip any `href` that `resolveFileLink` rejects while rendering.
- **Regression test:**
  - Render `[a](ms-settings:x)`, `[b](search-ms:q)` and `[c](javascript:alert(1))` through the same DOMPurify config. Assert that none keeps an `href`.
  - Assert that `https://`, relative and `discobox-file:` links keep theirs.

### D2: Desktop `write` does not enforce the input claim (Informational/Low, confidence 2/10)

- **Evidence:**
  - `internal/bridge/app_desktop.go:140-151` (the `desktop_action` `write` case) calls `desktopSession.write`, defined at `app_desktop.go:199-225`. Neither checks `view.manager.controller == view.id`.
  - The terminal path does check: `internal/bridge/app_terminal.go:162-169` calls `writeAs(view.id, …)`.
  - The comment at `internal/bridge/server.go:93-109` says one lock covers ownership and writing.
  - The terminal and the desktop share one `managerConnection` per box (`app_desktop.go:161-168`).
- **Reproduction (scratch copy of the repo, fake VNC server):**
  - View A claims control. View B, which never claimed, writes one byte.
  - Result: `isError=false`, and the fake VNC server received the byte.
- **Why it is not a vulnerability:**
  - Desktop IDs are 128-bit `crypto/rand` values returned only in `_meta` (`app_desktop.go:53-57`).
  - Anyone holding one is the same principal, and could call `claim` or the terminal tools anyway.
  - noVNC also needs non-input protocol writes (SetEncodings, FramebufferUpdateRequest) from a view that is only watching. That explains why ownership is enforced on the client (`rfb.viewOnly`, `web/desktop-view.js:36-38`).
- **Fix:**
  - Parse RFB client messages per view on the server. Let SetPixelFormat (0), SetEncodings (2) and FramebufferUpdateRequest (3) through.
  - Send KeyEvent (4), PointerEvent (5) and ClientCutText (6) only inside `manager.writeAs(view.id, …)`.
- **Regression test:**
  - Views A and B both complete the handshake, then A claims.
  - A KeyEvent from B is dropped or rejected, and the fake server receives 0 input bytes.
  - A FramebufferUpdateRequest from B is delivered. A KeyEvent from A is delivered.
  - Repeat with the terminal view holding the claim.

## 5. Hardening suggestions (filtered out as vulnerabilities)

| # | Item | Evidence | Why not a vuln (filter score) | Recommendation / regression check |
|---|---|---|---|---|
| H1 | **The launcher's upstream token is not bound to the endpoint it was configured for.** With `DISCOBOX_MCP_TOKEN` set and no endpoint/project override (the mcpb default when `endpoint` is blank), startup loads the saved profile and sends `Authorization: Bearer` to whatever `unix://`/`npipe://`/`discobox://` endpoint the profile names. | `cmd/discobox-mcp/main.go:69,94-101,181-192`; `internal/discobox/client.go:125-131`; `internal/connection/connection.go:213-236` (`Save`); `internal/bridge/onboarding.go:73-83` (`connection_save`). The connection-test path deliberately withholds the token: `cmd/discobox-mcp/onboarding.go:45-53`. Reproduced with a fake unix socket: the header arrived. | 3/10. The pinned upstream user API does not use generic bearer tokens: auth is socket access or Iroh enrollment (`docs/connection-onboarding.md:74-75`). In the HTTP case where a bearer matters, the launcher pins the endpoint and `Save` refuses. Redirecting the token needs the user to choose the server, or a host that breaks app-only visibility. | Send the env token only to the endpoint supplied by the launcher. Drop it, or refuse to start, when the endpoint came from the saved profile or is `discobox://`, matching the probe. Test: token set plus a saved `unix://` profile, then assert no `Authorization` header (or that startup refuses). |
| H2 | **The access token is printed to stderr**, including when `--access-token-file` supplies a stable token. Under the declared workspace service, stderr becomes service logs, readable with `read_service_logs`. | `cmd/discobox-mcp/main.go:228,299,314`; `.discobox/services/20-mcp-server.sh`; `internal/bridge/services.go:350`; upstream ADR 0070 (service logs keep stderr). | 3/10. Anyone who can read the logs already has per-box access (terminal/exec/files) and can read the token file. On a host, the log reader is the same OS user. The `#token=` links are documented "private link" UX. | Don't print the token when it comes from a file or stderr is not a TTY, or require an opt-in `--print-access-links`. Test: run with a fixture token file and a captured logger, and assert the token is absent while the origin is present. |
| H3 | **App terminal handle and bytes reach model-visible content.** `terminal_open` and `terminal_read` return `terminalId` and output in `structuredContent`, and go-sdk also copies that into `TextContent` (`mcp/server.go` ~477). A `terminalId` lets `terminal_action input` write as the human's view, bypassing `writeAs("mcp")`. | `internal/bridge/app_terminal.go:107,138,144`. Compare `desktopResult`, which uses `_meta` only (`app_desktop.go:53-57`). | 2/10. The model would need two host violations: seeing an app-initiated result, and calling an app-only tool. `claim` cannot take control from a person. | Return terminal handles and bytes in `_meta` the way `desktopResult` does, and read `_meta.terminal` in `web/app.js`. Test: assert `terminalId` and chunk bytes are absent from `Content`/`StructuredContent` and present in `_meta`. |
| H4 | `connection_save` has no review nonce, unlike `approval_decide`. If a host lets the model call it, the model can retarget the adapter after restart, which also triggers H1. | `internal/bridge/onboarding.go:73-83` vs `internal/bridge/approvals.go:107-114`. | Depends on host enforcement (L1). | Issue a single-use, `_meta`-only nonce from an app-only "begin edit" call, and require it in `connection_save`. |
| H5 | Ctrl/Cmd-click in the terminal: ghostty-web's built-in link provider calls `window.open` on `mailto:`, `tel:`, `ssh:`, `magnet:`, `ftp:` and others. The app's handler only intercepts `http(s)`. A plain click on an `http(s)` URL opens it immediately through `openLink`. | `web/terminal-links.js:1-9,52-55`. | Needs a user gesture, and popups must be allowed by the host's iframe sandbox. | Intercept every modifier-click on the terminal canvas. Consider requiring a modifier key or a confirmation before opening links that come from box output. |
| H6 | The approval duration defaults to the TTL the box requested, up to 30 days. | `web/approvals-view.js:82`. | UX/policy choice; a human still confirms. | Default to a conservative value (e.g. 1 h) and highlight long or delegated grants. |
| H7 | The standalone page CSP allows `script-src 'unsafe-inline'`. | `internal/bridge/server.go:154`. | No DOM XSS sink was found. All dynamic DOM writes use `textContent`, apart from DOMPurify Markdown and one static `innerHTML`. | Use hashes or nonces for the inlined bundles. Test: the CSP header contains no `'unsafe-inline'` in `script-src`. |
| H8 | Loopback service forwards have no authentication. Other local OS users, or a DNS-rebinding page, can reach a forwarded box service, because it is a raw TCP splice and the box service sees the attacker's Host header. | `internal/bridge/services.go:185`. | Same model as `ssh -L`/`kubectl port-forward`; documented. | For `http(s)` forwards, consider checking the Host header, or a per-forward secret path or cookie. State the exposure in the UI. |
| H9 | Secret files are read without checking their permissions. | `cmd/discobox-mcp/main.go:211-219` (`--access-token-file`), Iroh identity load in `internal/discobox/peer.go`. Writes are private (0600 files in 0700 directories). | User-supplied, trusted configuration. | Warn when the file is group- or world-readable on Unix. |
| H10 | Staged uploads survive a crash. | `internal/bridge/file_upload.go:78`: `os.CreateTemp("", "discobox-upload-")`, 0600, O_EXCL. Cleaned on cancel, finish, expiry and shutdown, but not on abnormal exit. On Windows, privacy relies on the per-user `%TEMP%` ACL. | Files are private to the owning user. | Sweep stale `discobox-upload-*` files at startup, or stage in a per-user cache directory. |
| H11 | The MCPJam CI job installs pinned CLI versions without a lockfile. | `.github/workflows/mcpjam.yml` (`npm install --save-exact` into `$RUNNER_TEMP`). | Runs only on `workflow_dispatch`, with `contents: read` and no secrets. | Commit a lockfile for the tool install, or use `npm ci` against one. |

## 6. Client / upstream limitations

- **L1: App-only tools are enforced only by the host.** `_meta.ui.visibility: ["app"]` is a declaration to the host. go-sdk v1.8.0 does not enforce it, and a plain MCP client could call `terminal_*`, `desktop_*`, `approval_*`, `connection_*`, `file_upload`, `service_open_browser` and others.
  - This is within the single-principal design. `docs/desktop.md` says outright that app-only is "not a separate authentication principal".
  - Mitigations already in place:
    - `approval_decide` needs a single-use, 5-minute review token that appears only in `_meta`. The token is bound on the server to the request ID and fingerprint (`internal/bridge/approvals.go:80-115`).
    - Desktop handles and pixels go only to `_meta`.
  - Remaining risk: a host that exposed both app-only tools and `_meta` to the model would let the model approve its own credential request. That would be a host bug. Hosts must not expose app-only tools or `_meta` to the model.
- **L2: Rendering is host-dependent.** Rendering of `ResourceLink`, and the behavior of `openLink`/`downloadFile` and the iframe sandbox flags, are decided by the host.
- **L3: Symlinks are handled upstream, inside the box.** Symlink handling for box files is resolved by the upstream agent inside the box. The adapter never touches the host filesystem for box paths.
- **L4: Bundles are not notarized.** macOS bundles are ad-hoc codesigned (`scripts/package-mcpb.mjs`). This is documented.

## 7. Controls verified (coverage evidence)

- **Listener and HTTP auth**
  - `--listen` must be a loopback IP (`cmd/discobox-mcp/main.go:158-161`).
  - The exact Host header is checked, which blocks DNS rebinding (`internal/bridge/server.go:191-196`). Tested: `Host: evil.example` → 403.
  - The bearer is compared in constant time with a strict `Bearer ` prefix (`server.go:81-83,208-228`).
  - Requests that send an Origin header must match the allow-list (`server.go:210-212`).
  - `/mcp`, `/api/session`, `/api/files/*`, `/api/managers/*` and `/api/connection/*` all require auth. Tested: no token → 401, lowercase `bearer` → 401, foreign Origin with a valid token → 403, `Origin: null` → 403 by default.
  - The only `Access-Control-Allow-Origin: *` is on public static assets (`server.go:163-180`). There is no credentialed CORS, so CSRF needs the bearer.
  - Headers set: `no-store`, `no-referrer`, `nosniff`. `frame-ancestors 'none'` is set on `/` and `/setup`.
  - The `/terminal` WebSocket checks Origin exactly, then requires a token auth frame within 5 s, with a 128 KiB read limit (`internal/bridge/terminal.go:24-46`).
- **Token**
  - 256 bits from `crypto/rand`; stable tokens must be 64 hex characters (`server.go:50-59`).
  - It never appears in tool results or the UI resource. It reaches only `/api/session` (authenticated), stderr (H2) and fragment-only links.
  - The setup page strips the fragment with `history.replaceState`.
  - `--request-log` records only method, escaped path, tool name and resource URI without query (`main.go:230-278`).
- **Upstream client and scoping**
  - IDs must match `^[A-Za-z0-9_-]+$` before a URL is built (`internal/discobox/client.go:27-36`, `catalog.go:152`, `manager.go:69-80`).
  - In `--local` mode, a parent/role check limits access to child boxes (`catalog.go:151-163`, `manager.go:323-340`).
  - The project is fixed for each process.
  - Endpoints reject userinfo, query and fragment. Redirects are not followed. Upstream error bodies are not forwarded. Responses are capped at 2 MiB.
  - `--local` clears the env token. A launcher-pinned profile blocks `Save` and arbitrary tests (`internal/connection/connection.go:198-222`).
  - The connection probe runs in a separate process with the token, endpoint and project removed from its environment, and its output is capped at 4 KiB.
- **Files**
  - All box file I/O goes through the upstream files API, which runs inside the box.
  - `cleanFilePath` (`internal/discobox/files.go:47`) requires an absolute path and rejects NUL, `\` and invalid UTF-8.
  - `ParseFileURI` (`files.go:80-98`) rejects cross-project URIs, userinfo, query, fragment and non-canonical encoding. Tested: `..`, `%2e%2e`, `..%2f`, `//`, `%5c`, `%00` and a cross-project URI were all rejected.
  - Listing entries must equal `path.Join(dir, name)`. Reads accept regular files only, re-stat to compare version and size, and are capped at 256 KiB (resources at 1 MiB).
  - Box access is re-checked for every operation.
  - `fileMIME` never returns `text/html`.
- **Uploads**
  - Staged with `os.CreateTemp` (O_EXCL, 0600, random name) under a 128-bit upload ID. No user-supplied filename touches the host.
  - Limits: 16 MiB per file, 4 concurrent uploads, strict sequential offsets, 5-minute expiry.
  - The upstream write uses `ifExists=fail`, so it never overwrites.
  - The upload ID goes only in `_meta`.
- **Forwards and browser launch**
  - Forwards bind `127.0.0.1:0`.
  - The forward target is resolved from the box's declared service catalog, and the dial happens inside the box.
  - Forwarding is refused in the sandbox (parent) role.
  - Limits: 8 forwards × 8 connections, 15-minute idle expiry. Access is re-checked every 30 s.
  - The browser opens only `http(s)://127.0.0.1:<port>/` via `exec.Command` with separate arguments and no shell (`internal/bridge/service_browser.go:17-47`).
- **Terminal and desktop**
  - Handles are 128-bit `crypto/rand` values.
  - Model writes (`send_*`, `interrupt_*`) are refused while a human holds control (`server.go:95-109`), and ownership and writing share one lock.
  - Input is never replayed: desktop writes are de-duplicated by sequence number plus SHA-256, and terminal input is not queued.
  - The desktop dial goes to 127.0.0.1 inside the box, and the upstream Authorization header is not forwarded to VNC.
  - ghostty-web has OSC 8 hyperlinks disabled and no OSC 52 clipboard write. noVNC does not touch `navigator.clipboard`.
- **Web UI**
  - Every dynamic DOM write uses `textContent`/`createElement`, except DOMPurify Markdown (D1) and one static `innerHTML`.
  - `</script` is escaped when inlining (`web/assets.go:48,66`).
  - The MCP App resource declares empty `connectDomains`/`resourceDomains` (`internal/bridge/tools.go:97-101`).
  - ext-apps `PostMessageTransport` checks that `event.source === window.parent`.
  - Model-context receipts carry IDs and status only, never tokens or handles.
- **CI and supply chain**
  - Actions are pinned to commit SHAs. `permissions: contents: read` and `persist-credentials: false` are set, and there is no `pull_request_target`.
  - Matrix values reach the shell only through `env`.
  - The upstream brand checkout is pinned to a commit.
  - `npm ci` uses the committed lockfile.

## 8. Coverage and limitations

- **Files reviewed:** all tracked implementation files: `cmd/`, `internal/`, `web/*.js|html|css`, `web/assets.go`, `scripts/`, `packaging/`, `.github/workflows/`, `.discobox/services/`. Tests and docs were read only for intent.
- **Excluded categories:** DoS and resource exhaustion were excluded by the native skill's rules, but the limits found are noted here:
  - Stream frames up to 16 MiB.
  - Global caps shared by all sessions: 8 app terminals, 4 desktops, 64 approval reviews.
  - Idle timers reset by long-polling.
  - A claim on an ended stream holds control until the view closes or idles out.
- **Reproductions:** run only against isolated fixtures — httptest servers, fake unix sockets, fake VNC/stream servers in a scratch copy of the repo, and pure regex evaluation. No live Discobox service, other box or real credential was used. No credential values appear in this report.
- **Not verified:**
  - The upstream Discobox per-route authorizer. Its Go source is not in the module cache, so conclusions about who can read service logs and whether bearer tokens are accepted rest on the OpenAPI spec and ADRs.
  - Real-host behavior: whether Claude Desktop or other MCP hosts enforce `visibility:["app"]`, keep `_meta` out of model context, and which iframe sandbox flags they set. These are assumed per the MCP Apps spec.
  - DOMPurify was not run in a browser. The D1 regex behavior was confirmed directly, and DOMPurify applies this regex after stripping whitespace.
- **Test suite:** `go test ./internal/bridge` in a fresh checkout fails until the UI assets are built (`npm run build`). This is unrelated to security. `./internal/discobox` passes.
