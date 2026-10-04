#!/usr/bin/env bash
# Proves the policy scripts can still go RED. A gate that silently passes
# everything (e.g. a regex typo that makes grep error out and skip a gate)
# looks exactly like a clean PR, so CI runs this alongside the gates.
set -uo pipefail
cd "$(dirname "$0")/.."
REPO_ROOT="$(pwd)"
t=$(mktemp -d); trap 'rm -rf "$t"' EXIT
pass=0 failn=0
expect() {  # expected-rc, description, command...
  local want=$1 what=$2; shift 2
  "$@" >"$t/out" 2>&1; local rc=$?
  if [ "$rc" -eq "$want" ]; then pass=$((pass + 1)); else failn=$((failn + 1)); echo "FAIL: $what (rc=$rc, want $want)"; sed 's/^/  | /' "$t/out"; fi
}

H=1111111111111111111111111111111111111111
gate() { scripts/check-pr-gates.sh --body-file "$t/body" --head-sha "$H" --files-file "$t/files" "$@"; }
all_gates() {
  for g in code-review project-audit pr-hygiene-review ha-discovery-validate chart-lint live-test; do
    printf -- '- [x] `$%s` clean — merge gate @ %s\n' "$g" "${1:-111111111111}"
  done
}

# --- check-pr-gates.sh -------------------------------------------------------
printf 'README.md\n' > "$t/files"
all_gates > "$t/body";                                   expect 0 "all gates stamped at head" gate
all_gates 2222222 > "$t/body";                           expect 1 "stale stamp" gate
all_gates | sed 's/ @ .*//' > "$t/body";                 expect 1 "ticked without stamp" gate
all_gates | sed 's/\[x\]/[ ]/' > "$t/body";              expect 1 "unticked" gate
all_gates | sed 's/@ 111111111111/@ `111111111111`/' > "$t/body"; expect 1 "backticked stamp is no stamp" gate
{ echo '<!--'; all_gates; echo '-->'; } > "$t/body";     expect 1 "task lines inside an HTML comment do not count" gate
{ echo '```'; all_gates; echo '```'; } > "$t/body";      expect 1 "task lines inside a code fence do not count" gate
all_gates | grep -v chart-lint > "$t/body"
printf 'README.md\n' > "$t/files";                       expect 0 "chart-lint not required for docs-only" gate
printf 'chart/values.yaml\n' > "$t/files";               expect 1 "chart-lint required for chart/" gate
all_gates | grep -v live-test > "$t/body"
printf 'internal/pulse/ws.go\n' > "$t/files";            expect 1 "live-test required for internal/pulse" gate
all_gates | grep -v ha-discovery-validate > "$t/body"
printf 'internal/output/output.go\n' > "$t/files";       expect 1 "ha-discovery-validate required for internal/output" gate
: > "$t/files";                                          expect 2 "empty file list is an input error" gate
expect 2 "short head sha rejected" scripts/check-pr-gates.sh --body-file "$t/body" --head-sha 1111111 --files-file "$t/files"

# Renovate exemption: same repo + renovate/* + Renovate author + managed files only.
: > "$t/body"
printf 'chart/values.yaml\ndocker-compose.yml\n' > "$t/files"
meta() { printf '{"head":{"ref":"%s","repo":{"full_name":"%s"}},"base":{"repo":{"full_name":"0Bu/tibber-pulse-bot"}}}' "$1" "$2" > "$t/meta"; }
commits() { printf '[{"commit":{"author":{"email":"%s"},"committer":{"email":"%s"}}}]' "$1" "${2:-$1}" > "$t/commits"; }
rgate() { gate --meta-file "$t/meta" --commits-file "$t/commits"; }
meta renovate/x 0Bu/tibber-pulse-bot; commits bot@renovateapp.com; expect 0 "Renovate PR exempt" rgate
commits someone@users.noreply.github.com;                   expect 1 "hand-pushed commit on renovate/* not exempt" rgate
commits bot@renovateapp.com someone@users.noreply.github.com; expect 1 "Renovate commit amended by a person not exempt" rgate
commits bot@renovateapp.com; meta feature/x 0Bu/tibber-pulse-bot; expect 1 "non-renovate branch not exempt" rgate
meta renovate/x fork/tibber-pulse-bot;                       expect 1 "fork not exempt" rgate
meta renovate/x 0Bu/tibber-pulse-bot; printf 'internal/sml/parse.go\n' >> "$t/files"; expect 1 "Renovate PR touching code not exempt" rgate

