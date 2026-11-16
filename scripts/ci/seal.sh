#!/usr/bin/env bash
# seal.sh — the RELEASE's whole run, on this machine: every scope, the
# production image build included, over one commit of trunk. It is the one
# place the whole suite is owed; a push runs only its scopes.
#
#   seal.sh [--post]
#
# Run it from a checkout whose HEAD is the commit to release. It REFUSES unless
# that commit is on origin/trunk and the checkout is clean, because a seal
# names a commit and must have judged exactly that commit's files.
#
# GREEN writes <git common dir>/certify/seals/<commit>.json (commit, tree,
# scopes, tests, seconds, host, time) and refreshes certify/whole-green-at,
# the 24 hour mark that lets a scoped push inherit an identical tree.
#
# --post  also records the seal ON THE COMMIT at GitHub, as the commit status
#         `roadtrip/seal` (gh api, with your own gh login). deploy.yml accepts
#         that status as proof of the commit and does not run the suite again.
#         Without --post nothing leaves this machine.
#
# EXIT 0 sealed. 1 red. 3 refused (nothing was sealed). Bash 3.2 compatible.
set -u
POST=0
[ "${1:-}" = --post ] && POST=1
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT" || exit 3
refuse() { echo "seal: REFUSING: $1" >&2; exit 3; }

SHA="$(git rev-parse HEAD 2>/dev/null)" || refuse "not a git checkout"
[ -z "$(git status --porcelain 2>/dev/null)" ] || refuse "the checkout has uncommitted or untracked files; a seal names a commit and judges only its files"
git fetch -q origin trunk 2>/dev/null || refuse "could not fetch origin/trunk, so whether $SHA is on trunk is unknown"
git merge-base --is-ancestor "$SHA" refs/remotes/origin/trunk 2>/dev/null \
  || refuse "$SHA is not on origin/trunk; a release is sealed from trunk, never from a branch"
TREE="$(git rev-parse "$SHA^{tree}")"
COMMON="$(git rev-parse --path-format=absolute --git-common-dir)"
CERT="$COMMON/certify"
LOG="$CERT/seal-logs/$(date -u +%Y%m%dT%H%M%SZ)-${SHA:0:12}"
mkdir -p "$LOG" "$CERT/seals" || refuse "cannot write under $CERT"
TOOLKIT="$(dirname "$(dirname "$COMMON")")/ABOUT/scripts/ci"

echo "seal: whole suite and image build for $SHA (tree ${TREE:0:12})"
OUT="$LOG/gate.out"
if [ -f "$TOOLKIT/host_lock.sh" ] && [ -z "${CERTIFY_HOST_LOCK_HELD_BY:-}" ]; then
  bash "$TOOLKIT/host_lock.sh" -- bash scripts/ci/gate.sh --docker --scopes whole,image --log-dir "$LOG" 2>&1 | tee "$OUT"; rc="${PIPESTATUS[0]}"
else
  bash scripts/ci/gate.sh --docker --scopes whole,image --log-dir "$LOG" 2>&1 | tee "$OUT"; rc="${PIPESTATUS[0]}"
fi
last="$(grep -E '^gate: VERDICT ' "$OUT" | tail -n 1)"
case "$rc" in
  0) ;;
  1) echo "seal: RED: $last" >&2; exit 1 ;;
  *) refuse "the whole run could not be measured (gate exit $rc): ${last:-no verdict line}" ;;
esac
case "$last" in 'gate: VERDICT GREEN scopes=static,js,unit,features,rigs,image '*) ;;
  *) refuse "the gate exited 0 without a GREEN verdict over all six scopes: ${last:-no verdict line}" ;; esac
tests="$(printf '%s' "$last" | sed -n 's/.* tests=\([0-9][0-9]*\) .*/\1/p')"
wall="$(printf '%s' "$last" | sed -n 's/.* wall=\([0-9][0-9]*\)s .*/\1/p')"
now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
printf '{"commit":"%s","tree":"%s","scopes":"static,js,unit,features,rigs,image","tests":%s,"wall_s":%s,"sealed_at":"%s","host":"%s"}\n' \
  "$SHA" "$TREE" "${tests:-0}" "${wall:-0}" "$now" "$(hostname -s 2>/dev/null || echo unknown)" > "$CERT/seals/$SHA.json"
date +%s > "$CERT/whole-green-at"
echo "seal: GREEN $SHA: $tests tests in ${wall}s; record $CERT/seals/$SHA.json"

if [ "$POST" -eq 1 ]; then
  repo="$(git remote get-url origin | sed -E 's#^.*[:/]([^/:]+/[^/]+)$#\1#; s#\.git$##')"
  gh api -X POST "repos/$repo/statuses/$SHA" -f state=success -f context=roadtrip/seal \
    -f description="whole suite green on a local host: $tests tests, ${wall}s, $now" >/dev/null \
    || { echo "seal: the seal is recorded here, and posting it to $repo FAILED; deploy will fall back to the CI record" >&2; exit 0; }
  echo "seal: posted as commit status roadtrip/seal on $repo@${SHA:0:12}; Deploy will trust it"
else
  echo "seal: not posted (run with --post to let Deploy trust it instead of waiting for CI)"
fi
