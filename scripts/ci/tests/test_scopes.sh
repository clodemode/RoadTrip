#!/usr/bin/env bash
# test_scopes.sh — the rig for the push gate's own judgement: the path-to-scope
# map (scripts/ci/scopes.sh), and gate.sh's rule that a scope which measured
# nothing is REFUSED and never GREEN.
#
# Each refusal case has a CONTROL beside it: the same input made measurable
# must pass, so a refusal is shown to come from the unmeasurable thing and not
# from a check that refuses everything.
#
# Prints one line per check and ends with `PASS=<n> FAIL=<n>`; exits 1 on any
# FAIL. Needs bash and, for the js cases, node. Bash 3.2 compatible.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
SCOPES_SH="$ROOT/scripts/ci/scopes.sh"
GATE_SH="$ROOT/scripts/ci/gate.sh"
MAP="$ROOT/.certify/scopes.map"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/roadtrip-rig.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT
PASS=0 FAIL=0
ok()  { PASS=$((PASS + 1)); echo "ok - $1"; }
bad() { FAIL=$((FAIL + 1)); echo "FAIL - $1"; }
# scopes <map> <path>... -> sets OUT, ERR, RC
scopes() { local m="$1"; shift; OUT="$(printf '%s\n' "$@" | bash "$SCOPES_SH" --map "$m" 2> "$TMP/err")"; RC=$?; ERR="$(cat "$TMP/err")"; }
expect() {  # expect <name> <want-rc> <want-out>
  if [ "$RC" -eq "$2" ] && [ "$OUT" = "$3" ]; then ok "$1"; else bad "$1 (rc=$RC out='$OUT' err='$ERR'; want rc=$2 out='$3')"; fi
}

# --- the map selects what the rule says ----------------------------------------
scopes "$MAP" README.md;                              expect "prose alone runs nothing" 0 docs
scopes "$MAP" README.md docs/hosting.md;              expect "two prose paths run nothing" 0 docs
scopes "$MAP" store.go;                               expect "a product Go file runs unit and features, with the guard set" 0 static,unit,features
scopes "$MAP" README.md store.go;                     expect "prose beside code does not hide the code" 0 static,unit,features
scopes "$MAP" features/x.feature x_steps_test.go;     expect "a scenario and its bindings run features alone" 0 static,features
scopes "$MAP" static/js/hud_test.mjs;                 expect "a browser script's own test runs js alone" 0 static,js
scopes "$MAP" static/js/hud.js;                       expect "a browser script runs js, unit and features" 0 static,js,unit,features
scopes "$MAP" templates/index.html;                   expect "a template runs unit and features" 0 static,unit,features
scopes "$MAP" Dockerfile;                             expect "the image recipe builds the image" 0 static,image
scopes "$MAP" scripts/ci/gate.sh;                     expect "the gate's own scripts run the rigs" 0 static,rigs
scopes "$MAP" .github/workflows/deploy.yml;           expect "a workflow runs the rigs" 0 static,rigs
scopes "$MAP" go.mod;                                 expect "the module file runs the whole suite" 0 static,js,unit,features,rigs
scopes "$MAP" go.sum Dockerfile;                      expect "scopes come out in gate order, once each" 0 static,js,unit,features,rigs,image

# --- FAIL CLOSED: a path no rule names -------------------------------------------
scopes "$MAP" brand-new-kind-of-file.xyz;             expect "an unmapped path refuses (exit 3) and selects nothing" 3 ''
case "$ERR" in *"UNMAPPED: brand-new-kind-of-file.xyz"*) ok "the refusal names the unmapped path" ;; *) bad "the refusal does not name the path: $ERR" ;; esac
scopes "$MAP" store.go brand-new-kind-of-file.xyz;    expect "one unmapped path among mapped ones still refuses" 3 ''
# CONTROL: the same path with a rule for it is placed. The refusal above is the
# missing rule, not a resolver that refuses every path.
{ cat "$MAP"; printf '*.xyz  docs\n'; } > "$TMP/with-rule.map"
scopes "$TMP/with-rule.map" brand-new-kind-of-file.xyz; expect "CONTROL: with a rule added, that same path is placed" 0 docs
# CONTROL the other way: take the last rule out of the real map and a Go file,
# placed a moment ago, is unmapped. The rules are what place a path.
grep -v '^\*\.go[[:space:]]' "$MAP" > "$TMP/no-go.map"
scopes "$TMP/no-go.map" store.go;                     expect "CONTROL: with its rule removed, a Go file refuses" 3 ''