# --- check-pr-hygiene.sh -----------------------------------------------------
# Fixtures are assembled at runtime so this file's own diff stays clean under
# the very check it tests.
at=@ dash=- key="PRIVATE KEY"
pw="A1B2${dash}C3D4"
hyg() { printf '%s\n' "$1" > "$t/text"; scripts/check-pr-hygiene.sh --text "$t/text" --diff /dev/null; }
expect 0 "clean English text"               hyg "fix: handle /ws 404 by falling back to poll (LGZ-81199038, v1.0.40)"
expect 0 "noreply trailer ignored"          hyg "Co-Authored-By: Someone <someone@example.com>"
expect 0 "password placeholder allowed"     hyg "TIBBER_PULSE_PASSWORD=XXXX-XXXX"
expect 1 "personal email"                   hyg "ping me at jane.doe${at}gmail.com"
expect 1 "phone number"                     hyg "call +49 170 12${dash}34567"
expect 1 "bridge-password-shaped token"     hyg "sticker says $pw"
expect 1 "private key"                      hyg "-----BEGIN OPENSSH $key-----"
expect 1 "German prose"                     hyg "Das ist nicht gut und wird entfernt"
printf '%s\n' "$pw" > "$t/diff"
expect 1 "password shape in diff" scripts/check-pr-hygiene.sh --text /dev/null --diff "$t/diff"
expect 0 "hygiene diagnostics redact detected values" bash -c '! grep -qF "$1" "$2"' _ "$pw" "$t/out"
expect 2 "hygiene rejects unreadable input" scripts/check-pr-hygiene.sh --text "$t/missing" --diff /dev/null
expect 2 "hygiene rejects a missing option value" scripts/check-pr-hygiene.sh --text

# --- install-hooks.sh --------------------------------------------------------
repo_t="$t/hook_repo"
mkdir -p "$repo_t"
(
  cd "$repo_t"
  git init -q
  git config core.hooksPath .custom-hooks
  "$REPO_ROOT/scripts/install-hooks.sh" >/dev/null 2>&1
)
expect 0 "install-hooks respects core.hooksPath" test -x "$repo_t/.custom-hooks/pre-push"
expect 0 "install-hooks installs pre-commit" test -x "$repo_t/.custom-hooks/pre-commit"
expect 0 "install-hooks installs pre-merge-commit" test -x "$repo_t/.custom-hooks/pre-merge-commit"

# --- pre-push-secret-gate.sh -------------------------------------------------
echo '{"name": "run_command", "arguments": {"CommandLine": "echo hello"}}' > "$t/tool_non_push"
expect 0 "non-push tool call passes through" scripts/pre-push-secret-gate.sh < "$t/tool_non_push"

(
  cd "$repo_t"
  touch .env.production
  git add -f .env.production 2>/dev/null
)
test_pre_commit_env() {
  (
    cd "$repo_t"
    "$REPO_ROOT/scripts/pre-push-secret-gate.sh" --pre-commit
  )
}
expect 2 "pre-commit blocks staged .env" test_pre_commit_env
(
  cd "$repo_t"
  git rm -f --cached .env.production >/dev/null 2>&1
  rm -f .env.production
)

