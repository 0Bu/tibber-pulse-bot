#!/usr/bin/env bash
# PR merge-gate policy: every review the change needs must be recorded in the
# PR body as a ticked, SHA-stamped task line whose stamp matches the PR head:
#
#   - [x] `$project-audit` clean — merge gate @ 1a2b3c4d5e6f
#
# A later push changes the head and re-stales every stamp, forcing a fresh
# review. Pure data check (no PR code is executed), so the trusted-base
# pr-policy workflow can run it on pull_request_target, and the Claude
# pre-merge hook runs the same script.
#
# Usage: check-pr-gates.sh --body-file F --head-sha SHA --files-file F
#                          [--meta-file pr.json --commits-file commits.json]
# pr.json / commits.json are the GitHub REST responses for the PR and its
# commits; they are only needed to recognise a Renovate PR (see below).
# Exit: 0 = all required gates recorded, 1 = missing/stale, 2 = bad input.
set -uo pipefail

body_file="" head_sha="" files_file="" meta_file="" commits_file=""
while [ $# -gt 0 ]; do
  case "$1" in
    --body-file) body_file="$2"; shift 2 ;;
    --head-sha) head_sha="$2"; shift 2 ;;
    --files-file) files_file="$2"; shift 2 ;;
    --meta-file) meta_file="$2"; shift 2 ;;
    --commits-file) commits_file="$2"; shift 2 ;;
    *) echo "check-pr-gates: unknown argument $1" >&2; exit 2 ;;
  esac
done
[ -r "$body_file" ] && [ -r "$files_file" ] || { echo "check-pr-gates: --body-file and --files-file are required" >&2; exit 2; }
grep -Eq '^[0-9a-f]{40}$' <<<"$head_sha" || { echo "check-pr-gates: --head-sha must be a full 40-char SHA" >&2; exit 2; }
[ -s "$files_file" ] || { echo "check-pr-gates: empty changed-file list; refusing to treat that as an irrelevant PR" >&2; exit 2; }

# Renovate owns renovate/* branches and automerges (RENOVATE_AUTOMERGE in
# renovate.yaml). Its PRs need no human records when they come from this repo,
# every commit's author AND committer is the Renovate identity configured
# there, and the diff stays inside the files Renovate manages. Anything else
# — a fork, an extra file, a commit amended or rebased by a person (git keeps
# the author but rewrites the committer) — falls back to the normal gates.
# Deliberately forging both identities needs push access; that residual risk
# is accepted for a single-maintainer repo.
RENOVATE_EMAIL="bot@renovateapp.com"
RENOVATE_FILES='^(Dockerfile|go\.mod|go\.sum|docker-compose\.yml|README\.md|CLAUDE\.md|chart/Chart\.yaml|chart/values\.yaml|\.github/workflows/[A-Za-z0-9._-]+\.ya?ml)$'
if [ -n "$meta_file" ] && [ -n "$commits_file" ]; then
  same_repo=$(jq -r '(.head.repo.full_name // "") == (.base.repo.full_name // "-")' "$meta_file" 2>/dev/null)
  ref=$(jq -r '.head.ref // ""' "$meta_file" 2>/dev/null)
  authors=$(jq -r '.[].commit | .author.email, .committer.email' "$commits_file" 2>/dev/null | sort -u)
  if [ "$same_repo" = true ] && [[ "$ref" == renovate/* ]] && [ "$authors" = "$RENOVATE_EMAIL" ] \
     && ! grep -vqE "$RENOVATE_FILES" "$files_file"; then
    echo "check-pr-gates: Renovate PR ($ref) touching only Renovate-managed files — no review records required."
    exit 0
  fi
fi

# gate name ; changed-file regex (ERE) that makes it required ; what it proves
GATES=$(cat <<'EOF'
code-review;.;/code-review found no blocking issue in the diff
project-audit;.;project-audit (incl. scripts/check-drift.sh) found no doc drift
pr-hygiene-review;.;commits, PR text and diff carry no personal data / secrets and are English
ha-discovery-validate;^(internal/(discovery|output|sml)/|cmd/tibber-pulse-bot/);HA discovery parity, availability and expire_after contracts hold
chart-lint;^chart/;chart-lint matrix (password modes, fail guards, knobs) renders as expected
live-test;^internal/(pulse|sml)/;end-to-end run against the real bridge (CLAUDE.md > Verification protocol)
EOF
)

# Only real, column-zero task-list lines count: lines inside HTML comments or
# fenced code (e.g. the template's own instructions) must neither satisfy nor
# shadow a gate.
tasks=$(awk '
  /^[[:space:]]*(```|~~~)/ { fence = !fence; next }
  fence { next }
  {
    line = $0
    while (1) {
      if (incomment) {
        i = index(line, "-->"); if (!i) { line = ""; break }
        line = substr(line, i + 3); incomment = 0
      } else {
        i = index(line, "<!--"); if (!i) break
        rest = substr(line, i + 4); out = substr(line, 1, i - 1)
        j = index(rest, "-->")
        if (j) { line = out substr(rest, j + 3) } else { line = out; incomment = 1; break }
      }
    }
    if (line ~ /^[-*] \[[ xX]\] /) print line
  }' "$body_file")

short=${head_sha:0:12}
missing=0
while IFS=';' read -r name rx what; do
  grep -qE "$rx" "$files_file"; rc=$?
  [ "$rc" -eq 2 ] && { echo "check-pr-gates: bad regex for \$$name: $rx" >&2; exit 2; }
  [ "$rc" -eq 0 ] || continue
  line=$(grep -F "\`\$$name\`" <<<"$tasks" | head -1)
  if [ -z "$line" ]; then
    echo "MISSING  \$$name — no task line in the PR body ($what)"; missing=1; continue
  fi
  if ! grep -qE '^[-*] \[[xX]\] ' <<<"$line"; then
    echo "UNTICKED \$$name — $what"; missing=1; continue
  fi
  stamp=$(grep -oE '@[[:space:]]*[0-9a-f]{7,40}\b' <<<"$line" | tail -1 | tr -d '@[:space:]')
  if [ -z "$stamp" ]; then
    echo "NOSTAMP  \$$name — ticked but carries no bare '@ <sha>' stamp (use @ $short)"; missing=1; continue
  fi
  if [ "${head_sha#"$stamp"}" = "$head_sha" ]; then
    echo "STALE    \$$name — stamped @ $stamp but PR head is $short; re-run it for the new commits"; missing=1; continue
  fi
  echo "ok       \$$name @ $stamp"
done <<<"$GATES"

if [ "$missing" -ne 0 ]; then
  cat >&2 <<EOF
check-pr-gates: FAILED. Run each missing review on head $short, then tick its
line in the PR body's "Merge gates" section with a bare stamp, e.g.
  - [x] \`\$project-audit\` clean — merge gate @ $short
(see .github/pull_request_template.md and CLAUDE.md > Merge gates).
EOF
  exit 1
fi
echo "check-pr-gates: all required gates recorded for $short"
