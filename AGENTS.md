# AGENTS.md — Agent & Developer Guide for `tibber-pulse-bot`

This document defines architecture, protocols, coding invariants, verification procedures, and skills for AI agents and human developers working in this repository.

---

## 1. Project Overview & Architecture

`tibber-pulse-bot` is a lightweight, robust Go service and container that decouples a local **Tibber Pulse Bridge** (via local HTTP polling or WebSocket push) and forwards electricity meter readings (SML 1.04 / OBIS) to an MQTT broker, with native **Home Assistant MQTT Discovery**.

### High-Level Architecture

```
┌──────────────────────────────────────────────┐
│             Tibber Pulse Bridge              │
│  - Push:    ws://<host>/ws                   │
│  - Poll:    http://<host>/data.json          │
│  - Health:  /metrics.json, /nodes.json, ...  │
└──────────────────────┬───────────────────────┘
                       │ SML 1.04 / HTTP
                       ▼
┌──────────────────────────────────────────────┐
│               tibber-pulse-bot               │
│  ├── cmd/tibber-pulse-bot/  (CLI / lifecycle)│
│  ├── internal/pulse/        (Bridge client)  │
│  ├── internal/sml/          (SML parser)     │
│  ├── internal/output/       (MQTT / Stdout)  │
│  └── internal/discovery/    (HA MQTT specs)  │
└──────────────────────┬───────────────────────┘
                       │ JSON topics
                       ▼
┌──────────────────────────────────────────────┐
│                 MQTT Broker                  │
│  ├── <prefix>/readings    (live state)       │
│  ├── <prefix>/diagnostics (health state)     │
│  └── <ha-prefix>/...      (retained config)  │
└──────────────────────────────────────────────┘
```

### Protocol Facts & Invariants

- **Bridge Credentials**: Authentication is always HTTP Basic Auth `admin:<password>` (9-character QR code sticker password).
- **Push Mode (`ws://<host>/ws`)**: Streams framed telegrams `<header>BODY`. Non-SML topics or bodies under 16 bytes are ignored. Reconnects quietly on EOF or idle timeout. Stops deterministically on permanent HTTP 401 (invalid password) or HTTP 404 (legacy bridge firmware).
- **Poll Mode (`http://<host>/data.json?node_id=N`)**: Polled at `--interval`. Returns binary SML 1.04, not JSON.
- **SML Framing**: Multiples of 4 bytes, starting with `1b 1b 1b 1b 01 01 01 01` and ending with `1b 1b 1b 1b 1a [pad] [crc16]`.
- **DIN 43863-5 FNN Server-ID**: Exactly 10 bytes starting with `0x0A 0x01`, followed by 3 uppercase ASCII characters for the manufacturer (e.g. `LGZ`, `EMH`, `ESY`, `ITZ`, `ISK`), generation byte, and 4-byte big-endian serial number. OBIS `1-0:96.1.0*255` is `server_id`; OBIS `1-0:0.0.9*255` is `device_id`.
- **Manufacturer Preservation**: An ASCII manufacturer name (`LGZ`) must never be overwritten by a raw hex string representation (`4c475a`).
- **Single Device Invariant**: Home Assistant MQTT Discovery groups both meter readings and bridge diagnostics under **one single device** with device identifier `dev.MeterSerial` (e.g. `LGZ-81199038`) and entity unique IDs prefixed with `tibber_pulse_<meter_serial>`.
- **Atomic Discovery Reservation**: Key discovery names are reserved under lock before publishing to prevent duplicate discovery messages during concurrent execution.

---

## 2. Directory Layout

```
.
├── cmd/tibber-pulse-bot/   # CLI entrypoint, flag parsing, signal handling
├── cmd/sml-inspect/        # SML 1.04 inspection and decoding CLI
├── internal/discovery/     # Home Assistant MQTT discovery specifications
├── internal/pulse/         # Bridge HTTP client (client.go) and WS client (ws.go)
├── internal/sml/           # SML parsing + OBIS-name mapping + serial decode
├── internal/output/        # Sink interface; StdoutSink, MQTTSink, TeeSink
├── chart/                  # Self-contained Helm chart
├── .agents/skills/         # Universal agent skills
├── .claude/skills/         # Claude Code skills & tooling
├── Dockerfile              # Multistage, distroless static, non-root
└── docker-compose.yml      # Local container orchestration
```

