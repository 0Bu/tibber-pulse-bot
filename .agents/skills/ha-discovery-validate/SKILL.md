---
name: ha-discovery-validate
description: Validate Home Assistant MQTT Discovery configurations and OBIS parity. Ensures all numeric OBIS entries have discovery specs, verifies device grouping under a single meter serial, the retained availability topic (LWT) and expire_after wiring, and checks retained topic cleanups.
disable-model-invocation: true
---

# ha-discovery-validate

Verify the Home Assistant MQTT Discovery integration contracts:
1. Every numeric OBIS reading parsed by `internal/sml` has a matching sensor entry in `discovery.Sensors`.
2. Readings and bridge diagnostics are grouped together under a single Home Assistant device keyed by the meter serial number.
3. Discovery configs are published with `retain: true`, while state topics (`readings`, `diagnostics`) are `retain: false`.
4. Every entity carries the shared availability topic `<topic-prefix>/status` and, unless disabled, an `expire_after`.
5. Stale discovery configurations from older bot versions are cleaned up via the retained message sweep.

## 1. Run Discovery Parity Tests

```bash
go test -v ./internal/sml -run TestObisNamesHaveDiscoverySpecs
go test -v ./internal/discovery
go test -v ./internal/output -run "TestDiscovery|TestLegacyBridgeDiscovery|TestEnumerateRetainedConfigs|TestMQTTSinkClosePublishesOffline"
go test -v ./cmd/tibber-pulse-bot -run TestCalculateExpiration
```

## 2. Invariants Checklist

When modifying discovery definitions in `internal/discovery/discovery.go` or the
MQTT sink in `internal/output/output.go`:
- **Identifiers**: Both readings and diagnostics must reference `identifiers: [ dev.MeterSerial ]` (e.g. `["LGZ-81199038"]`).
- **Entity Categories**: Diagnostic sensors (bridge battery voltage, bridge temperature, router WiFi RSSI, meter link RSSI) MUST specify `entity_category: "diagnostic"`.
- **Unique IDs**: Must follow the convention `tibber_pulse_<meter_serial>_<sensor_slug>` (lower-case, sanitized alphanumeric).
- **Parity**: If a new OBIS reading is added in `obisNames`, add a matching `SensorSpec` to `discovery.Sensors` (unless it is a string-valued metadata field like `manufacturer`, `meter_serial`, `device_id`, or `server_id`).
- **Availability**: `<topic-prefix>/status` is the only other retained state
  topic. The sink registers it as MQTT Last Will (`offline`, QoS 1, retained),
  publishes `online` in the on-connect handler (so it is restored after every
  broker reconnect), and publishes `offline` in `Close()` before disconnecting.
  Every discovery config sets `availability_topic` plus
  `payload_available: online` / `payload_not_available: offline`.
- **Expiration**: `expire_after` is emitted only when > 0. Readings and
  diagnostics get separate values from `calculateExpiration` in
  `cmd/tibber-pulse-bot/main.go`:
  - `--expire-after 0` (auto): readings 30 s in push mode, `max(30, 3 × --interval)` in poll mode;
    diagnostics `3 × --metrics-interval`, raised to the readings value if that is larger.
  - `--expire-after N > 0`: readings N; diagnostics `max(N, 3 × --metrics-interval)`.
  - `--expire-after < 0`: disabled for both.
  - `--metrics-interval 0`: no `expire_after` on diagnostics.
  The sink chooses between the two values by comparing the state topic to
  `<prefix>/readings` / `<prefix>/diagnostics` — a new state topic needs its own branch there.

## 3. Live check (optional, needs a broker)

```bash
mosquitto_sub -h <broker> -t 'tibber/pulse/status' -t 'homeassistant/+/+/config' -v -W 10 \
  | grep -E 'status (online|offline)|availability_topic|expire_after'
```

Expect a retained `tibber/pulse/status online` while the bot runs, `offline`
after it stops (clean shutdown or LWT on a crash), and `availability_topic` /
`expire_after` inside every config payload.
