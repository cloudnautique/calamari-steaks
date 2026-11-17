# MCPJam evaluation for Discobox MCP

Research date: **2026-11-14**. Recommendation: **adopt locally for inspection and add a small, deterministic MCP Apps contract check first**. Keep existing Go/Node tests and native Claude/Codex acceptance checks. Defer hosted evaluation infrastructure and service-forwarding integration until there is a demonstrated need. This report changes no server behavior.

## Evidence and versions

The adapter checkout inspected was `ca33b14df99d3648d289c60a7f535fdd5413e457`, server/bundle version **0.8.0**. It pins Go MCP SDK **1.8.0**, Discobox **v0.19.2-0.20261111144542-d6c392a3cce7**, MCP Apps SDK **2.0.4**, Ghostty Web **0.4.0**, noVNC **1.7.0**, and MCPB packer **2.1.2**. Local evidence comes from `cmd/discobox-mcp/main.go`, `internal/bridge/`, `internal/discobox/`, `web/`, `packaging/mcpb/manifest.json`, build/package scripts, and the connection, desktop-extension, managers, terminal-input, files, desktop, approvals, and service-forwarding documents.

Official MCPJam source was cloned from [MCPJam/inspector](https://github.com/MCPJam/inspector) at **[`6092fb33dbad518708eedf077f8012d4d7392494`](https://github.com/MCPJam/inspector/tree/6092fb33dbad518708eedf077f8012d4d7392494)** (commit time 2026-11-14 11:13:15 UTC). Source links below are pinned to that revision. Registry `npm view` checks on the research date agreed with its package versions:

| Component | Inspected version | Executed here |
| --- | --- | --- |
| `@mcpjam/inspector` | 3.21.2 | No browser/desktop Inspector run |
| `@mcpjam/cli` | 6.2.2 | Yes, temporary npm install |
| `@mcpjam/sdk` | 10.7.0 | Yes, installed transitively with CLI; direct SDK probe |
| `@mcpjam/vitest` | 0.3.0 in source | No |
| Runtime | Node 24.22.1; Go 1.27.4, Linux ARM64 | Yes |

The [package manifests](https://github.com/MCPJam/inspector/tree/6092fb33dbad518708eedf077f8012d4d7392494/cli) matter more than older installation examples: CLI 6.2.2 requires Node `^22.23.4 || ^24.22.1 || >=26.12.1`. The checkout's default Node 25.9.0 does not meet that range, so the proof used an isolated Node 24.22.1. Inspector/SDK declare Node >=22. Pin versions and retain a lockfile in any future integration; do not put `@latest` in CI.

Official live documentation was checked alongside the source: [overview](https://docs.mcpjam.com/), [Playground](https://docs.mcpjam.com/inspector/playground), [Apps conformance](https://docs.mcpjam.com/cli/apps-conformance), [local evals](https://docs.mcpjam.com/cli/local-evals), and [pricing](https://www.mcpjam.com/pricing). The source's versioned `docs/` was also read for transport flags, host presets, SDK APIs, CI, telemetry, and architecture. These are moving products: a doc's “upcoming” release note is not proof that a restriction is absent from the installed version.

## Fit with this adapter

The adapter is a compiled Go process, not a Node MCP server. MCPJam connects over MCP and therefore needs no Go-specific plugin.

- **Packaging:** the MCPB is a binary extension, with configurable upstream endpoint/project and a sensitive optional upstream API token. Its manifest launches `--browse --transport stdio`; this avoids manager discovery/creation at startup. The same process reserves a loopback listener for standalone support. Packaging rebuilds the self-contained frontend, cross-compiles, and packs an explicit allowlist. MCPJam can test the extracted executable with equivalent arguments; it does not establish that Claude installs an MCPB correctly. See [desktop-extension.md](desktop-extension.md) and [package-mcpb.mjs](../scripts/package-mcpb.mjs).
- **Workspace (as evaluated at 0.10; see `docs/contract.md` for the current surface):** 37 tools include 13 UI entry points and 11 app-only tools. `ui://discobox/workspace-v12.html` plus 11 historical aliases return the same bundled HTML with MIME `text/html;profile=mcp-app`. The widget contains SDK, renderer, WASM, CSS, Markdown sanitization, and noVNC. Its declared connect/resource domain lists are empty. Embedded tools use the host bridge; standalone HTTP/WebSocket behavior is a separate path. See [tools.go](../internal/bridge/tools.go) and [assets.go](../web/assets.go).
- **Terminal:** `terminal_open/read/action` are app-only. They carry terminal session handles and bounded base64 output in structured data, rather than the desktop's special private metadata envelope. Human focus claims shared ownership and blocks assistant writes; reads use cursors, long-poll up to 12 seconds, and idle views expire after 90 seconds. Uncertain input must never be replayed. Visibility filtering is especially consequential here.
- **Files:** public list/read/open tools and `discobox-file://{project}/{boxId}/{+path}` resources expose explicitly requested contents. App-only `file_read/stat` power preview/version checks. Files stay scoped to the configured project/box; reads have range/version/size limits. Explicit Send to chat uses `ui/message`; background `ui/update-model-context` carries identity and action receipts. Do not confuse visible sharing with background preview traffic.
- **Desktop:** app-only `desktop_open/read/action` carry session handles and VNC bytes exclusively in `_meta.desktop`, with a constant text acknowledgement and no graphics in structured content. The adapter discovers the declared desktop service and relays framed TCP/WebSocket data. A successful tool call is not evidence of painted pixels or model vision. The sandbox backend role denies raw TCP even for led children; MCPJam does not change that permission.
- **Approvals:** app-only `approval_list/review/decide` return private details, credential metadata, and review tokens in `_meta.approval`. Public tools/context return limited status receipts. The explicit confirmation, five-minute single-use token, snapshot recheck, separate use/delegation decisions, and no uncertain-write retry need fixture tests. App-only status is not proof of human identity and does not grant backend authority. See [approvals.md](approvals.md).

The adapter's client-facing HTTP bearer, its optional upstream API token, and an enrolled Iroh identity serve different purposes. Never substitute one for another in a test configuration. `--local` can cause real upstream access and is unnecessary for all checks proposed here. Without `--browse`, ordinary startup can discover or create a manager.

## Capability assessment

### Protocol inspection and transports

MCPJam supplies manual tools/resources/templates/prompts operations, initialization/capability inspection, JSON-RPC logs, and chat/trace/raw model views. This is useful for diagnosing resource alias lookup, metadata lost by a host, invalid arguments, and host capability negotiation. It complements the adapter's deliberately body-free `--request-log`; full Inspector traces can contain terminal bytes, filenames, input, and private review metadata. A trace is developer-visible, even when data is absent from model context. Use synthetic fixtures before retaining or sharing traces. [Playground source documentation](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/docs/inspector/playground.mdx).

Local Inspector/desktop and CLI/SDK support stdio and HTTP. Hosted Inspector reaches HTTPS endpoints and cannot spawn this executable. For stdio, configure command, argument array, environment overrides, and working directory; logs must remain on stderr. For HTTP, use the adapter's actual `http://127.0.0.1:PORT/mcp` plus its bearer in `--access-token` or a server config header. The adapter enforces Host/Origin as well as auth; preserve the exact listener host, and add a specific permitted origin only if testing demonstrates it is necessary. A Docker container's localhost and a host machine's localhost differ; start with a native local npm install. [Connection/inspection flags](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/docs/cli/server-inspection.mdx).

OAuth discovery, login, refresh, and versioned authorization conformance are also supported, but the current loopback adapter uses a static process bearer, not an OAuth authorization server. Its bare 401 is not an OAuth defect to fix for this proposal. CLI generic auth failure advice suggests OAuth even for that static-bearer case. Public remote HTTPS/auth hosting remains separate adapter work; neither MCPJam's tunnel nor hosted app is needed now.

### MCP Apps rendering and bridge testing

MCPJam recognizes `_meta.ui.resourceUri` and the OpenAI output-template convention, reads the referenced resource, and renders it with a host bridge. The current shared bridge uses `AppBridge` and gates server tools/resources, message delivery, model-context updates, display modes, downloads, and other features on advertised host capabilities. It also blocks app calls to model-only tools. This is a strong match for our SDK-native widget. Inspect the current [shared bridge](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/sdk/src/widget-runtime/host-app-bridge.ts) and [renderer](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/widget-react/src/mcp-apps-renderer.tsx), rather than assuming every older architecture page still describes it precisely.

Use host presets and explicit strict CSP to test inline/fullscreen/fixed height, light/dark mode, narrow viewports, safe areas, missing capabilities, teardown, and handshake/result ordering. Test our `select_view` app-provided tool as well as server fallback `show_box_view`; a server result alone cannot prove an existing widget switched. Capture initialization and bridge traffic when a feature fails. Disable unnecessary OpenAI compatibility injection when checking a plain MCP Apps contract; source and docs distinguish native and compatibility runtimes.

`tools call --ui --require-render` can inject an already completed tool result into an active local Inspector browser, so a successful render must be asserted separately from tool success. It does not execute the entry tool twice, but the rendered widget may immediately call more tools to load data. A no-backend adapter can validate resource wiring, not populate the workspace. A Go fixture API is needed for useful rendering. HTML snapshots in eval traces allow replay; they are not proof that Ghostty/WASM or a live noVNC stream executed correctly. [UI command behavior](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/docs/cli/apps-conformance.mdx), [SDK widget snapshots](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/docs/sdk/reference/host-runner.mdx).

### App-only tools and model context: useful coverage, one important caveat

The SDK defaults to excluding tools whose visibility is exactly `["app"]` from its model tool set. Raw inspection still lists them, and manual calls remain possible. For public MCP App entry tools, its model projection removes `_meta` and structured content while retaining text/resource content; the UI receives the original result. This makes the adapter's meaningful text acknowledgements and resource links important. It is not the same as testing every native client's model projection. [Tool conversion and projection implementation](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/sdk/src/mcp-client-manager/tool-converters.ts).

**Tested caveat:** an app-only tool such as `desktop_read` has visibility metadata but no `resourceUri`. If its result is forcibly run through `projectMcpToolResultForModel`, the helper does not recognize it as a public MCP App entry tool and can retain private `_meta`. The normal filter prevents this model path; `includeAppOnly: true` deliberately removes that protection. Our synthetic sentinel check reproduced the distinction. This is not evidence of a default-path leak. Keep that override disabled; if intentionally simulating a broken host, use synthetic values and assert the resulting exposure explicitly. The same warning applies to approval tokens and terminal structured output. Do not use MCPJam as a proof of privacy against a host that disregards app visibility.

In future tests, inspect both app result delivery and the model-facing tool inventory/messages. Assert that `_meta.desktop`, `_meta.approval`, review handles, clipboard text, and raw input are absent from model requests/context, while intentional bounded Send to chat content is present once. Visibility and model separation are distinct from upstream authorization, and a host/logger can observe both channels.

### Schemas, regression automation, and CI

Apps conformance verifies seven categories: UI presence, UI tool metadata, non-null object input schemas, listed UI resource URI/MIME, readable resource references, one HTML payload per content item, and resource metadata shapes. Both stdio and HTTP work. It **does not** render the app or prove bridge ordering, permissions enforcement, semantics, or full JSON Schema correctness. Discovery reported 13 resource-bearing UI tools here; do not infer exhaustive validation of all 11 app-only descriptors from a green result. [Conformance documentation](https://docs.mcpjam.com/cli/apps-conformance), [runner source](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/sdk/src/apps-conformance/runner.ts).

The SDK also validates declared tool output schemas on call paths, with dialect-aware validation and explicit structured-content checks. This only helps where an output schema exists and constrains the result. Much of this Go server returns generic maps/`any`; a permissive object schema cannot catch wrong cursors, privacy leakage, or a mismatched box. Semantic assertions remain necessary. Some `jsonschema` descriptions list valid actions without encoding a JSON Schema enum; invalid-action tests must exercise the handler too. [Client implementation](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/sdk/src/mcp-client-manager/MCPClientManager.ts).

Use CLI JSON/JUnit reports and SDK direct calls in Node's built-in test runner; adopting Vitest is optional for a Go repository. Snapshot normalized tool names/schemas/visibility and resource/template identities, excluding timestamps, process ports, handles, and bundled HTML churn. Make expected schema changes reviewable. Test invalid inputs, required fields, errors, canonical file URIs, bounds, stale versions, and output semantics with the Go fixtures. Protocol conformance is currently **HTTP-only**, whereas Apps conformance and direct SDK checks cover stdio. Select appropriate HTTP checks against an isolated adapter rather than claiming the whole conformance catalog applies to MCPB stdio. [Protocol conformance scope](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/docs/sdk/reference/protocol-conformance.mdx).

### Evaluations and client fidelity

MCPJam can drive multi-turn cases through models, assert tool selection/order/arguments/counts and forbidden calls, apply output/schema checks and optional judges, compare models/hosts, and produce CI artifacts. Useful cases for this adapter are “show” choosing a UI tool, “read” choosing text tools, carrying the exact box ID, preserving wait cursors, not calling app-only tools, respecting human ownership, not treating timeout as success, and not retrying ambiguous input. Use stubbed writes and fixture counters, not real boxes. Keep nondeterministic model scores advisory initially; use hard assertions for forbidden actions. [Local eval design](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/docs/cli/local-evals.mdx), [assertion API](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/sdk/src/assertions/index.ts).

Local evals are not synonymous with offline operation. CLI 6/SDK 10 code resolves an MCPJam identity and performs an online start request, even with BYOK and uploads disabled; telemetry opt-out does not disable it. Local CLI results remain local, while hosted inference still sends prompts to MCPJam/provider services. The docs' “upcoming” note is stale relative to the code inspected; no authenticated eval or billing transaction was attempted here. Direct inspection/conformance succeeded without MCPJam login. [Live eval docs](https://docs.mcpjam.com/cli/local-evals), [run-start implementation](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/sdk/src/eval-run-start.ts).

Host profiles capture identities, protocol versions, capability advertisements, widget context, sandbox settings, and model/tool policy. Their provenance can be vendor docs, real probes, or best effort; cloud catalog updates can differ from the SDK fallback snapshot. A compatibility “Works” result is computed from declarations and HTML capability scans, not an actual native install, and intentionally omits transport/auth reachability blockers. [Host compatibility](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/docs/inspector/host-compat.mdx), [preset provenance](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/docs/inspector/host-templates.mdx).

The inspected source distinguishes Claude web, Claude Desktop, Claude Code variants, Codex desktop, Codex CLI, and ChatGPT Work. Notably the Codex CLI preset has no widget rendering and models a captured 0.168.0 handshake at protocol 2025-06-18. Some profiles can select real Claude Code/Codex harnesses on an attached personal computer; the Codex path is feature-gated. That is different from the CLI's emulated `--host` mode and still does not run Claude Desktop's extension installer or native canvas. We did not use a real harness or an attached computer. [Pinned preset source](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/sdk/src/host-config/templates/seed-host-template.ts).

Using a Claude or OpenAI model with a preset does not reproduce proprietary prompts, approval policy, tool discovery/context budgets, scheduling, desktop webview, or OS integration completely. Treat presets as focused interoperability tests and compare captured native traces before attributing a failure to the adapter. These statements describe MCPJam's implementation, not a claim about every current native client version.

## Service-forwarding implications

[service-forwarding.md](service-forwarding.md) is a design, not implemented tools. Its upstream research references `c6df6a65`, while this adapter remains pinned to `d6c392a3`. MCPJam can eventually test service discovery/lifecycle schemas, forwarding receipts, app leases, and capability/CSP/open-link errors using fixtures. It cannot supply Discobox forwarding or change sandbox TCP denial.

The proposed reliable first UX is a host-running MCPB adapter with a separate loopback listener per forwarded service and Open in browser. That requires native networking tests: a listener inside a sandbox/container/Inspector machine is not necessarily reachable from the user's browser. MCPJam's own sharing tunnel is a different product feature; it is not an implementation of this design.

A future nested service preview also depends on actual host `frameDomains`, localhost/mixed-content rules, service `frame-ancestors`/X-Frame-Options, cookies, redirects, and WebSocket/HMR/SSE behavior. Test those separately in Claude and Codex, preserving service framing protections and the external-browser fallback. Never give untrusted service HTML the workspace's bridge or approval handles. Framed TCP half-closes, restart/revocation cleanup, port conflicts, and machine-vs-container reachability belong primarily in Go integration/native tests, not model evals. No forwarding was attempted here.

## Proof of concept: executed, with no live backend

All MCP processes used `--browse --discobox http://127.0.0.1:1`, never `--local`, a real socket, or an enrolled peer. Port 1 was an unreachable upstream. The HTTP MCP adapter used its own temporary loopback port and a generated fixture access-token file. The built binary and npm tools were under `/tmp/discobox-mcpjam-poc`; generated frontend/reference-token files stayed ignored. No box input, box launch, credential decision, real desktop connection, or production configuration change occurred.

| Executed check | Result | What it establishes |
| --- | --- | --- |
| CLI `server doctor`, stdio | Exit 0, ready, `discobox-manager` 0.8.0; negotiated 2026-07-28 | Initialize and surface discovery without backend startup |
| CLI `apps conformance`, stdio | Exit 0, 7/7 passed | 37 tools, 13 UI tools, 12 readable HTML resource aliases |
| CLI doctor and Apps conformance, bearer HTTP | Both exit 0; 7/7 Apps checks; negotiated 2025-11-25 | Authenticated Streamable HTTP works; default negotiation differed from stdio |
| HTTP server info without bearer | Exit 1, `AUTH_REQUIRED`, HTTP 401 in diagnostic | Missing bearer rejected |
| HTTP `read_box` with numeric `boxId: 42` | Exit 1, `isError: true`, expected string | Input validation fails before box access |
| Direct SDK tool inventory | 37 raw; 11 app-only excluded; 26 model tools | Default model visibility works for this actual server |
| Synthetic private-result projection | Public UI metadata stripped sentinel; forced app-only projection retained it | Privacy caveat above; no real desktop/review data involved |
| Existing Go bridge/discobox tests; Node tests | Both Go packages passed (cached); 40/40 Node tests passed | Existing fixture/unit suite still passes; not new browser or native evidence |

No Inspector browser rendering, MCPB installation/extraction run, full protocol suite, LLM eval, live OAuth flow, or native client test was performed for this report. Prior browser/native validation claims in other docs are their historical evidence, not new MCPJam results. Detailed temporary JSON artifacts are local scratch files, not committed logs; the commands below reproduce the core result.

## Reproducible setup

Run from the repository root. Requirements: Go >=1.27.1 and npm. The following builds existing code, installs pinned tools outside the repository, and creates a wrapper that explicitly overrides inherited upstream connection settings. The reference clone is needed for brand tokens; this proof downloaded only the pinned `assets/brand/tokens.css` into the ignored reference path to avoid cloning the full repository.

```bash
# On a fresh checkout, prepare the build's reference dependency:
git clone https://github.com/discobox-ai/discobox.git discobox
git -C discobox checkout d6c392a3cce73ee87e716ff6e9c9082fb3ef2c61
# If discobox already exists, verify its pin instead of cloning over it.
npm ci
npm run build

JAM_POC=$(mktemp -d /tmp/discobox-mcpjam.XXXXXX)
export JAM_POC
npm install --prefix "$JAM_POC" --save-exact --no-audit --no-fund \
  node@24.22.1 @mcpjam/cli@6.2.2 @mcpjam/sdk@10.7.0
export PATH="$JAM_POC/node_modules/.bin:$PATH"
export DO_NOT_TRACK=1
node --version
mcpjam --version
go build -o "$JAM_POC/discobox-mcp" ./cmd/discobox-mcp

python3 - <<'PY'
import os, pathlib, shlex
p = pathlib.Path(os.environ['JAM_POC'])
wrapper = p / 'adapter-stdio'
wrapper.write_text('#!/bin/sh\n'
    'unset DISCOBOX_MCP_TOKEN DISCOBOX_MCP_ENDPOINT\n'
    'exec ' + shlex.quote(str(p / 'discobox-mcp')) +
    ' --browse --discobox http://127.0.0.1:1 --transport stdio\n')
wrapper.chmod(0o755)
PY

mcpjam server doctor --command "$JAM_POC/adapter-stdio" \
  --format json --out "$JAM_POC/doctor.json"
mcpjam apps conformance --command "$JAM_POC/adapter-stdio" \
  --format json > "$JAM_POC/apps.json"
mcpjam apps conformance --command "$JAM_POC/adapter-stdio" \
  --reporter junit-xml > "$JAM_POC/apps.xml"
go test ./internal/bridge ./internal/discobox
npm test
```

Doctor, Apps JSON, and JUnit emission were executed here with this configuration; JUnit output parsed as XML with no failures. A future CI change should commit a dedicated tooling lockfile and run `npm ci` there; a top-level version pin alone does not freeze transitive packages. Keep npm dependency updates out of the server runtime bundle.

The SDK visibility and synthetic projection check can be reproduced without a model or backend. The final assertion documents a deliberately forced path, not a default-model leak:

```bash
cat > "$JAM_POC/privacy.mjs" <<'JS'
import assert from 'node:assert/strict';
import {MCPClientManager, projectMcpToolResultForModel} from '@mcpjam/sdk';
const manager = new MCPClientManager({discobox: {
  command: `${process.env.JAM_POC}/adapter-stdio`
}});
try {
  await manager.connectToServer('discobox');
  const {tools} = await manager.listTools('discobox');
  const appOnly = tools.filter(t => t._meta?.ui?.visibility?.join(',') === 'app');
  const model = await manager.getToolsForAiSdk('discobox');
  assert.equal(tools.length, 37);
  assert.equal(appOnly.length, 11);
  assert.equal(Object.keys(model).length, 26);
  for (const t of appOnly) assert(!model[t.name], t.name);
  const fixture = {
    content: [{type: 'text', text: 'Fixture acknowledgement'}],
    structuredContent: {public: 'fixture'},
    _meta: {desktop: {data: 'PRIVATE_FIXTURE_SENTINEL'}}
  };
  const project = name => projectMcpToolResultForModel(
    fixture, tools.find(t => t.name === name)._meta);
  assert(!JSON.stringify(await project('open_box_desktop'))
    .includes('PRIVATE_FIXTURE_SENTINEL'));
  assert(JSON.stringify(await project('desktop_read'))
    .includes('PRIVATE_FIXTURE_SENTINEL'));
  console.log('Inventory and synthetic projection checks passed');
} finally {
  await manager.disconnectAllServers();
}
JS
node "$JAM_POC/privacy.mjs"
```

For a separate HTTP session, the following uses only a random **fixture** bearer, an unreachable upstream, and a local adapter. Its log contains private local URLs, so keep it in the temporary directory. Choose another free loopback port if 18765 is occupied. This reproduces the tested HTTP configuration; the actual proof allocated a free ephemeral port automatically.

```bash
python3 - <<'PY'
import os, pathlib, secrets
p = pathlib.Path(os.environ['JAM_POC']) / 'access-token'
p.write_text(secrets.token_hex(32))
p.chmod(0o600)
PY
env -u DISCOBOX_MCP_TOKEN -u DISCOBOX_MCP_ENDPOINT \
  "$JAM_POC/discobox-mcp" --browse --discobox http://127.0.0.1:1 \
  --transport http --listen 127.0.0.1:18765 \
  --access-token-file "$JAM_POC/access-token" \
  > "$JAM_POC/adapter.stdout" 2> "$JAM_POC/adapter.stderr" &
JAM_ADAPTER_PID=$!
# Run after the local listener is ready:
mcpjam server doctor --url http://127.0.0.1:18765/mcp \
  --access-token "$(cat "$JAM_POC/access-token")" --format json
mcpjam apps conformance --url http://127.0.0.1:18765/mcp \
  --access-token "$(cat "$JAM_POC/access-token")" --format json
# Expected failures, each exits 1:
mcpjam server info --url http://127.0.0.1:18765/mcp --format json
mcpjam tools call --url http://127.0.0.1:18765/mcp \
  --access-token "$(cat "$JAM_POC/access-token")" \
  --tool-name read_box --tool-args '{"boxId":42}' --format json
kill "$JAM_ADAPTER_PID"
wait "$JAM_ADAPTER_PID" || true
```

For later interactive work, run `npx -y @mcpjam/inspector@3.21.2` with the supported Node on PATH and use its printed private access link. Configure STDIO command as the absolute `adapter-stdio` wrapper path; alternatively configure the loopback HTTP URL and fixture bearer. No model key or real backend is necessary for inspecting schemas/resources. Rendering the catalog successfully requires a fixture API, because the UI's first data calls cannot succeed against port 1. Do not invoke launch/send/decision tools against a real configured server while following this recipe.

## Small adoption plan

1. **First integration: one offline Apps contract job (roughly half a day).** Add a separate locked dev-tool directory and a temporary browse-mode adapter wrapper. Build frontend/Go, run stdio doctor and Apps conformance, publish sanitized JSON/JUnit, and fail on nonzero/incomplete results (Apps exits 3 when required checks could not run). Assert exact public/app-only inventory, current resource URI plus historical aliases, and the file template. Keep this job free of model/API credentials and live backend configuration. It has already demonstrated value by validating all 12 aliases and the packaged app's resource shape. It complements rather than replaces Go tests.
2. **Next: reusable fixture API plus a small browser smoke suite (one to two days).** Extract a test-only executable from the patterns already in `internal/bridge/*_test.go` and `internal/testhttp`, with deterministic catalog, file versions, terminal frames, synthetic VNC framebuffer, and approval records. Reject unexpected endpoints and count mutations. Connect the existing adapter to that fixture and automate the local Inspector browser with Playwright. Assert bridge handshake, actual terminal/framebuffer paint, view navigation without input/focus, Files sanitization and explicit chat sharing, private metadata delivery, absent-capability fallbacks, and cleanup. Keep an opaque `connect-src 'none'` check so permissive emulator settings cannot mask new network dependencies. This fixture work is proposed, not implemented.
3. **Only after that: a few model-selection evals.** Use fixture-only cases and pinned model/profile settings; include positive tool choice, exact box arguments, cursor handling, and prohibited launch/send/approval calls. Disable judges initially, run multiple iterations, and keep the score advisory until stable. First agree on MCPJam identity, online dependency, data retention, and budget. A deterministic schema/bridge gate should not depend on eval service availability.

High-value regressions for the fixture/browser stage:

| Area | Assertions |
| --- | --- |
| Terminal | Replay/read cursors, ownership blocks assistant write, two viewers, focus loss, resize only while visible, uncertain input not retried, 64 KiB paste bounds, no added Enter |
| Files | Wrong project/box/URI refusal, pagination/ranges/version change, no terminal attach for file-only entry, hostile Markdown sanitized, stale responses isolated, bounded Send to chat exactly once |
| Desktop | Pixels and handles only in `_meta.desktop`, byte order/backpressure across >4 MiB, 128 KiB reads, 64 KiB writes, sequence deduplication, ambiguous input closes, teardown/90-second expiry, mocked TCP denial |
| Approvals | `_meta.approval` delivered only to app, no private choices/reason/token in model context, explicit use/delegation confirmation, field edits invalidate review, stale/single-use token rejection, double-click/uncertain-write no retry |
| Host bridge | `ui/message` rejection and copy fallback, slow context coalescing, unsupported downloads, fixed/max canvas heights, app-provided `select_view` and blocked-selection receipt, private payload sentinel absent from model request |

Existing tests already cover many server/JS invariants. Reuse their fixtures and assertions; add the external MCPJam/client path where it catches integration mistakes rather than duplicating every implementation detail.

## Licensing, costs, and operational limits

The Inspector package declares Apache-2.0; SDK 10.7.0 and Vitest 0.3.0 declare MIT. The repository's [root license](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/LICENSE) has an enterprise carve-out for server services/eval routing, and its [EE license](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/mcpjam-inspector/server/evals/LICENSE) distinguishes development/testing use from production subscription requirements. The carve-out path wording is not perfectly aligned with the monorepo layout. Adopt the published local inspection tools; do not infer blanket Apache rights to deploy the hosted eval backend. Review exact files/terms if self-hosting those services becomes a requirement.

On the research date the official pricing page listed Free at $0 with 200 daily credits, and annual-billing rates of $24/month Pro (5,000 monthly credits) and $199/month Team (50,000). It described eval prompts at 3 credits plus hosted inference and a one-time free eval allowance. BYOK still incurs provider charges, and billing prose/older docs should not be interpreted as a permanent promise of unlimited free local evals. The first proposed direct CLI gate needed no subscription or account in this proof. Recheck pricing before adopting hosted runs. [Pricing and credit terms](https://www.mcpjam.com/pricing).

CLI telemetry is on by default; `DO_NOT_TRACK=1` disables it and was used here. Eval run-start is separate. Inspector stores useful but potentially sensitive traces and configs; hosted collaboration or replay uploads change the data boundary. Pin/lock tools, retain synthetic reports only, and avoid importing real credentials or personal-computer harness settings into this initial workflow. [Telemetry behavior](https://github.com/MCPJam/inspector/blob/6092fb33dbad518708eedf077f8012d4d7392494/docs/cli/telemetry.mdx).

## Native release checks still required

Use an explicitly authorized test deployment when those checks are scheduled; they were not performed here:

- Install the actual macOS ARM64/AMD64 MCPB in Claude Desktop; verify sensitive settings substitution, binary permissions/architecture, stdio lifecycle, Iroh native-library extraction/enrollment/reconnect, and duplicate-extension/tool-cache behavior. Bundle validation and an extracted executable smoke check do not cover installation.
- In both Claude Desktop and the intended Codex desktop surface/version, verify actual Ghostty/WASM rendering, noVNC paint, host CSP, private `_meta` delivery, and model-visible context with synthetic data. Confirm host tool/resource caching and restoration of old resource URIs.
- Exercise OS Cmd/Ctrl shortcuts, clipboard denial/paste fallback, focus/control handoff, canvas height changes, fullscreen, close/teardown, app-tool discovery, download behavior, and whether accepted `ui/message` starts an assistant response. These are precisely the areas already marked pending in this checkout's docs.
- Test authorized user-vs-sandbox permission failures, connection interruption and no-input-retry semantics; later verify forwarded URLs open on the user's machine, service framing/cookies/WS/SSE work, and revocation/cleanup close the correct listeners.

**Decision:** MCPJam is worth adopting for the first deterministic Apps contract gate and local bridge debugging. A green MCPJam run should mean the selected contract or fixture passed; it should never be recorded as proof of native Claude/Codex parity, safe credential decisions, or working service forwarding.
