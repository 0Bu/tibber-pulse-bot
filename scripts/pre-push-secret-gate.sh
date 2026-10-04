#!/usr/bin/env bash
# Pre-push and secret safety gate for tibber-pulse-bot.
# Hardens AGENTS.md > Security ("Never commit the bridge password").
#
# Runs in three modes:
#   1. Git pre-push hook: receives `<local-ref> <local-sha> <remote-ref> <remote-sha>` on stdin.
#      Scans every outgoing commit and ref individually (catches secrets added and deleted in later commits).
#   2. Git pre-commit hook: `pre-push-secret-gate.sh --pre-commit` (checks staged files and diffs).
#   3. Manual scan or Agent PreToolUse hook: `pre-push-secret-gate.sh [--scan]`
#
# Fails CLOSED (exit 2, blocks the push/commit) on:
#   1. A real `.env` or `.env.*` file tracked in any outgoing ref or commit (.env.example is allowed).
#   2. Outgoing commits adding a real TIBBER_PULSE_PASSWORD / mqtt password value.
#   3. scripts/check-pr-hygiene.sh reporting personal data, leaked tokens, or German prose.
set -u

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT" 2>/dev/null || exit 0
command -v git >/dev/null 2>&1 || exit 0

fail() {
  echo "BLOCKED (secret gate): $1" >&2
  echo "See AGENTS.md > Security. Run the secret-scanner agent for a full audit;" >&2
  echo "if the value is real, scrub it (and rotate the bridge password) before pushing." >&2
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

is_git_push_cmd() {
  has_git_subcommand "push" "$1"
}

# Mode 2: Git pre-commit hook
if [ "${1:-}" = "--pre-commit" ]; then
  # Check staged file names
  staged_files=$(git diff --cached --name-only 2>/dev/null || true)
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    case "$(basename "$f")" in
      .env.example|.env.sample) ;;
      .env|.env.*)
        fail "Cannot stage/commit $f. Only .env.example may be tracked."
        ;;
    esac
  done <<<"$staged_files"

  # Check staged diff additions
  added=$(git diff --cached 2>/dev/null | grep -E '^\+' || true)
  hits=$(printf '%s\n' "$added" \
    | grep -iE '^\+[[:space:]]*(export[[:space:]]+)?(TIBBER_PULSE_PASSWORD|MQTT_PASSWORD|pulse[._]?password)[[:space:]]*[:=]' \
    | grep -vE '\$\{|\*|<[^>]*>|changeme|change-me|example|dummy|placeholder|your[-_]|replace|xxxx|=[[:space:]]*("")?[[:space:]]*$' \
    || true)
  if [ -n "$hits" ]; then
    fail "Staged changes add a real password value:"$'\n'"$hits"
  fi
  exit 0
fi

# Check individual commit for secrets and forbidden file paths (F01, F05)
check_single_commit() {
  local commit="$1"
  local ref_name="${2:-HEAD}"

  # 1. Check touched file names in this specific commit
  local files
  files=$(git diff-tree --no-commit-id --name-only -r "$commit" 2>/dev/null || true)
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    case "$(basename "$f")" in
      .env.example|.env.sample) ;;
      .env|.env.*)
        fail "Commit $commit in $ref_name touches forbidden env file '$f'. Secrets must not be committed even if removed in subsequent commits."
        ;;
    esac
  done <<<"$files"

  # 2. Check lines added in this specific commit
  local added hits
  added=$(git diff-tree -p "$commit" 2>/dev/null | grep -E '^\+' || true)
  hits=$(printf '%s\n' "$added" \
    | grep -iE '^\+[[:space:]]*(export[[:space:]]+)?(TIBBER_PULSE_PASSWORD|MQTT_PASSWORD|pulse[._]?password)[[:space:]]*[:=]' \
    | grep -vE '\$\{|\*|<[^>]*>|changeme|change-me|example|dummy|placeholder|your[-_]|replace|xxxx|=[[:space:]]*("")?[[:space:]]*$' \
    || true)
  if [ -n "$hits" ]; then
    fail "Commit $commit in $ref_name adds a real password value:"$'\n'"$hits"
  fi
}

# Mode 1: Check if input comes from stdin
stdin_content=""
if [ "${1:-}" != "--scan" ] && [ ! -t 0 ]; then
  stdin_content=$(cat)
fi

# Check if stdin is agent tool call JSON
if [ -n "$stdin_content" ] && printf '%s' "$stdin_content" | jq -e '(.tool_name // .ToolName // .name // .tool_input // .arguments // .Arguments // .command // .CommandLine)' >/dev/null 2>&1; then
  cmd=$(printf '%s' "$stdin_content" | jq -r '(.tool_input.command // .tool_input.CommandLine // .tool_input.cmd // .arguments.command // .arguments.CommandLine // .arguments.cmd // .Arguments.command // .Arguments.CommandLine // .Arguments.cmd // .command // .CommandLine // empty)' 2>/dev/null)
  if ! is_git_push_cmd "$cmd"; then
    # Not a git push command, pass through
    exit 0
  fi
  # If it is a git push command, proceed to scan working tree & outgoing commits
  stdin_content=""
