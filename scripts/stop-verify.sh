#!/usr/bin/env bash
# Stop hook: before an agent ends a turn with changes on the branch, run the
# CI-gated static checks (AGENTS.md > Verification protocol) and the mechanical
# drift check, so "done" is never reported on a tree CI will reject. Exit 2
# feeds the failure back to the agent instead of letting the turn end.
set -u

input=""
if [ ! -t 0 ]; then
  if read -t 1 -r first_line; then
    input=$(printf '%s\n' "$first_line"; cat 2>/dev/null || true)
  fi
fi
# Already blocked once this turn: let agent stop and report instead of looping.
[ -n "$input" ] && [ "$(jq -r '.stop_hook_active // false' <<<"$input" 2>/dev/null)" = "true" ] && exit 0

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT" || exit 0
command -v git >/dev/null 2>&1 || exit 0
base=$(git merge-base HEAD origin/main 2>/dev/null) || exit 0
changed=$( { git diff --name-only "$base"; git ls-files --others --exclude-standard; } 2>/dev/null | sort -u)
[ -n "$changed" ] || exit 0

fail() { echo "BLOCKED (stop gate): $1" >&2; echo "$2" | tail -30 >&2; exit 2; }

if grep -qE '(\.go|^go\.(mod|sum))$' <<<"$changed" && command -v go >/dev/null 2>&1; then
  unformatted=$(gofmt -l . 2>&1)
  [ -z "$unformatted" ] || fail "gofmt found unformatted files" "$unformatted"
  out=$(go vet ./... 2>&1) || fail "go vet failed" "$out"
  out=$(go test ./... 2>&1) || fail "go test failed" "$out"
fi

out=$(scripts/check-drift.sh 2>&1) || fail "docs drifted from code (scripts/check-drift.sh)" "$(grep DRIFT <<<"$out")"
exit 0