sec_repo="$t/sec_repo"
mkdir -p "$sec_repo"
(
  cd "$sec_repo"
  git init -q
  git config user.email "test@example.com"
  git config user.name "Test"
  git config commit.gpgsign false
  git commit -q --allow-empty -m "initial"
  git branch -M main
  git checkout -q -b feat
  k="TIBBER"
  p="PASSWORD"
  v="secret1234"
  printf '%s_PULSE_%s=%s\n' "$k" "$p" "$v" > config.txt
  git add config.txt
  git commit -q -m "add secret"
  printf '%s_PULSE_%s=\n' "$k" "$p" > config.txt
  git add config.txt
  git commit -q -m "delete secret"
  git checkout -q main
)
feat_sha=$(git -C "$sec_repo" rev-parse feat)
printf 'refs/heads/feat %s refs/heads/feat 0000000000000000000000000000000000000000\n' "$feat_sha" > "$t/push_input"
test_pre_push_leak() {
  (
    cd "$sec_repo"
    "$REPO_ROOT/scripts/pre-push-secret-gate.sh" < "$t/push_input"
  )
}
expect 2 "pre-push catches secret deleted in later commit" test_pre_push_leak

# --- pre-merge-review-gate.sh ------------------------------------------------
echo '{"tool_name": "bash", "tool_input": {"command": "git -C . merge feature"}}' > "$t/merge_unapproved"
expect 2 "blocks git merge without marker" scripts/pre-merge-review-gate.sh < "$t/merge_unapproved"

mkdir -p "$sec_repo/.agents"
printf '%s\n' "1111111111111111111111111111111111111111" > "$sec_repo/.agents/.review-marker"
printf '{"name": "run_command", "arguments": {"CommandLine": "git merge feat"}}\n' > "$t/merge_wrong_sha"
test_merge_wrong_sha() {
  (
    cd "$sec_repo"
    "$REPO_ROOT/scripts/pre-merge-review-gate.sh" < "$t/merge_wrong_sha"
  )
}
expect 2 "blocks git merge when marker SHA does not match" test_merge_wrong_sha

printf '%s\n' "$feat_sha" > "$sec_repo/.agents/.review-marker"
test_merge_matching_sha() {
  (
    cd "$sec_repo"
    "$REPO_ROOT/scripts/pre-merge-review-gate.sh" < "$t/merge_wrong_sha"
  )
}
expect 0 "allows git merge when marker SHA matches target" test_merge_matching_sha

# Regression fixtures use their own Git configuration and never install hooks
# in the user's checkout. Sensitive-looking strings are assembled at runtime.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
k=TIBBER p=PASSWORD v=syntheticsecret
fixture_init() {
  mkdir -p "$1"
  git -C "$1" init -q
  git -C "$1" config user.email fixture@example.com
  git -C "$1" config user.name Fixture
  git -C "$1" config commit.gpgsign false
  if [ "${2:-}" != root ]; then
    git -C "$1" commit -q --allow-empty -m baseline
    git -C "$1" branch -M main
    git -C "$1" update-ref refs/remotes/origin/main HEAD
  fi
}
fixture_commit() { git -C "$1" add -A && git -C "$1" commit -qm "$2"; }
secret_line() { printf '%s_PULSE_%s=%s\n' "$k" "$p" "$v"; }
scan_fixture() { (cd "$1" && "$REPO_ROOT/scripts/pre-push-secret-gate.sh" "${2:---scan}"); }
merge_command() {
  jq -n --arg cmd "$2" '{tool_name:"Bash",tool_input:{command:$cmd}}' > "$t/command"
  (cd "$1" && "$REPO_ROOT/scripts/pre-merge-review-gate.sh" < "$t/command")
}
push_fixture() {
  printf 'refs/heads/feat %s refs/heads/feat %s\n' "$2" "$3" > "$t/refs"
  (cd "$1" && "$REPO_ROOT/scripts/pre-push-secret-gate.sh" origin fixture < "$t/refs")
}
push_command() {
  jq -n --arg cmd "$2" '{tool_name:"Bash",tool_input:{command:$cmd}}' > "$t/command"
  (cd "$1" && "$REPO_ROOT/scripts/pre-push-secret-gate.sh" < "$t/command")
}
expect 2 "agent push inspects explicit source branch instead of checkout" push_command "$sec_repo" 'git push origin feat'
expect 2 "agent all-branches push inspects every branch" push_command "$sec_repo" 'git push --all origin'
expect 0 "explicit clean ref can be pushed from an unrelated checkout" push_command "$sec_repo" 'git push origin main:main'

