# AGENTS.md — Agent & Developer Guide for `tibber-pulse-bot`

This document defines architecture, protocols, coding invariants, verification procedures, quality tooling, and agent skills for AI agents and human developers working in this repository.

---

## 1. Project Overview & Architecture

`tibber-pulse-bot` is a lightweight, robust Go service and container that decouples a local **Tibber Pulse Bridge** (via local HTTP polling or WebSocket push) and forwards electricity meter readings (SML 1.04 / OBIS) to an MQTT broker, with native **Home Assistant MQTT Discovery**. Designed to run as a single Deployment in a home k3s cluster ([rpi-k3s-cluster](https://github.com/0Bu/rpi-k3s-cluster)) and distributable as a standalone open-source service.

### High-Level Architecture

```
┌──────────────────────────────────────────────┐
│             Tibber Pulse Bridge              │
│  - Push:    ws://<host>/ws                   │
│  - Poll:    http://<host>/node_data.json     │
│             (or /data.json on older FW)      │
│  - Health:  /node_metrics.json, /nodes.json  │
└──────────────────────┬───────────────────────┘
                       │ SML 1.04 / HTTP
                       ▼
┌──────────────────────────────────────────────┐
│               tibber-pulse-bot               │
│  ├── cmd/tibber-pulse-bot/  (CLI / lifecycle)│
│  ├── cmd/sml-inspect/       (SML inspect CLI)│
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
│  ├── <prefix>/status      (retained LWT)     │
│  └── <ha-prefix>/...      (retained config)  │
└──────────────────────────────────────────────┘
```

### Scope and Non-Goals

- **Scope**: bridge → SML → MQTT (and stdout). One small binary, one container, one Helm chart.
- **Non-goals**: HACS custom component / direct integration (use [marq24/ha-tibber-pulse-local](https://github.com/marq24/ha-tibber-pulse-local) if you want a Home Assistant integration directly polling the bridge without MQTT), Tibber cloud GraphQL API, persistence, dashboards. Downstream consumers (Telegraf → InfluxDB, Node-RED, Grafana) live elsewhere.

### Bridge Protocol Facts & Invariants

- **Bridge Credentials**: Authentication is always HTTP Basic Auth `admin:<password>` (9-character QR code sticker password).
- **Push Endpoint (Live, Default)**: `ws://<bridge>/ws` with HTTP Basic auth header. Streams framed telegrams `<header>BODY`. Non-SML topics or bodies under 16 bytes are ignored. Reconnect-delay defaults to 100 ms (avoids dropped telegrams during routine bridge idle socket drops). EOF / abnormal-close errors are returned as `pulse.ErrPeerClosed` and logged silently unless `-v` is set. Stops deterministically on permanent HTTP 401 (invalid password). On HTTP 404 (legacy bridge firmware) or when `/ws` delivers no frame while HTTP polling works, it falls back to poll mode automatically.
- **Push Frame Format**: `<key:value key:"value" ...>BODY` — ASCII header (key/value pairs, values optionally quoted) followed by raw payload after the first `>` byte. For SML topics, the body is the same SML 1.04 binary the poll endpoint returns.
- **Poll Endpoint (Fallback)**: `http://<bridge>/node_data.json?node_id=N` or `http://<bridge>/data.json?node_id=N` at `--interval` (default 10 s) with HTTP Basic auth. Returns binary SML 1.04, **not** JSON despite the suffix. Modern firmware uses `/node_data.json`, older firmware uses `/data.json`; the client automatically detects and caches the working endpoint.
- **Diagnostics Endpoints**: Modern firmware uses `/node_metrics.json` with top-level `node`/`ir`/`hub` sections; legacy firmware uses `/metrics.json` with `node_status`/`hub_attachments`. The client seamlessly parses both schemas.
- **Bridge Prereq**: `webserver_force_enable` (param 39) must be `TRUE`, otherwise `/node_data.json`, `/data.json`, and `/ws` are dead. Set once via the bridge's AP-mode console.
- **Common Bridge Behavior**: Drops the WS connection every ~30–60 s with EOF (no Close frame). This is normal — reconnect silently with short backoff.
- **SPA UI Route Warning**: The path `http://<bridge>/nodes/1/data` is the **SPA UI route**, not the data API — it always returns the HTML shell. Do not point code at it.
- **`status.json` `up_time` Unit**: 10 ms FreeRTOS ticks (ESP-IDF 100 Hz default — measured 100.15 ticks/s on a live bridge), **not** milliseconds. Divide by 100 for seconds. In contrast, `metrics.json` `node_uptime_ms`, `nodes.json` `last_seen_ms` and `last_data_ms` are milliseconds.

### SML / OBIS Facts

- **SML Framing**: Multiples of 4 bytes, starting with `1b 1b 1b 1b 01 01 01 01` and ending with `1b 1b 1b 1b 1a [pad] [crc16]`.
- **Parser**: [`github.com/andig/gosml`](https://github.com/andig/gosml). Pure Go. Feed transport frames via `TransportRead(bufio.Reader)` then `FileParse(buf[8:len-8])` to skip start-escape and end+CRC.
- **DIN 43863-5 FNN Server-ID**: Exactly 10 bytes starting with `0x0A 0x01`, followed by 3 uppercase ASCII characters for the manufacturer (e.g. `LGZ`, `EMH`, `ESY`, `ITZ`, `ISK`), generation byte, and 4-byte big-endian serial number. Decoded into derived readings `manufacturer` and `meter_serial` (`<MFG>-<dec serial>`, e.g. `LGZ-81199038`). OBIS `1-0:96.1.0*255` is `server_id`; OBIS `1-0:0.0.9*255` is `device_id`.
- **Manufacturer Preservation**: An ASCII manufacturer name (`LGZ`) must never be overwritten by a raw hex string representation (`4c475a`).
- **Meter PIN ≠ Data Scope**: The PIN entered at the meter LCD typically only enables the momentary power output on the optical interface. Per-phase power, voltage, current, and frequency live in the **extended InfoDF / EDL40 profile**, which is configured separately by the *Messstellenbetreiber* (MSB) and almost always off by default in Germany.
- **OBIS Names Mapping**: `internal/sml/parse.go` `obisNames` already covers the extended EDL40 set. When the MSB enables EDL40, new fields surface automatically without code changes.

### Acquisition Modes

- **`push` (default)**: Lower latency, no polling load on the bridge. Reconnect delay default 100 ms. Falls back to poll on HTTP 404 or missing WS frames while HTTP polling works; HTTP 401 stops acquisition.
- **`poll`**: Polling interval default 10 s.

### Stdout & Logging Conventions

- **Without `--mqtt-host`**: Full multi-line formatted block per update (intended for debug and interactive terminal use).
- **With `--mqtt-host`**: A Tee sink writes a one-line compact summary per update on stdout in addition to MQTT publishing:
  `HH:MM:SS P=...W Eimp=...Wh Eexp=...Wh ...` — exactly one log event per telegram, no ANSI escape sequences, no in-place carriage-return overwriting.
  **Reason**: In containers (`docker logs`, `kubectl logs`), line-based logs preserve clean searchability; ANSI escapes corrupt log aggregators.
- `--quiet`: Suppresses per-update log lines entirely (only startup information and errors are logged).
- `-v`: Verbose logging (includes routine WebSocket reconnect notices).

---

## 2. Directory Layout

```
.
├── cmd/tibber-pulse-bot/   # CLI entrypoint, flag parsing, lifecycle
├── cmd/sml-inspect/        # SML 1.04 inspection and decoding CLI
├── internal/discovery/     # Home Assistant MQTT discovery specifications
├── internal/pulse/         # Bridge HTTP client (client.go) and WS client (ws.go)
├── internal/sml/           # SML parsing + OBIS-name mapping + serial decode
├── internal/output/        # Sink interface; StdoutSink, CompactStdoutSink, MQTTSink, TeeSink
├── chart/                  # Self-contained Helm chart (no upstream subchart)
├── scripts/                # Vendor-independent verification and git gate scripts
├── .agents/skills/         # Universal agent skills (verify, security-scan, audit, etc.)
├── .agents/agents/         # Role definitions for code review and security scanning
├── Dockerfile              # Multistage, distroless static, non-root
└── docker-compose.yml      # Local container orchestration
```

---

## 3. Security & Safety Rules

1. **Never Commit Secrets**:
   - Never commit `.env` or `.env.*` files (only `.env.example` is tracked as template).
   - Never commit raw bridge passwords, credentials, private keys, or API tokens.
   - Always read credentials from environment variables (`$TIBBER_PULSE_PASSWORD`).
   - Run `bash scripts/pre-push-secret-gate.sh --scan` before pushing.
2. **Fail-Closed Helm Guardrails**:
   - The Helm chart requires the password to be configured via **exactly one** of: `pulse.password`, `pulse.sealedSecret.encryptedPassword`, or `pulse.existingSecret`.
   - The chart fails if zero or multiple password mechanisms are specified.
   - Required values (`pulse.host`, `mqtt.host`) are guarded with Helm `fail`.
   - All chart-rendered Secrets use `stringData` so values are operator-inspectable in `helm get manifest`.
3. **Container Security**:
   - Containers run non-root (`UID/GID 65532`), with `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, and `drop: [ALL]` capabilities.
   - Runtime base is `gcr.io/distroless/static-debian12`, `CGO_ENABLED=0`, no shell, no extraneous files. Pin digest: `ghcr.io/0bu/tibber-pulse-bot:1.0.40@sha256:18d4326bc944dc7bb03f435c4fdaa0944e75a4fb3b1e49ead720db5b78d9cb3f`.

---

## 4. MQTT & Home Assistant Discovery

### MQTT Topic Naming

- One JSON document per SML telegram → `<topic-prefix>/readings`. Known OBIS values are top-level fields; unknown values live under the nested `obis` object with their original OBIS code as key.
- One reduced bridge-health JSON document → `<topic-prefix>/diagnostics` every `--metrics-interval`. Contains availability, last-data age, WiFi & bridge RSSIs, battery voltage, temperature, and corrupt-reading count.
- Availability → `<topic-prefix>/status`, retained `online`/`offline`. Set as MQTT Last Will, republished `online` on every (re)connect, `offline` in `MQTTSink.Close()`. It is the only retained non-discovery topic.
- Never reintroduce per-value state topics.
- HA discovery config topics remain one retained topic per entity because those represent registry configuration, not live state.

### Home Assistant MQTT Discovery

- Implemented in `internal/discovery/discovery.go`.
- Flags: `--ha-discovery` (off by default) + `--ha-discovery-prefix` (default `homeassistant`).
- Discovery publishes lazily after the first SML frame because the meter serial is the shared HA device identifier. Measurements and diagnostics belong to the same device; diagnostics use `entity_category: diagnostic`.
- **Single Device Invariant**: Both meter readings and bridge diagnostics are grouped under one single device with device identifier `dev.MeterSerial` (e.g. `LGZ-81199038`) and entity unique IDs prefixed with `tibber_pulse_<meter_serial>`.
- Newly appearing sensors (e.g. upon EDL40 activation) auto-announce on the fly without restart.
- Discovery messages are published with **`retain: true`** (HA convention to rebuild registry after restart). The `readings` and `diagnostics` state messages are **not** retained.
- Every config carries `availability_topic: <topic-prefix>/status` and, unless `--expire-after < 0`, an `expire_after` (readings and diagnostics get separate values from `calculateExpiration`; chart value `homeAssistant.expireAfter`).
- `unique_id` and `object_id` derive from `tibber_pulse_<serial>_<sensor>` (lowercased, non-alphanumerics → underscore) — stable across restarts and upgrades.
- **OBIS ↔ Discovery Parity**: `discovery.Sensors` MUST stay in sync with `obisNames` in `internal/sml/parse.go`. String-valued readings (`server_id`, `device_id`, `manufacturer`, `meter_serial`) carry metadata and are excluded from numeric sensors. Guarded by `TestObisNamesHaveDiscoverySpecs`.
- **Atomic Discovery Reservation**: Key discovery names are reserved under lock before publishing to prevent duplicate discovery messages during concurrent execution.

---

## 5. Build, Versioning & Deployment

### Build CLI

```bash
go build -o tibber-pulse-bot ./cmd/tibber-pulse-bot
```

### Build-Time Version Injection

- `cmd/tibber-pulse-bot/main.go` declares `var version, commit string` populated via `-ldflags="-X main.version=... -X main.commit=..."`:
  ```bash
  VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
  COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
  go build -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT}" -o tibber-pulse-bot ./cmd/tibber-pulse-bot
  ```
- Dockerfile accepts `VERSION` and `COMMIT` build-args and passes them through.
- Bot logs `tibber-pulse-bot version=X.Y.Z commit=abc1234` on startup; `--version` prints this and exits.
- **Tag Scheme in GHCR**: A `vX.Y.Z` tag build produces and pushes **exactly one** image tag `:X.Y.Z` — intentionally **no** `:latest`, no floating `:X` / `:X.Y`, no `:main`, no bare `:<sha>`. Consumers always pin the immutable digest `:X.Y.Z@sha256:<digest>`.
- Renovate automation updates `chart/values.yaml`, `chart/Chart.yaml`, `docker-compose.yml`, and documentation to the new digest automatically.

---

## 6. Verification Protocol & Quality Gates

### Static Gates (Always Run — Must Pass)

```bash
# Code formatting
test -z "$(gofmt -l .)" && echo "gofmt: clean" || { echo "gofmt: FAIL"; gofmt -l .; }

# Go vet
go vet ./...

# Unit tests with coverage
go test -v -cover ./...

# Mechanical doc drift + merge-policy selftest (both CI-gated)
scripts/check-drift.sh
scripts/selftest-policy.sh

# Helm lint & template render
helm lint chart
helm template test-plain chart \
  --set pulse.host=192.168.1.50 \
  --set mqtt.host=broker.local \
  --set pulse.password=dummy1234
```

### End-to-End Checks (Before Reporting Done on Hardware Changes)

1. **Stdout-only smoke test**:
   `./tibber-pulse-bot --pulse-host <ip> --pulse-password <pw>` — verify ≥ 5 readings per frame, including `meter_serial`.
2. **MQTT round-trip**:
   Run with `--mqtt-host` and subscribe with `mosquitto_sub -t "tibber/pulse/#"`. Verify live `power_total` on `tibber/pulse/readings` ~every 2–4 s and diagnostics on `tibber/pulse/diagnostics`.
3. **Helm template coverage**:
   Render chart with plaintext, sealedSecret, and existingSecret to verify all 3 modes.

---

## 7. Quality Tooling & Universal Agent Setup

This repository uses an open, vendor-independent agent setup adhering to universal standards (`AGENTS.md`, `.agents/skills/`, `.agents/agents/`, `scripts/`).

### Agent Skills (`.agents/skills/`)

| Skill | Purpose | Command / Scope |
|---|---|---|
| **`verify`** | Runs full verification protocol (gofmt, vet, test, helm render, e2e checks) | `go test ./...`, `helm lint` |
| **`project-audit`** | Audits documentation drift, CLI flags parity, image versions, and path references | Cross-file consistency checks |
| **`sml-inspect`** | Decodes binary SML 1.04 telegrams, hex dumps, and verifies OBIS mappings | SML framing, DIN 43863-5 FNN server-ID |
| **`bridge-diag`** | Queries and diagnoses Tibber Pulse Bridge endpoints safely | `/node_metrics.json`, `/metrics.json`, `/nodes.json`, `/status.json`, `/ws` |
| **`chart-lint`** | Validates Helm chart across all 3 password modes, failure modes, and flags (incl. `homeAssistant.expireAfter`) | Multi-mode `helm template` testing |
| **`security-scan`** | Audits codebase for vulnerabilities (`govulncheck`), credential leaks, and git isolation | Secret scanning, dependency checks |
| **`ha-discovery-validate`** | Validates Home Assistant MQTT Discovery specs, OBIS-sensor parity, availability topic and `expire_after` | `TestObisNamesHaveDiscoverySpecs`, `TestCalculateExpiration` |
| **`live-test`** | Automated end-to-end test against real bridge hardware (`192.168.107.118`) | `run_live_test.sh`, REST, SML, WS & MQTT round-trip (`LIVE_TEST_SKIP_MQTT=1` to skip) |
| **`release`** | Automates patch and minor/major releases via GitHub Actions pipeline | Workflow dispatch, tag verification |
| **`pr-hygiene-review`** | Human pass over commits, PR text and diff for personal data, secrets and non-English prose | `scripts/check-pr-hygiene.sh` + manual read |

### Agent Personas & Reviewers (`.agents/agents/`)

- **`go-reviewer`** (`.agents/agents/go-reviewer.md`): Reviews pending diffs against project conventions (comment policy, no abstractions ahead of demand, discovery parity, topic formats).
- **`secret-scanner`** (`.agents/agents/secret-scanner.md`): Audits staged and working changes for credentials, password leaks, and `.gitignore` coverage.

### Safety Gates & Hook Architecture (`scripts/`)

- `scripts/install-hooks.sh`: Installs git safety hooks (`pre-push`, `pre-commit`, `pre-merge-commit`) respecting `core.hooksPath` and chaining existing executable hooks with the same arguments and pre-push input.
- `scripts/pre-push-secret-gate.sh`: Checks the actual pushed refs and every outgoing commit, including root and merge commits. Blocks `.env`/`.env.*` (except `.env.example`), key files, credential assignments and hygiene findings. `--scan` also checks staged, working and non-ignored untracked changes. Unavailable objects fail closed; fetch the remote before retrying.
- `scripts/pre-merge-review-gate.sh`: Checks live PR metadata and review stamps, including the current connector argument schema. Local approval requires `--approve-local <incoming-commit>`; the marker must match that incoming commit. Native hooks also check commits made after `git merge --no-commit`.
- `scripts/hook-command.sh`: Shared shell-command tokenizer for the agent push and merge gates; preserves quoted paths and checks each command in a chain without evaluating shell input.
- `scripts/check-pr-gates.sh`: Pure data policy verifying that all required review gates are stamped with the PR head SHA.
- `scripts/check-pr-hygiene.sh`: Per-commit patch and message scanner for credentials, tokens, and non-English prose.
- `scripts/check-drift.sh`: Static documentation drift gate for CLI flags, image tags, chart values, and skills.
- `scripts/selftest-policy.sh`: Policy selftest proving that gates reliably fail when violated.
- `scripts/stop-verify.sh`: Pre-turn completion verification hook running applicable Go checks, documentation drift and policy selftests.

Run `scripts/install-hooks.sh` once per clone to activate the native Git hooks. The optional `.claude/settings.json` adapter calls these same scripts before shell pushes and merges, before connector PR merges/auto-merges, and at turn completion; shared safety logic stays in `scripts/`. Other agents must invoke the scripts through their own tool hooks or follow the verification protocol explicitly.

Git does not run `pre-merge-commit` for fast-forward or squash merges. Those paths require the agent pre-tool merge gate or the protected-branch PR policy. Native Git hooks cover merge commits and pushes; they do not intercept GitHub API calls.

### Merge gates

Every PR records its reviews in the body's **Merge gates** section ([`.github/pull_request_template.md`](.github/pull_request_template.md)) as a ticked task line with a **bare** stamp of the current head SHA:

```
- [x] `$project-audit` clean — merge gate @ 1a2b3c4d5e6f
```

`scripts/check-pr-gates.sh` decides which gates the diff needs: always `$code-review`, `$project-audit`, `$pr-hygiene-review`; `$ha-discovery-validate` for `internal/discovery|output|sml/` or `cmd/tibber-pulse-bot/`; `$chart-lint` for `chart/`; `$live-test` for `internal/pulse|sml/` (the end-to-end run the Verification protocol already demands). A push re-stales every stamp. Only tick a gate after running it on that head — the check verifies syntax and freshness, not that the review happened.

- **Enforced twice**: `pr-policy.yml` (`pull_request_target`, job `gates`, scripts loaded from the protected base, never runs PR code; it also runs `check-pr-hygiene.sh` on the PR title/body and every commit's patch) and the pre-merge review gate hook (same script, fails closed if the PR can't be fetched).
- **Renovate exemption**: a same-repo `renovate/*` PR whose commits are all authored AND committed by `bot@renovateapp.com` and which only touches Renovate-managed files (`Dockerfile`, `go.mod`/`go.sum`, `docker-compose.yml`, `README.md`/`AGENTS.md` pins, chart `values.yaml`/`Chart.yaml`, workflows) needs no records, so `RENOVATE_AUTOMERGE` keeps working. A hand-pushed or amended commit on the branch voids the exemption.
- Never weaken a gate, regex or allowlist to get a PR green; fix the PR.

---

## 8. Code Style & Contribution Guidelines

1. **No Premature Abstractions**: Add code to existing packages (`cmd/…`, `internal/pulse|sml|output|discovery`). Do not introduce unnecessary layers, files, or interfaces ahead of concrete demand.
2. **Comment Policy**: Only write comments explaining **WHY** something is done (e.g. non-obvious protocol quirks or hardware idiosyncrasies, such as why `buf[8:len-8]` is sliced), never restate what the next line of code does.
3. **OBIS ↔ Discovery Parity**: When adding a new numeric OBIS code to `obisNames` in `internal/sml/parse.go`, always add a matching entry in `discovery.Sensors` in `internal/discovery/discovery.go`.
4. **Graceful Goroutine Shutdown**: Background goroutines (such as `runMetrics`) must be synchronized with a `sync.WaitGroup` before shutting down network sinks.
5. **Module Path Consistency**: Go module path is `github.com/0Bu/tibber-pulse-bot`. If updated, all imports, chart values, docker-compose, and documentation links must be updated together.

---

## 9. Out-of-Scope Reminders for Future Work

- Adding HAN/SMGW (Smart Meter Gateway) support is a completely different protocol (CMS-encrypted, separate hardware) — not a small extension of this codebase.
