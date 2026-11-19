# Repository-backed manager setup prototype

Implemented in `77d50ad` on local branch `prototype/repo-manager-setup`, based on `566b518`. The prototype uses the regular `discobox-mcp` executable and workspace widget. The walk used only loopback fixtures and a disposable archive of the factory template. No deployment, push, real factory run, GitHub grant, or target repository change took place.

## Walkthrough

The intended settings are visible in the launch receipt before any setup confirmation.

| Purpose | Requested value |
| --- | --- |
| Bootstrap and repo-owned setup | `discobox-ai/factory-demo@main`, `/setup` |
| Persist manager settings | `cloudnautique/terraform-provider-obot-factory` |
| Watch issues and target draft PRs | `obot-platform/terraform-provider-obot`, PR base `main` |
| Push worker branches | `cloudnautique/terraform-provider-obot` |
| GitHub actor and initial work | `cloudnautique` is an illustrative fixture login. Confirm the real authenticated actor and its repository roles during setup. Start with triage only, one worker and human confirmation. |

1. Launch with `launch_box` or expand **Repository and setup** in **Start a new box**. The receipt gives a box ID, creation reference, intended settings, and any checkout pin and delivery state seen upstream. The repository workflow enters a compact setup view without attaching the terminal. Replay with the same request ID and settings returned the same box. A changed branch with that ID was rejected.
2. Call `setup_status` with the receipt ID or click **Refresh setup**. The private source fixture reported `needs_credential` and named the exact box to open on the credentialed host. The running fixture still showed setup and watcher pending. A forbidden approval read was reported as limited and a failing read as unknown.
3. The child is asked to request its own Discobox API access. On an API approval blocker, use the same server and project in Discobox console → **Secrets (F4)** → **approval inbox**. Match the request type, box ID and request ID when visible. On return, refresh the same box. The fixture showed an API denial and then a separate approval. The return read confirmed the box's `discoboxApi` flag as well as the history. A GitHub use approval with delegation denied stayed visibly split, even when the overall request read approved.
4. Continue through `read_box`, `send_box`, and `wait_box` or **Open workspace**. The fixture received the private settings repository on the original manager's session and returned a blocked-state cursor for the next question. No search for recipes or PRs was needed.
5. Refresh the same box to see each separate observation. The fixture went from watcher pending to box-reported watcher verified, then child worker running with CI pending. The stopped state displayed the last agent observation and left live watcher status unverified. A user can recover the setup view from the repository-backed box's catalog card.

Use [the demo script](../scripts/demo-repo-setup.py) from the repository root.

```sh
npm ci
npm run build
go build -o bin/discobox-mcp ./cmd/discobox-mcp
python3 scripts/demo-repo-setup.py bin/discobox-mcp
```

For a browser walk, append `--browser`, open the printed private loopback widget link and enter the settings above. Use `claude-code` as the fixture harness and `0123456789abcdef0123456789abcdef01234567` as the fixture checkout pin. The printed loopback fixture route sets the simulated phase, for example `pending`, `denied`, `allowed`, `watching`, or `worker`. Refresh after each transition. Ctrl-C ends the fixture.

## Evidence and boundaries

- **Discobox.** Reviewed the pinned upstream module at `d6c392a3cce7` and local CLI help against the box's reported server version `v0.19.5-rc.1`. The OpenAPI create request accepts `config.source` with an immutable checkout pin. The pool can deliver public sources, and runtime records delivery separately from agent materialization. ADRs 26-10-23-948 and 26-11-04-253 describe the anonymous pool pull and explicit host credential policy. The adapter resolves an unpinned public GitHub branch anonymously and reports when credentialed source delivery needs the host. The exercise used a fixed fixture pin.
- **Roles and approvals.** The pinned API exposes `/api-access-requests` and `/secret-requests`; API requests identify the requester with `requesterSandboxId`. Upstream ADRs 26-10-27-880, 26-11-02-022, and 26-11-04-389 cover caller authority, separate use/delegation outcomes and delegation ceilings. An aggregate approval may coexist with denied delegation. The new workflow requests no initial API grant or credential binding. It reads within the connected principal's visibility and routes human decisions to Discobox. The existing credential widget can submit a reviewed decision only within that connection's upstream authority. The fixture checked that an injected value in upstream error bodies, request reasons and report fields did not cross the receipt boundary.
- **Factory.** Reviewed the provided checkout at `de54ef7eb769d0f4d4fa5e17f440184b27d0af8a`, especially `setup`, `start`, `watch`, `scripts/config`, `scripts/request`, `scripts/launch`, and the repo-owned roles. In a temporary archive, `scripts/config check` passed with a disposable settings repo and a single triage slot. `scripts/request --print` emitted `github-manager` and `github-push` for use and only `github-triage` for delegation. A delivery role was absent. Settings persistence in an existing private repo follows the template's `self` and setup §11, with its own access and Git checks. In fork mode the template derives the fork owner from the GitHub login, so the desired organization fork and authenticated actor need to be checked together before delivery can be enabled.
- **Watcher.** `discobox-monitor status` in this box showed zero enabled instances. The factory's watch skill enables GitHub and child-box monitors and keeps one event consumer running. Monitor registration, a working consumer, and a running box are distinct. The prototype reads a small box-authored status report via the real Discobox files API when the box is running. It labels provenance and freshness. The repo checkout reviewed here has no native publisher for this report. The launch prompt asks the agent to publish it after verifying the watcher and consumer. The fixture provides the report directly and labels its transitions simulated.

## Remaining work

| Priority | Owner | Evidence and next step |
| --- | --- | --- |
| P0 | Discobox and MCP | The private-source phase needs a credentialed host to deliver the existing checkout. Integrate a supported client delivery API with explicit local credential policy and a return link for the same box. Exercise anonymous pool delivery and the credentialed path with a disposable pool and repository. |
| P0 | Discobox and MCP | Limited API approval visibility still requires a console round trip, and the current handoff is a location by project/box/request ID. Expose a principal-scoped read and canonical approval link, then test approval, denial, and revocation with real disposable principals. |
| P0 | Discobox and factory repo | Setup, persistence and watcher/worker/CI state rely on repository reports. Add a durable, versioned publisher and stable watcher health/event observation with timestamps. Verify the persisted ref, live consumer and child identity on a disposable end-to-end run. Resolve the factory fork/actor convention before enabling delivery. |
| P1 | MCP and host | Setup questions still use the terminal. Provide structured host input where supported, preserve setup view identity across restoration, and retry the widget/catalog regression against Claude and Obot. The widget currently has a refresh control and the text path works without a widget. |
| P1 | MCP and Discobox | The local creation journal prevents duplicate POSTs, and an ambiguous POST can still require manual reconciliation by name. A server-owned idempotency key and create receipt would make it recoverable by ID even after a response is lost. Manager discovery tagging during provisioning is best effort. |

## Test results

- `go test ./...`, `npm run build`, `npm run check`, and `npm test` passed. The Node suite ran 52 tests.
- The stdio fixture walk passed launch, same-intent replay, changed-intent rejection, private source handoff, limited and unknown visibility, API denial and approval, split credential outcomes, same-box read/send/wait, watcher/worker/CI transitions, and stopped-box reporting.
- Headless Chromium passed the standalone widget walk for launch, receipt refresh through pending/denied/verified phases, and reopening setup from the same catalog card. The embedded Claude and Obot hosts were not exercised.
- The 100-box regression measured a 23,650-byte model page and an 8,621-byte app catalog page. The model-visible tool schemas plus instructions used 19,311 bytes. Normal launch and setup status do not return a catalog.