root_repo="$t/root"
fixture_init "$root_repo" root
secret_line > "$root_repo/config.txt"
fixture_commit "$root_repo" root
expect 2 "root commit password is rejected" scan_fixture "$root_repo"

stage_repo="$t/staged"
fixture_init "$stage_repo"
secret_line > "$stage_repo/config.txt"
git -C "$stage_repo" add config.txt
expect 2 "manual scan includes staged credentials" scan_fixture "$stage_repo"
expect 2 "pre-commit rejects credential assignment" scan_fixture "$stage_repo" --pre-commit
git -C "$stage_repo" reset -q
expect 2 "manual scan includes working credentials" scan_fixture "$stage_repo"
printf '%s_PULSE_%s=\n' "$k" "$p" > "$stage_repo/config.txt"
expect 0 "empty credential assignment is allowed" scan_fixture "$stage_repo"
printf '%s_PULSE_%s=${CONFIGURED_PASSWORD}\n' "$k" "$p" > "$stage_repo/config.txt"
expect 0 "environment reference is allowed" scan_fixture "$stage_repo"
printf '%s_PULSE_%s=%s # example\n' "$k" "$p" "$v" > "$stage_repo/config.txt"
expect 2 "placeholder word in a comment cannot hide a credential" scan_fixture "$stage_repo"
printf '%s_PULSE_%s=%s # example=dummy\n' "$k" "$p" "$v" > "$stage_repo/config.txt"
expect 2 "placeholder assignment in a comment cannot hide a credential" scan_fixture "$stage_repo"
printf '%s_PULSE_%s=%s # example=${CONFIGURED_PASSWORD}\n' "$k" "$p" "$v" > "$stage_repo/config.txt"
expect 2 "environment reference in a comment cannot hide a credential" scan_fixture "$stage_repo"
rm "$stage_repo/config.txt"
mkdir -p "$stage_repo/nested directory"
touch "$stage_repo/nested directory/.env.sample"
git -C "$stage_repo" add -f 'nested directory/.env.sample'
expect 2 "only env.example is exempt" scan_fixture "$stage_repo" --pre-commit
git -C "$stage_repo" reset -q
rm "$stage_repo/nested directory/.env.sample"
touch "$stage_repo/.env.example"
git -C "$stage_repo" add -f .env.example
expect 0 "env.example remains allowed" scan_fixture "$stage_repo" --pre-commit
git -C "$stage_repo" reset -q
touch "$stage_repo/signing.key"
git -C "$stage_repo" add signing.key
expect 2 "pre-commit rejects a key file" scan_fixture "$stage_repo" --pre-commit
git -C "$stage_repo" reset -q
rm "$stage_repo/signing.key"

resolution_repo="$t/resolution"
fixture_init "$resolution_repo"
git -C "$resolution_repo" checkout -qb feat
echo safe > "$resolution_repo/feature.txt"
fixture_commit "$resolution_repo" feature
incoming=$(git -C "$resolution_repo" rev-parse HEAD)
git -C "$resolution_repo" checkout -q main
git -C "$resolution_repo" merge --no-commit --no-ff feat >/dev/null 2>&1
secret_line > "$resolution_repo/config.txt"
fixture_commit "$resolution_repo" resolution
expect 2 "merge resolution credential is rejected" scan_fixture "$resolution_repo"
expect 2 "unknown remote commit fails closed" push_fixture "$stage_repo" "$(git -C "$stage_repo" rev-parse HEAD)" "$H"
printf 'invalid ref input\n' > "$t/refs"
expect 2 "malformed native hook input fails closed" bash -c 'cd "$1"; "$2" origin fixture < "$3"' _ "$stage_repo" "$REPO_ROOT/scripts/pre-push-secret-gate.sh" "$t/refs"

env_repo="$t/env-history"
fixture_init "$env_repo"
mkdir -p "$env_repo/nested directory"
touch "$env_repo/nested directory/.env.production"
fixture_commit "$env_repo" environment
git -C "$env_repo" rm -q 'nested directory/.env.production'
fixture_commit "$env_repo" cleanup
env_sha=$(git -C "$env_repo" rev-parse HEAD)
expect 2 "deleted nested env is still blocked in history" push_fixture "$env_repo" "$env_sha" 0000000000000000000000000000000000000000
git -C "$env_repo" tag -a fixture -m fixture "$env_sha"
expect 2 "annotated tag is dereferenced and inspected" push_fixture "$env_repo" "$(git -C "$env_repo" rev-parse fixture)" 0000000000000000000000000000000000000000

