#!/usr/bin/env bash
# Runs the checks of .github/workflows/ci.yml's test job locally. Run it
# before pushing. SLOW=1 adds a single-CPU race run, closer to the macOS
# runner's timing.
set -euo pipefail
cd "$(dirname "$0")/.."
npm ci
npm run build
npm run check
npm test
go test -race -count=1 ./...
go vet ./...
if [[ "${SLOW:-}" == 1 ]]; then
  GOMAXPROCS=1 go test -race -count=1 ./...
fi
echo "CI checks passed"
