# Desktop in the MCP canvas

`open_box_desktop(boxId)` opens the existing MCP App with Desktop selected.
The same Desktop tab is available beside Terminal and Files for either box role.
It uses bundled noVNC 1.7.0 with a custom channel: no browser WebSocket, external
iframe, localhost fetch, or graphics tool call by the model is needed.

Reconnect the desktop client's MCP connection after upgrading, close an old
widget, and ask: "Open the desktop of box BOX_ID in the canvas."
The host decides where to render the app; the server cannot force host chrome.

## What stays private from model context

Only box/project identity, view selection, connection state, and focus receipts
are shared as app context. `open_box_desktop` returns that identity and a short
status. Opening the canvas does not give the assistant vision of its desktop.

The app-only `desktop_open`, `desktop_read`, and `desktop_action` tools return
all session handles and VNC bytes in tool-result `_meta.desktop`. They never put
graphics in `content` or `structuredContent`. Their text content is a constant
transport acknowledgement. Input travels in app-initiated tool arguments and is
not copied to context. No automatic screenshot, desktop title, clipboard bridge,
chat message, or framebuffer MCP resource is registered. The `ui://` resource
contains the bundled renderer, not a captured desktop.

The MCP host still transports the data and may log it. App-only visibility is a
host-facing capability declaration, not a separate authentication principal.
Transport metadata also keeps payloads out of model-visible output if a host
were to expose an app-only tool accidentally. HTTP request logging records paths
and method timing, not pixel or input bodies.

## Transport and lifecycle

The adapter discovers the declared `ai.discobox.desktop` service, not an arbitrary
port number, URL, or VNC host supplied by a caller. It checks the configured
project, parent permission, and lifecycle before connecting. No primary terminal
exec is needed. Attaching can wake a stopped box. Archived boxes must be restored
through Discobox first.

The desktop viewer binds loopback, which Discobox's container-IP HTTP port proxy
cannot reach. The adapter therefore opens `/api/projects/PROJECT/sandboxes/BOX/
tcp/attach?host=127.0.0.1&port=DECLARED_DESKTOP_PORT`, decodes the TCP tunnel's
framing, and opens the viewer's `/websockify` over that connection. The upstream
credential is used only on the outer API handshake, never sent into the desktop
service or canvas. Desktop bytes then travel over the private app tool channel.

Reads long-poll for at most 12 seconds and return at most 128 KiB. Acknowledgement
cursors allow read replay; producer backpressure bounds buffered output to 2 MiB
per session without losing bytes midway through a VNC frame. Writes are batched
up to 64 KiB, client pending input is capped at 256 KiB, and server sequence
numbers deduplicate the last acknowledged batch. Uncertain input closes the
channel and is never automatically replayed. At most four desktop opens/views
are allowed at once. An abandoned view expires after 90 seconds.

Opening starts as a viewer. Focus in the desktop claims the same input ownership
used by terminal and assistant writes; leaving focus yields it. The first click
may only acquire focus, rather than replaying a click after an async claim. A
second view cannot take an existing controller's lease. The display scales to
its available canvas without changing the remote desktop resolution. Leaving
the Desktop tab, changing box, closing the widget, or archiving the box closes
the desktop connection. An existing terminal stream can remain connected.

## Current Discobox permission limitation

The current sandbox API role explicitly rejects TCP tunnels, including tunnels
to boxes the caller leads. This is tested as Forbidden in Discobox's
`server/internal/auth/sandbox_role_internal_test.go`. API-access approval alone
does not grant this route, and there is no per-widget toggle to enable it.
The adapter reports the denial; it does not tunnel through an exec as a bypass.

The supported deployment for this prototype is an adapter running with an
authorized user connection, such as an enrolled adapter identity on the host.
Build the current adapter there using the existing connection guide, then run:

```sh
./bin/discobox-mcp \
  --discobox discobox://SERVER_PEER_ID \
  --browse --project default \
  --transport http --listen 127.0.0.1:8085 \
  --access-token-file .discobox-mcp/access-token
```

Use the adapter's already-enrolled identity (or enroll the new identity using
`discobox admin peer add`, as described in connection.md). Port 8085 lets it run
beside the sandbox-forwarded adapter on 8080. Point Claude's mcp-remote URL at
`http://127.0.0.1:8085/mcp` and use the bearer belonging to that host process.
The URL/token do not migrate automatically from the sandbox process.
An authorized Unix-socket or HTTP connection is another supported endpoint.

## Verification and remaining validation

Go tests cover declared-service discovery, parent/lifecycle denial, the nested
TCP/WebSocket transport, credential isolation, metadata-only output, input
sequence deduplication, shared control with later terminal connection, output
batch caps, and backpressure transferring more than 4 MiB without lost bytes.
Node tests cover ordered batched writes, queue bounds, renderer byte delivery,
and failure without automatic input retry.

An isolated Chromium opaque iframe decoded and painted a real 1280x1024 VNC
framebuffer through this MCP relay. It used this sandbox's own desktop behind a
fixture API, not a user task box or a bypass of the sandbox-role denial. The
check verified focus claim/yield without test keystrokes, narrow/short display
scaling, close on leaving, zero direct iframe network requests, zero browser
exceptions, and no graphics in model-facing content or context updates.

Live task-box desktop validation is blocked by the sandbox API role. Native
Claude/OpenAI desktop validation using an authorized host adapter is pending.