expect 2 "abort does not exempt a later merge" merge_command "$sec_repo" 'git merge --abort && git merge main'
expect 2 "every merge in a compound command is reviewed" merge_command "$sec_repo" 'git merge feat && git merge main'
expect 0 "abort-only command is allowed" merge_command "$sec_repo" 'git merge --abort'
expect 0 "quoted merge target is resolved" merge_command "$sec_repo" 'git -C "." merge "feat"'
expect 0 "merge message is not a target" merge_command "$sec_repo" 'git merge -m "message with spaces" feat'
expect 0 "merge message file is not a target" merge_command "$sec_repo" 'git merge -F "message file" feat'
expect 0 "echoed merge text is not executed" merge_command "$sec_repo" 'echo "git merge main"'
git -C "$sec_repo" branch --set-upstream-to=feat main >/dev/null
expect 0 "implicit upstream merge checks incoming commit" merge_command "$sec_repo" 'git merge'
expect 2 "unapproved implicit upstream merge is rejected" merge_command "$sec_repo" 'git merge --no-ff main'
expect 2 "unresolved merge target fails closed" merge_command "$sec_repo" 'git merge unavailable'
expect 2 "approval cannot be recorded for an unknown object" bash -c 'cd "$1"; "$2" --approve-local unavailable' _ "$sec_repo" "$REPO_ROOT/scripts/pre-merge-review-gate.sh"
expect 0 "quoted repository path survives git options" merge_command "$stage_repo" "git -C \"$sec_repo\" -c merge.ff=false merge feat"
expect 2 "cd context applies to every chained merge" merge_command "$stage_repo" "cd \"$sec_repo\" && git merge main"

chain_repo="$t/hook chain"
fixture_init "$chain_repo"
cp -R "$REPO_ROOT/scripts" "$chain_repo/scripts"
printf '.agents/.review-marker\n' > "$chain_repo/.gitignore"
fixture_commit "$chain_repo" scripts
git -C "$chain_repo" update-ref refs/remotes/origin/main HEAD
mkdir -p "$chain_repo/.git/hooks"
printf '#!/usr/bin/env bash\ncat > legacy-input\nexit 1\n' > "$chain_repo/.git/hooks/pre-push"
chmod +x "$chain_repo/.git/hooks/pre-push"
(cd "$chain_repo" && scripts/install-hooks.sh >/dev/null)
(cd "$chain_repo" && scripts/install-hooks.sh >/dev/null)
expect 0 "reinstall preserves a single previous-hook backup" test "$(find "$chain_repo/.git/hooks" -name 'pre-push.backup.*' | wc -l | tr -d '[:space:]')" -eq 1
git init --bare -q "$t/remote.git"
git -C "$chain_repo" remote add fixture "$t/remote.git"
expect 1 "existing pre-push hook remains active" git -C "$chain_repo" push -q fixture main
expect 0 "existing hook receives complete pre-push ref input" grep -qE '^refs/heads/main [0-9a-f]{40} refs/heads/main 0{40}$' "$chain_repo/legacy-input"
git -C "$chain_repo" checkout -qb feature
echo safe > "$chain_repo/feature.txt"
fixture_commit "$chain_repo" feature
chain_incoming=$(git -C "$chain_repo" rev-parse HEAD)
git -C "$chain_repo" checkout -q main
expect 1 "native merge commit rejects an unapproved incoming commit" git -C "$chain_repo" merge --no-ff feature -m merge
expect 1 "later commit cannot bypass an unapproved no-commit merge" git -C "$chain_repo" commit -qm merge
expect 0 "explicit incoming commit can be approved" bash -c 'cd "$1"; scripts/pre-merge-review-gate.sh --approve-local "$2"' _ "$chain_repo" "$chain_incoming"
expect 0 "native merge commit accepts the approved incoming commit" git -C "$chain_repo" commit -qm merge
git -C "$chain_repo" checkout -qb feature-two
echo safe > "$chain_repo/feature-two.txt"
fixture_commit "$chain_repo" feature
chain_incoming=$(git -C "$chain_repo" rev-parse HEAD)
git -C "$chain_repo" checkout -q main
(cd "$chain_repo" && scripts/pre-merge-review-gate.sh --approve-local "$chain_incoming" >/dev/null)
expect 0 "native automatic merge accepts a pre-approved incoming commit" git -C "$chain_repo" merge --no-ff feature-two -m merge