# --- FAIL CLOSED: nothing to judge, and a map that cannot be trusted --------------
OUT="$(printf '' | bash "$SCOPES_SH" --map "$MAP" 2> "$TMP/err")"; RC=$?; ERR="$(cat "$TMP/err")"
expect "no changed path at all refuses: nothing was measured" 3 ''
scopes "$TMP/absent.map" store.go;                    expect "a missing map is an error, not an empty selection" 2 ''
printf 'store.go  unit,nonsense\n' > "$TMP/bad-scope.map"
scopes "$TMP/bad-scope.map" store.go;                 expect "a rule naming an unknown scope is an error" 2 ''
printf 'store.go unit extra-word\n' > "$TMP/bad-line.map"
scopes "$TMP/bad-line.map" store.go;                  expect "a malformed rule is an error" 2 ''
: > "$TMP/empty.map"
scopes "$TMP/empty.map" store.go;                     expect "a map with no rule is an error" 2 ''

# --- every file the repository tracks is placed -----------------------------------
if git -C "$ROOT" rev-parse --git-dir >/dev/null 2>&1; then
  n="$(git -C "$ROOT" ls-files | wc -l | tr -d ' ')"
  # One call: the resolver names every unmapped path on stderr.
  miss="$(git -C "$ROOT" ls-files | bash "$SCOPES_SH" --map "$MAP" 2>&1 >/dev/null | sed -n 's/^scopes.sh: UNMAPPED: \(.*\) matches no rule.*/\1/p')"
  if [ "$n" -gt 0 ] && [ -z "$miss" ]; then ok "all $n tracked files are placed by the map"; else bad "tracked files with no rule: $(printf '%s' "$miss" | tr '\n' ' ') (of $n)"; fi
else
  ok "not a git checkout: the tracked-file sweep has nothing to list (skipped, said)"
fi

# --- gate.sh: a scope that measured nothing is REFUSED ------------------------------
gate() { OUT="$(bash "$GATE_SH" "$@" --log-dir "$TMP/gate" 2>&1)"; RC=$?; }
gate --scopes nonsense
if [ "$RC" -eq 3 ] && printf '%s\n' "$OUT" | grep -q '^gate: VERDICT REFUSED '; then ok "gate.sh refuses a scope that is not a scope (exit 3)"; else bad "gate.sh --scopes nonsense: rc=$RC $OUT"; fi
gate --scopes docs
if [ "$RC" -eq 0 ] && printf '%s\n' "$OUT" | grep -q '^gate: VERDICT GREEN scopes=docs '; then ok "gate.sh: docs runs nothing and is green"; else bad "gate.sh --scopes docs: rc=$RC $OUT"; fi
if command -v node >/dev/null 2>&1; then
  # A js scope whose test files hold no test: node exits 0 having run nothing.
  mkdir -p "$TMP/js"; printf '// a test file with no test in it\n' > "$TMP/js/empty_test.mjs"
  OUT="$(GATE_JS_TESTS="$TMP/js/empty_test.mjs" bash "$GATE_SH" --scopes js --log-dir "$TMP/gate" 2>&1)"; RC=$?
  if [ "$RC" -eq 3 ] && printf '%s\n' "$OUT" | grep -q '^gate: scope js REFUSED '; then ok "gate.sh: a js scope that ran no test is REFUSED, not green"; else bad "empty js scope: rc=$RC $OUT"; fi
  # CONTROL: one real test in the same place, and the same scope is green.
  printf "import test from 'node:test';\ntest('one', () => {});\n" > "$TMP/js/one_test.mjs"
  OUT="$(GATE_JS_TESTS="$TMP/js/one_test.mjs" bash "$GATE_SH" --scopes js --log-dir "$TMP/gate" 2>&1)"; RC=$?
  if [ "$RC" -eq 0 ] && printf '%s\n' "$OUT" | grep -q '^gate: scope js GREEN .*1 node tests passed'; then ok "CONTROL: with one test in it, the js scope is green and counts 1"; else bad "one-test js scope: rc=$RC $OUT"; fi
  # And a failing test is RED, exit 1, with its FAILED line.
  printf "import test from 'node:test';\nimport assert from 'node:assert';\ntest('breaks', () => { assert.equal(1, 2); });\n" > "$TMP/js/red_test.mjs"
  OUT="$(GATE_JS_TESTS="$TMP/js/red_test.mjs" bash "$GATE_SH" --scopes js --log-dir "$TMP/gate" 2>&1)"; RC=$?
  if [ "$RC" -eq 1 ] && printf '%s\n' "$OUT" | grep -q '^gate: VERDICT RED ' && printf '%s\n' "$OUT" | grep -q '^FAILED js:'; then ok "gate.sh: a failing js test is RED (exit 1) and named"; else bad "failing js scope: rc=$RC $OUT"; fi
else
  bad "node is not on PATH, so the gate.sh measured-nothing cases did not run"
fi

echo "PASS=$PASS FAIL=$FAIL"
[ "$FAIL" -eq 0 ]
