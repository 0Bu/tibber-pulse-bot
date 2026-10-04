#!/usr/bin/env bash
# Git supplies the actual pushed objects; the checked-out index may belong
# to an unrelated branch. Every outgoing commit must pass, including roots
# and changes introduced while resolving a merge.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 2
cd "$REPO_ROOT" || exit 2
tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

fail() {
  echo "BLOCKED (secret gate): $1" >&2
  echo "See AGENTS.md > Security; remove sensitive material from outgoing commits before pushing." >&2
  exit 2
}

check_paths() {
  local path
  while IFS= read -r -d '' path; do
    case "${path##*/}" in
      .env.example) ;;
      .env|.env.*) fail "forbidden env file: $path" ;;
      *.pem|*.key) fail "key file requires removal: $path" ;;
    esac
  done < "$1"
}

check_content() {
  local added="$1" assignment='^\+[[:space:]]*(export[[:space:]]+)?(TIBBER_PULSE_PASSWORD|MQTT_PASSWORD|pulse[._]?password|password)[[:space:]]*[:=][[:space:]]*'
  if grep -qE '^\+[[:space:]]*-----BEGIN[ A-Z0-9_-]*PRIVATE KEY-----' "$added"; then
    fail "private key block detected (content redacted)"
  fi
  if grep -iE "$assignment" "$added" \
    | grep -viE "${assignment}$" \
    | grep -viE "${assignment}((\"\"|''|null|~)|[\"']?(changeme|change-me|example|dummy|dummy1234|dummy-9char|placeholder|your[-_]password|replace[-_]me|xxxx(-xxxx)?|<[^>]*>)[\"']?)[[:space:]]*(#.*)?$" \
    | grep -viE "${assignment}"'"?(\$\{.*\}|\$\(.*\)|\$[A-Za-z_][A-Za-z0-9_]*)"?[[:space:]]*(#.*)?$' \
    > "$tmp/password-hits"; then
    fail "credential assignment detected (values redacted)"
  fi
}

check_tree() {
  git ls-tree -r -z --name-only "$1" > "$tmp/paths" || fail "cannot inspect tree $1"
  check_paths "$tmp/paths"
}

check_commit() {
  check_tree "$1"
  git diff-tree --root -m --no-renames -p "$1" > "$tmp/patch" || fail "cannot inspect commit $1"
  check_content "$tmp/patch"
}

check_hygiene() {
  local result rc
  result=$("$SCRIPT_DIR/check-pr-hygiene.sh" --text "$tmp/text" --diff "$tmp/diff" 2>&1)
  rc=$?
  if [ "$rc" -ne 0 ]; then
    printf '%s\n' "$result" | grep '^FINDING' >&2 || true
    fail "PR hygiene check failed (exit $rc)"
  fi
}

scan_commits() {
  local commit
  git rev-list "$@" > "$tmp/commits" || fail "cannot enumerate outgoing commits; fetch the remote before retrying"
  : > "$tmp/text"
  : > "$tmp/diff"
  while IFS= read -r commit; do
    check_commit "$commit"
    git show -s --format='%B' "$commit" >> "$tmp/text" || fail "cannot inspect commit message $commit"
    # diff-tree -m compares a merge against every parent; git log -p omits
    # merge resolutions by default and would miss newly introduced secrets.
    sed -n '/^+[^+]/s/^+//p' "$tmp/patch" >> "$tmp/diff"
  done < "$tmp/commits"
  check_hygiene
}

scan_changes() {
  local path rc
  git diff --cached --name-only -z --diff-filter=ACMT > "$tmp/paths" || fail "cannot inspect staged paths"
  check_paths "$tmp/paths"
  git diff --cached --no-renames > "$tmp/patch" || fail "cannot inspect staged changes"
  check_content "$tmp/patch"
  if [ "${1:-}" != "--pre-commit" ]; then
    git ls-files -z > "$tmp/paths" || fail "cannot inspect tracked paths"
    check_paths "$tmp/paths"
    git diff --no-renames > "$tmp/unstaged" || fail "cannot inspect working changes"
    cat "$tmp/unstaged" >> "$tmp/patch"
    git ls-files --others --exclude-standard -z > "$tmp/paths" || fail "cannot inspect untracked paths"
    check_paths "$tmp/paths"
    while IFS= read -r -d '' path; do
      git diff --no-index -- /dev/null "$path" > "$tmp/untracked"
      rc=$?
      [ "$rc" -le 1 ] || fail "cannot inspect untracked file"
      cat "$tmp/untracked" >> "$tmp/patch"
    done < "$tmp/paths"
    check_content "$tmp/patch"
  fi
  : > "$tmp/text"
  sed -n '/^+[^+]/s/^+//p' "$tmp/patch" > "$tmp/diff"
  check_hygiene
}

