#!/bin/bash
# SessionStart hook — prime the Go toolchain cache so a web session can build,
# vet and test immediately instead of paying for the module download on the
# first command it runs.
#
# There is nothing else to install: the app is a single static Go binary, its
# SQLite driver (modernc.org/sqlite) is pure Go, and the test fixtures seed a
# throwaway database from the geojson the binary already embeds. No service, no
# container, no network at test time.
set -euo pipefail

# Local runs already have a warm cache and a real checkout; this is for the
# web sandbox only.
if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

cd "${CLAUDE_PROJECT_DIR:-.}"

if ! command -v go >/dev/null 2>&1; then
  echo "go toolchain not found on PATH — skipping warmup" >&2
  exit 0
fi

echo "go: $(go version)"

# modernc.org/sqlite is the slow one to fetch; download once and the container
# snapshot carries it into every later session.
go mod download

# Warm the build and test caches. Failures here are not fatal: a session that
# starts on a branch that does not compile still needs to start, and the agent
# will see the real error when it builds.
go build ./... || echo "warmup build failed — continuing" >&2
go vet ./... >/dev/null 2>&1 || true
go test -count=1 -run XXX_NONE ./... >/dev/null 2>&1 || true

echo "roadtrip: Go caches warm — go build/vet/test are ready"
