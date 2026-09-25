#!/usr/bin/env bash
# Mechanical doc-drift gate: the subset of the project-audit skill that has a
# reliable shape, so CI (test.yml) and the Stop hook can enforce it instead of
# relying on someone remembering to run the audit. Judgement calls (is the
# README claim still *true*?) stay in the project-audit skill.
#
# Exit: 0 = clean, 1 = drift found.
set -uo pipefail
cd "$(dirname "$0")/.."

fail=0
drift() { echo "DRIFT: $*"; fail=1; }
ok() { echo "ok:    $*"; }

# 1. CLI flags <-> README flag table, both directions.
code_flags=$(grep -oE 'flag\.(String|Bool|Int|Duration)\("[a-z0-9-]+"' cmd/tibber-pulse-bot/main.go \
  | sed -E 's/.*\("//; s/"$//' | sort -u)
doc_flags=$(grep -oE '^\| `-{1,2}[a-z0-9-]+`' README.md | sed -E 's/^\| `-{1,2}//; s/`$//' | sort -u)
n=0
for f in $code_flags; do
  grep -qx -- "$f" <<<"$doc_flags" || { drift "flag --$f (cmd/tibber-pulse-bot/main.go) missing from README.md 'CLI flags' table"; n=1; }
done
for f in $doc_flags; do
  grep -qx -- "$f" <<<"$code_flags" || { drift "README.md documents --$f, which cmd/tibber-pulse-bot/main.go no longer defines"; n=1; }
done
[ "$n" = 0 ] && ok "CLI flags match README ($(wc -w <<<"$code_flags") flags)"

# 2. Every .Values path a chart template reads is documented in chart/README.md.
n=0
for v in $(grep -rhoE '\.Values\.[A-Za-z0-9_.]+' chart/templates/ | sed 's/^\.Values\.//' | sort -u); do
  # A documented parent (e.g. `resources`) covers its children; documented
  # children cover a parent read as a whole (e.g. `pulse.sealedSecret`).
  covered=0 p="$v"
  grep -qF "| \`$v." chart/README.md && covered=1
  while [ -n "$p" ]; do
    grep -qF "| \`$p\`" chart/README.md && { covered=1; break; }
    [ "${p%.*}" = "$p" ] && break
    p="${p%.*}"
  done
  [ "$covered" = 1 ] || { drift "chart value '$v' is used in chart/templates but not in chart/README.md's values table"; n=1; }
done
[ "$n" = 0 ] && ok "chart template values documented in chart/README.md"

# 3. One pinned image version+digest everywhere; Chart.yaml appVersion agrees.
pins=$(grep -hoE '[0-9]+\.[0-9]+\.[0-9]+@sha256:[a-f0-9]{64}' chart/values.yaml docker-compose.yml README.md CLAUDE.md AGENTS.md 2>/dev/null | sort -u)
app=$(sed -nE 's/^appVersion:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/p' chart/Chart.yaml)
if [ "$(grep -c . <<<"$pins")" -ne 1 ]; then
  drift "image pins disagree across chart/values.yaml, docker-compose.yml, README.md: $(tr '\n' ' ' <<<"$pins")"
elif [ "${pins%%@*}" != "$app" ]; then
  drift "chart/Chart.yaml appVersion $app != pinned image ${pins%%@*}"
else
  ok "image pin ${pins%%@*} consistent (appVersion $app)"
fi

# 4. Repo paths named in CLAUDE.md / AGENTS.md exist (build outputs excluded).
n=0
for doc in CLAUDE.md AGENTS.md; do
  for p in $(grep -oE '(cmd|internal|chart|scripts|\.claude|\.github)/[A-Za-z0-9._/-]*[A-Za-z0-9_]' "$doc" | sort -u); do
    case "$p" in chart/charts*|dist*) continue ;; esac
    [ -e "$p" ] || { drift "$doc references missing path $p"; n=1; }
  done
done
[ "$n" = 0 ] && ok "paths referenced in CLAUDE.md / AGENTS.md exist"

# 5. Skill registry: directory name == frontmatter name, and every skill is
#    listed in CLAUDE.md "Quality tooling" and AGENTS.md "Agent Skills".
n=0
for d in .claude/skills/*/; do
  s=$(basename "$d")
  [ -f "$d/SKILL.md" ] || { drift "$d has no SKILL.md"; n=1; continue; }
  name=$(sed -nE '2,5s/^name:[[:space:]]*//p' "$d/SKILL.md" | head -1)
  [ "$name" = "$s" ] || { drift "$d/SKILL.md frontmatter name '$name' != directory '$s'"; n=1; }
  grep -qF "\`$s\`" CLAUDE.md || { drift "skill '$s' not listed in CLAUDE.md > Quality tooling"; n=1; }
  grep -qF "\`$s\`" AGENTS.md || { drift "skill '$s' not listed in AGENTS.md > Agent Skills"; n=1; }
done
[ "$n" = 0 ] && ok "skills registry consistent ($(ls -d .claude/skills/*/ | wc -l) skills)"

# 6. Go test names cited by skills / agents / docs still exist (prefix match,
#    as `go test -run` does).
tests=$(grep -rhoE '^func (Test[A-Za-z0-9_]+)' --include='*_test.go' . | sed 's/^func //' | sort -u)
n=0
for t in $(grep -rhoE '\bTest[A-Z][A-Za-z0-9_]*' .claude CLAUDE.md AGENTS.md | sort -u); do
  grep -q "^$t" <<<"$tests" || { drift "test '$t' cited in .claude/ or docs matches no Go test"; n=1; }
done
[ "$n" = 0 ] && ok "Go test names cited in skills/docs exist"

# 7. Every variable docker-compose.yml requires exists in .env.example.
n=0
for v in $(grep -oE '\$\{[A-Z0-9_]+:\?' docker-compose.yml | sed -E 's/^\$\{//; s/:\?$//' | sort -u); do
  grep -qE "^$v=" .env.example || { drift "docker-compose.yml requires \$$v but .env.example lacks it"; n=1; }
done
[ "$n" = 0 ] && ok "docker-compose required vars present in .env.example"

if [ "$fail" -ne 0 ]; then
  echo "check-drift: FAILED — fix the docs (or code) above; see the project-audit skill for context." >&2
  exit 1
fi
echo "check-drift: clean"
