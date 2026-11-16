#!/usr/bin/env bash
# gate.sh — ONE definition of RoadTrip's checks, by TEST SCOPE.
#
# The pre-push hook (scripts/hooks/pre-push), the release seal
# (scripts/ci/seal.sh) and GitHub (.github/actions/gates) all run this file, so
# the command exists once. The scopes and the rule that picks them for a change
# are documented in .certify/scopes.map.
#
#   gate.sh [--docker] --scopes <a,b,...> [--shards <n>] [--log-dir <dir>]
#
#   --scopes   static, js, unit, features, rigs, image, or whole (= the first
#              five). `docs` runs nothing and is GREEN.
#   --docker   run inside the gate image (scripts/ci/gate.Dockerfile), for a
#              host with no Go toolchain. The image is built once and reused;
#              the module cache and the build cache are named volumes, so a
#              second run compiles only what changed. Without --docker, go and
#              node must be on PATH (inside the image, or a GitHub runner).
#   --shards   how many processes run the scenarios side by side (default: the
#              CPU count, at most 8; or GATE_SHARDS).
#   --log-dir  where each scope's full output goes (default: a temp dir, named
#              on the VERDICT line).
#
# EXIT 0 GREEN. 1 RED: a check failed; each failing test is a `FAILED <id>`
# line. 3 REFUSED: a scope could not be MEASURED (it ran no test, printed no
# count, or is not a scope at all). A scope that measured nothing never reads
# as a scope that passed. 2 usage.
#
# The last line is always
#   gate: VERDICT <GREEN|RED|REFUSED> scopes=<...> tests=<n> wall=<s>s log=<dir>
# and above it one `gate: scope <name> <GREEN|RED|REFUSED> <s>s (<what was counted>)`
# per scope.
#
# Bash 3.2 compatible on purpose (macOS /bin/bash).
set -u

DOCKER=0 SCOPES='' SHARDS="${GATE_SHARDS:-}" LOG_DIR=''
while [ $# -gt 0 ]; do
  case "$1" in
    --docker) DOCKER=1; shift ;;
    --scopes) SCOPES="${2:-}"; shift 2 2>/dev/null || shift ;;
    --shards) SHARDS="${2:-}"; shift 2 2>/dev/null || shift ;;
    --log-dir) LOG_DIR="${2:-}"; shift 2 2>/dev/null || shift ;;
    *) echo "gate.sh: unknown argument '$1'" >&2; exit 2 ;;
  esac
done
[ -n "$SCOPES" ] || { echo "gate.sh: --scopes is required" >&2; exit 2; }

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT" || exit 2
START=$(date +%s)
[ -n "$LOG_DIR" ] || LOG_DIR="$(mktemp -d "${TMPDIR:-/tmp}/roadtrip-gate.XXXXXX")"
mkdir -p "$LOG_DIR" || { echo "gate.sh: cannot make the log dir $LOG_DIR" >&2; exit 2; }

# whole expands; the list is de-duplicated and put in gate order.
want=" $(printf '%s' "$SCOPES" | tr ',' ' ') "
case "$want" in *" whole "*) want="$want static js unit features rigs " ;; esac
LIST=''
for s in $want; do
  case " docs static js unit features rigs image whole " in
    *" $s "*) ;;
    *) echo "gate: scope $s REFUSED 0s (not a scope; see .certify/scopes.map)"
       echo "gate: VERDICT REFUSED scopes=$SCOPES tests=0 wall=0s log=$LOG_DIR"; exit 3 ;;
  esac
done
for s in static js unit features rigs image; do
  case "$want" in *" $s "*) LIST="$LIST $s" ;; esac
done
LIST="${LIST# }"

