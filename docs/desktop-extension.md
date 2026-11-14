# Claude Desktop extension

Claude Desktop's local extension format is `.mcpb` (formerly `.dxt`). This
bundle installs the compiled adapter; Claude launches it over stdio. No Node,
Go, `mcp-remote`, manually started MCP server, or adapter bearer token is needed
on the user's machine. Discobox itself must already be running and accessible.

## Install

Choose the bundle matching your machine:

- `discobox-mcp-0.8.0-darwin-arm64.mcpb`: Apple Silicon Mac.
- `discobox-mcp-0.8.0-darwin-amd64.mcpb`: Intel Mac.

In Claude Desktop, open Settings → Extensions → Advanced settings → Install
Extension, then select the bundle. Organization policies may restrict local
extensions. These development bundles are unsigned; they are not directory
submissions or notarized releases. They have been cross-compiled and validated,
but native macOS installation still needs testing.

Set the connection URL to **Discobox's API**, for example
`unix:///absolute/path/to/server.sock` for a host-local installation. Use the
actual absolute socket path from your Discobox installation. An HTTP(S) API
origin or `discobox://<server-peer-id>` is also supported. Do not enter
`http://127.0.0.1:8080/mcp`: that is the separately running adapter, not Discobox.
Set the project (normally `default`) and an upstream API token only if your
connection requires one. The token field is sensitive and passed via environment,
never included in the bundle. Install settings cannot grant backend permissions.

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
npm run package:mcpb -- darwin-arm64 darwin-amd64
```

The script builds and embeds the frontend before cross-compiling, validates and
packs with pinned `@anthropic-ai/mcpb`, and writes bundles plus SHA-256 checksums
under `dist/`. It stages only the binary, manifest, installation guide, and frontend
third-party notices. It never packs the repository root, logs, screenshots,
credentials, or enrollment keys. Iroh's platform library is embedded by its Go
module. Developer dependencies are not shipped.

`linux-amd64` and `linux-arm64` are additional build targets for compatible hosts
and local stdio smoke checks; these do not assert Claude Desktop availability on
Linux. Windows packaging is not currently provided.

Specification: https://github.com/modelcontextprotocol/mcpb/blob/main/MANIFEST.md
CLI: https://github.com/modelcontextprotocol/mcpb/blob/main/CLI.md

Verification in the sandbox: both macOS architectures cross-compiled, all bundle
manifests passed the official packer's validation, archive file allowlists and
executable permissions passed, and SHA-256 checksums matched. An extracted Linux
ARM64 archive initialized over stdio, listed all 37 tools, and returned the
self-contained workspace resource with terminal, files, and desktop panes. This
smoke check intentionally used an unreachable API origin: browsing startup must
not contact the backend or create a box. It is not a native macOS install test.
