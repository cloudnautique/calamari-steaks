# Managers in chat and the canvas

A manager is a persistent Discobox running an agent. The Go adapter provides
both text tools and an MCP App UI for the same manager catalog. Closing a canvas
or switching views detaches a terminal; it does not end the manager.

```text
Codex / Claude chat ---- MCP tools --------------------+
                                                      |
Canvas HTML resource -- host's MCP App bridge --------> Go adapter
                                                      |
                                         Discobox API +-- manager A
                                                      +-- manager B
```

The adapter is configured for one Discobox endpoint and project. `--local`
uses this sandbox's pool-forwarded API and sandbox identity. Other configured
HTTP, Unix or Iroh endpoints keep their existing authentication. In local mode,
listing may show managers this sandbox does not lead, but connecting, sending
and archiving remain subject to current parent and egress permissions.

## Start the workspace

```sh
./bin/discobox-mcp --local --browse --transport http --listen 127.0.0.1:8080
```

`--browse` starts the picker without creating a manager. The adapter remains
usable when every manager is archived or when no manager exists. Omitting it
preserves startup discovery/reuse/creation, and `--box` still pins an explicit
startup default. `examples/mcp-local.json` launches the workspace over stdio.

For an existing HTTP client connection, reload MCP discovery after upgrading
so the client sees the new tools and `ui://discobox/managers-v3.html` resource.
Previously published terminal resource URIs remain readable.

## Text and UI operations

| Tool | What it does |
| --- | --- |
| `list_managers` | Returns manager IDs, names, state, archive status and access eligibility. Opens the picker in UI-capable hosts. |
| `create_manager` | Creates one manager with a durable `requestId`, optional name, harness and pool. Returns its pinned session and opens the terminal. |
| `connect_manager` | Connects a specified `managerId` and opens its terminal. Does not change another view's selection. |
| `open_manager` | Opens a specified manager or the startup default; opens the picker when there is no default. |
| `read_manager` | Reads a specified manager's screen and optional scrollback as structured/text output. |
| `send_manager` | Sends text to that manager, optionally submitting it. Returns a `resumeAfter` cursor. |
| `wait_manager` | Waits from that cursor for progress, completion or a timeout. |
| `interrupt_manager` | Sends Ctrl-C to the specified manager. |
| `archive_manager` | Archives a manager when finished; never permanently deletes it. |

For example, ask chat to “show my managers,” “create a manager named
release-review,” or “connect to release-review and ask it to inspect this
repository.” These are normal MCP tools and return useful results in clients
that do not render a UI. The manager needs its own source instructions; it does
not automatically share the adapter's checkout.

After listing or creating, carry the returned manager ID into subsequent calls:

```json
{"managerId":"sbx_…","text":"Inspect the repository and propose a plan. Wait for approval before editing.","submit":true}
```

Use `send_manager`'s `resumeAfter` in `wait_manager` with that same `managerId`.
A timeout does not prove completion. Without an explicit startup default,
calls that omit `managerId` fail rather than targeting a global last-selected
manager.

Creation requires a stable key, for example:

```json
{"requestId":"release-review-1","name":"release-review"}
```

Repeating the same key and settings reuses the saved creation/session. A changed
configuration under the same key is refused. If the create result is uncertain,
the intent stays reserved and no new create is automatically attempted. The UI
keeps the key for its “Check creation” action. Names use at most 64 letters,
digits, underscores or hyphens; omitted names are generated.

## Canvas behavior

The picker shows active managers, with an option to include archived ones.
Each card has a name, ID, state, open action and an explicit archive action.
The create control starts a manager using the project's configured harness and
pool. Managers requiring adoption are visible but cannot be opened here until
the relevant approval is obtained.

Opening a card displays the live Ghostty terminal and a message composer.
Take control to type or send from the composer. Release control to let chat
send instructions. Input ownership belongs to each manager: controlling A
does not block chat from working with B. The Managers button returns to the
picker once control is released.

The canvas includes its scripts, renderer and WASM in the resource and uses
app-only MCP terminal tools for I/O. It needs no direct network connection and
does not rely on form submission permissions in the host iframe. The standalone
browser page offers the same catalog operations through authenticated HTTP and
uses WebSocket terminal streaming.

Each connection keeps its manager and exec IDs pinned. Selecting a different
manager opens a separate connection; it never retargets existing views or
queued input. Archiving is separate from detaching, refuses while a person
holds control, and invalidates connected views. An uncertain archive result
should be checked by listing managers before trying again.

The archive API acknowledges the request with an empty `202 Accepted` response;
the picker follows the desired archive state while the backend finishes it.
Empty successful archive responses are handled without requiring JSON.

