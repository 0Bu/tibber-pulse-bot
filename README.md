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
- `webserver_force_enable` (param 39) set to `TRUE` on the bridge — see
  [Enabling the local web server](#enabling-the-local-web-server) below
- An MQTT broker (e.g. Mosquitto)

If your meter is in PIN-locked mode, ask your *Messstellenbetreiber* (MSB) to
unlock the optical interface — see [Meter PIN & data scope](#meter-pin--data-scope).

### Enabling the local web server

Out of the box, the Tibber Pulse Bridge keeps its local HTTP server **off**
to save power and limit attack surface. The bot needs it on. The setting
is `param 39 / webserver_force_enable` and changing it requires putting
the bridge into **AP (access-point) mode** once — the local web UI is
unreachable in normal operation precisely because of this flag.

You only need to do this once per bridge.

**Hardware:** the bridge is the small white plug-in box that sits in your
fuse cabinet next to (or wirelessly near) the meter. It has an LED on
the front and a tiny **reset/pairing button** in a pinhole on the side
(some revisions have a visible button instead).

#### 1. Enter AP mode

1. Unplug the bridge from the wall outlet.
2. With a paperclip / SIM tool, **press and hold the pinhole button**.
3. **Plug the bridge back in** while still holding the button.
4. Keep holding for ~10 s, until the LED starts blinking **blue**.
5. Release. The bridge is now broadcasting a temporary WiFi network.

#### 2. Connect to the bridge's AP

- On your phone or laptop, look for a WiFi SSID like
  **`TibberBridge-XXXXXX`** (the suffix matches the EUI on the sticker).
- The WPA2 password is the **same 9-character code** printed on the
  bridge sticker (format `XXXX-XXXX`) — the one you also use as the
  admin password later.
- After connecting, the bridge's gateway is typically `10.133.70.1`.
  Some firmware revisions advertise a captive portal that pops up
  automatically.

#### 3. Flip param 39

1. Open `http://10.133.70.1/` in a browser.
2. Log in with username `admin`, password = the same 9-char code.
3. Click **Params** in the top nav.
4. Scroll to **`param_id 39  webserver_force_enable`** (it's a `bool`,
   currently `FALSE`).
5. Set it to **`TRUE`** and click **Save** / **Apply**.
6. While you're there, write down the **EUI** shown on the **Nodes** page
   (16-hex-char string) — handy later for HA device naming.

#### 4. Leave AP mode

- **Unplug and replug** the bridge (no button this time).
- It boots back into normal mode, reconnects to your home WiFi, and the
  web server now answers on its DHCP IP.

#### 5. Verify

```bash
curl -u admin:XXXX-XXXX -I "http://<bridge-ip>/data.json?node_id=1"
# expect: HTTP/1.1 200 OK   Content-Type: text/text
```

If you get `401 Unauthorized` instead, the password is wrong. If you get
no response at all, the bridge isn't reachable on your LAN — check DHCP /
that it actually rejoined WiFi. If you get the SPA HTML shell from
`http://<bridge-ip>/`, the web server is up but you may have hit the SPA
route (`/nodes/1/data`) instead of the data endpoint
(`/data.json?node_id=1`); use the JSON path.

> **Note**: AP mode also doubles as a factory-reset entry point. Holding
> the button longer (~30 s) on some firmware revisions wipes the WiFi
> credentials. If you only want to flip param 39, release as soon as the
> LED turns blue.

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
| `--ha-discovery` | `false` | Publish Home Assistant MQTT-Discovery configs |
| `--ha-discovery-prefix` | `homeassistant` | HA discovery topic prefix |
| `--metrics-interval` | `60s` | Bridge metrics (`/metrics.json`) poll cadence; `0` disables |

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

Bridge state is fetched from `/metrics.json`, `/nodes.json`, `/status.json`
and `/ota_manifest.json` every `--metrics-interval` (default 60s) and
published under `<topic-prefix>/bridge/<name>`:

| Source | Sensor | Type |
|---|---|---|
| metrics.json | `battery_voltage`, `temperature`, `rssi` (meter link), `lqi`, `uptime`, `pkg_sent`, `pkg_received`, `readings_received`, `corrupt_readings`, `invalid_readings` | numeric |
| nodes.json | `last_data_age` (s since last meter telegram) | numeric |
| nodes.json | `available` | binary `ON`/`OFF` |
| status.json | `wifi_rssi` (router link, distinct from `rssi`) | numeric |
| status.json | `cloud_mqtt` (Tibber cloud connection) | binary `ON`/`OFF` |
| ota_manifest.json | `update_available` (any component out of date) | binary `ON`/`OFF` |

With `--ha-discovery` they appear as a separate **Tibber Pulse Bridge
\<EUI\>** device in HA. The bridge device card shows both ESP32 hub and
EFR32 node firmware versions, the bridge's MAC-style EUI under
"connections", and a "Visit device" link to the bridge web UI. The meter
device gets a `via_device` link so HA shows "Connected via Tibber Pulse
Bridge …" on the meter card.

> Migration note (v1.0.4 → v1.0.5): the bridge identifier changed from
> IP-based to EUI-based (DHCP-stable). On first run, v1.0.5 publishes
> empty retained payloads to the legacy IP-based discovery topics so HA
> garbage-collects the old "Tibber Pulse Bridge \<ip\>" device card. If
> HA still shows the orphan device, delete it manually once.

## Home Assistant integration

Pass `--ha-discovery` to enable [MQTT
Discovery](https://www.home-assistant.io/integrations/mqtt/#mqtt-discovery).
On the first SML frame the bot publishes one **retained** config message per
known sensor under `homeassistant/sensor/<unique_id>/config`. HA picks them up
automatically and groups all entities under one Device named after the meter
serial (e.g. `Tibber Pulse LGZ-81199038`).

```bash
tibber-pulse-bot --pulse-host 192.168.107.118 --pulse-password ... \
  --mqtt-host 192.168.1.27 --ha-discovery
```

Each entity gets the right `device_class` / `state_class` /
`unit_of_measurement` so it shows up correctly in the HA Energy dashboard
(`energy_import_total` and `energy_export_total` are tagged as
`total_increasing` energy in Wh — usable as Grid consumption / Return to
grid sources directly). When the MSB later enables the extended EDL40
profile (per-phase power, voltage, current, frequency), those entities
appear in HA automatically — the bot announces newly seen sensors on the fly.

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
