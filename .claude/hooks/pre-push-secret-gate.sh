#!/usr/bin/env bash
# PreToolUse gate for `git push`. Hardens CLAUDE.md > Security ("Never commit
# the bridge password") into an automatic check: the manual secret-scanner
# agent is thorough but only runs when invoked; this fires on every push.
#
# Fails CLOSED (exit 2, blocks the push) only on high-confidence signals:
#   1. a real `.env` file is tracked by git (only `.env.example` may be), or
#   2. the outgoing commits add a real TIBBER_PULSE_PASSWORD / mqtt password
#      value (placeholders and ${VAR} references are ignored).
# Fails OPEN (exit 0) on anything ambiguous or any git error, so it never
# bricks a legitimate push. For a full audit, run the secret-scanner agent.
set -u

input=$(cat)
cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty' 2>/dev/null)
case "$cmd" in
  *"git push"*) ;;
  *) exit 0 ;;
esac

cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || exit 0
command -v git >/dev/null 2>&1 || exit 0

fail() {
  echo "BLOCKED (pre-push secret gate): $1" >&2
  echo "See CLAUDE.md > Security. Run the secret-scanner agent for a full audit;" >&2
  echo "if the value is real, scrub it (and rotate the bridge password) before pushing." >&2
  exit 2
}

# 1. A real .env must never be tracked (.env.example is the allowed template).
if git ls-files --error-unmatch .env >/dev/null 2>&1; then
  fail ".env is tracked by git — it may hold the bridge password. Untrack it (git rm --cached .env)."
fi

# 2. Scan the commits this push would add for a real password assignment.
base=$(git merge-base HEAD origin/main 2>/dev/null)
if [ -z "$base" ]; then
  base=$(git rev-list --max-parents=0 HEAD 2>/dev/null | tail -1)
fi
[ -z "$base" ] && exit 0

added=$(git diff "$base"...HEAD 2>/dev/null | grep -E '^\+' || true)
[ -z "$added" ] && exit 0

# Added lines assigning a password. Then subtract obvious placeholders /
# shell-var references so .env.example and docker-compose.yml don't trip it.
hits=$(printf '%s\n' "$added" \
  | grep -iE '^\+[[:space:]]*(export[[:space:]]+)?(TIBBER_PULSE_PASSWORD|MQTT_PASSWORD|pulse[._]?password)[[:space:]]*[:=]' \
  | grep -vE '\$\{|<[^>]*>|changeme|change-me|example|dummy|placeholder|your[-_]|replace|xxxx|=[[:space:]]*("")?[[:space:]]*$' \
  || true)

if [ -n "$hits" ]; then
  fail "outgoing commits add a real password value:"$'\n'"$hits"
fi

exit 0
