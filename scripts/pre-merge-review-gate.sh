#!/usr/bin/env bash
# Pre-merge review gate: ensures all required reviews are recorded before merging.
#
# Supports:
#   1. Agent tool hook (JSON on stdin):
#      - GitHub MCP merge tools (merge_pull_request, enable_pr_auto_merge, codex_apps, etc.)
#        -> Fetches PR from GitHub API and validates with scripts/check-pr-gates.sh
#      - Shell merge commands (gh pr merge, git merge)
#   2. Git pre-merge-commit hook (`scripts/pre-merge-review-gate.sh --git-merge`):
#      - Blocks local git merge into main unless explicitly approved for the merged commit SHA
#   3. CLI invocation:
#      - `scripts/pre-merge-review-gate.sh --pr <number> [--repo owner/repo]`
#      - `scripts/pre-merge-review-gate.sh --approve-local <commit-sha>`
#
# Fails CLOSED (exit 2) when requirements are not met.
set -uo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT" 2>/dev/null || exit 0

MARKER="${REPO_ROOT}/.agents/.review-marker"

block() {
  echo "BLOCKED (pre-merge review gate): $1" >&2
  echo "Run the missing reviews on the PR head (/code-review, project-audit," >&2
  echo "pr-hygiene-review, plus chart-lint / ha-discovery-validate / live-test when" >&2
  echo "the diff needs them), tick each in the PR body's 'Merge gates' section with" >&2
  echo "a bare '@ <head-sha>' stamp, then retry. See AGENTS.md > Merge gates." >&2
  exit 2
}

has_git_subcommand() {
  local target="$1"
  local full_cmd="$2"
  local segments
  segments=$(printf '%s\n' "$full_cmd" | sed -E 's/[;&|]+/\n/g')
  while IFS= read -r seg; do
    [ -z "$seg" ] && continue
    local in_git=0
    local prev_opt=""
    for token in $seg; do
      if [ "$in_git" -eq 0 ]; then
        if [ "$token" = "git" ] || [[ "$token" == */git ]]; then
          in_git=1
          prev_opt=""
        fi
        continue
      fi
      if [ "$prev_opt" = "-C" ] || [ "$prev_opt" = "-c" ] || [ "$prev_opt" = "--git-dir" ] || [ "$prev_opt" = "--work-tree" ]; then
        prev_opt=""
        continue
      fi
      if [[ "$token" == -* ]]; then
        if [ "$token" = "-C" ] || [ "$token" = "-c" ] || [ "$token" = "--git-dir" ] || [ "$token" = "--work-tree" ]; then
          prev_opt="$token"
        fi
        continue
      fi
      if [ "$token" = "$target" ]; then
        return 0
      else
        break
      fi
    done
  done <<<"$segments"
  return 1
}

is_git_merge_cmd() {
  local cmd_str="$1"
  # Ignore abort/quit/continue
  if printf '%s\n' "$cmd_str" | grep -qE 'git[[:space:]]+.*merge[[:space:]]+--(abort|quit|continue)'; then
    return 1
  fi
  has_git_subcommand "merge" "$cmd_str"
}

is_gh_pr_merge_cmd() {
  local cmd_str="$1"
  if printf '%s\n' "$cmd_str" | grep -E '(^|[;&|[:space:]])gh[[:space:]]+pr[[:space:]]+merge([[:space:]]|$)' >/dev/null 2>&1; then
    return 0
  fi
  return 1
}

extract_git_merge_target() {
  local cmd_str="$1"
  local target=""
  local in_merge=0
  local prev=""
  for tok in $cmd_str; do
    if [ "$in_merge" -eq 0 ]; then
      if [ "$tok" = "merge" ]; then
        in_merge=1
        prev="merge"
      fi
      continue
    fi
    # If chained command begins, stop
    case "$tok" in
      \;|\&\&|\|\||\|) break ;;
    esac
    # Skip flag options with values
    if [ "$prev" = "-m" ] || [ "$prev" = "-s" ] || [ "$prev" = "-X" ]; then
      prev=""
      continue
    fi
    if [[ "$tok" == -* ]]; then
      prev="$tok"
      continue
    fi
    target="$tok"
    break
  done
  printf '%s' "$target"
}

