#!/usr/bin/env bash
# Remote approvals belong to the live PR head; local approvals belong to the
# incoming commit, never merely to the branch that happens to be checked out.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 2
cd "$REPO_ROOT" || exit 2
source "$SCRIPT_DIR/hook-command.sh"
tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

block() {
  echo "BLOCKED (pre-merge review gate): $1" >&2
  echo "Run the required reviews and record their current PR-head stamps; see AGENTS.md > Merge gates." >&2
  exit 2
}

check_local_target() {
  local root="$1" target="$2" sha recorded=""
  sha=$(git -C "$root" rev-parse --verify --end-of-options "$target^{commit}" 2>/dev/null) || block "cannot resolve incoming merge commit"
  [ -f "$root/.agents/.review-marker" ] && recorded=$(cat "$root/.agents/.review-marker")
  [ "$sha" = "$recorded" ] || block "incoming commit $sha has no matching local review approval"
}

check_git_merge() {
  local root="$1" current_branch path target variable count=0
  current_branch=$(git -C "$root" symbolic-ref --short HEAD 2>/dev/null || true)
  [ "$current_branch" = main ] || return 0
  path=$(git -C "$root" rev-parse --git-path MERGE_HEAD) || block "cannot locate incoming merge state"
  [[ "$path" = /* ]] || path="$root/$path"
  if [ -s "$path" ]; then
    while IFS= read -r target; do
      count=$((count+1))
      check_local_target "$root" "$target"
    done < "$path"
  else
    # Git exports the resolved incoming objects as GITHEAD_<sha> before
    # running pre-merge-commit, but writes MERGE_HEAD only if it stops.
    for variable in $(compgen -e); do
      if [[ "$variable" =~ ^GITHEAD_([0-9a-f]{40})$ ]]; then
        target="${BASH_REMATCH[1]}"
        count=$((count+1))
        check_local_target "$root" "$target"
      fi
    done
  fi
  [ "$count" -gt 0 ] || block "no incoming commit available for local merge validation"
  [ "$count" -eq 1 ] || block "local approval supports one incoming commit at a time"
}

default_repo() {
  if [ -n "${GH_REPO:-}" ]; then printf '%s' "$GH_REPO"; return; fi
  local remote host
  remote=$(git -C "${HOOK_CWD:-$REPO_ROOT}" remote get-url origin 2>/dev/null) || return 1
  case "$remote" in
    https://github.com/*) remote="${remote#https://github.com/}" ;;
    git@*:*)
      host="${remote#git@}"; host="${host%%:*}"
      [ "$host" = github.com ] || return 1
      remote="${remote#*:}" ;;
    *) return 1 ;;
  esac
  printf '%s' "${remote%.git}"
}

check_pr_via_api() {
  local repository="$1" pr="$2" expected="${3:-}" token="${GH_TOKEN:-${GITHUB_TOKEN:-}}" head out
  [[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] && [[ "$pr" =~ ^[1-9][0-9]*$ ]] || block "cannot determine a valid repository and PR number"
  if [ -z "$token" ] && command -v gh >/dev/null 2>&1; then token=$(gh auth token 2>/dev/null || true); fi
  api() {
    local auth=(-H 'Accept: application/vnd.github+json')
    [ -z "$token" ] || auth+=(-H "Authorization: Bearer $token")
    curl -fsSL --max-time 20 "${auth[@]}" \
      "https://api.github.com/repos/$repository/$1"
  }
  api "pulls/$pr" > "$tmp/pr.json" || block "cannot fetch PR #$pr"
  api "pulls/$pr/files?per_page=100" | jq -er '.[].filename' > "$tmp/files" || block "cannot fetch changed files"
  api "pulls/$pr/commits?per_page=100" > "$tmp/commits.json" || block "cannot fetch PR commits"
  [ "$(wc -l < "$tmp/files" | tr -d '[:space:]')" = "$(jq -r '.changed_files' "$tmp/pr.json")" ] || block "incomplete PR file list; use the paginated CI gate"
  [ "$(jq 'length' "$tmp/commits.json")" = "$(jq '.commits' "$tmp/pr.json")" ] || block "incomplete PR commit list; use the paginated CI gate"
  head=$(jq -er '.head.sha' "$tmp/pr.json") || block "cannot resolve PR head"
  [ -z "$expected" ] || [ "$expected" = "$head" ] || block "requested merge head differs from the live PR head"
  jq -r '.body // ""' "$tmp/pr.json" > "$tmp/body" || block "cannot read PR body"
  out=$("$SCRIPT_DIR/check-pr-gates.sh" --body-file "$tmp/body" --head-sha "$head" \
    --files-file "$tmp/files" --meta-file "$tmp/pr.json" --commits-file "$tmp/commits.json" 2>&1) || block "$out"
  echo "OK: PR #$pr head ${head:0:12} has all required review gates recorded."
}

case "${1:-}" in
  --approve-local)
    [ "$#" -eq 2 ] || block "specify the incoming commit to approve"
    sha=$(git rev-parse --verify --end-of-options "$2^{commit}" 2>/dev/null) || block "approval target is not an available commit"
    mkdir -p "$REPO_ROOT/.agents" || exit 2
    printf '%s\n' "$sha" > "$REPO_ROOT/.agents/.review-marker" || exit 2
    echo "Recorded local review approval for $sha."
    exit 0 ;;
  --git-merge) check_git_merge "$REPO_ROOT"; exit 0 ;;
  --check) check_local_target "$REPO_ROOT" "${2:-HEAD}"; exit 0 ;;
  --pr)
    [ "$#" -ge 2 ] || block "missing PR number"
    pr="$2"; shift 2
    repository=$(default_repo || true); expected=""
    while [ "$#" -gt 0 ]; do
      [ "$#" -ge 2 ] || block "missing CLI option value"
      case "$1" in
        --repo) repository="$2" ;;
        --expected-head) expected="$2" ;;
        *) block "unknown CLI option" ;;
      esac
      shift 2
    done
    check_pr_via_api "$repository" "$pr" "$expected"
    exit 0 ;;
esac

inspect_merge_command() {
  local root token skip=0 targets=() operation="" selector="" repository="" expected="" i
  if hook_git_command "$@" && [ "$HOOK_SUBCOMMAND" = merge ]; then
    root=$(git "${HOOK_GIT_OPTIONS[@]}" rev-parse --show-toplevel 2>/dev/null) || block "cannot resolve merge repository"
    for ((i=0; i<${#HOOK_ARGS[@]}; i++)); do
      token="${HOOK_ARGS[i]}"
      if [ "$skip" -eq 1 ]; then skip=0; continue; fi
      case "$token" in
        --abort|--quit) operation=abort ;;
        --continue) operation=continue ;;
        -m|-F|-s|-X|--message|--file|--strategy|--strategy-option|--into-name) skip=1 ;;
        -*) ;;
        *) targets+=("$token") ;;
      esac
    done
    [ "$operation" != abort ] || return 0
    if [ "$operation" = continue ]; then check_git_merge "$root"; return 0; fi
    [ "${#targets[@]}" -le 1 ] || block "review each incoming merge commit separately"
    if [ "${#targets[@]}" -eq 0 ]; then targets=('@{upstream}'); fi
    check_local_target "$root" "${targets[0]}"
    return 0
  fi

  hook_normalize_command "$@"
  [ "${#HOOK_WORDS[@]}" -gt 0 ] && [ "${HOOK_WORDS[0]##*/}" = gh ] || return 0
  repository=$(default_repo || true)
  for ((i=1; i<${#HOOK_WORDS[@]}; i++)); do
    token="${HOOK_WORDS[i]}"
    case "$token" in
      -R|--repo|--match-head-commit)
        [ "$((i+1))" -lt "${#HOOK_WORDS[@]}" ] || block "missing gh option value"
        if [ "$token" = --match-head-commit ]; then expected="${HOOK_WORDS[i+1]}"; else repository="${HOOK_WORDS[i+1]}"; fi
        i=$((i+1)) ;;
      --repo=*) repository="${token#*=}" ;;
      -R?*) repository="${token#-R}" ;;
      --match-head-commit=*) expected="${token#*=}" ;;
      pr) operation=pr ;;
      merge) if [ "$operation" = pr ]; then operation=merge; fi ;;
      -t|-b|-F|--subject|--body|--body-file|--author-email) i=$((i+1)) ;;
      -*) ;;
      *) if [ "$operation" = merge ] && [ -z "$selector" ]; then selector="$token"; fi ;;
    esac
  done
  [ "$operation" = merge ] || return 0
  if [[ "$selector" == https://github.com/*/pull/* ]]; then
    repository="${selector#https://github.com/}"; repository="${repository%/pull/*}"
    selector="${selector##*/}"
  fi
  if [[ ! "$selector" =~ ^[1-9][0-9]*$ ]]; then
    local args=(pr view --repo "$repository" --json number -q .number)
    [ -z "$selector" ] || args+=("$selector")
    selector=$(cd "${HOOK_CWD:-$REPO_ROOT}" && gh "${args[@]}" 2>/dev/null) || block "cannot resolve gh merge PR"
  fi
  check_pr_via_api "$repository" "$selector" "$expected"
}

input=""
[ -t 0 ] || input=$(cat)
[ -n "$input" ] || exit 0
jq -e 'type == "object"' >/dev/null 2>&1 <<< "$input" || block "malformed tool input"
tool=$(jq -r '(.tool_name // .ToolName // .name // empty)' <<< "$input" | tr '[:upper:]' '[:lower:]')
case "$tool" in
  *merge*pull*request*|*pull*request*merge*|*enable*auto*merge*|*enable*automerge*|*merge*pr*|*pr*merge*)
    args=$(jq '(.tool_input // .arguments // .Arguments // .)' <<< "$input")
    repository=$(jq -r '(.repository_full_name // .repo // .repository // empty)' <<< "$args")
    owner=$(jq -r '.owner // empty' <<< "$args")
    if [[ "$repository" != */* ]] && [ -n "$owner" ]; then repository="$owner/$repository"; fi
    [ -n "$repository" ] || repository=$(default_repo || true)
    pr=$(jq -r '(.pr_number // .pullNumber // .pull_number // .pull_request_number // .pr // empty)' <<< "$args")
    expected=$(jq -r '(.expected_head_sha // .sha // empty)' <<< "$args")
    check_pr_via_api "$repository" "$pr" "$expected"
    exit 0 ;;
esac
cmd=$(jq -r '(.tool_input.command // .tool_input.CommandLine // .tool_input.cmd // .arguments.command // .arguments.CommandLine // .arguments.cmd // .Arguments.command // .Arguments.CommandLine // .Arguments.cmd // .command // .CommandLine // empty)' <<< "$input")
hook_each_command "$cmd" inspect_merge_command || block "cannot parse shell hook command"
