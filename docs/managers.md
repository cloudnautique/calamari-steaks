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