api_dir="$t/api"
mkdir -p "$api_dir"
all_gates > "$t/api-body"
jq -n --rawfile body "$t/api-body" --arg head "$H" '{head:{sha:$head},body:$body,changed_files:1,commits:1}' > "$t/api-pr"
printf '[{"filename":"README.md"}]\n' > "$t/api-files"
printf '[{"commit":{"author":{"email":"fixture@example.com"},"committer":{"email":"fixture@example.com"}}}]\n' > "$t/api-commits"
cat > "$api_dir/curl" <<'EOF'
#!/usr/bin/env bash
url="${@: -1}"
printf '%s\n' "$url" >> "$POLICY_FIXTURE/api-urls"
case "$url" in
  */files\?*) cat "$POLICY_FIXTURE/api-files" ;;
  */commits\?*) cat "$POLICY_FIXTURE/api-commits" ;;
  *) cat "$POLICY_FIXTURE/api-pr" ;;
esac
EOF
chmod +x "$api_dir/curl"
api_merge() {
  POLICY_FIXTURE="$t" GH_TOKEN=fixture PATH="$api_dir:$PATH" scripts/pre-merge-review-gate.sh < "$t/api-input"
}
jq -n '{tool_name:"mcp__codex_apps__github_merge_pull_request",tool_input:{repository_full_name:"other/project",pr_number:42}}' > "$t/api-input"
expect 0 "current connector PR argument schema is supported" api_merge
expect 0 "connector repository is honored" grep -q '/repos/other/project/pulls/42$' "$t/api-urls"
jq -n '{tool_name:"mcp__codex_apps__github_enable_auto_merge",tool_input:{repository_full_name:"other/project",pr_number:42}}' > "$t/api-input"
expect 0 "current auto-merge tool schema is supported" api_merge
jq -n '{tool_name:"Bash",tool_input:{command:"gh -R other/project pr merge 42 --squash"}}' > "$t/api-input"
expect 0 "gh repository option before pr merge is honored" api_merge
jq -n '{tool_name:"Bash",tool_input:{command:"gh pr merge 42 --repo=other/project --match-head-commit=2222222222222222222222222222222222222222"}}' > "$t/api-input"
expect 2 "gh match-head-commit is checked against live head" api_merge
jq -n --arg sha 2222222222222222222222222222222222222222 '{tool_name:"mcp__codex_apps__github_merge_pull_request",tool_input:{repository_full_name:"other/project",pr_number:42,expected_head_sha:$sha}}' > "$t/api-input"
expect 2 "requested head cannot differ from fetched PR head" api_merge
all_gates 2222222 > "$t/api-body"
jq -n --rawfile body "$t/api-body" --arg head "$H" '{head:{sha:$head},body:$body,changed_files:1,commits:1}' > "$t/api-pr"
jq -n '{tool_name:"mcp__codex_apps__github_merge_pull_request",tool_input:{repository_full_name:"other/project",pr_number:42}}' > "$t/api-input"
expect 2 "live PR review stamps must match live head" api_merge
all_gates > "$t/api-body"
jq -n --rawfile body "$t/api-body" --arg head "$H" '{head:{sha:$head},body:$body,changed_files:1,commits:2}' > "$t/api-pr"
expect 2 "partial commit page cannot grant a merge" api_merge

# --- check-drift.sh ----------------------------------------------------------
expect 0 "repository tree has no drift" scripts/check-drift.sh

echo "selftest-policy: $pass passed, $failn failed"
[ "$failn" -eq 0 ]
