# Discobox connection scaffold

The adapter uses the public API and the upstream
[`pkg/endpoint`](https://github.com/discobox-ai/discobox/tree/d6c392a3cce73ee87e716ff6e9c9082fb3ef2c61/pkg/endpoint)
transport from Discobox commit `d6c392a3cce73ee87e716ff6e9c9082fb3ef2c61`
(upstream HEAD fetched on 2026-11-11). `go.mod` pins that source as
`v0.19.2-0.20261111144542-d6c392a3cce7`; builds do not depend on the ignored
reference checkout for Go code. The frontend build still uses that checkout.

```text
Claude / Codex -- stdio MCP ------> adapter
browser ------ HTTP/WebSocket ---> adapter
adapter ------ Iroh -------------> Discobox server
                                  persistent manager
```

HTTP MCP also exists on the adapter's loopback listener. Public HTTPS/WSS hosting
and remote client authentication remain work in `todo.md`.

## Local sandbox API mode

Inside a Discobox, use its pool-forwarded API rather than dialing the host's
Iroh identity:

```sh
discobox api-access request --reason 'Connect the MCP adapter to a persistent manager'
./bin/discobox-mcp --local --transport http --listen 127.0.0.1:8080
```

The access request waits for a person to approve it. Once enabled, `--local`
reads `DISCOBOX_API_URL` (falling back to `DISCOBOX_SERVER`), takes the calling
sandbox ID from `/etc/discobox/sandbox.json`, and uses `DISCOBOX_PROJECT` or
`default` unless `--project` is supplied. It keeps the standard HTTP proxy and
TLS verification configuration. The pool authenticates requests as this sandbox;
the adapter does not send a bearer token, use an Iroh key, or enroll a peer.
`--local` cannot be combined with `--discobox`, `DISCOBOX_MCP_ENDPOINT`, or
Iroh identity/relay flags. This mode is for the sandbox; host processes can
already use `--discobox unix:///absolute/server.sock`.

The upstream implementation is
[`pool-agent/proxyagent/gate.go`](https://github.com/discobox-ai/discobox/blob/d6c392a3cce73ee87e716ff6e9c9082fb3ef2c61/pool-agent/proxyagent/gate.go):
it forwards both JSON requests and WebSocket upgrades, replacing caller-supplied
credentials with the pool's authenticated sandbox assertion. The
[`sandbox role`](https://github.com/discobox-ai/discobox/blob/d6c392a3cce73ee87e716ff6e9c9082fb3ef2c61/server/internal/auth/authorizers.go)
only permits terminal access on boxes currently led by the caller and holds
input/attach to its effective egress permissions.

If a tagged manager exists without this sandbox as parent, startup stops before
creating a replacement. To reuse that manager, request adoption and wait for
the person's approval:

```sh
discobox adopt MANAGER_BOX_ID --reason 'Connect the local MCP adapter to this manager'
./bin/discobox-mcp --local --transport http --listen 127.0.0.1:8080
```

Discovery considers tagged managers led by this sandbox. With no existing
tagged manager, it creates one with API access and lets the server resolve
project defaults, without requesting project settings that the sandbox role
cannot read. Saved state also records the calling parent. Losing parenthood or
API access stops subsequent operations; startup does not replace the manager.

The HTTP mode prints a private terminal URL and an authenticated `/mcp` endpoint.
For an MCP client launching the adapter directly, use `--local` with default
stdio transport, as in `examples/mcp-local.json`. The compiled binary still
needs its frontend assets prepared before building, as described below.

Live verification on 2026-11-11 passed through the pool-forwarded API: manager
creation, MCP initialization and read/send/wait calls, terminal WebSocket output,
and adapter restart with the same saved manager/exec. Chromium loaded the
standalone page, initialized its terminal canvas, and reached `Live` without
JavaScript exceptions. Terminal attaches request `replay=true` so an idle
manager's existing screen appears immediately. Embedded desktop host rendering
remains unverified.

## Identity and enrollment

Build with Go 1.27.1 or later and Node/npm. On a fresh checkout, fetch the
reference source used for frontend brand tokens, install the locked frontend
dependencies, and build the UI before compiling Go so the binary embeds it:

```sh
git clone https://github.com/discobox-ai/discobox.git discobox
git -C discobox checkout d6c392a3cce73ee87e716ff6e9c9082fb3ef2c61
npm ci
npm run build
go build -o bin/discobox-mcp ./cmd/discobox-mcp
bin/discobox-mcp --peer-id --iroh-identity /absolute/durable/adapter/id_ed25519
```

Skip the clone when the reference checkout already exists. Node/npm are only
needed while building; run the compiled adapter directly afterward.

The second command creates or loads a private PKCS#8 PEM key and prints only
its public `d1-...` peer ID. It requires no server connection and does not start
MCP or create a manager. New keys have mode `0600`. Existing corrupt keys fail
without replacement; simultaneous initialization publishes only one key.

On an already authorized Discobox client, enroll that printed adapter ID:

```sh
discobox admin peer add ADAPTER_PEER_ID --name discobox-mcp
```

Run against the intended server using that client's configured server address.
The first enrollment can be performed over the server's local socket. This is
the current command in upstream
[`cli/internal/cli/peer.go`](https://github.com/discobox-ai/discobox/blob/d6c392a3cce73ee87e716ff6e9c9082fb3ef2c61/cli/internal/cli/peer.go).
Enrollment authenticates as the server's default user across projects; the
adapter's `--project` selection does not restrict the enrolled credential's
authority. Revocation uses `discobox admin peer rm ADAPTER_PEER_ID`.

Then start the adapter, using the server's public peer ID as the destination:

```sh
bin/discobox-mcp \
  --discobox 'discobox://SERVER_PEER_ID' \
  --iroh-identity /absolute/durable/adapter/id_ed25519 \
  --project default
```

Replace `SERVER_PEER_ID` with the full `d1-...` ID from the server address.
On the host, `discobox admin id` prints both the CLI client's identity and the
server's identity; use the **server** ID for the destination above. The adapter
ID from `--peer-id` is the one to enroll with `admin peer add`.
The default identity location is the user config directory under
`discobox-mcp/iroh/id_ed25519`. A process uses an ephemeral dialing key and
presents a certificate signed by the persistent enrolled key, matching current
Discobox clients and ALPN `discobox/http/1+cert`. This requires a server with
certificate support; there is no silent downgrade. The upstream package owns
the wire protocol, QUIC connection reuse and redial behavior.

Only peer IDs are accepted in `discobox://` endpoints: DNS names and IP-based
Discobox addresses are outside this initial pinned-peer configuration. Optional
repeated `?addr=IP:port` hints reach a server directly, including
`?addr=127.0.0.1:1234&addr=[::1]:1234`. Custom relay deployments should repeat
`--iroh-relay https://relay.example.com` to match the server. Omitting it uses
the upstream public relay/discovery defaults.

Normal JSON API requests and terminal WebSocket upgrades use the same upstream
transport and identity. `DISCOBOX_MCP_TOKEN` remains an optional separate API
bearer token, useful for HTTP endpoints; peer enrollment is handled by Iroh.
HTTP(S) origins and `unix:///absolute/server.sock` remain supported.

Keep both the identity and manager state on durable storage. Manager selection
continues to use the existing per-endpoint/project state file (or
`--manager-state`). Reuse the same configured endpoint spelling: the current
state key includes the full endpoint, including address hints. A saved manager
is retained when unavailable; a failed or uncertain input is not replayed by
the adapter.

## Native library and verification

The pinned upstream module brings `github.com/discobox-ai/iroh-go v0.4.0`.
Its per-platform native library is embedded in the executable and loaded through
purego; no Rust compiler or separate library download is needed at runtime.
The bindings ship macOS, Windows and Linux for amd64/arm64, with additional
Linux musl variants selected using `-tags musl`. Other architectures are outside
this scaffold's support.

On first Iroh use the library is extracted to the user cache directory, which
must be writable and permit loading executable libraries. `IROH_GO_CACHE_DIR`
overrides the cache and `IROH_GO_LIBRARY` specifies an existing library path.
See the pinned upstream
[packaging decision](https://github.com/discobox-ai/discobox/blob/d6c392a3cce73ee87e716ff6e9c9082fb3ef2c61/docs/adr/0067-iroh-ships-in-every-build.md)
and the [bindings](https://github.com/discobox-ai/iroh-go/tree/v0.4.0).

`internal/discobox/peer_test.go` exercises concurrent identity creation, restart
reuse, corruption refusal, target validation, certificate enrollment checks,
API requests and terminal attach over real loopback Iroh streams. Relay fallback,
two-machine connectivity, server restart recovery and desktop embedded UI
rendering still need live verification. This transport scaffold does not finish
UI packaging or hosted deployment.
