---
name: project-audit
description: Audit tibber-pulse-bot for internal inconsistencies and documentation drift — CLI flags vs docs, image version/digest sync across chart/compose/README, module-path consistency, CLAUDE.md path references, env-var and chart-values parity, and code defaults vs documented defaults. Use before a PR merge (the pre-merge review gate expects it) or whenever you want to confirm docs still match the code. Read-only: reports drift, never edits.
---

# project-audit

Find places where the project has drifted out of sync with itself — the
inconsistencies that compile and pass tests but mislead a reader or an
operator. **Read-only: report each drift as `file:line`, propose the fix, do
not edit.** The static gates and `TestObisNamesHaveDiscoverySpecs` already
cover code-level invariants; this covers the cross-file / doc ones they can't.

Work through every check. Report `OK` for the ones that pass so the reader
knows the audit had teeth.

## 0. Mechanical subset first

```bash
scripts/check-drift.sh
```

It enforces checks 1 (flag names), 2 (image pins), 4 (paths), 5 (compose ↔
`.env.example`), 6 (template values documented), the skill registry and cited
Go test names — and runs in CI and the Stop hook. Fix any `DRIFT:` line first;
the checks below then cover what a script can't judge (defaults, wording,
behavioural claims).

## 1. CLI flags vs documentation

Flags are the contract. Every flag in `cmd/tibber-pulse-bot/main.go` should be
reflected in the docs, and no doc should mention a flag that no longer exists.

```bash
grep -nE 'flag\.(String|Bool|Int|Duration)\(' cmd/tibber-pulse-bot/main.go
```

Cross-check each `--flag` and its **default** against `README.md`, `CLAUDE.md`,
and the chart (`chart/values.yaml` + `chart/templates/deployment.yaml` args).
Flag added but undocumented, flag removed but still documented, or a default
that disagrees (current defaults: `--reconnect-delay` 100 ms,
`--ws-idle-timeout` 60 s, `--interval` 10 s, `--metrics-interval` 60 s,
`--mode` push, `--expire-after` 0 = auto) → drift. The chart must expose every
operator-facing flag as a value (e.g. `--expire-after` ↔
`homeAssistant.expireAfter`) and `chart/README.md`'s values table must list it.

## 2. Image version + digest sync

Renovate bumps these together after a release; a mismatch means a partial or
failed Renovate PR. All four must reference the **same** `X.Y.Z@sha256:<digest>`:

```bash
grep -rn 'ghcr.io/0bu/tibber-pulse-bot:[0-9]' README.md docker-compose.yml
grep -n 'tag:' chart/values.yaml
grep -n 'appVersion:' chart/Chart.yaml
```

`values.yaml` `image.tag`, `Chart.yaml` `appVersion`, `docker-compose.yml`
image, and the README pinned image should all match. (`Chart.yaml` `version`
is the chart's own semver and is expected to differ.)

## 3. Module path / GitHub handle consistency

```bash
head -1 go.mod
grep -rn '0Bu\|0bu' go.mod chart/values.yaml docker-compose.yml README.md chart/README.md
```

The Go module (`github.com/0Bu/tibber-pulse-bot`) and every import must use the
same handle. Image references are deliberately lowercase (`ghcr.io/0bu/…` —
GHCR rejects mixed case); flag any *other* mixed/loweredcase inconsistency,
and confirm README/chart links point at `github.com/0Bu/…`.

## 4. CLAUDE.md path references exist

Every repo-relative path CLAUDE.md points at should resolve:

```bash
grep -oE '(cmd|internal|chart)/[A-Za-z0-9._/-]+' CLAUDE.md | sort -u \
  | grep -vE '^(chart/charts/|dist/)' \
  | while read -r p; do
      [ -e "$p" ] || echo "MISSING: $p referenced in CLAUDE.md"
    done
```

A missing path means a file was moved/renamed without updating the docs.
`chart/charts/` and `dist/` are excluded — they're gitignored build outputs
CLAUDE.md mentions by name but that don't exist until a build runs; their
absence is expected, not drift.

## 5. Env-var parity (.env.example ↔ docker-compose ↔ chart)

```bash
grep -oE '^[A-Z0-9_]+=' .env.example | tr -d '='
grep -oE '\$\{[A-Z0-9_]+' docker-compose.yml | tr -d '${'
```

Every variable `docker-compose.yml` requires (`${VAR:?...}`) should exist in
`.env.example`, and vice versa. `TIBBER_PULSE_PASSWORD` must be present in
`.env.example` as a placeholder (never a real value) and be the Secret key the
chart uses (`chart/templates/*secret*.yaml`).

## 6. Chart values ↔ template usage

Flag values defined in `chart/values.yaml` that no template references (dead
knob), and `.Values.*` used in `chart/templates/` that aren't documented in
`chart/README.md`'s values table.

```bash
grep -roE '\.Values\.[A-Za-z0-9._]+' chart/templates/ | sed 's/.*\.Values\.//' | sort -u
```

## 7. Discovery ↔ OBIS parity (cross-check)

`TestObisNamesHaveDiscoverySpecs` enforces this in CI, so it should already be
green — but confirm the `discovery.Sensors` keys and `obisNames` numeric values
still line up, and that `CLAUDE.md`'s claim "obisNames already covers the
extended set" matches the actual map.

## 8. MQTT topic set vs docs

The sink publishes exactly three state topics under `<topic-prefix>`:
`readings` and `diagnostics` (not retained) and `status` (retained
`online`/`offline`, also the MQTT Last Will). Confirm README "MQTT topics",
CLAUDE.md "MQTT topic naming", and AGENTS.md's architecture diagram list the
same set, and that no doc still claims "exactly two" topics:

```bash
grep -nE 'availabilityTopic|SetWill|"/readings"|"/diagnostics"|"/status"' internal/output/output.go
grep -nE 'topic-prefix>/|<prefix>/|exactly two' README.md CLAUDE.md AGENTS.md
```

## 9. Bridge endpoint names

Code prefers the modern `/node_data.json` / `/node_metrics.json` and falls back
to legacy `/data.json` / `/metrics.json` (`internal/pulse/client.go`,
`internal/pulse/metrics.go`). Docs and skills that show a curl or name an
endpoint should mention the modern one first:

```bash
grep -rnE '/(node_)?(data|metrics)\.json' README.md CLAUDE.md AGENTS.md .claude/skills
```

A lone `/data.json` or `/metrics.json` without the modern counterpart → drift.

## 10. README behavioural claims vs code

Spot-check that headline claims still hold: default acquisition mode (`push`)
and its automatic fall-back to poll (on `/ws` 404, or no WS frame while HTTP
works),
the `--mqtt-host` present/absent stdout behaviour, and the "no `:latest`,
single `:X.Y.Z` tag" release claim vs `.github/workflows/docker.yml`. Also
confirm no doc pins a toolchain / base-image version that Renovate bumps
elsewhere (e.g. a `golang:1.NN-alpine` tag outside the Dockerfile).

## Reporting

Emit a short report grouped as **drift** (needs a fix, with `file:line` and the
one-line correction) and **OK** (checks that passed). If everything is clean,
say so plainly and list the checks run. This skill only reports — the operator
or a follow-up change applies the fixes.

## Recording the merge gate

When the audit is clean on the PR head, tick its line in the PR body's
**Merge gates** section with the bare head SHA (`git rev-parse --short=12 HEAD`):

```
- [x] `$project-audit` clean — merge gate @ 1a2b3c4d5e6f
```

Don't tick it while drift remains. Any later push re-stales the stamp.
