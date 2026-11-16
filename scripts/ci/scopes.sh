#!/usr/bin/env bash
# scopes.sh — changed paths in, TEST SCOPES out. The path-to-scope rule of the
# push gate, read from .certify/scopes.map (which documents the scopes).
#
#   scopes.sh [--map <file>] < paths          one path per line on stdin
#
# stdout  one line: the scopes to run, comma-separated, in gate order. `docs`
#         alone means nothing runs; any other scope adds `static` (the guard
#         set); `whole` expands to static,js,unit,features,rigs.
# exit 0  every path matched a rule.
# exit 3  FAIL CLOSED: at least one path matched no rule, or the input was
#         empty (a gate handed no paths has measured nothing). Each unmapped
#         path is named on stderr. Nothing is printed on stdout.
# exit 2  the map is missing, unreadable, or names a scope nobody defines.
#
# Bash 3.2 compatible on purpose: the hook that calls it runs on macOS.
set -u

MAP=''
while [ $# -gt 0 ]; do
  case "$1" in
    --map) MAP="${2:-}"; shift 2 2>/dev/null || shift ;;
    *) echo "scopes.sh: unknown argument '$1'" >&2; exit 2 ;;
  esac
done
[ -n "$MAP" ] || MAP="$(cd "$(dirname "$0")/../.." && pwd)/.certify/scopes.map"
[ -r "$MAP" ] || { echo "scopes.sh: cannot read the scope map $MAP" >&2; exit 2; }

KNOWN=' docs static js unit features rigs image whole '
ORDER='static js unit features rigs image'

PATTERNS=() SCOPES=()
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in ''|'#'*) continue ;; esac
  # read, never `set -- $line`: an unquoted pattern would glob against the cwd.
  pat='' scp='' extra=''
  read -r pat scp extra <<EOF
$line
EOF
  if [ -z "$pat" ] || [ -z "$scp" ] || [ -n "$extra" ]; then
    echo "scopes.sh: $MAP: not 'pattern scopes': $line" >&2; exit 2
  fi
  for s in $(printf '%s' "$scp" | tr ',' ' '); do
    case "$KNOWN" in *" $s "*) ;; *) echo "scopes.sh: $MAP: unknown scope '$s' in: $line" >&2; exit 2 ;; esac
  done
  PATTERNS+=("$pat"); SCOPES+=("$scp")
done < "$MAP"
[ "${#PATTERNS[@]}" -gt 0 ] || { echo "scopes.sh: $MAP holds no rule" >&2; exit 2; }

want=' ' seen=0 unmapped=0
while IFS= read -r path || [ -n "$path" ]; do
  [ -n "$path" ] || continue
  seen=$((seen + 1))
  hit=''
  i=0
  while [ "$i" -lt "${#PATTERNS[@]}" ]; do
    # shellcheck disable=SC2254
    case "$path" in ${PATTERNS[$i]}) hit="${SCOPES[$i]}"; break ;; esac
    i=$((i + 1))
  done
  if [ -z "$hit" ]; then
    echo "scopes.sh: UNMAPPED: $path matches no rule in $MAP" >&2
    unmapped=$((unmapped + 1))
    continue
  fi
  for s in $(printf '%s' "$hit" | tr ',' ' '); do
    if [ "$s" = whole ]; then want="$want static js unit features rigs "; else want="$want$s "; fi
  done
done

if [ "$seen" -eq 0 ]; then
  echo "scopes.sh: REFUSING: no changed path was given, so no scope was measured" >&2
  exit 3
fi
if [ "$unmapped" -gt 0 ]; then
  echo "scopes.sh: REFUSING: $unmapped path(s) match no rule; add a line to $MAP in the commit that adds the file" >&2
  exit 3
fi

out=''
for s in $ORDER; do
  case "$want" in *" $s "*) out="${out:+$out,}$s" ;; esac
done
if [ -z "$out" ]; then
  echo docs
else
  case ",$out," in *,static,*) ;; *) out="static,$out" ;; esac
  echo "$out"
fi
