---
name: release
description: Ship a new tibber-pulse-bot release. Releases are automatic — release.yml patch-bumps and tags on every code merge to main. Use this to drive and verify that pipeline, or to cut a manual minor/major tag when a patch bump isn't enough.
disable-model-invocation: true
---

# release

Releases are **automated** — for the normal case you do not tag by hand (see
[CLAUDE.md](../../../CLAUDE.md) › "Build-time version injection" and
[.github/workflows/release.yml](../../../.github/workflows/release.yml)).

## The automated flow (normal patch release)

1. Merge a code change to `main`. `release.yml` triggers on pushes that touch
   `**.go`, `go.mod`, `go.sum`, or `Dockerfile` — doc / chart / workflow-only
   changes do **not** release.
2. `release.yml` reads the latest `vX.Y.Z` tag, bumps the **patch**
   (`X.Y.(Z+1)`, or `v0.1.0` if no tag exists yet), and pushes that tag as
   `github-actions[bot]`.
3. It dispatches `docker.yml` at the new tag, which runs govulncheck, builds
   `linux/amd64,linux/arm64`, and pushes **exactly one** image tag `:X.Y.Z` to
   GHCR — no `:latest`, no floating `:X` / `:X.Y`, no `:main`, no bare `:<sha>`.
4. `docker.yml` then dispatches `renovate.yaml`, which opens a PR bumping the
   chart (`values.yaml` tag + `Chart.yaml`), `docker-compose.yml`, and the
   pinned digest in the docs to the new `X.Y.Z@sha256:<digest>`. Those bumps are
   path-filtered out of the release trigger, so they never start another build.

**So to ship a patch release: merge the code PR to `main`** and let the pipeline
run. Then verify and merge the Renovate PR.

## Verify a release

```bash
gh run list --workflow=release.yml --limit 3
gh run list --workflow=docker.yml  --limit 3
gh run watch                                  # follow the in-flight build
git fetch --tags origin && git tag --list 'v*' | sort -V | tail -3
```

Confirm the Renovate PR lands, review the new version + digest, and merge it.
Consumers pin the immutable digest `:X.Y.Z@sha256:<digest>`.

## Manual override — minor / major bump

`release.yml` only auto-bumps the **patch** number. To cut a minor or major
release, push the tag yourself from a clean, green `main` — `docker.yml` builds
any `v*.*.*` tag directly (and dispatches Renovate), while `release.yml` ignores
tag pushes, so there's no double-build:

```bash
git fetch --tags origin
git tag --list 'v*' | sort -V | tail -5        # current version
git status --porcelain                         # must be empty
VERSION=vX.Y.0                                 # the minor/major you want
git tag "$VERSION"
git push origin "$VERSION"
```

The next code merge then patch-bumps from your new baseline
(`vX.Y.0` → `vX.Y.1`). Don't also hand-run `docker.yml` — pushing the tag is
enough.