scan_ref() {
  local commit base
  commit=$(git rev-parse --verify --end-of-options "$1^{commit}" 2>/dev/null) || fail "cannot resolve pushed source ref"
  check_tree "$commit"
  base=$(git merge-base "$commit" origin/main 2>/dev/null || true)
  if [ -n "$base" ]; then scan_commits "$base..$commit"; else scan_commits "$commit"; fi
}

if [ "${1:-}" = --scan-ref ]; then
  [ "$#" -eq 2 ] || fail "missing pushed source ref"
  scan_ref "$2"
  exit 0
fi

if [ "${1:-}" = "--pre-commit" ]; then
  scan_changes --pre-commit
  if git rev-parse --verify MERGE_HEAD >/dev/null 2>&1; then
    "$SCRIPT_DIR/pre-merge-review-gate.sh" --git-merge || exit 2
  fi
  exit 0
fi

input=""
if [ "${1:-}" != "--scan" ] && [ ! -t 0 ]; then
  input=$(cat) || fail "cannot read hook input"
fi

if [ -n "$input" ] && jq -e 'type == "object"' >/dev/null 2>&1 <<< "$input"; then
  cmd=$(jq -r '(.tool_input.command // .tool_input.CommandLine // .tool_input.cmd // .arguments.command // .arguments.CommandLine // .arguments.cmd // .Arguments.command // .Arguments.CommandLine // .Arguments.cmd // .command // .CommandLine // empty)' <<< "$input")
  # Both adapters must handle quoting and compound commands identically;
  # hook-command.sh tokenizes the input without evaluating any shell code.
  source "$SCRIPT_DIR/hook-command.sh"
  scan_push_command() {
    if hook_git_command "$@" && [ "$HOOK_SUBCOMMAND" = push ]; then
      local root token ref skip=0 destination=0 deleting=0 i refs=() list=""
      root=$(git "${HOOK_GIT_OPTIONS[@]}" rev-parse --show-toplevel 2>/dev/null) || fail "cannot resolve push repository"
      for ((i=0; i<${#HOOK_ARGS[@]}; i++)); do
        token="${HOOK_ARGS[i]}"
        if [ "$skip" -eq 1 ]; then skip=0; continue; fi
        case "$token" in
          -o|--push-option|--receive-pack|--exec) skip=1 ;;
          --repo) destination=1; skip=1 ;;
          --repo=*) destination=1 ;;
          --delete|-d) deleting=1 ;;
          --all|--branches) list=refs/heads ;;
          --tags) list="${list:+$list }refs/tags" ;;
          --mirror) list=refs ;;
          -*) ;;
          *)
            if [ "$destination" -eq 0 ]; then destination=1; continue; fi
            ref="${token#+}"; ref="${ref%%:*}"
            [ -z "$ref" ] || refs+=("$ref") ;;
        esac
      done
      [ "$deleting" -eq 0 ] || return 0
      if [ -n "$list" ]; then
        git -C "$root" for-each-ref --format='%(refname)' $list > "$tmp/push-refs" || fail "cannot enumerate pushed refs"
        while IFS= read -r ref; do refs+=("$ref"); done < "$tmp/push-refs"
      fi
      if [ "${#refs[@]}" -eq 0 ]; then
        (cd "$root" && "$SCRIPT_DIR/pre-push-secret-gate.sh" --scan) || exit 2
      else
        for ref in "${refs[@]}"; do
          (cd "$root" && "$SCRIPT_DIR/pre-push-secret-gate.sh" --scan-ref "$ref") || exit 2
        done
      fi
    fi
  }
  hook_each_command "$cmd" scan_push_command || fail "cannot parse shell hook command"
  exit 0
fi

if [ -n "$input" ] || [ "$#" -ge 2 ]; then
  while read -r local_ref local_sha remote_ref remote_sha extra; do
    [ -n "$local_ref" ] || continue
    [[ "$local_sha" =~ ^[0-9a-f]{40}$ && "$remote_sha" =~ ^[0-9a-f]{40}$ ]] && [ -n "$remote_ref" ] && [ -z "$extra" ] || fail "malformed pre-push input"
    [ "$local_sha" != 0000000000000000000000000000000000000000 ] || continue
    local_commit=$(git rev-parse --verify "$local_sha^{commit}" 2>/dev/null) || fail "pushed ref is not an available commit: $local_ref"
    check_tree "$local_commit"
    if [ "$remote_sha" = 0000000000000000000000000000000000000000 ]; then
      if [ "$#" -ge 2 ] && [[ "$1" =~ ^[A-Za-z0-9._-]+$ ]]; then
        scan_commits "$local_commit" --not "--remotes=$1"
      else
        scan_commits "$local_commit"
      fi
    else
      remote_commit=$(git rev-parse --verify "$remote_sha^{commit}" 2>/dev/null) || fail "remote commit is unavailable; fetch the remote before retrying"
      scan_commits "$remote_commit..$local_commit"
    fi
  done <<< "$input"
  exit 0
fi

scan_changes
scan_ref HEAD
echo "pre-push-secret-gate: scan clean."