A stopped manager may need tens of seconds to boot when a terminal view attaches.
The canvas allows 45 seconds for establishing that stream, inside the usual
60-second MCP tool budget. This startup deadline is removed once connected;
it does not limit the live terminal's lifetime. If startup still times out,
reconnect to the same manager once it is running. Input is not sent or replayed
by the attach operation. HTTP permission/session failures retain their status
codes without exposing upstream error bodies.

## Verification

Tests cover scoped chat and input ownership across two managers, independent
exec pinning, access boundaries, archive behavior, creation reuse and uncertain
creation without duplicate retries. Live checks exercise the local Discobox API
and the self-contained canvas in an opaque iframe with network and native form
submission blocked. Native Codex canvas rendering was confirmed during the
terminal work; the expanded picker flow and Claude desktop still need native
host verification.

## Task-first workspace

`list_boxes` opens a launcher and the catalog of task boxes and managers made
through this adapter. `launch_box` takes `requestId`, `task`, and optional
`name`, `harness`, `model`, and `pool`. These settings apply to this run only;
they never change a saved harness. A task box starts without Discobox API access
or explicit credential bindings. The selected harness's existing configuration
still applies. Legacy `create_manager` keeps its coordinator role and API access.

The launcher currently starts with an empty workspace. Registering a repository
source also requires delivering its Git objects into the pool cache; accepting
just a URL would leave the box waiting for source delivery. Source delivery,
desktop streaming, service previews, and structured change/artifact results are
follow-up work, not controls in this first workspace.

`open_box` takes `boxId` (`managerId` remains a legacy alias) and opens the
live terminal immediately. Displaying the widget attaches to the pinned session
and can wake its box. Task details are collapsible; the terminal remains live.
There is no output refresh button or separate snapshot view. New bytes arrive
through the MCP App bridge, or WebSocket in the standalone browser. Opening a
viewer does not claim input; focusing the composer or terminal does. A failed
send retains its text and requires the person to check the live output before
allowing resubmission. Input is never automatically replayed.

All text mechanisms remain available: use the returned `managerId` with
`read_manager`, `send_manager`, `wait_manager`, and `interrupt_manager`.
`inspect_box` is a read-only snapshot alternative. A terminal screen is untrusted
output, not a structured conversation or proof that checks passed.

`list_attention` opens the **Needs you** inbox and also returns plain text/JSON.
It projects pending approval requests stored in Discobox and the primary agent's
reported blocked/error/done states. A running container alone never implies that
its agent is working or finished. Observation timestamps distinguish last-known
reports. The open widget refreshes the inbox and selected box's reported status
approximately every 15 seconds; a closed widget cannot produce background chat
or OS notifications. Reopening reconstructs pending items from Discobox.

The inbox is scoped to accessible, non-archived boxes. Local sandbox
API mode can list their credential requests, but cannot read the human-only API,
adoption, trust, or refresh inbox. The UI explicitly reports this coverage gap;
a failed refresh shows unknown coverage rather than a misleading zero badge.
Human-token connections use the unified approval listing, still filtered to
accessible boxes; project-wide refresh requests remain in the
console. This adapter does not collect credential values or approve requests
under the person's identity. Use the Discobox console's approval inbox to answer
requests, then refresh this view.

The current resource is `ui://discobox/workspace-v4.html`. Previously published
resource URIs remain aliases so restored desktop sessions can still initialize.

This task-first pass was verified against the live local API in an opaque
Chromium iframe with `connect-src 'none'`: launch, overview message and actual
harness response, inbox reconstruction after reopening, explicit terminal
attachment, take/release control, and detach back to the overview. The temporary
test box was archived afterwards. No direct HTTP requests from the iframe or
browser exceptions occurred during the final interaction check. Native Codex
and Claude validation of this expanded flow is still pending.

## Keeping the desktop assistant in sync

The `list_boxes` result uses `boxes` and explicitly labels its API-visible
project scope. A manager is a box with the coordinator role, not an extra session inside
a task box. `managerId` remains the historical box-ID parameter for existing
tools. UI-opening tool descriptions distinguish rendering a widget from reading
a text snapshot. A successful tool response alone does not prove host rendering.

Canvas launch/open/archive, instruction delivery (including uncertain delivery),
and terminal ownership actions now send bounded, recent action receipts and the
active box ID through the MCP Apps `updateModelContext` bridge. Returning to the
catalog clears the active selection. User text excerpts are capped at 2000
characters and labeled as untrusted data. Raw terminal keystrokes and credential
values are not included. Full terminal output still requires the explicit Share
output action. Context updates are serialized, and rejection never retries the
underlying launch or instruction. The footer reports context synchronization
failure without blocking work.

