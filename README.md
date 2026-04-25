# tibber-pulse-bot

Read SML 1.04 meter telegrams from a local **Tibber Pulse Bridge** and republish
them to **MQTT** (or stdout). Lightweight Go binary, single static container,
no Home Assistant required.

- **Live push** via WebSocket (`ws://<bridge>/ws`) — one MQTT message per
  meter telegram (~2–4 s on a typical Landis+Gyr E220)
- **Polling fallback** via HTTP `/data.json` for older bridge firmware
- Decodes manufacturer + serial number from the FNN server-ID
- Auto-reconnect on bridge-side TCP drops
- Distroless container (~9 MB), readable as `kubectl logs` / `docker logs`

> Inspired by [marq24/ha-tibber-pulse-local](https://github.com/marq24/ha-tibber-pulse-local).
> That integration targets Home Assistant; this project is a standalone CLI/container
> for users who want the data on their own MQTT bus and pipe it elsewhere
> (Telegraf → InfluxDB, Node-RED, openHAB, …).

---

## Prerequisites

- A Tibber Pulse Bridge with a SML 1.04 meter (e.g. Landis+Gyr E220)
- The 9-character bridge admin password (printed on the sticker, format `XXXX-XXXX`)
- `webserver_force_enable` (param 39) set to `TRUE` on the bridge — done once
  via the bridge's AP-mode console
- An MQTT broker (e.g. Mosquitto)

If your meter is in PIN-locked mode, ask your *Messstellenbetreiber* (MSB) to
unlock the optical interface — see [Meter PIN & data scope](#meter-pin--data-scope).

---

## Run with Docker

```bash
docker build -t tibber-pulse-bot .

docker run --rm \
  -e TIBBER_PULSE_PASSWORD=AD56-54BA \
  tibber-pulse-bot \
  --pulse-host 192.168.107.118 \
  --mqtt-host 192.168.1.27
```

## Run with docker-compose

```bash
cp .env.example .env
$EDITOR .env                 # fill in TIBBER_PULSE_*, MQTT_*
docker compose up -d
docker compose logs -f
```

`.env` is git-ignored. Compose uses the values from it for both the bridge
password (env var) and CLI flags (substituted into `command:`).

## Run as a binary

```bash
go build -o tibber-pulse-bot ./cmd/tibber-pulse-bot

# Live push to MQTT (default)
./tibber-pulse-bot \
  --pulse-host 192.168.107.118 \
  --pulse-password AD56-54BA \
  --mqtt-host 192.168.1.27

# Stdout only — no --mqtt-host
./tibber-pulse-bot \
  --pulse-host 192.168.107.118 \
  --pulse-password AD56-54BA

# Polling fallback for old bridge firmware
./tibber-pulse-bot --mode poll --interval 10s \
  --pulse-host 192.168.107.118 \
  --pulse-password AD56-54BA \
  --mqtt-host 192.168.1.27
```

## Run on Kubernetes

A self-contained Helm chart lives in [`chart/`](chart/) — see
[`chart/README.md`](chart/README.md) for full configuration options.

```bash
helm install tibber-pulse-bot ./chart \
  --set pulse.host=192.168.107.118 \
  --set pulse.existingSecret=tibber-pulse \
  --set mqtt.host=mosquitto.default.svc.cluster.local
```

---

## Modes

| Mode | When to use | Cadence |
|---|---|---|
| `push` (default) | Bridge firmware ≥ `1428-6debbaf6` / `795-379a5e21`. Lower latency, no polling load. | one frame per meter telegram (~2–4 s) |
| `poll` | Older firmware that returns 404 on `/ws` | every `--interval` |

Push automatically reconnects when the bridge drops the TCP socket (every
30–60 s typical) — those events are silent unless you pass `-v`.

## CLI flags

| Flag | Default | Description |
|---|---|---|
| `--pulse-host` | (required) | Bridge IP / hostname |
| `--pulse-password` | `$TIBBER_PULSE_PASSWORD` | Bridge admin password |
| `--pulse-node` | `1` | Bridge node id (poll mode) |
| `--mode` | `push` | `push` (WebSocket) or `poll` (HTTP) |
| `--interval` | `10s` | Poll interval (poll mode) |
| `--ws-idle-timeout` | `60s` | Reconnect WS if no message arrives |
| `--reconnect-delay` | `1s` | Delay before reconnecting after WS drop |
| `--mqtt-host` | (empty → stdout) | MQTT broker host |
| `--mqtt-port` | `1883` | MQTT broker port |
| `--mqtt-topic` | `tibber/pulse` | Topic prefix |
| `--mqtt-client-id` | `tibber-pulse-bot` | MQTT client id |
| `--quiet` | `false` | Suppress per-update stdout when `--mqtt-host` is set |
| `-v` | `false` | Log every WS reconnect (default: only real errors) |

## MQTT topics

Known OBIS values are published as `<topic-prefix>/<name>`:

- `power_total`, `power_l1` / `l2` / `l3` — current active power [W]
- `energy_import_total`, `energy_export_total` — total energy [Wh]
- `voltage_l1` / `l2` / `l3`, `current_l1` / `l2` / `l3`, `frequency`
- `manufacturer` — 3-letter ASCII (e.g. `LGZ`)
- `meter_serial` — `<manufacturer>-<serial>` (e.g. `LGZ-81199038`)
- `device_id` — raw 10-byte FNN server-ID as hex

Unknown OBIS codes fall through to `<topic-prefix>/obis/<code>` (e.g.
`tibber/pulse/obis/1-0:96.50.1_1`).

## Stdout output

- **No `--mqtt-host`**: full multi-line block per update (debug / interactive).
- **With `--mqtt-host`**: one compact line per update, container-log friendly:
  ```
  19:46:08 P=0.000W   Eimp=2423174.800Wh Eexp=253615.600Wh
  19:46:11 P=4.000W   Eimp=2423174.800Wh Eexp=253615.600Wh
  ```
  No in-place overwriting — that doesn't work in `docker logs` /
  `kubectl logs` and would bloat the log with ANSI escapes. Use `--quiet` to
  suppress entirely.

---

## Meter PIN & data scope

The PIN you enter at the meter's LCD typically only enables the **momentary
power output** on the optical interface (otherwise the bridge reads nothing
from a fresh meter). It does **not** automatically widen the data set.

A "minimal" InfoDF profile sends only:

- `1-0:1.8.0` — total import [Wh]
- `1-0:2.8.0` — total export [Wh]
- `1-0:16.7.0` — sum power [W]
- `1-0:96.1.0` — server-ID

To get per-phase power/voltage/current and frequency you have to ask your
*Messstellenbetreiber* (MSB) to switch the meter to the **extended InfoDF /
EDL40 profile**. The bot will pick the new fields up automatically — no code
or config change needed; they just start appearing in MQTT.

---

## Project layout

```
.
├── cmd/tibber-pulse-bot/   # CLI entrypoint
├── internal/pulse/         # Bridge HTTP + WebSocket clients
├── internal/sml/           # SML 1.04 parsing + OBIS mapping
├── internal/output/        # Stdout / MQTT / Tee sinks
├── chart/                  # Helm chart
├── Dockerfile              # multistage, distroless static
└── docker-compose.yml
```

## License

[MIT](LICENSE)
