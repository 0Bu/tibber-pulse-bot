---
name: verify
description: Run the tibber-pulse-bot verification protocol — gofmt, go vet, go test, then guide the end-to-end bridge/MQTT smoke tests and the Helm render. Use before reporting a bridge / SML / output change as done.
disable-model-invocation: true
---

# verify

Runs the project's Verification protocol (see [CLAUDE.md](../../../CLAUDE.md) ›
"Verification protocol"). The static gates are CI-gated and run automatically;
the end-to-end steps need a real bridge + broker and are run on request.

## 1. Static gates (always run — all must pass)

```bash
test -z "$(gofmt -l .)" && echo "gofmt: clean" || { echo "gofmt: FAIL"; gofmt -l .; }
go vet ./...
go test ./...
scripts/check-drift.sh
```

These are required checks on `main` (the Stop hook also runs them before a
turn ends with changes). Stop and fix before going further if
any fail.

## 2. Helm render (run for chart / output changes)

`helm lint` plus a template render of at least one password mode:

```bash
helm lint chart
helm template r chart \
  --set pulse.host=192.0.2.10 \
  --set mqtt.host=mosquitto.default.svc.cluster.local \
  --set pulse.password=dummy-9char
```

Expect zero errors. For the other two password modes swap the last `--set` for
`--set pulse.sealedSecret.encryptedPassword=...` or `--set pulse.existingSecret=...`.
For a broader matrix (negative tests, `homeAssistant.expireAfter`), use the
`chart-lint` skill.

## 3. End-to-end (run for bridge / SML / WS / output changes)

These need the real bridge IP and the 9-char sticker password — **never commit
them**. Ask the operator for `<ip>` and `<pw>`, or read `$TIBBER_PULSE_PASSWORD`.

**a. Stdout smoke test** — expect ≥ 5 readings per frame, including `meter_serial`:

```bash
go build -o tibber-pulse-bot ./cmd/tibber-pulse-bot
./tibber-pulse-bot --pulse-host <ip> --pulse-password <pw>
```

Watch the startup log: `switching to poll mode` means push mode fell back to
polling (bridge 404s on `/ws`, or `/ws` delivered nothing) — report that, since
the push path then went untested.

**b. MQTT round-trip** — run the bot against the broker in one shell, subscribe
in another, and confirm:
- `tibber/pulse/readings` carries `power_total` every ~2–4 s,
- `tibber/pulse/diagnostics` arrives every `--metrics-interval` (60 s default),
- `tibber/pulse/status` is retained `online` while running and flips to
  `offline` after Ctrl-C (clean shutdown) — the same payload is the LWT on a crash.

```bash
./tibber-pulse-bot --pulse-host <ip> --pulse-password <pw> --mqtt-host <broker>
# separate shell:
mosquitto_sub -h <broker> -t 'tibber/pulse/#' -v
```

## Reporting

State which steps ran and their results. Do **not** claim "verified" if the
end-to-end steps were skipped — say so explicitly.
