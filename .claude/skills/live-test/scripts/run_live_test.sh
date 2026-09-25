#!/usr/bin/env bash
# run_live_test.sh — Automated live verification against Tibber Pulse Bridge
set -euo pipefail

BRIDGE_IP="${1:-${TIBBER_PULSE_HOST:-192.168.107.118}}"
PASSWORD="${2:-${TIBBER_PULSE_PASSWORD:-}}"
NODE_ID="${3:-1}"
MQTT_HOST="${4:-${MQTT_HOST:-192.168.1.27}}"

# If password not provided in env/arg, try reading from .env
if [[ -z "$PASSWORD" && -f .env ]]; then
  PASSWORD=$(grep -E '^TIBBER_PULSE_PASSWORD=' .env | cut -d'=' -f2- | tr -d '"' | tr -d "'" || true)
fi

if [[ -z "$PASSWORD" ]]; then
  echo "Error: Bridge password not specified. Set \$TIBBER_PULSE_PASSWORD, add to .env, or pass as arg 2." >&2
  exit 1
fi

echo "==================================================================="
echo "Tibber Pulse Bridge Live Test"
echo "Target Bridge: $BRIDGE_IP (Node: $NODE_ID)"
echo "MQTT Target:   $MQTT_HOST"
echo "Date:          $(date)"
echo "==================================================================="

# 1. HTTP Ping / Reachability
echo ""
echo "[1/5] Checking bridge HTTP reachability and credentials..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -u "admin:${PASSWORD}" "http://${BRIDGE_IP}/status.json?timeout=0" || true)
if [[ "$HTTP_CODE" == "200" ]]; then
  echo "  ✓ HTTP 200 OK — Bridge reachable & authenticated"
elif [[ "$HTTP_CODE" == "401" ]]; then
  echo "  ✗ HTTP 401 Unauthorized — Password incorrect!" >&2
  exit 1
else
  echo "  ✗ HTTP check failed with code: $HTTP_CODE (Bridge unreachable?)" >&2
  exit 1
fi

# 2. Query Diagnostics Endpoints
echo ""
echo "[2/5] Querying bridge status, nodes, and metrics..."
STATUS_JSON=$(curl -s -u "admin:${PASSWORD}" "http://${BRIDGE_IP}/status.json?timeout=0")
NODES_JSON=$(curl -s -u "admin:${PASSWORD}" "http://${BRIDGE_IP}/nodes.json")
METRICS_JSON=$(curl -s -u "admin:${PASSWORD}" "http://${BRIDGE_IP}/node_metrics.json?node_id=${NODE_ID}")
if echo "$METRICS_JSON" | grep -q -i "Nothing matches"; then
  METRICS_JSON=$(curl -s -u "admin:${PASSWORD}" "http://${BRIDGE_IP}/metrics.json?node_id=${NODE_ID}")
fi

WIFI_RSSI=$(echo "$STATUS_JSON" | python3 -c "import sys, json; print(json.load(sys.stdin).get('wifi_status', {}).get('rssi', 'n/a'))" 2>/dev/null || echo "n/a")
NODE_AVAIL=$(echo "$NODES_JSON" | python3 -c "import sys, json; nodes=json.load(sys.stdin); print(next((n.get('available') for n in nodes if n.get('node_id')==${NODE_ID}), 'n/a'))" 2>/dev/null || echo "n/a")
NODE_EUI=$(echo "$NODES_JSON" | python3 -c "import sys, json; nodes=json.load(sys.stdin); print(next((n.get('eui') for n in nodes if n.get('node_id')==${NODE_ID}), 'n/a'))" 2>/dev/null || echo "n/a")
VOLT=$(echo "$METRICS_JSON" | python3 -c "import sys, json; data=json.load(sys.stdin); m=data.get('node') or data.get('node_status') or {}; print(m.get('battery_voltage', m.get('node_battery_voltage', 'n/a')))" 2>/dev/null || echo "n/a")
TEMP=$(echo "$METRICS_JSON" | python3 -c "import sys, json; data=json.load(sys.stdin); m=data.get('node') or data.get('node_status') or {}; print(m.get('temperature', m.get('node_temperature', 'n/a')))" 2>/dev/null || echo "n/a")
LINK_RSSI=$(echo "$METRICS_JSON" | python3 -c "import sys, json; data=json.load(sys.stdin); m=data.get('node') or data.get('node_status') or {}; print(m.get('avg_rssi', m.get('node_avg_rssi', 'n/a')))" 2>/dev/null || echo "n/a")