ESC="$(printf '\033')"
TOTAL=0 WORST=0   # WORST: 0 green, 1 red, 3 refused (refused wins over red)
worse() { if [ "$1" -eq 3 ] || { [ "$1" -eq 1 ] && [ "$WORST" -eq 0 ]; }; then WORST="$1"; fi; }
# report <scope> <rc> <seconds> <count> <what>: one scope line; a GREEN with
# no positive count is turned into REFUSED here, in one place.
report() {
  local scope="$1" rc="$2" secs="$3" count="$4" what="$5" word
  case "$count" in ''|*[!0-9]*) count=0 ;; esac
  if [ "$rc" -eq 0 ] && [ "$count" -eq 0 ]; then
    rc=3; what="$what; it measured nothing, and nothing measured is not a pass"
  fi
  case "$rc" in 0) word=GREEN ;; 3) word=REFUSED ;; *) word=RED; rc=1 ;; esac
  echo "gate: scope $scope $word ${secs}s ($what)"
  [ "$rc" -eq 0 ] && TOTAL=$((TOTAL + count))
  worse "$rc"
}
finish() {
  local word
  case "$WORST" in 0) word=GREEN ;; 3) word=REFUSED ;; *) word=RED ;; esac
  echo "gate: VERDICT $word scopes=${LIST// /,} tests=$TOTAL wall=$(( $(date +%s) - START ))s log=$LOG_DIR"
  exit "$WORST"
}

if [ -z "$LIST" ]; then   # docs only
  echo "gate: scope docs GREEN 0s (prose no test reads; nothing to run)"
  echo "gate: VERDICT GREEN scopes=docs tests=0 wall=0s log=$LOG_DIR"
  exit 0
fi

# ------------------------------------------------------------------ --docker
# Everything but `image` runs inside the gate image; `image` is a docker build
# and stays on the host.
if [ "$DOCKER" -eq 1 ]; then
  command -v docker >/dev/null 2>&1 || { echo "gate: VERDICT REFUSED scopes=${LIST// /,} tests=0 wall=0s log=$LOG_DIR (docker is not on PATH)"; exit 3; }
  inner='' host_image=0
  for s in $LIST; do
    if [ "$s" = image ]; then host_image=1; else inner="${inner:+$inner,}$s"; fi
  done
  if [ -n "$inner" ]; then
    gover="$(sed -n 's/^go \([0-9][0-9.]*\).*/\1/p' go.mod | head -n 1)"
    [ -n "$gover" ] || { echo "gate: VERDICT REFUSED scopes=${LIST// /,} tests=0 wall=0s log=$LOG_DIR (go.mod names no go version)"; exit 3; }
    key="$( { printf '%s\n' "$gover"; cat scripts/ci/gate.Dockerfile; } | shasum -a 256 | cut -c1-12)"
    tag="roadtrip-gate:$key"
    if ! docker image inspect "$tag" >/dev/null 2>&1; then
      t0=$(date +%s)
      if ! docker build -q --build-arg "GO_VERSION=$gover" -t "$tag" -f scripts/ci/gate.Dockerfile scripts/ci > "$LOG_DIR/gate-image.log" 2>&1; then
        tail -n 20 "$LOG_DIR/gate-image.log"
        echo "gate: VERDICT REFUSED scopes=${LIST// /,} tests=0 wall=$(( $(date +%s) - START ))s log=$LOG_DIR (the gate image did not build)"; exit 3
      fi
      echo "gate: built the gate image $tag in $(( $(date +%s) - t0 ))s (once per host, until gate.Dockerfile or the Go version changes)"
    fi
    gitdir="$(cd "$(git rev-parse --git-common-dir)" && pwd)"
    # The checkout is mounted at its OWN path with its git dir beside it: a
    # .wt/ worktree's .git file points at the main checkout's .git/worktrees/.
    docker run --rm -v "$ROOT":"$ROOT" -v "$gitdir":"$gitdir" -v "$LOG_DIR":"$LOG_DIR" \
      -v roadtrip-gomod:/go/pkg/mod -v roadtrip-gocache:/root/.cache/go-build \
      -e GATE_SHARDS="${SHARDS:-}" -e GATE_INNER=1 -w "$ROOT" "$tag" \
      bash scripts/ci/gate.sh --scopes "$inner" --log-dir "$LOG_DIR" | sed '/^gate: VERDICT /d' ; rc="${PIPESTATUS[0]}"
    # The inner run printed its scope lines; fold its totals into this one.
    n="$(sed -n 's/^tests=//p' "$LOG_DIR/inner.total" 2>/dev/null)"; case "$n" in ''|*[!0-9]*) n=0 ;; esac
    TOTAL=$((TOTAL + n))
    case "$rc" in 0) ;; 1) worse 1 ;; *) worse 3 ;; esac
  fi
  if [ "$host_image" -eq 1 ]; then DOCKER=0; LIST_SAVE="$LIST"; LIST=image; else finish; fi
fi

