#!/usr/bin/env bash
# Installs git safety hooks into the repository's effective hooks directory.
# Enforces:
#   1. pre-push: secret scanner and PR hygiene check on outgoing commits
#   2. pre-commit: blocks staging/committing .env files and hardcoded passwords
#   3. pre-merge-commit: ensures review gate approval before local merge
#
# Respects core.hooksPath if configured, falling back to $(git rev-parse --git-path hooks)
# or .git/hooks. Backs up existing non-matching hooks rather than clobbering them.
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

# Determine effective git hooks directory:
configured_hooks_path=$(git config --get core.hooksPath 2>/dev/null || true)
if [ -n "$configured_hooks_path" ]; then
  configured_hooks_path="${configured_hooks_path/#\~/$HOME}"
  if [[ "$configured_hooks_path" = /* ]]; then
    HOOKS_DIR="$configured_hooks_path"
  else
    HOOKS_DIR="$REPO_ROOT/$configured_hooks_path"
  fi
else
  HOOKS_DIR=$(git rev-parse --git-path hooks 2>/dev/null || echo "$REPO_ROOT/.git/hooks")
  if [[ "$HOOKS_DIR" != /* ]]; then
    HOOKS_DIR="$REPO_ROOT/$HOOKS_DIR"
  fi
fi

mkdir -p "$HOOKS_DIR"
echo "Installing hooks into effective hooks directory: $HOOKS_DIR"

install_hook() {
  local hook_name="$1"
  local hook_file="$2"
  local extra_args="${3:-}"
  local target="$HOOKS_DIR/$hook_name"

  if [ -f "$target" ] && ! grep -q "$hook_file" "$target" 2>/dev/null; then
    local backup="${target}.backup.$(date +%s)"
    echo "Backing up existing $hook_name hook to $backup"
    mv "$target" "$backup"
  fi

  cat > "$target" <<EOF
#!/usr/bin/env bash
# Auto-installed by scripts/install-hooks.sh
REPO_ROOT="\$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
exec "\$REPO_ROOT/$hook_file" $extra_args "\$@"
EOF
  chmod +x "$target"
  echo "✓ Installed $hook_name -> $hook_file $extra_args"
}

install_hook "pre-push" "scripts/pre-push-secret-gate.sh" ""
install_hook "pre-commit" "scripts/pre-push-secret-gate.sh" "--pre-commit"
install_hook "pre-merge-commit" "scripts/pre-merge-review-gate.sh" "--git-merge"

echo "All git hooks successfully installed in $HOOKS_DIR."
