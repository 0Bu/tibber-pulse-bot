#!/usr/bin/env bash
# Mechanical half of the pr-hygiene-review gate: contributor-authored text must
# not carry personal data or secrets, and prose must be English (the project
# is public; see CLAUDE.md > Security). Catches what has a reliable SHAPE:
#   - an email outside GitHub noreply / example / the Renovate identity
#   - an international or German phone number, a GPS coordinate pair
#   - a private key block or a GitHub / AWS token
#   - a bridge-password-shaped token (9-char `XXXX-XXXX` sticker code) —
#     the leak that already happened once, in a skill file
#   - German prose (>= 3 distinct German function words on one line;
#     commit messages and PR text only, not the diff)
# A real name, a spelled-out address, or other languages need the human
# pr-hygiene-review skill.
#
# Local:  check-pr-hygiene.sh                 (commits + diff vs origin/main)
# CI:     check-pr-hygiene.sh --text F --diff F
#         --text: commit messages + PR title/body, --diff: lines added by
#         each commit (per-commit patches, not the net PR diff)
# Exit: 0 = clean, 1 = findings, 2 = bad input.
set -uo pipefail
cd "$(dirname "$0")/.."

text_file="" diff_file="" base="${HYGIENE_BASE:-origin/main}"
while [ $# -gt 0 ]; do
  case "$1" in
    --text) text_file="$2"; shift 2 ;;
    --diff) diff_file="$2"; shift 2 ;;
    --base) base="$2"; shift 2 ;;
    *) echo "check-pr-hygiene: unknown argument $1" >&2; exit 2 ;;
  esac
done

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
if [ -z "$text_file$diff_file" ]; then
  mb=$(git merge-base HEAD "$base" 2>/dev/null) || { echo "check-pr-hygiene: cannot find merge-base with $base" >&2; exit 2; }
  git log --format='%B' "$mb"..HEAD > "$tmp/text"
  # Per-commit patches, not the net diff: a secret added and removed again
  # inside the range still lives in the pushed commit objects.
  git log --format= -p "$mb"..HEAD | grep -E '^\+([^+]|$)' | cut -c2- > "$tmp/diff" || true
  text_file="$tmp/text" diff_file="$tmp/diff"
  echo "check-pr-hygiene: local mode — commits $mb..HEAD and their diff (PR text is checked in CI)"
fi
: "${text_file:=/dev/null}" "${diff_file:=/dev/null}"

# Git trailers are expected attribution, not prose.
grep -viE '^(Co-Authored-By|Signed-off-by|Reviewed-by|Claude-Session):' "$text_file" > "$tmp/prose" || true
cat "$tmp/prose" "$diff_file" > "$tmp/all"

found=0
report() {  # label, file, ERE, [exclude ERE]
  local hits rc
  hits=$(grep -noE -e "$3" "$2"); rc=$?
  [ "$rc" -eq 2 ] && { echo "check-pr-hygiene: bad pattern for $1" >&2; exit 2; }
  [ -n "${4:-}" ] && hits=$(grep -vE -e "$4" <<<"$hits")
  hits=$(head -5 <<<"$hits")
  [ -n "$hits" ] || return 0
  found=1
  echo "FINDING  $1:"
  sed 's/^/           /' <<<"$hits"
}

report "email address" "$tmp/all" \
  '[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}' \
  '@(users\.noreply\.github\.com|noreply\.github\.com|anthropic\.com|example\.(com|org|net)|renovateapp\.com)$|noreply@'
report "phone number" "$tmp/all" '(\+[1-9][0-9]{1,2}[ /-]?[0-9][0-9 /-]{6,}[0-9]|\b0[1-9][0-9]{2,4}[ /-][0-9]{5,})'
report "GPS coordinates" "$tmp/all" '-?[0-9]{1,2}\.[0-9]{4,}, ?-?[0-9]{1,3}\.[0-9]{4,}'
report "private key" "$tmp/all" '-----BEGIN [A-Z ]*PRIVATE KEY-----'
report "access token" "$tmp/all" '(ghp_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16})'
# Sticker codes are 4-4 uppercase alphanumerics mixing letters and digits;
# the documented placeholder XXXX-XXXX has no digit, so it never matches.
pw=$(grep -noE '\b[A-Z0-9]{4}-[A-Z0-9]{4}\b' "$tmp/all" | awk -F: '$2 ~ /[A-Z]/ && $2 ~ /[0-9]/' | head -5)
if [ -n "$pw" ]; then
  found=1
  echo "FINDING  bridge-password-shaped token (rotate the bridge password if it is real):"
  sed 's/^/           /' <<<"$pw"
fi

german=$(awk '
  BEGIN { n = split("und nicht der die das ist mit für auf wird werden auch eine einen sind oder wenn noch nach bei zum zur dem den des sich wir ich bitte über aber", w, " "); for (i = 1; i <= n; i++) de[w[i]] = 1 }
  {
    delete seen; c = 0
    line = tolower($0); gsub(/[^a-zäöüß]+/, " ", line)
    m = split(line, t, " ")
    for (i = 1; i <= m; i++) if ((t[i] in de) && !(t[i] in seen)) { seen[t[i]] = 1; c++ }
    if (c >= 3) printf "%d:%s\n", NR, $0
  }' "$tmp/prose" | head -5)
if [ -n "$german" ]; then
  found=1
  echo "FINDING  German prose (project text is English):"
  sed 's/^/           /' <<<"$german"
fi

if [ "$found" -ne 0 ]; then
  echo "check-pr-hygiene: FAILED. Fix the text. If it is already pushed, the finding lives in the" >&2
  echo "commit object — rewrite that commit on your own branch, and rotate any leaked secret." >&2
  exit 1
fi
echo "check-pr-hygiene: clean"
