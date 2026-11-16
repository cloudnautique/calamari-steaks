# GitHub Actions

`CI` runs on pull requests, pushes to `main`, and manual dispatch. It builds the
embedded frontend, checks JavaScript, runs Node tests, and tests/vets Go on native
Ubuntu 24.04 x64, Windows 2025 x64, and macOS 26 ARM64 runners. Unix runners use
the race detector; Windows currently runs ordinary Go tests rather than assuming
a particular C compiler is installed for race builds.

After all test jobs pass, pushes and manual runs build unsigned MCPB candidates
for macOS, Windows, and Linux. macOS uses an Apple Silicon `macos-26` runner
and produces one `darwin-universal` bundle: Go builds ARM64 and AMD64 slices,
`lipo` merges them, and `codesign` applies and verifies an ad-hoc signature.
CI verifies both slices and smoke-tests the packaged executable natively on ARM64.
Intel execution is not tested. Windows and Linux retain separate AMD64 and ARM64
bundles, with one architecture per platform cross-compiled. The workflow uploads
only `.mcpb` files and SHA-256 sidecars, retained for 14 days. It does not apply
Developer ID signing, notarize, publish releases, or push changes. Download bundles
from the successful run's Artifacts section. Native installation and
real Iroh connections still require separate acceptance testing.

The build-time brand dependency is checked out sparsely at Discobox commit
`d6c392a3cce73ee87e716ff6e9c9082fb3ef2c61`. Keep that reference aligned with the
pseudo-version in `go.mod` when upgrading upstream. No live Discobox server,
credentials, enrolled identity, browser profile, or sandbox runtime is required.
All third-party Actions are pinned to verified full commit hashes, repository
permissions are read-only, and checkouts do not retain Git credentials.

`MCPJam conformance` is manual-only initially: open Actions, select that workflow,
and choose Run workflow. It builds a temporary browse-mode adapter directed at
an unreachable API origin and runs CLI doctor plus MCP Apps conformance, producing
JSON/JUnit reports. It neither renders widgets nor invokes an LLM or account-bound
evaluation service. It uses Node 24.22.1, CLI 6.2.2, SDK 10.7.0, and disables CLI
telemetry. Runtime installs stay under the runner's temporary directory.

MCPJam top-level versions are pinned, but that temporary install resolves its
transitive dependencies each run. Before making it a required PR gate, add a
reviewed tooling lockfile and change the job to `npm ci` against that lockfile.
This workflow currently provides an explicit development check, not a reproducible
native-host or privacy certification. Only synthetic reports are uploaded; raw
logs, temporary private adapter links, keys, and configuration are excluded.

The workflow syntax and steps can be checked locally, but GitHub-hosted execution
is only verified after these files are committed, pushed, and a run succeeds.
Enabling a workflow does not configure branch protection: add required checks in
repository settings separately if desired.

Official references used for the workflow contracts:

- [GitHub-hosted runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)
- [checkout v7.0.1](https://github.com/actions/checkout/tree/3d3c42e5aac5ba805825da76410c181273ba90b1)
- [setup-go v7.0.0](https://github.com/actions/setup-go/tree/b7ad1dad31e06c5925ef5d2fc7ad053ef454303e)
- [setup-node v7.0.0](https://github.com/actions/setup-node/tree/820762786026740c76f36085b0efc47a31fe5020)
- [upload-artifact v6.0.0](https://github.com/actions/upload-artifact/tree/b7c566a772e6b6bfb58ed0dc250532a479d7789f)
