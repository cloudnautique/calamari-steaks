# Services and local TCP forwarding

`list_services(boxId)` opens the Services tab. It combines declared service names,
status and ports with current agent port metadata, including ports that have no
service declaration. Service catalog reads may wake a box. Existing permissions
and project/parent checks apply; denied/unavailable management is distinguished
from an empty service list. Forwarding never creates or starts a service.

Start/Stop/Restart acts on an existing declaration through `control_service`.
New declarations must be created in the box by its harness or project source.
No new arbitrary-command tool was added. Mutations are not automatically retried;
check service state after an unconfirmed response before another explicit action.

The private log viewer reads recent output only after Logs is selected, then
updates while visible. App-only `service_logs` returns bytes in `_meta.services`,
never in structured/model content. Output is capped at 100 entries/64 KiB, within
the upstream 2 MiB response limit. Large upstream logs beyond that cap are refused,
not silently presented as empty. `read_service_logs` is the separate text tool for
an explicitly requested model-visible read. Log text is untrusted plain text.

## Open a port

`open_service(boxId, port)` validates a fresh agent-reported TCP port, its box
scope, and a TCP tunnel handshake before binding an ephemeral **127.0.0.1** port.
It rejects unreported ports, UDP, invalid targets, and mismatched service IDs.
Declaration-only entries awaiting an agent report are listed but not forwarded.
The destination host is derived from current bind-address metadata, never accepted
from the caller. Loopback/wildcard binds use localhost for IPv4/IPv6 fallback;
specific IP binds retain their reported address.

The URL belongs to the machine running the adapter. A Windows/macOS/Linux native
adapter can open it in that machine's browser. A URL on a hosted adapter is not
magically reachable on the user's localhost. Hosted HTTPS gateways and nested
browser previews remain separate work. No service HTML is embedded in the trusted
MCP App, and no service content, credentials, cookies, or TCP bytes enter a tool
result. Only forwarding identity/address/status metadata does.

The tunnel is Discobox's framed TCP WebSocket, preserving byte traffic, half-close,
HTTP, WebSocket upgrades/HMR, SSE, and application auth. Outer Discobox credentials
remain on the tunnel handshake, never injected into the application byte stream.
HTTP(S)-identified ports get a browser link. Other TCP ports get an address to
use with the relevant client. Forwarding TLS does not fix certificate hostnames. Hard-coded absolute URLs or
redirects may require the service's public-origin configuration.
A forward is not service authentication: preserve the application's own access
controls. Ports on one localhost host also share cookie scope, despite distinct
web origins; applications may need distinct cookie names or hostnames.

**The sandbox API role explicitly disallows TCP tunnels.** This adapter's `--local`
mode therefore offers service controls/private logs where permitted, with a clear
forwarding limitation. Use an authorized host connection for forwards. Unix socket
reachability alone is not user authentication; peer enrollment and upstream auth
requirements still apply. No exec-based tunnel or permission bypass is attempted.

## Lifetime and limits

- At most eight forwards per adapter. Each splices 32 connections at once;
  up to 64 more wait 15 s for a slot instead of being reset (0.12).
- The local port is the box's own port number when free, otherwise the address
  this box/port had earlier in the process, otherwise any free port, so
  bookmarks and OAuth callbacks survive a reopen.
- Port metadata and box authority are checked when the forward opens and every
  30 s; a connection arriving while that check is older than 30 s checks again.
  A changed destination closes the binding rather than retargeting it; a port
  that is briefly unreported (a restarting dev server) does not.
- Established streams have no idle deadline (quiet WebSockets and SSE stay
  open); TCP keepalive is on. Input/traffic is not retried automatically.
- An inactive forward expires after fifteen minutes (two hours while it still
  has open connections). Traffic or the visible
  Services widget renews it. Closing the widget stops its renewal but leaves a
  browser still using the forward alone. Box access is revalidated every thirty
  seconds; archive, lost scope, or failed validation closes the forward.
- `close_service_forward(forwardId)` closes the listener and connections only.
  The remote process stays running. Adapter exit closes every forward. A new
  adapter process allocates new addresses; nothing is persisted or auto-reopened.
- Existing-stream revocation semantics still require a real host test; metadata
  authorization checks are not a claim that upstream closes every old stream.

Local `list_service_forwards` is read-only; `service_keepalive` is app-only. Neither
sends application content to the model. Standalone service operations use the
existing bearer/Host/Origin-protected `/api/services/` routes.

## Evidence and remaining validation

Fixture tests exercise byte transfer and half-close, repeated-open binding reuse,
invalid/unreported/UDP targets, sandbox-role refusal, HTTP 403 with no listener,
service IDs/path validation, private-vs-explicit log reads, non-retried lifecycle,
HTTP streaming, WebSocket echo, and no upstream auth in service requests. Existing
Go race checks pass. Live box TCP forwarding remains unavailable under this
sandbox identity; actual host Iroh/Unix forwarding, native browser opening, cross-
platform listeners, service restart/revocation, and long-lived HMR need acceptance
testing. UI checks use isolated synthetic services rather than real mutations.

The opaque-frame UI fixture also verified Services opens without a primary
terminal attach, scoped lifecycle calls are submitted once, private log text stays
out of model-context updates, browser links use host openLink, Close forward
restores the port action, and UDP entries show an explicit limitation. No direct
iframe network requests or browser exceptions occurred.

## Opening outside the chat client

**Open via chat app** uses the MCP host's link opener and may use its built-in
browser. **Open in default browser** is an app-only request to the adapter:
`open` on macOS, `rundll32 url.dll,FileProtocolHandler` on Windows, and `xdg-open`
on Linux. It accepts only an existing HTTP(S) forward handle; no arbitrary URL
or shell command can be supplied. A host with no graphical session should use
Copy address and open it on the adapter machine. Browser launch is a request,
not confirmation the page loaded.

The Services view shows the current idle-expiry time and a bounded history of
closed forwards (the latest 16 per adapter), including idle expiry, failed access
revalidation, and a changed destination. Adapter restart loses that history and
all old addresses. A connection-refused error means the address had no accepting
listener at that time; it is not evidence of a Claude-only forward. Reopen a
forward explicitly to get the current address. Forwards remain loopback-only.
