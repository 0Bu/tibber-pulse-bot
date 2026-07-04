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

## 1. CLI flags vs documentation

Flags are the contract. Every flag in `cmd/tibber-pulse-bot/main.go` should be
reflected in the docs, and no doc should mention a flag that no longer exists.

```bash
grep -nE 'flag\.(String|Bool|Int|Duration)\(' cmd/tibber-pulse-bot/main.go
```

Cross-check each `--flag` and its **default** against `README.md`, `CLAUDE.md`,
and the chart (`chart/values.yaml` + `chart/templates/deployment.yaml` args).
Flag added but undocumented, flag removed but still documented, or a default
that disagrees (e.g. `--reconnect-delay` 1 s, `--interval` 10 s,
`--metrics-interval` 60 s, `--mode` push) → drift.

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

## 8. README behavioural claims vs code

Spot-check that headline claims still hold: default acquisition mode (`push`),
the `--mqtt-host` present/absent stdout behaviour, `status.json up_time` 10 ms
ticks note, and the "no `:latest`, single `:X.Y.Z` tag" release claim vs
`.github/workflows/docker.yml`.

## Reporting

Emit a short report grouped as **drift** (needs a fix, with `file:line` and the
one-line correction) and **OK** (checks that passed). If everything is clean,
say so plainly and list the checks run. This skill only reports — the operator
or a follow-up change applies the fixes.