# -------------------------------------------------------------------- scopes
scope_static() {
  local t0 n js f rc=0 log="$LOG_DIR/static.log"; t0=$(date +%s)
  n="$(ls ./*.go 2>/dev/null | wc -l | tr -d ' ')"
  : > "$log"
  local unf; unf="$(gofmt -l . 2>>"$log")" || rc=1
  if [ -n "$unf" ]; then echo "gofmt needed on:" >> "$log"; echo "$unf" >> "$log"; printf '%s\n' "$unf" | sed 's/^/FAILED gofmt:/'; rc=1; fi
  js=0
  for f in static/js/*.js; do
    [ -f "$f" ] || continue
    js=$((js + 1))
    node --check "$f" >> "$log" 2>&1 || { echo "FAILED node--check:$f"; rc=1; }
  done
  go vet ./... >> "$log" 2>&1 || { echo "FAILED go-vet"; rc=1; }
  go build ./... >> "$log" 2>&1 || { echo "FAILED go-build"; rc=1; }
  [ "$rc" -ne 0 ] && tail -n 30 "$log"
  [ "$js" -eq 0 ] && n=0   # no script checked: the scope did not measure its JS half
  report static "$rc" "$(( $(date +%s) - t0 ))" "$n" "gofmt and vet and build over $n Go files, node --check over $js scripts"
}

scope_js() {
  local t0 rc=0 n log="$LOG_DIR/js.log"; t0=$(date +%s)
  # TAP, so each test is one `ok N - <name>` line whatever the node version.
  # shellcheck disable=SC2086
  node --test --test-reporter=tap ${GATE_JS_TESTS:-static/js/*_test.mjs} > "$log" 2>&1 || rc=1
  # node reports a test FILE that holds no test as one passing test named by
  # its path, so a count of passes alone calls an empty file green (the rig
  # scripts/ci/tests/test_scopes.sh caught exactly that). Count the tests that
  # are not a file: any `ok` line whose name is not a *_test.mjs path.
  n="$(grep -E '^ *ok [0-9]+ - ' "$log" | grep -v -E '_test\.mjs( #.*)?$' | wc -l | tr -d ' ')"
  if [ "$rc" -ne 0 ]; then
    sed -n -E 's/^ *not ok [0-9]+ - (.*)$/FAILED js:\1/p' "$log" | awk '!s[$0]++' | head -n 50
    tail -n 30 "$log"
  fi
  report js "$rc" "$(( $(date +%s) - t0 ))" "${n:-0}" "${n:-0} node tests passed"
}

# unit and features share one preparation: each test binary is compiled ONCE,
# then unit runs under the race detector beside the scenario shards.
scope_go() {   # scope_go <unit:0|1> <features:0|1>
  local do_unit="$1" do_feat="$2" t0 pkgs bin="$LOG_DIR/bin" shards i pids='' rc
  t0=$(date +%s)
  pkgs="$(go list ./... 2>/dev/null | wc -l | tr -d ' ')"
  if [ "$pkgs" != 1 ]; then
    [ "$do_unit" -eq 1 ] && report unit 3 0 0 "go list ./... names $pkgs packages and this gate knows one; teach scope_go the new package"
    [ "$do_feat" -eq 1 ] && report features 3 0 0 "go list ./... names $pkgs packages and this gate knows one"
    return
  fi
  mkdir -p "$bin"
  if [ "$do_unit" -eq 1 ]; then ( go test -c -race -o "$bin/unit.test" . > "$LOG_DIR/unit.build.log" 2>&1 ) & pids="$!"; fi
  if [ "$do_feat" -eq 1 ]; then ( go test -c -o "$bin/features.test" . > "$LOG_DIR/features.build.log" 2>&1 ) & pids="$pids $!"; fi
  rc=0
  for i in $pids; do wait "$i" || rc=1; done
  if [ "$rc" -ne 0 ]; then
    cat "$LOG_DIR"/unit.build.log "$LOG_DIR"/features.build.log 2>/dev/null | tail -n 40
    echo "FAILED go-test-compile"
    [ "$do_unit" -eq 1 ] && report unit 1 "$(( $(date +%s) - t0 ))" 0 "the test binary did not compile"
    [ "$do_feat" -eq 1 ] && report features 1 "$(( $(date +%s) - t0 ))" 0 "the test binary did not compile"
    return
  fi
  local built=$(( $(date +%s) - t0 )) upids='' ut0 ft0 ushards listed=0
  ut0=$(date +%s)
  if [ "$do_unit" -eq 1 ]; then
    # The plain tests run alone too (they set the environment), so they are
    # sharded the same way the scenarios are: by process. The binary lists its
    # own tests, the names are dealt round-robin into shards, and each process
    # is told exactly its names. What was listed is compared with what ran.
    ushards="${GATE_UNIT_SHARDS:-4}"
    case "$ushards" in ''|*[!0-9]*|0) ushards=4 ;; esac
    "$bin/unit.test" -test.list '.*' 2> "$LOG_DIR/unit.list.err" | grep -E '^(Test|Fuzz|Example)' | grep -v '^TestFeatures$' | sort > "$LOG_DIR/unit.list"
    listed="$(grep -c . "$LOG_DIR/unit.list")"
    [ "$listed" -lt "$ushards" ] && ushards=1
    i=1
    while [ "$i" -le "$ushards" ] && [ "$listed" -gt 0 ]; do
      ( pat="$(awk -v k="$ushards" -v i="$i" 'NR % k == i % k' "$LOG_DIR/unit.list" | paste -sd'|' -)"
        "$bin/unit.test" -test.count=1 -test.timeout=10m -test.run "^($pat)\$" -test.v > "$LOG_DIR/unit.$i.log" 2>&1
        echo "$?" > "$LOG_DIR/unit.$i.rc" ) &
      upids="$upids $!"
      i=$((i + 1))
    done
  fi
  if [ "$do_feat" -eq 1 ]; then
    shards="${SHARDS:-}"
    case "$shards" in ''|*[!0-9]*) shards="$( (nproc || getconf _NPROCESSORS_ONLN) 2>/dev/null | head -n 1)" ;; esac
    case "$shards" in ''|*[!0-9]*) shards=4 ;; esac
    [ "$shards" -gt 8 ] && [ -z "${SHARDS:-}" ] && shards=8
    [ "$shards" -lt 1 ] && shards=1
    ft0=$(date +%s); pids=''
    i=1
    while [ "$i" -le "$shards" ]; do
      ( FEATURE_SHARD="$i/$shards" "$bin/features.test" -test.count=1 -test.timeout=10m -test.run '^TestFeatures$' -test.v \
          > "$LOG_DIR/features.$i.log" 2>&1; echo "$?" > "$LOG_DIR/features.$i.rc" ) &
      pids="$pids $!"
      i=$((i + 1))
    done
    # shellcheck disable=SC2086
    wait $pids
    local frc=0 scen=0 steps=0 s p one
    i=1
    while [ "$i" -le "$shards" ]; do
      one="$(cat "$LOG_DIR/features.$i.rc" 2>/dev/null)"; [ "$one" = 0 ] || frc=1
      # godog's own summary: "N scenarios (N passed)" / "M steps (M passed)".
      # It colours the count, so the escape codes come off first.
      s="$(sed "s/$ESC\[[0-9;]*m//g" "$LOG_DIR/features.$i.log" | sed -n -E 's/^([0-9]+) scenarios? \(([0-9]+) passed\)$/\2/p' | tail -n 1)"
      p="$(sed "s/$ESC\[[0-9;]*m//g" "$LOG_DIR/features.$i.log" | sed -n -E 's/^([0-9]+) steps? \(([0-9]+) passed\)$/\2/p' | tail -n 1)"
      if [ "$one" = 0 ] && [ -z "$s" ]; then frc=3; echo "gate: shard $i/$shards exited 0 and printed no scenario count: read $LOG_DIR/features.$i.log"; fi
      scen=$((scen + ${s:-0})); steps=$((steps + ${p:-0}))
      i=$((i + 1))
    done
    if [ "$frc" -eq 1 ]; then
      sed -n -E 's/^ *--- FAIL: (TestFeatures\/[^ ]+).*/FAILED \1/p' "$LOG_DIR"/features.*.log | awk '!s[$0]++' | head -n 50
      grep -hE 'panic:|^FAIL|test timed out' "$LOG_DIR"/features.*.log | head -n 10
    fi
    report features "$frc" "$(( $(date +%s) - ft0 ))" "$scen" "$scen scenarios and $steps steps passed in $shards shards; both test binaries compiled in ${built}s"
  fi
  if [ "$do_unit" -eq 1 ]; then
    # shellcheck disable=SC2086
    [ -n "$upids" ] && wait $upids
    local urc=0 n=0 one ran
    i=1
    while [ "$i" -le "${ushards:-0}" ] && [ "$listed" -gt 0 ]; do
      one="$(cat "$LOG_DIR/unit.$i.rc" 2>/dev/null)"; [ "$one" = 0 ] || urc=1
      i=$((i + 1))
    done
    n="$(cat "$LOG_DIR"/unit.[0-9]*.log 2>/dev/null | grep -c '^--- PASS: ')"
    ran="$(cat "$LOG_DIR"/unit.[0-9]*.log 2>/dev/null | grep -c -E '^--- (PASS|FAIL|SKIP): ')"
    if [ "$urc" -ne 0 ]; then
      sed -n -E 's/^--- FAIL: ([^ ]+).*/FAILED \1/p' "$LOG_DIR"/unit.[0-9]*.log | head -n 50
      grep -hE 'WARNING: DATA RACE|panic:|test timed out' "$LOG_DIR"/unit.[0-9]*.log | head -n 5
    elif [ "$ran" -ne "$listed" ]; then
      urc=3
      echo "gate: the unit binary lists $listed tests and $ran ran: a test in no shard is a test nobody ran (read $LOG_DIR/unit.list)"
    fi
    report unit "$urc" "$(( $(date +%s) - ut0 ))" "${n:-0}" "${n:-0} of $listed Go tests passed under the race detector in ${ushards} shards"
  fi
}