check_pr_via_api() {
  local owner="$1" repo="$2" pr="$3"
  if [[ "$repo" == *"/"* ]]; then
    owner="${repo%%/*}"
    repo="${repo##*/}"
  fi
  [ -n "$owner" ] && [ -n "$repo" ] && [ -n "$pr" ] || block "cannot determine owner, repo, or pull_number"

  local tmp
  tmp=$(mktemp -d); trap 'rm -rf "$tmp"' RETURN

  api() {
    local auth=()
    local token="${GITHUB_TOKEN:-}"
    if [ -z "$token" ] && command -v gh >/dev/null 2>&1; then
      token=$(gh auth token 2>/dev/null || true)
    fi
    [ -n "$token" ] && auth=(-H "Authorization: Bearer $token")
    curl -fsSL --max-time 20 "${auth[@]}" -H 'Accept: application/vnd.github+json' \
      "https://api.github.com/repos/$owner/$repo/$1"
  }

  api "pulls/$pr" > "$tmp/pr.json" || block "cannot fetch PR #$pr from the GitHub API (repos/$owner/$repo/pulls/$pr)"
  api "pulls/$pr/files?per_page=100" | jq -r '.[].filename' > "$tmp/files.txt" || block "cannot fetch PR #$pr files"
  api "pulls/$pr/commits?per_page=100" > "$tmp/commits.json" || block "cannot fetch PR #$pr commits"

  local expected_files
  expected_files=$(jq -r '.changed_files // 0' "$tmp/pr.json")
  if [ "$(wc -l < "$tmp/files.txt")" -lt "$expected_files" ]; then
    block "PR #$pr changes more files ($expected_files) than one API page returned; verify in CI (pr-policy) instead"
  fi

  jq -r '.body // ""' "$tmp/pr.json" > "$tmp/body.md"
  local head
  head=$(jq -r '.head.sha // empty' "$tmp/pr.json")
  [ -n "$head" ] || block "cannot resolve head SHA for PR #$pr"

  local out rc
  out=$(scripts/check-pr-gates.sh --body-file "$tmp/body.md" --head-sha "$head" \
    --files-file "$tmp/files.txt" --meta-file "$tmp/pr.json" --commits-file "$tmp/commits.json" 2>&1)
  rc=$?
  [ "$rc" -eq 0 ] || block "PR #$pr is missing review records for head ${head:0:12}:"$'\n'"$out"
  echo "OK: PR #$pr head ${head:0:12} has all required review gates recorded."
  return 0
}

# CLI Mode: --approve-local <sha>
if [ "${1:-}" = "--approve-local" ]; then
  sha="${2:-$(git rev-parse HEAD 2>/dev/null || true)}"
  [ -n "$sha" ] || { echo "pre-merge-review-gate: specify commit SHA to approve" >&2; exit 1; }
  full_sha=$(git rev-parse "$sha" 2>/dev/null || echo "$sha")
  mkdir -p "$(dirname "$MARKER")"
  printf '%s\n' "$full_sha" > "$MARKER"
  echo "Recorded review approval for commit $full_sha — local merge of this commit is now allowed."
  exit 0
fi

# CLI Mode: --pr <number> [--repo owner/repo]
if [ "${1:-}" = "--pr" ]; then
  pr="$2"
  repo_arg="${4:-0Bu/tibber-pulse-bot}"
  owner="${repo_arg%%/*}"
  repo="${repo_arg##*/}"
  check_pr_via_api "$owner" "$repo" "$pr"
  exit 0
fi

# Mode: Git pre-merge-commit hook
if [ "${1:-}" = "--git-merge" ]; then
  current_branch=$(git symbolic-ref --short HEAD 2>/dev/null || echo "HEAD")
  if [ "$current_branch" = "main" ]; then
    merge_head=$(git rev-parse MERGE_HEAD 2>/dev/null || true)
    recorded_sha=""
    [ -f "$MARKER" ] && recorded_sha=$(cat "$MARKER" 2>/dev/null | tr -d '[:space:]')
    if [ -z "$recorded_sha" ]; then
      block "Local git merge into main blocked without review approval.
To approve local merge after code review (.agents/agents/go-reviewer.md) and project-audit:
    bash scripts/pre-merge-review-gate.sh --approve-local <commit-sha>"
    fi
    if [ -n "$merge_head" ] && [ "$merge_head" != "$recorded_sha" ]; then
      block "Local git merge into main blocked: merged commit $merge_head does not match approved commit $recorded_sha."
    fi
    echo "OK: Local merge of $recorded_sha into main is approved by marker."
  fi
  exit 0
fi

