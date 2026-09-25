---
name: live-test
description: Run automated live tests against the local Tibber Pulse Bridge at 192.168.107.118 (or custom IP). Tests HTTP endpoints, verifies SML decoding via sml-inspect, and checks live WebSocket push stream.
---

# Live Test Skill (`live-test`)

This skill runs automated live verification tests against a physical **Tibber Pulse Bridge** (default target: `192.168.107.118`).

## Prerequisites

- Local network reachability to `192.168.107.118`.
- Bridge password configured via `$TIBBER_PULSE_PASSWORD` or in `.env` (9-char sticker password, format `XXXX-XXXX`). Never paste the real value into this file.

## Quick Start

Run the automated live verification script:

```bash
bash .claude/skills/live-test/scripts/run_live_test.sh [BRIDGE_IP] [PASSWORD] [NODE_ID] [MQTT_HOST]
```

Or simply (inheriting defaults `192.168.107.118` and password from `.env`/env):

```bash
bash .claude/skills/live-test/scripts/run_live_test.sh
```

## What This Skill Tests

1. **HTTP Reachability & Basic Auth**:
   - Queries `http://<bridge-ip>/status.json?timeout=0` with `admin:<password>`.
   - Confirms HTTP 200 and validates credentials.
2. **Bridge Diagnostics & Nodes**:
   - Fetches WiFi RSSI, bridge battery voltage, bridge temperature, and link RSSI from `/nodes.json` and `/node_metrics.json` (legacy fallback `/metrics.json`).
3. **SML Binary Parsing**:
   - Fetches binary SML telegram from `/node_data.json?node_id=1` (legacy fallback `/data.json`).
   - Pipes into `cmd/sml-inspect` and verifies Landis+Gyr / DIN 43863-5 FNN server ID decoding.
4. **WebSocket Push Mode (`/ws`)**:
   - Runs `tibber-pulse-bot` live in push mode with `--reconnect-delay=100ms`.
   - Verifies real-time telegram acquisition without frame loss during bridge idle socket drops.
   - Warns if the bot logged `switching to poll mode` (WS→poll fallback), because then push was not exercised.
5. **Live MQTT round-trip (Optional)**:
   - Runs only if `mosquitto_sub` is installed and the broker (`$MQTT_HOST`, arg 4) accepts a connection; otherwise skipped with a notice.
   - Runs the bot with `--mqtt-host --ha-discovery` and checks `tibber/pulse/readings` receives data, `tibber/pulse/status` turns `online`, and discovery configs carry `availability_topic`.
