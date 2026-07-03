#!/usr/bin/env bash
# PreToolUse gate: no PR merge without a code review of the merged commit.
#
# A Claude Code hook can only run shell, not invoke a skill — so instead of
# "running" /code-review itself, this blocks the merge until a review has
# been recorded for the current HEAD. The block message tells Claude to run
# /code-review + the project-audit skill first; after a clean review, an
# explicit --approve records HEAD, and the retried merge is allowed.
#
# Gates merges Claude performs via the GitHub MCP tools
# (merge_pull_request / enable_pr_auto_merge) — that's the merge path in this
# environment (no gh CLI). Matching is by tool NAME only, never by Bash
# command text: a substring match on "gh pr merge" / "git merge" would also
# trip on commit messages, echoes and docs that merely mention merging.
# Merges done in the GitHub UI don't reach any hook — enforce those with
# branch protection and a required status check instead.
set -u

MARKER="${CLAUDE_PROJECT_DIR:-.}/.claude/.review-marker"

# --approve: record current HEAD as reviewed (run after a clean review).
if [ "${1:-}" = "--approve" ]; then
  cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || { echo "cannot cd to project dir" >&2; exit 1; }
  sha=$(git rev-parse HEAD 2>/dev/null) || { echo "cannot resolve HEAD" >&2; exit 1; }
  mkdir -p "$(dirname "$MARKER")"
  printf '%s\n' "$sha" > "$MARKER"
  echo "Recorded review approval for $sha — merge of this commit is now allowed."
  exit 0
fi

input=$(cat)
tool=$(printf '%s' "$input" | jq -r '.tool_name // empty' 2>/dev/null)

case "$tool" in
  *merge_pull_request*|*enable_pr_auto_merge*) ;;
  *) exit 0 ;;
esac

cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || exit 0
sha=$(git rev-parse HEAD 2>/dev/null)

block() {
  echo "BLOCKED (pre-merge review gate): $1" >&2
  echo "Run /code-review and the project-audit skill, resolve any findings, then" >&2
  echo "record approval and retry the merge:" >&2
  echo "    bash .claude/hooks/pre-merge-review-gate.sh --approve" >&2
  echo "(Only gates merges Claude performs; for GitHub-UI merges use branch" >&2
  echo "protection + a required check.)" >&2
  exit 2
}

[ -z "$sha" ] && block "cannot resolve local HEAD to confirm a review ran"
[ -f "$MARKER" ] || block "no review recorded for HEAD $sha"
recorded=$(cat "$MARKER" 2>/dev/null)
[ "$recorded" = "$sha" ] || block "recorded review is for ${recorded:-<none>} but HEAD is now $sha — re-review the new commits"
exit 0
