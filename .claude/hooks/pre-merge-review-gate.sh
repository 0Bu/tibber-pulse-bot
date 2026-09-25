#!/usr/bin/env bash
# PreToolUse gate for the GitHub MCP merge tools (merge_pull_request /
# enable_pr_auto_merge): refuse the merge unless the PR body records every
# required review, stamped with the PR's current head SHA — the same
# scripts/check-pr-gates.sh the trusted-base pr-policy workflow runs, so the
# local hook and CI can never disagree about what "reviewed" means.
#
# Matching is by tool NAME only, never by Bash command text: a substring match
# on "merge" would trip on commit messages and docs that merely mention it.
# Fails CLOSED: if the PR cannot be fetched, the merge is blocked.
set -u

input=$(cat)
tool=$(jq -r '.tool_name // empty' <<<"$input" 2>/dev/null)
case "$tool" in
  *merge_pull_request*|*enable_pr_auto_merge*) ;;
  *) exit 0 ;;
esac

block() {
  echo "BLOCKED (pre-merge review gate): $1" >&2
  echo "Run the missing reviews on the PR head (/code-review, project-audit," >&2
  echo "pr-hygiene-review, plus chart-lint / ha-discovery-validate / live-test when" >&2
  echo "the diff needs them), tick each in the PR body's 'Merge gates' section with" >&2
  echo "a bare '@ <head-sha>' stamp, then retry. See CLAUDE.md > Merge gates." >&2
  exit 2
}

owner=$(jq -r '.tool_input.owner // empty' <<<"$input")
repo=$(jq -r '.tool_input.repo // empty' <<<"$input")
pr=$(jq -r '.tool_input.pullNumber // .tool_input.pull_number // empty' <<<"$input")
[ -n "$owner" ] && [ -n "$repo" ] && [ -n "$pr" ] || block "cannot read owner/repo/pullNumber from the $tool call"

cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || block "cannot cd to the project dir"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
api() {
  local auth=()
  [ -n "${GITHUB_TOKEN:-}" ] && auth=(-H "Authorization: Bearer $GITHUB_TOKEN")
  curl -fsSL --max-time 20 "${auth[@]}" -H 'Accept: application/vnd.github+json' \
    "https://api.github.com/repos/$owner/$repo/$1"
}

api "pulls/$pr" > "$tmp/pr.json" || block "cannot fetch PR #$pr from the GitHub API"
api "pulls/$pr/files?per_page=100" | jq -r '.[].filename' > "$tmp/files.txt" || block "cannot fetch PR #$pr files"
api "pulls/$pr/commits?per_page=100" > "$tmp/commits.json" || block "cannot fetch PR #$pr commits"
[ "$(wc -l < "$tmp/files.txt")" -eq "$(jq -r '.changed_files' "$tmp/pr.json")" ] \
  || block "PR #$pr changes more files than one API page returned; verify in CI (pr-policy) instead"
jq -r '.body // ""' "$tmp/pr.json" > "$tmp/body.md"
head=$(jq -r '.head.sha // empty' "$tmp/pr.json")

out=$(scripts/check-pr-gates.sh --body-file "$tmp/body.md" --head-sha "$head" \
  --files-file "$tmp/files.txt" --meta-file "$tmp/pr.json" --commits-file "$tmp/commits.json" 2>&1)
rc=$?
[ "$rc" -eq 0 ] || block "PR #$pr is missing review records for head ${head:0:12}:
$out"
exit 0
