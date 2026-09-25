<!--
Keep it honest: say what was actually verified, and what could not be (a cloud
session has no route to the bridge, so live-test needs the maintainer's LAN).
Only real, column-zero "- [x]" lines count as gate records — lines inside this
comment or in code fences are ignored by scripts/check-pr-gates.sh.
-->

## Summary

<!-- What changed and why, in 1-3 sentences. -->

## Changes

-

## Verification

- [ ] `gofmt -l .` empty, `go vet ./...`, `go test ./...` pass (CI `test`)
- [ ] `scripts/check-drift.sh` clean (CI `test`)
- [ ] `scripts/check-pr-hygiene.sh` clean (pre-push hook; CI `pr-policy` also checks this PR's text)
- [ ] `helm lint chart` + render of the password modes (CI `helm`) — only if `chart/` changed
- [ ] End-to-end against the real bridge / broker, or why not (CLAUDE.md > Verification protocol)

## Merge gates

<!-- Required by the `pr-policy` check and the Claude pre-merge hook. Tick a gate
     only after running it on the CURRENT head, and stamp it with a BARE sha:
     `git rev-parse --short=12 HEAD`. Wrapping the sha in backticks reads as no
     stamp. Any new push re-stales every stamp. Delete lines whose condition does
     not apply; a required line that is missing fails the check. Renovate PRs
     touching only Renovate-managed files need no records. -->

- [ ] `$code-review` clean — merge gate @ <sha> (always: /code-review, no blocking finding)
- [ ] `$project-audit` clean — merge gate @ <sha> (always: no doc drift, beyond what check-drift.sh sees)
- [ ] `$pr-hygiene-review` clean — merge gate @ <sha> (always: commits, PR text and diff free of personal data and secrets, English)
- [ ] `$ha-discovery-validate` clean — merge gate @ <sha> (if `internal/discovery|output|sml/` or `cmd/tibber-pulse-bot/` changed)
- [ ] `$chart-lint` clean — merge gate @ <sha> (if `chart/` changed)
- [ ] `$live-test` clean — merge gate @ <sha> (if `internal/pulse/` or `internal/sml/` changed: run against the real bridge)