# Mode: Agent tool hook or piped stdin
if [ ! -t 0 ] && [ "${1:-}" != "--check" ]; then
  input=$(cat)
  if [ -n "$input" ]; then
    # 1. Direct GitHub MCP merge tools
    tool=$(jq -r '(.tool_name // .ToolName // .name // empty)' <<<"$input" 2>/dev/null)
    tool_lower=$(tr '[:upper:]' '[:lower:]' <<<"$tool")
    case "$tool_lower" in
      *merge*pull*request*|*pull*request*merge*|*enable*auto*merge*|*enable*automerge*|*merge*pr*|*pr*merge*)
        owner=$(jq -r '(.tool_input.owner // .arguments.owner // .Arguments.owner // .owner // empty)' <<<"$input" 2>/dev/null)
        repo=$(jq -r '(.tool_input.repo // .tool_input.repository // .arguments.repo // .arguments.repository // .Arguments.repo // .Arguments.repository // .repo // empty)' <<<"$input" 2>/dev/null)
        pr=$(jq -r '(.tool_input.pullNumber // .tool_input.pull_number // .tool_input.pr // .arguments.pullNumber // .arguments.pull_number // .arguments.pr // .Arguments.pullNumber // .Arguments.pull_number // .Arguments.pr // .arguments.pull_request_number // .tool_input.pull_request_number // .pullNumber // .pull_number // empty)' <<<"$input" 2>/dev/null)
        if [[ "$repo" == *"/"* ]]; then
          owner="${repo%%/*}"
          repo="${repo##*/}"
        fi
        owner="${owner:-0Bu}"
        repo="${repo:-tibber-pulse-bot}"
        check_pr_via_api "$owner" "$repo" "$pr"
        exit 0
        ;;
    esac

    # 2. Shell command invocations
    cmd=$(jq -r '(.tool_input.command // .tool_input.CommandLine // .tool_input.cmd // .arguments.command // .arguments.CommandLine // .arguments.cmd // .Arguments.command // .Arguments.CommandLine // .Arguments.cmd // .command // .CommandLine // empty)' <<<"$input" 2>/dev/null)
    if [ -n "$cmd" ]; then
      if is_gh_pr_merge_cmd "$cmd"; then
        pr=$(printf '%s\n' "$cmd" | grep -oE '(pull/|[[:space:]])[0-9]+' | grep -oE '[0-9]+' | head -1 || true)
        if [ -z "$pr" ] && command -v gh >/dev/null 2>&1; then
          pr=$(gh pr view --json number -q .number 2>/dev/null || true)
        fi
        [ -n "$pr" ] || block "gh pr merge detected but could not determine PR number"
        repo_arg=$(printf '%s\n' "$cmd" | grep -oE '(-R|--repo)[[:space:]=]+[^[:space:]]+' | awk '{print $NF}' | tr -d '="' || true)
        owner="0Bu"
        repo="tibber-pulse-bot"
        if [ -n "$repo_arg" ] && [[ "$repo_arg" == *"/"* ]]; then
          owner="${repo_arg%%/*}"
          repo="${repo_arg##*/}"
        fi
        check_pr_via_api "$owner" "$repo" "$pr"
        exit 0
      fi

      if is_git_merge_cmd "$cmd"; then
        recorded_sha=""
        [ -f "$MARKER" ] && recorded_sha=$(cat "$MARKER" 2>/dev/null | tr -d '[:space:]')
        if [ -z "$recorded_sha" ]; then
          block "git merge command detected in shell without recorded review approval. Run review and approve: bash scripts/pre-merge-review-gate.sh --approve-local <sha>"
        fi
        target=$(extract_git_merge_target "$cmd")
        if [ -n "$target" ]; then
          target_sha=$(git rev-parse "$target" 2>/dev/null || true)
          if [ -n "$target_sha" ] && [ "$target_sha" != "$recorded_sha" ]; then
            block "git merge target ($target, SHA $target_sha) does not match recorded approved commit ($recorded_sha)."
          fi
        fi
        exit 0
      fi

      # If this was an agent tool call and not a merge command, pass through
      if jq -e '(.tool_name // .ToolName // .name // .tool_input // .arguments // .Arguments)' <<<"$input" >/dev/null 2>&1; then
        exit 0
      fi
    fi
  fi
fi

# Fallback check
if [ "${1:-}" = "--check" ]; then
  target_sha="${2:-$(git rev-parse HEAD 2>/dev/null || true)}"
  recorded_sha=""
  [ -f "$MARKER" ] && recorded_sha=$(cat "$MARKER" 2>/dev/null | tr -d '[:space:]')
  if [ -n "$target_sha" ] && [ "$target_sha" = "$recorded_sha" ]; then
    echo "OK: $target_sha is approved for local merge."
    exit 0
  else
    echo "NOTICE: $target_sha is not recorded as approved (approved: ${recorded_sha:-<none>})." >&2
    exit 1
  fi
fi

exit 0
