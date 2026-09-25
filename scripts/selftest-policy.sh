#!/usr/bin/env bash
# Proves the policy scripts can still go RED. A gate that silently passes
# everything (e.g. a regex typo that makes grep error out and skip a gate)
# looks exactly like a clean PR, so CI runs this alongside the gates.
set -uo pipefail
cd "$(dirname "$0")/.."
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

# --- check-drift.sh ----------------------------------------------------------
expect 0 "repository tree has no drift" scripts/check-drift.sh

echo "selftest-policy: $pass passed, $failn failed"
[ "$failn" -eq 0 ]
