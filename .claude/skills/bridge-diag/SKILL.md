---
name: bridge-diag
description: Diagnose and inspect a Tibber Pulse Bridge over the local network. Tests HTTP endpoints (/data.json, /metrics.json, /nodes.json, /status.json) and WebSocket (/ws push), checks param 39, extracts hardware telemetry, and validates credentials safely.
disable-model-invocation: true
---

# bridge-diag

Diagnose connectivity, firmware version, and telemetry from a Tibber Pulse Bridge.
All endpoints require HTTP Basic Auth with username `admin` and the 9-character bridge sticker password.

**Safety note**: Never commit bridge passwords or full IP addresses to git. Read passwords from `$TIBBER_PULSE_PASSWORD`.

## 1. Fast Reachability & Auth Check

```bash
curl -s -o /dev/null -w "%{http_code}\n" -u "admin:${TIBBER_PULSE_PASSWORD:?missing}" "http://<bridge-ip>/data.json?node_id=1"
```

- `200`: Success (AP webserver enabled, password correct).
- `401`: Password incorrect.
- `404`: Node ID not found or invalid route (ensure `.json` suffix is present).
- Connection refused / timeout: Bridge not reachable on LAN or webserver not enabled.

## 2. Query Bridge Diagnostics & Nodes

Inspect bridge voltage, temperature, meter RSSI, and router WiFi RSSI:

```bash
# Metrics (voltage, temp, RSSI, corrupt counter)
curl -s -u "admin:$TIBBER_PULSE_PASSWORD" "http://<bridge-ip>/metrics.json" | jq .

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

If the bridge returns `404 Not Found`, its firmware does not support WebSocket streaming; use `--mode poll` instead.

## 4. Bridge Webserver Prerequisite (Param 39)

If the bridge webserver does not answer on LAN:
1. Put bridge in AP mode (hold button until LED turns blue, connect to SSID `TibberBridge-...`).
2. Open `http://10.133.70.1/` in a browser and log in with `admin:<password>`.
3. Under **Params**, ensure `webserver_force_enable` (param 39) is set to **`TRUE`**.
4. Unplug and replug the bridge to return to normal WiFi station mode.
