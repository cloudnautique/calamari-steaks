# Claude Desktop extension

Claude Desktop's local extension format is `.mcpb` (formerly `.dxt`). This
bundle installs the compiled adapter; Claude launches it over stdio. No Node,
Go, `mcp-remote`, manually started MCP server, or adapter bearer token is needed
on the user's machine. Discobox itself must already be running and accessible.

## Install

Choose the bundle matching your machine:

- `discobox-mcp-0.10.0-darwin-arm64.mcpb`: Apple Silicon Mac.
- `discobox-mcp-0.10.0-darwin-amd64.mcpb`: Intel Mac.
- `discobox-mcp-0.10.0-windows-amd64.mcpb`: Windows x64.
- `discobox-mcp-0.10.0-windows-arm64.mcpb`: Windows ARM64.
- `discobox-mcp-0.10.0-linux-amd64.mcpb`: Linux x64 (glibc build).
- `discobox-mcp-0.10.0-linux-arm64.mcpb`: Linux ARM64 (glibc build).

In Claude Desktop, open Settings → Extensions → Advanced settings → Install
Extension, then select the bundle. Organization policies may restrict local
extensions. These development bundles are unsigned; they are not directory
submissions or notarized releases. They have been cross-compiled and validated,
but native macOS installation still needs testing.

Leave the optional connection/project overrides blank for guided setup. Once
installed, ask the assistant to **connect to Discobox**. The setup widget discovers
the local socket or guides remote peer enrollment, tests the connection, and saves
a private profile. Restart the adapter to apply changed settings. See
[connection-setup-widget.md](connection-setup-widget.md).

Advanced overrides pin the connection and make the widget read-only. The endpoint
is Discobox's API, not `http://127.0.0.1:8080/mcp` (the separate adapter). An API
token is optional and applies only where the upstream deployment accepts one.
It is not the bearer token for a separately running MCP adapter.

Peer connections retain the adapter's existing per-user enrollment identity;
that peer must be enrolled in Discobox. The desktop user must be able to open the socket, but socket access alone
does not authenticate to Discobox. A server requiring authentication still needs
a supported authorized connection (for example an enrolled Iroh peer). The optional
token setting only applies to deployments that accept that credential; it is not
a promise of built-in bearer-token login. `--local` means sandbox pool-forwarded access, so this host extension
uses an explicit connection URL instead. No installation-specific socket, peer,
credential, or sandbox ID is baked into the release.

Disable the older manually configured Discobox entry if you replace it with
this extension, to avoid duplicate tool sets. The extension starts in browse
mode and does not create a manager on startup. Ask Claude to list boxes to begin.

The terminal, Files, approvals, and Desktop remain MCP App resources served by
the same binary. Their private app-only tools still use the host bridge; packaging
does not put pixels or clipboard contents into model context. Rendering requires
MCP Apps support in the client. Desktop transport retains Discobox's permissions.
The adapter reserves an ephemeral loopback listener for its existing browser
support; MCP communication with Claude uses stdio.

## Build

Requirements: Node/npm, Go as specified in `go.mod`, and the reference Discobox
checkout required by `scripts/build-ui.mjs` for its brand tokens. From the repo:

```sh
npm ci
npm run package:mcpb -- darwin-arm64 darwin-amd64 windows-amd64 windows-arm64 linux-amd64 linux-arm64
```

The script builds and embeds the frontend before cross-compiling, validates and
packs with pinned `@anthropic-ai/mcpb`, and writes bundles plus SHA-256 checksums
under `dist/`. It stages only the binary, manifest, installation guide, and frontend
third-party notices. It never packs the repository root, logs, screenshots,
credentials, or enrollment keys. Iroh's platform library is embedded by its Go
module. Developer dependencies are not shipped.

`linux-amd64` and `linux-arm64` are additional build targets for compatible hosts
and local stdio smoke checks; these do not assert Claude Desktop availability on
Linux. Windows bundles contain a native .exe and declare win32 compatibility. Local
Windows discovery uses Discobox's named pipe; macOS/Linux use a Unix socket.
A server running inside WSL has a different local endpoint; use an authorized
Iroh connection rather than assuming the Windows pipe reaches its Unix socket.
Windows/macOS native installation and Windows ARM64 execution remain unverified.
Cross-compilation does not establish those runtime behaviors.

Specification: https://github.com/modelcontextprotocol/mcpb/blob/main/MANIFEST.md
CLI: https://github.com/modelcontextprotocol/mcpb/blob/main/CLI.md

Verification in the sandbox: both macOS architectures cross-compiled, all bundle
manifests passed the official packer's validation, archive file allowlists and
executable permissions passed, and SHA-256 checksums matched. An extracted Linux
ARM64 archive initialized over stdio, listed the workspace tools, and returned the
self-contained workspace resource with terminal, files, and desktop panes. This
smoke check intentionally used an unreachable API origin: browsing startup must
not contact the backend or create a box. It is not a native macOS install test.