fi

# If stdin has git pre-push format: `<local-ref> <local-sha> <remote-ref> <remote-sha>`
if [ -n "$stdin_content" ] && grep -qE '^[^[:space:]]+ [0-9a-f]{40} [^[:space:]]+ [0-9a-f]{40}' <<<"$stdin_content"; then
  while IFS=' ' read -r local_ref local_sha remote_ref remote_sha; do
    [ -z "$local_sha" ] && continue
    # Branch deletion
    if [ "$local_sha" = "0000000000000000000000000000000000000000" ]; then
      continue
    fi

    # Check files tracked in pushed tree (F05)
    tree_files=$(git ls-tree -r --name-only "$local_sha" 2>/dev/null || true)
    while IFS= read -r f; do
      [ -z "$f" ] && continue
      case "$(basename "$f")" in
        .env.example|.env.sample) ;;
        .env|.env.*)
          fail "Pushed ref $local_ref contains tracked env file '$f'. Only .env.example may be tracked."
          ;;
      esac
    done <<<"$tree_files"

    # Determine outgoing commit range
    if [ "$remote_sha" = "0000000000000000000000000000000000000000" ]; then
      base=$(git merge-base "$local_sha" origin/main 2>/dev/null || true)
      if [ -n "$base" ]; then
        range="$base..$local_sha"
      else
        range="$local_sha"
      fi
    else
      range="$remote_sha..$local_sha"
    fi

    # Inspect each outgoing commit individually (F01)
    commits=$(git rev-list "$range" 2>/dev/null || true)
    for c in $commits; do
      check_single_commit "$c" "$local_ref"
    done

    # Run check-pr-hygiene on actual pushed commits (F01, F05)
    if [ -x scripts/check-pr-hygiene.sh ]; then
      tmp_hyg=$(mktemp -d)
      git log --format='%B' "$range" > "$tmp_hyg/text" 2>/dev/null || true
      git log --format= -p "$range" 2>/dev/null | grep -E '^\+([^+]|$)' | cut -c2- > "$tmp_hyg/diff" || true
      hyg_out=$(scripts/check-pr-hygiene.sh --text "$tmp_hyg/text" --diff "$tmp_hyg/diff" 2>&1)
      hyg_rc=$?
      rm -rf "$tmp_hyg"
      if [ "$hyg_rc" -ne 0 ]; then
        fail "PR hygiene check failed for pushed ref $local_ref:"$'\n'"$hyg_out"
      fi
    fi
  done <<<"$stdin_content"

  exit 0
fi

# Mode 3: Manual scan or fallback check (current HEAD against origin/main)
# 1. Check working directory / index for tracked .env files
tracked_env=$(git ls-files 2>/dev/null | grep -E '^(\.env|\.env\..*)$' | grep -vE '^(\.env\.example|\.env\.sample)$' || true)
if [ -n "$tracked_env" ]; then
  fail "Tracked env file detected: $tracked_env. Untrack it (git rm --cached $tracked_env)."
fi

# 2. Check each outgoing commit individually (F01)
base=$(git merge-base HEAD origin/main 2>/dev/null || true)
if [ -n "$base" ]; then
  commits=$(git rev-list "$base..HEAD" 2>/dev/null || true)
else
  commits=$(git rev-list HEAD 2>/dev/null || true)
fi
for c in $commits; do
  check_single_commit "$c" "HEAD"
done

# 3. Run check-pr-hygiene
if [ -x scripts/check-pr-hygiene.sh ]; then
  if [ -n "$base" ]; then
    hyg_out=$(scripts/check-pr-hygiene.sh 2>&1)
  else
    tmp_hyg=$(mktemp -d)
    git log --format='%B' HEAD > "$tmp_hyg/text" 2>/dev/null || true
    git log --format= -p HEAD 2>/dev/null | grep -E '^\+([^+]|$)' | cut -c2- > "$tmp_hyg/diff" || true
    hyg_out=$(scripts/check-pr-hygiene.sh --text "$tmp_hyg/text" --diff "$tmp_hyg/diff" 2>&1)
    rm -rf "$tmp_hyg"
  fi
  hyg_rc=$?
  if [ "$hyg_rc" -ne 0 ]; then
    fail "PR hygiene check failed:"$'\n'"$hyg_out"
  fi
fi

if [ "${1:-}" = "--scan" ]; then
  echo "pre-push-secret-gate: scan clean."
fi
exit 0