---

## 3. Security & Safety Rules

1. **Never Commit Secrets**:
   - Never commit `.env` or `.env.*` files (only `.env.example` is tracked).
   - Never commit raw bridge passwords, credentials, or private keys.
   - Always read credentials from environment variables (`$TIBBER_PULSE_PASSWORD`).
2. **Fail-Closed Helm Guardrails**:
   - The Helm chart requires the password to be configured via **exactly one** of: `pulse.password`, `pulse.sealedSecret.encryptedPassword`, or `pulse.existingSecret`.
   - The chart fails if zero or multiple password mechanisms are specified.
3. **Container Security**:
   - Containers run non-root (`UID/GID 65532`), with `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, and `drop: [ALL]` capabilities.

---

## 4. Development & Build Commands

### Build CLI

```bash
go build -o tibber-pulse-bot ./cmd/tibber-pulse-bot
```

### Version Injection

```bash
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
go build -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT}" -o tibber-pulse-bot ./cmd/tibber-pulse-bot
```

### Static Gates & Tests

```bash
# Code formatting
test -z "$(gofmt -l .)" && echo "gofmt: clean" || { echo "gofmt: FAIL"; gofmt -l .; }

# Go vet
go vet ./...

# Unit tests with coverage
go test -v -cover ./...

# Helm lint & template render
helm lint chart
helm template test-plain chart \
  --set pulse.host=192.168.1.50 \
  --set mqtt.host=broker.local \
  --set pulse.password=dummy1234
```

---

## 5. Agent Skills

Skills are available under `.agents/skills/` (and `.claude/skills/`):

| Skill | Purpose | Command / Scope |
|---|---|---|
| **`verify`** | Runs full verification protocol (gofmt, vet, test, helm render, e2e checks) | `go test ./...`, `helm lint` |
| **`project-audit`** | Audits documentation drift, CLI flags parity, image versions, and path references | Cross-file consistency checks |
| **`sml-inspect`** | Decodes binary SML 1.04 telegrams, hex dumps, and verifies OBIS mappings | SML framing, DIN 43863-5 FNN server-ID |
| **`bridge-diag`** | Queries and diagnoses Tibber Pulse Bridge endpoints safely | `/metrics.json`, `/nodes.json`, `/status.json`, `/ws` |
| **`chart-lint`** | Validates Helm chart across all 3 password modes, failure modes, and flags | Multi-mode `helm template` testing |
| **`security-scan`** | Audits codebase for vulnerabilities (`govulncheck`), credential leaks, and git isolation | Secret scanning, dependency checks |
| **`ha-discovery-validate`** | Validates Home Assistant MQTT Discovery specs and OBIS-sensor parity | `TestObisNamesHaveDiscoverySpecs` |
| **`live-test`** | Automated end-to-end test against real bridge hardware (`192.168.107.118`) | `run_live_test.sh`, REST, SML & WS |
| **`release`** | Automates patch and minor/major releases via GitHub Actions pipeline | Workflow dispatch, tag verification |

---

## 6. Code Style & Contribution Guidelines

1. **No premature abstractions**: Add code to existing packages (`cmd/...`, `internal/pulse|sml|output|discovery`). Do not introduce unnecessary layers or interfaces.
2. **Comment policy**: Only write comments explaining **WHY** something is done (e.g. non-obvious protocol quirks or hardware idiosyncrasies), never restate what the next line of code does.
3. **OBIS ↔ Discovery Parity**: When adding a new numeric OBIS code to `obisNames` in `internal/sml/parse.go`, always add a matching entry in `discovery.Sensors` in `internal/discovery/discovery.go`. String-valued readings (`server_id`, `device_id`, `manufacturer`, `meter_serial`) are handled as metadata and excluded from numerical sensors.
4. **Graceful Goroutine Shutdown**: Background goroutines (such as `runMetrics`) must be synchronized with a `sync.WaitGroup` before shutting down network sinks.