echo "  ✓ Status:   WiFi RSSI=${WIFI_RSSI} dBm"
echo "  ✓ Node:     EUI=${NODE_EUI}, Available=${NODE_AVAIL}"
echo "  ✓ Metrics:  Battery=${VOLT} V, Temp=${TEMP} °C, Link RSSI=${LINK_RSSI} dBm"

# 3. SML Telegram Decoding via sml-inspect
echo ""
echo "[3/5] Polling binary SML telegram and decoding with sml-inspect..."
TMP_SML=$(mktemp /tmp/sml_live_XXXXXX.bin)
SML_LEN=0
for attempt in {1..5}; do
  HTTP_STATUS=$(curl -s -w "%{http_code}" -u "admin:${PASSWORD}" "http://${BRIDGE_IP}/node_data.json?node_id=${NODE_ID}" -o "$TMP_SML")
  if [[ "$HTTP_STATUS" == "404" ]]; then
    HTTP_STATUS=$(curl -s -w "%{http_code}" -u "admin:${PASSWORD}" "http://${BRIDGE_IP}/data.json?node_id=${NODE_ID}" -o "$TMP_SML")
  fi
  SML_LEN=$(wc -c < "$TMP_SML" | tr -d ' ')
  if [[ "$SML_LEN" -gt 0 && "$HTTP_STATUS" == "200" ]]; then
    break
  fi
  sleep 1
done

echo "  ✓ Received $SML_LEN bytes binary SML data"
DECODED_OUTPUT=$(go run ./cmd/sml-inspect -file "$TMP_SML" -json 2>/dev/null || true)
rm -f "$TMP_SML"

SERIAL=$(echo "$DECODED_OUTPUT" | python3 -c "import sys, json; data=json.load(sys.stdin); print(next((x['Raw'] for x in data if x.get('Name')=='meter_serial'), ''))" 2>/dev/null || true)
MFR=$(echo "$DECODED_OUTPUT" | python3 -c "import sys, json; data=json.load(sys.stdin); print(next((x['Raw'] for x in data if x.get('Name')=='manufacturer'), ''))" 2>/dev/null || true)
POWER=$(echo "$DECODED_OUTPUT" | python3 -c "import sys, json; data=json.load(sys.stdin); print(next((x['Value'] for x in data if x.get('Name')=='power_total'), ''))" 2>/dev/null || true)

if [[ -n "$SERIAL" ]]; then
  echo "  ✓ SML Decoded: Manufacturer=${MFR}, Serial=${SERIAL}, Power=${POWER} W"
else
  echo "  ✗ SML Decoding failed or no readings found!" >&2
  exit 1
fi

# 4. Live WebSocket Push Stream Test
echo ""
echo "[4/5] Testing live WebSocket push stream (--mode push, --reconnect-delay 100ms)..."
go build -o /tmp/tibber-pulse-bot-livetest ./cmd/tibber-pulse-bot

