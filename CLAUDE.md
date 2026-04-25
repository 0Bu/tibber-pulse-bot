# tibber-pulse-bot — Project Instructions

Standalone Go CLI / container that reads SML 1.04 meter telegrams from a
local Tibber Pulse Bridge and republishes them to MQTT (or stdout). Designed
to run as a single Deployment in the home k3s cluster
([rpi-k3s-cluster](https://github.com/0Bu/rpi-k3s-cluster)) and to be
distributable as a public GitHub project.

## Scope and non-goals

- **Scope**: bridge → SML → MQTT (and stdout). One small binary, one container,
  one Helm chart.
- **Non-goals**: Home Assistant integration (use
  [marq24/ha-tibber-pulse-local](https://github.com/marq24/ha-tibber-pulse-local)
  for that), Tibber cloud GraphQL API, persistence, dashboards. Downstream
  consumers (Telegraf → InfluxDB, Node-RED, Grafana) live elsewhere.

## Bridge protocol facts (don't re-research these)

- **Data endpoint (poll)**: `GET http://<bridge>/data.json?node_id=N`
  with HTTP Basic auth `admin:<9-char QR-code from sticker>`. Returns raw
  binary SML 1.04 frames, **not** JSON despite the suffix.
- **Push endpoint (live)**: `ws://<bridge>/ws`, same Basic auth (sent as
  `Authorization: Basic ...` header on the HTTP upgrade). Pure server-push,
  no subscribe message. One frame per meter telegram (~2–4 s on Landis+Gyr).
- **Push frame format**: `<key:value key:"value" ...>BODY` — ASCII header
  (key/value pairs, values optionally quoted) followed by raw payload after
  the first `>` byte. For SML topics, the body is the same SML 1.04 binary
  the poll endpoint returns.
- **Bridge prereq**: `webserver_force_enable` (param 39) must be `TRUE`,
  otherwise `/data.json` and `/ws` are dead. Set once via the bridge's
  AP-mode console.
- **Common bridge behaviour**: drops the WS every ~30–60 s with EOF (no
  Close frame). This is normal — silently reconnect with short backoff.
- The path `http://<bridge>/nodes/1/data` is the **SPA UI route**, not the
  data API — it always returns the HTML shell. Do not point new code at it.

## SML / OBIS facts

- Parser: [`github.com/andig/gosml`](https://github.com/andig/gosml). Pure
  Go, unmaintained but works for OBIS extraction. Feed transport frames via
  `TransportRead(bufio.Reader)` then `FileParse(buf[8:len-8])` to skip
  start-escape and end+CRC.
- **Meter PIN ≠ data scope.** The PIN entered at the meter LCD typically
  only enables the *momentary power* output on the optical interface (which
  the Pulse needs to show anything). Per-phase power, voltage, current, and
  frequency live in the **extended InfoDF / EDL40 profile**, which is
  configured separately by the *Messstellenbetreiber* (MSB) and almost
  always off by default in Germany.
- Bot's `obisNames` mapping in [`internal/sml/parse.go`](internal/sml/parse.go)
  already covers the extended set. When the MSB enables EDL40, the new
  fields surface automatically — no code change needed.
- **FNN server-ID format** (OBIS `1-0:96.1.0`, 10 bytes):
  `[prefix=0x0A] [medium=0x01] [3-byte ASCII manufacturer] [version] [4-byte big-endian serial]`.
  Decoded into derived readings `manufacturer` and `meter_serial`
  (`<MFG>-<dec serial>`, e.g. `LGZ-81199038`).

## Acquisition modes

- **`push` is the default.** Lower latency, no polling load on the bridge.
  Reconnect-delay default is 1 s; EOF / abnormal-close errors are returned
  as `pulse.ErrPeerClosed` and logged silently unless `-v` is set.
- **`poll`** is a fallback for old bridge firmware that 404s on `/ws`.
  Default interval 10 s.

## Stdout / logging conventions

- **Without `--mqtt-host`**: full multi-line block per update (debug /
  interactive use).
- **With `--mqtt-host`**: a Tee sink writes a one-line compact summary per
  update on stdout *in addition* to the MQTT publish. Format:
  `HH:MM:SS P=...W Eimp=...Wh Eexp=...Wh ...` — one log event per
  telegram, no ANSI escapes, no in-place overwriting. **Reason**: the bot
  is meant to live in a container; `docker logs` / `kubectl logs` are
  line-based, ANSI escapes become garbage and overwriting bloats the log.
- `--quiet` suppresses the per-update line entirely (only startup line + errors).
- `-v` enables verbose logging of routine WS reconnects.

## Project conventions

- **Go module**: `github.com/0Bu/tibber-pulse-bot`. Update if the GitHub
  handle changes — including imports, chart `image.repository`, and README links.
- **Layout**:
  ```
  cmd/tibber-pulse-bot/   # CLI entrypoint, flag parsing, signal handling
  internal/pulse/         # bridge HTTP client (client.go) and WS client (ws.go)
  internal/sml/           # SML parsing + OBIS-name mapping + serial decode
  internal/output/        # Sink interface; StdoutSink, CompactStdoutSink,
                          # MQTTSink, TeeSink
  chart/                  # self-contained Helm chart (no upstream subchart)
  Dockerfile              # multistage, distroless static, non-root
  docker-compose.yml      # reads .env; fail-fast on missing required vars
  ```
- **No new files unless needed.** Prefer editing existing modules; don't
  introduce abstractions ahead of demand.
- **No comments that restate the code.** Only document non-obvious WHY
  (e.g. why we strip `buf[8:len-8]`, why `--reconnect-delay` defaults to 1 s).

## Container / deployment

- **Dockerfile**: `golang:1.26-alpine` builder → `gcr.io/distroless/static-debian12`
  runtime. `CGO_ENABLED=0`, runs as UID 65532, no shell, no extra files.
- **docker-compose.yml**: reads `.env` (git-ignored), `.env.example` is the
  template. Required variables fail-fast (`${VAR:?...}` syntax).
- **Helm chart** (`chart/`): self-contained, follows the same parent-chart
  layout as [rpi-k3s-cluster](https://github.com/0Bu/rpi-k3s-cluster)
  charts. Three mutually exclusive password supply modes (chart fails if
  none is set):
  1. `pulse.password` — inline plaintext, dev only.
  2. `pulse.sealedSecret.encryptedPassword` — `kubeseal --raw` ciphertext
     committed into values; renders a `SealedSecret` (bitnami CRD).
     Mirrors the [`telegraf` chart in
     rpi-k3s-cluster](https://github.com/0Bu/rpi-k3s-cluster/tree/main/telegraf)
     pattern. Requires the sealed-secrets controller in-cluster. Optional
     `pulse.sealedSecret.scope` for `namespace-wide` / `cluster-wide`.
  3. `pulse.existingSecret` — name of an out-of-band Secret with key
     `TIBBER_PULSE_PASSWORD` (sops-flux, external-secrets, plain kubectl).
- Required values guarded with Helm `fail`: `pulse.host`, `mqtt.host`, and
  the password-mode triplet above.

## MQTT topic naming

- Known OBIS values → `<topic-prefix>/<name>` (e.g. `tibber/pulse/power_total`,
  `tibber/pulse/energy_import_total`, `tibber/pulse/meter_serial`).
- Unknown OBIS → `<topic-prefix>/obis/<code>` with `*` replaced by `_`.
- Numeric values: `%.3f` formatted, no unit suffix in payload (the topic
  name implies the unit). String values (manufacturer, serial, device_id):
  raw string.

## Security

- **Never commit the bridge password.** `.gitignore` excludes `.env`,
  `tibber-pulse-bot` binary, `dist/`, `chart/charts/`. Verify with
  `grep -ri <password-fragment>` before any push.
- The bridge password from this development cycle was leaked once in chat.
  If the bridge is rotated, update local `.env` and any sealed-secret
  ciphertext in cluster values.
- All chart-rendered Secrets use `stringData` so the value is visible in
  `helm get manifest` to the operator (intentional; this is a homelab).

## Verification protocol

When changing the bridge or SML code, re-run end-to-end before reporting
done:

1. **Stdout-only smoke test**:
   `./tibber-pulse-bot --pulse-host <ip> --pulse-password <pw>` —
   expect ≥ 5 readings per frame, including `meter_serial`.
2. **MQTT round-trip**: run the bot with `--mqtt-host` and a separate
   `mosquitto_sub` (or equivalent) subscribed to `tibber/pulse/#`. Verify
   live `power_total` updates ~every 2–4 s.
3. **Helm**: `helm lint chart && helm template r chart --set pulse.host=...
   --set mqtt.host=... --set pulse.password=...` for at least one of the
   three password modes; expect zero errors.

## Home Assistant MQTT-Discovery

- Implemented in [`internal/discovery`](internal/discovery/discovery.go).
- Flags: `--ha-discovery` (off by default) + `--ha-discovery-prefix`
  (default `homeassistant`).
- Discovery happens **lazily inside `MQTTSink.Publish`**, not at startup —
  the meter serial is needed as HA device identifier and only arrives in
  the first SML frame. Per session, each sensor's config is announced once.
- Newly appearing sensors (e.g. when MSB later enables EDL40) auto-announce
  on the fly — no restart needed.
- Discovery messages are published with **`retain: true`** (HA convention,
  so HA can rebuild its registry after restart). State messages are NOT
  retained — they update every 2–4 s.
- `unique_id` and `object_id` derive from `tibber_pulse_<serial>_<sensor>`
  (lowercased, non-alphanumerics → underscore) — stable across restarts and
  bot upgrades.
- The `discovery.Sensors` map MUST stay in sync with the OBIS-name set in
  [`internal/sml/parse.go`](internal/sml/parse.go) `obisNames`. Adding a
  new OBIS code without a discovery entry means HA won't surface it.

## Out-of-scope reminders for future work

- Adding HAN/SMGW (Smart Meter Gateway) support is a different protocol
  (CMS-encrypted, separate hardware) — not a small extension of this codebase.
- A `metrics.json` poller for bridge battery/RSSI/uptime could be added at
  ~30 min cadence as a second goroutine — keep it optional.
