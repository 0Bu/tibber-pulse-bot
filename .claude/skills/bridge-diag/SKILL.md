---
name: bridge-diag
description: Diagnose and inspect a Tibber Pulse Bridge over the local network. Tests HTTP endpoints (/node_data.json or legacy /data.json, /node_metrics.json or legacy /metrics.json, /nodes.json, /status.json) and WebSocket (/ws push), checks param 39, extracts hardware telemetry, and validates credentials safely.
disable-model-invocation: true
---

# bridge-diag

Diagnose connectivity, firmware version, and telemetry from a Tibber Pulse Bridge.
All endpoints require HTTP Basic Auth with username `admin` and the 9-character bridge sticker password.

**Safety note**: Never commit bridge passwords or full IP addresses to git. Read passwords from `$TIBBER_PULSE_PASSWORD`.

## 1. Fast Reachability & Auth Check

```bash
# Modern firmware
curl -s -o /dev/null -w "%{http_code}\n" -u "admin:${TIBBER_PULSE_PASSWORD:?missing}" "http://<bridge-ip>/node_data.json?node_id=1"
# Legacy firmware (only if the above returns 404)
curl -s -o /dev/null -w "%{http_code}\n" -u "admin:$TIBBER_PULSE_PASSWORD" "http://<bridge-ip>/data.json?node_id=1"
```

- `200`: Success (AP webserver enabled, password correct).
- `401`: Password incorrect.
- `404` on both: Node ID not found or invalid route (ensure `.json` suffix is present).
  `404` on only one is normal — the bot (`pulse.Client.FetchData`) tries
  `/node_data.json` first, falls back to `/data.json`, and caches whichever works.
- Connection refused / timeout: Bridge not reachable on LAN or webserver not enabled.

## 2. Query Bridge Diagnostics & Nodes

Inspect bridge voltage, temperature, meter RSSI, and router WiFi RSSI:

```bash
# Metrics (voltage, temp, RSSI, corrupt counter)
# Modern firmware: top-level `node` / `ir` / `hub` sections
curl -s -u "admin:$TIBBER_PULSE_PASSWORD" "http://<bridge-ip>/node_metrics.json?node_id=1" | jq .
# Legacy firmware: `node_status` / `hub_attachments` sections
curl -s -u "admin:$TIBBER_PULSE_PASSWORD" "http://<bridge-ip>/metrics.json?node_id=1" | jq .

# Node status (EUI, availability, last data timestamp)
curl -s -u "admin:$TIBBER_PULSE_PASSWORD" "http://<bridge-ip>/nodes.json" | jq .

# Router WiFi signal strength
curl -s -u "admin:$TIBBER_PULSE_PASSWORD" "http://<bridge-ip>/status.json?timeout=0" | jq .
```

## 3. Test WebSocket Live Push Stream

To verify the bridge firmware supports `/ws` push mode:

```bash
# Using curl (firmware ≥ 1428-6debbaf6 returns 101 Switching Protocols or chunks)
curl -i -N -u "admin:$TIBBER_PULSE_PASSWORD" \
  -H "Connection: Upgrade" \
  -H "Upgrade: websocket" \
  -H "Sec-WebSocket-Version: 13" \
  -H "Sec-WebSocket-Key: SGVsbG8sIHdvcmxkIQ==" \
  "http://<bridge-ip>/ws"
```

If the bridge returns `404 Not Found`, its firmware does not support WebSocket streaming.
The bot handles this on its own: in `--mode push` it logs
`ws not supported by bridge firmware (HTTP 404): falling back to poll` and
switches to polling at `--interval`. It also falls back when `/ws` connects but
delivers no frame within the first 15 s while an HTTP poll of the data endpoint
succeeds. Pinning `--mode poll` just skips that probe.

## 4. Bridge Webserver Prerequisite (Param 39)

If the bridge webserver does not answer on LAN:
1. Put bridge in AP mode (hold button until LED turns blue, connect to SSID `TibberBridge-...`).
2. Open `http://10.133.70.1/` in a browser and log in with `admin:<password>`.
3. Under **Params**, ensure `webserver_force_enable` (param 39) is set to **`TRUE`**.
4. Unplug and replug the bridge to return to normal WiFi station mode.