# Run the bot in push mode for 6 seconds
PUSH_LOG=$(python3 -c '
import subprocess, time, sys

proc = subprocess.Popen(
    ["/tmp/tibber-pulse-bot-livetest",
     "--pulse-host", sys.argv[1],
     "--pulse-password", sys.argv[2],
     "--mode", "push",
     "--reconnect-delay", "100ms"],
    stdout=subprocess.PIPE,
    stderr=subprocess.STDOUT,
    text=True
)
time.sleep(6)
proc.terminate()
try:
    stdout, _ = proc.communicate(timeout=2)
except Exception:
    proc.kill()
    stdout, _ = proc.communicate()
print(stdout)
' "$BRIDGE_IP" "$PASSWORD")

TELEGRAM_COUNT=$(echo "$PUSH_LOG" | grep -c -E 'readings\)|power=' || true)
echo "  ✓ Received $TELEGRAM_COUNT telegram(s) in 6 seconds"
if [[ $TELEGRAM_COUNT -gt 0 ]]; then
  echo "  ✓ Sample reading: $(echo "$PUSH_LOG" | grep -E 'power_total|power=' | head -n1 | tr -d ' ')"
fi

if echo "$PUSH_LOG" | grep -q 'bridge:'; then
  echo "  ✓ Diagnostics:    $(echo "$PUSH_LOG" | grep 'bridge:' | head -n1)"
fi
if echo "$PUSH_LOG" | grep -q 'switching to poll mode'; then
  echo "  ! Bot fell back from push to poll — /ws push path was NOT exercised:"
  echo "    $(echo "$PUSH_LOG" | grep -E 'falling back' | head -n1)"
fi

# 5. Optional MQTT round-trip. Uses its own topic/discovery prefix and client id
# so it never kicks the production bot off the broker or flips the production
# availability topic to offline in Home Assistant.
echo ""
echo "[5/5] MQTT round-trip against ${MQTT_HOST}..."
LT_PREFIX="tibber-livetest/pulse"
LT_DISCOVERY="tibber-livetest/homeassistant"
# Skipping is explicit: CLAUDE.md's verification protocol requires the MQTT
# round-trip, so an unavailable broker fails the run unless the operator opts
# out with LIVE_TEST_SKIP_MQTT=1 (and then must not tick the $live-test gate).
MQTT_SKIPPED=""
if [[ "${LIVE_TEST_SKIP_MQTT:-}" == 1 ]]; then
  MQTT_SKIPPED="LIVE_TEST_SKIP_MQTT=1"
elif ! command -v mosquitto_pub >/dev/null 2>&1 || ! command -v mosquitto_sub >/dev/null 2>&1; then
  echo "  ✗ mosquitto_pub/mosquitto_sub not installed (set LIVE_TEST_SKIP_MQTT=1 to skip)" >&2
  rm -f /tmp/tibber-pulse-bot-livetest; exit 1
# A plain publish proves the broker accepts connections on any broker, unlike
# reading $SYS topics, which may be disabled or ACL-blocked.
elif ! timeout 5 mosquitto_pub -h "$MQTT_HOST" -t "${LT_PREFIX}/probe" -n >/dev/null 2>&1; then
  echo "  ✗ broker ${MQTT_HOST} not reachable (set LIVE_TEST_SKIP_MQTT=1 to skip)" >&2
  rm -f /tmp/tibber-pulse-bot-livetest; exit 1
fi
if [[ -n "$MQTT_SKIPPED" ]]; then
  echo "  - skipped ($MQTT_SKIPPED)"
else
  MQTT_LOG=$(mktemp /tmp/mqtt_live_XXXXXX.log)
  mosquitto_sub -h "$MQTT_HOST" -v -W 20 \
    -t "${LT_PREFIX}/#" -t "${LT_DISCOVERY}/+/+/config" >"$MQTT_LOG" 2>/dev/null &
  SUB_PID=$!
  sleep 1
  timeout -s INT 15 /tmp/tibber-pulse-bot-livetest \
    --pulse-host "$BRIDGE_IP" --pulse-password "$PASSWORD" \
    --mqtt-host "$MQTT_HOST" --mqtt-topic "$LT_PREFIX" \
    --mqtt-client-id "tibber-pulse-bot-livetest-$$" \
    --ha-discovery --ha-discovery-prefix "$LT_DISCOVERY" --quiet >/dev/null 2>&1 || true
  wait "$SUB_PID" 2>/dev/null || true

  check() { grep -qE "$1" "$MQTT_LOG" && echo "  ✓ $2" || { echo "  ✗ $2" >&2; MQTT_FAIL=1; }; }
  MQTT_FAIL=0
  check "^${LT_PREFIX}/readings .*power_total" "readings JSON with power_total"
  check "^${LT_PREFIX}/status online" "availability topic went online"
  check "^${LT_PREFIX}/status offline" "availability topic went offline on shutdown"
  check "^${LT_DISCOVERY}/.*\"availability_topic\":\"${LT_PREFIX}/status\"" "discovery configs carry availability_topic"

  # Clear the retained test topics again.
  if command -v mosquitto_pub >/dev/null 2>&1; then
    for t in "${LT_PREFIX}/status" $(grep -oE "^${LT_DISCOVERY}/[^ ]+/config" "$MQTT_LOG" | sort -u); do
      mosquitto_pub -h "$MQTT_HOST" -r -n -t "$t" || true
    done
  fi
  rm -f "$MQTT_LOG"
  [[ $MQTT_FAIL -eq 0 ]] || { rm -f /tmp/tibber-pulse-bot-livetest; exit 1; }
fi
rm -f /tmp/tibber-pulse-bot-livetest

# Summary
echo ""
echo "==================================================================="
if [[ -n "$MQTT_SKIPPED" ]]; then
  echo "✓ BRIDGE TESTS PASSED AGAINST $BRIDGE_IP — MQTT round-trip SKIPPED"
  echo "  (not sufficient for the \$live-test merge gate)"
else
  echo "✓ ALL LIVE TESTS COMPLETED SUCCESSFULLY AGAINST $BRIDGE_IP"
fi
echo "==================================================================="