scope_rigs() {
  local t0 rc=0 pass=0 fail=0 f line p q log="$LOG_DIR/rigs.log"; t0=$(date +%s)
  : > "$log"
  for f in scripts/ci/tests/test_*.sh; do
    [ -f "$f" ] || continue
    echo "== $f" >> "$log"
    bash "$f" >> "$log" 2>&1 || rc=1
    line="$(grep -E '^PASS=[0-9]+ FAIL=[0-9]+$' "$log" | tail -n 1)"
    p="$(printf '%s' "$line" | sed -n 's/^PASS=\([0-9]*\) .*/\1/p')"; q="$(printf '%s' "$line" | sed -n 's/.* FAIL=\([0-9]*\)$/\1/p')"
    pass=$((pass + ${p:-0})); fail=$((fail + ${q:-0}))
  done
  [ "$fail" -gt 0 ] && rc=1
  if [ "$rc" -ne 0 ]; then grep -E '^(FAIL|not ok)' "$log" | sed 's/^/FAILED rig:/' | head -n 50; tail -n 30 "$log"; fi
  report rigs "$rc" "$(( $(date +%s) - t0 ))" "$pass" "$pass rig checks passed, $fail failed"
}

scope_image() {
  local t0 rc=0 id='' log="$LOG_DIR/image.log"; t0=$(date +%s)
  if ! command -v docker >/dev/null 2>&1; then
    report image 3 0 0 "docker is not on PATH, so the production image was not built"; return
  fi
  docker build -q -t roadtrip-gate-image-check . > "$log" 2>&1 || rc=1
  [ "$rc" -eq 0 ] && id="$(tail -n 1 "$log")"
  [ "$rc" -ne 0 ] && { tail -n 30 "$log"; echo "FAILED docker-build"; }
  case "$id" in sha256:*) report image "$rc" "$(( $(date +%s) - t0 ))" 1 "the production image builds: ${id:0:19}" ;;
    *) report image "$rc" "$(( $(date +%s) - t0 ))" 0 "docker build printed no image id" ;; esac
}

DO_UNIT=0 DO_FEAT=0
for s in $LIST; do case "$s" in unit) DO_UNIT=1 ;; features) DO_FEAT=1 ;; esac; done
for s in $LIST; do
  case "$s" in
    static) scope_static ;;
    js) scope_js ;;
    unit|features) if [ $((DO_UNIT + DO_FEAT)) -gt 0 ]; then scope_go "$DO_UNIT" "$DO_FEAT"; DO_UNIT=0; DO_FEAT=0; fi ;;
    rigs) scope_rigs ;;
    image) scope_image ;;
  esac
done
[ -n "${GATE_INNER:-}" ] && echo "tests=$TOTAL" > "$LOG_DIR/inner.total"
[ -n "${LIST_SAVE:-}" ] && LIST="$LIST_SAVE"
finish
