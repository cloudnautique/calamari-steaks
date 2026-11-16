# Connection setup widget

The adapter starts in browse/setup mode by default. Installing the 0.10.0 MCPB
requires no connection settings. Ask the assistant to **connect to Discobox**;
`open_connection_setup` opens the independent setup app, including when the
server is offline, the profile is corrupt, or the Iroh key cannot be loaded.
Ordinary backend tools remain unavailable when initialization failed.

## Local

“This computer” means the machine running the adapter. On macOS/Linux, `unix://` uses Discobox's
shared platform resolver, including `DISCOBOX_SERVER_ADDRESS`; it does not start
a server or scan arbitrary filesystem locations. A custom absolute socket URL
is supported. Windows defaults to `npipe://`, resolved through the same upstream
resolver (normally `npipe:////./pipe/discobox`); a custom local named pipe is supported.
Test connection performs one bounded GET of the selected project's
box catalog. A successful socket open is not API authentication: 401/403 remain
visible as authorization failures. An empty catalog can still be a valid connection.

## Remote

Enter `discobox://<server-peer-id>`, select a project, and optionally supply up to
four custom relay URLs. Show my peer ID loads or initializes the adapter's own
persistent key; only its public ID leaves the process. Copy the enrollment command
and have an already-authorized operator run it on the intended server computer:

```sh
discobox --server unix:// admin peer add ADAPTER_PEER_ID --name discobox-mcp
```

This enrolls the adapter as the server's default user, not a project-scoped role.
The widget cannot approve itself, request a pairing code, or grant sandbox TCP
permissions. An operator can use another already-authorized server endpoint in
place of `unix://`. A timeout does not establish that enrollment was denied.

Copy uses the clipboard only after a button press and selects a manual-copy
fallback when the host denies clipboard access. Corrupt identity files are kept;
there is no automatic identity rotation. Native relay/two-host enrollment testing
is still required.

## Saving and applying settings

Profiles can be saved offline. The profile contains only endpoint, project and
relay URLs, is written with a temporary-file/rename replacement (0600 on Unix; Windows
permissions follow the user-profile filesystem ACLs), and normally lives
under `os.UserConfigDir()/discobox-mcp/connection.json`. The identity remains in
`discobox-mcp/iroh/id_ed25519`, outside the extension bundle. `--connection-config`
and `--iroh-identity` can set explicit locations.

**Restart the adapter to apply a changed profile.** Disable/re-enable its desktop
extension or restart the client's MCP connection. Saving does not retarget a
terminal, desktop, approval review or file view. The setup widget says when restart
is required. `connection_status` reports the last explicit test, not live health.
Testing a draft does not declare the currently active connection healthy.

Nonempty `--discobox`/`DISCOBOX_MCP_ENDPOINT`, an explicit project override, or
`--local` pin the connection to its launcher. The widget then displays read-only
settings and allows testing that exact target; it does not overwrite the profile.
MCPB install fields are optional advanced overrides; leave them blank for guided
setup. Upstream API tokens are not stored in the connection profile or exposed by
the widget. Candidate probes inherit a token only for the exact current endpoint,
and never for an Iroh draft. No keys are borrowed from Discobox UI or the CLI.

A saved profile is used when present; otherwise startup uses local discovery.
Explicit launcher settings take precedence. Explicit `--box` or manager startup options retain the
legacy startup behavior. `--browse=false` opts into startup discovery/creation and
can wait for the backend; the MCPB and ordinary default startup use browse mode.

## Text tools and standalone fallback

- `open_connection_setup`: opens the UI and supplies plain-text structured status.
- `connection_status`: reads settings, last test, pending restart and public identity.
- `test_connection`: tests only the current configured target.
- `get_connection_peer_id`: loads/creates the durable identity and returns its public
  ID plus the operator command. It performs no enrollment or network call.
- `connection_test` and `connection_save` are app-only. Their details are in
  `_meta.onboarding`, with a constant model-visible acknowledgement.

Text-only clients can read the instructions/public ID, test the active target,
and set launcher settings or edit the documented profile, then restart. The widget
is served as `ui://discobox/connection-v2.html` (the v1 alias remains readable) and uses only the MCP Apps bridge.
No browser fetch of a socket or Iroh connection is attempted. Direct connection
probes run in short-lived subprocesses, keeping global Iroh transport configuration
isolated from live sessions. No failed probe, input, or enrollment is retried.

A private `/setup#token=…` URL is printed for the standalone setup page. Its JSON
routes require the adapter's existing bearer, allowed Origin, and exact Host.
The root workspace and legacy resource aliases remain available.

## Verification

Go unit and child-process tests cover discovery, nil-backend gating, profile/key
corruption, default socket resolution, private atomic persistence, managed settings,
identity reuse, HTTP authorization results, and read-only probes. An isolated real
stdio check exercised first launch, missing socket, saving without retargeting,
restart into a working fixture Unix socket, and invalid-endpoint redaction.

An opaque browser iframe with `connect-src 'none'` exercised the actual compiled
adapter and helper probes: missing-socket feedback, remote public ID, copy fallback,
offline remote saving, local fixture test/save/restart, fixed canvas height, and
launcher-pinned read-only settings. There were no direct iframe HTTP requests,
browser exceptions, or background model-context updates. No real boxes or approvals
were used. Native Claude/Codex setup and real two-host Iroh still need validation.