These are context updates for a subsequent chat turn; they do not force the
host to run the model or send a new chat message. The host must support MCP Apps
context updates. Refresh/reconnect the desktop MCP connection after an upgrade
so its tool descriptions are current. `npm test` checks context ordering and
host rejection; an opaque iframe bridge check also verified selection updates
and clearing the active selection without writing to any box.

## Focus-driven input handoff

The person no longer needs a separate Take control click. Focusing the box
composer or raw terminal claims that view's input ownership; leaving the input
areas, hiding the terminal, switching windows, or hiding the document yields it.
The input indicator shows whether the person or assistant can write. Send and
already queued keystrokes finish before the release, so a blur cannot split a
submission. A pending claim followed by blur releases after the claim returns.
Two writers still cannot interleave terminal input. Focus is not a signal that
an agent operation has finished, and commands already running continue.

Focusing an unattached composer attaches to the selected session first and may
wake its box. Observing output does not claim input. Terminal focus fits the
shared grid to the visible pane; observers retain the existing session width.
Hidden or collapsed panels cannot resize the PTY. Visible resizes are debounced
and repeated dimensions are skipped; the toolbar shows columns and rows.

The focus handoff was checked in the opaque iframe through the live MCP bridge:
composer focus/blur, terminal focus and fit, and yielding on terminal hide. No
instructions were sent in that check. Unit tests cover blur during a pending
claim, release after a pending submission, failure without replay, and rejecting
hidden/invalid layout measurements.


## Direct interaction with any box

`list_boxes` now lists every box the configured project API exposes, including
boxes made in the Discobox console and other clients. Opening, reading, sending,
waiting, interrupting, attaching a terminal, and archiving require no adapter tag
or manager role. The Manager badge identifies a coordinator; it does not unlock
a different interaction path. Tasks and harness details also appear for ordinary
untagged boxes.

Use `open_box` / `inspect_box` with `boxId`, and the canonical text tools
`read_box`, `send_box`, `wait_box`, `interrupt_box`, and `archive_box` with the same
`boxId`. `send_box` returns a `resumeAfter` cursor for `wait_box`. The existing
manager-named tools remain compatible. `open_box` and `inspect_box` still accept
`managerId` as a legacy alias, rejecting conflicting targets.

Visibility is not authority: sandbox API mode can control only boxes it leads.
Other visible boxes require adoption approval, as the list indicates. The
adapter keeps parent checks and the server checks permissions on actual API
calls; no approval is bypassed by removing a tag filter. Each connected box
retains its independently pinned exec and human-focus ownership. The standalone
page uses the same target checks as the MCP tools.

Direct access was verified live with a temporary box created through the bare
Discobox API, without adapter tags: listing, open/read/send/wait, terminal
attachment, human ownership blocking model input, release/detach, and archive.
The test box was archived after the check. Automated coverage also exercises
interrupt, standalone box-ID routing, conflicting/invalid targets, denied
parent access, archived-session refusal, and legacy manager-call compatibility.

Box names follow Discobox's authoritative `displayName`, matching the console's
task/session titles. Older servers fall back to meta name, configured name, then
box ID. Names can change as the harness reports its title; every action remains
pinned to the stable box ID. Selected workspace labels follow refreshed inventory
without replacing the selection or discarding a draft instruction.


## Live output and reconnection

The open workspace immediately starts the terminal stream and initial replay.
MCP Apps keep an outstanding cursor read; the adapter wakes it as soon as bytes
arrive rather than waiting for the 12-second idle timeout. The standalone page
uses the existing WebSocket byte stream. Task details do not stop observation.
Dropping observation triggers bounded-backoff reconnection to the same pinned
box/exec; it never resubmits user input. Explicit detach or leaving the workspace
cancels reconnection. Actual process exit, changed exec identity, or authorization
failure requires a deliberate reopen rather than silently replacing a session.

Automated stream tests verify new output wakes a pending read without repeated
screen requests, and distinguish transport drops from process exit. An opaque
iframe check verifies automatic attachment, fresh bytes repainting the renderer,
and reconnecting after an injected read failure without input writes or snapshot
requests. The injected bytes are test fixtures, not output from the research box.

The box catalog requests at most 520px of height (less in a constrained host)
and scrolls internally, independent of the box count. The launcher is collapsed
under Start a new box. Terminal and file workspaces retain their own sizing.
A 100-box opaque-iframe check verified bounded height, internal scrolling,
launcher expansion, and a narrow 360px-high host without page overflow.
