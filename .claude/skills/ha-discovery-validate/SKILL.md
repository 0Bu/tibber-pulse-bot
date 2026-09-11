---
name: ha-discovery-validate
description: Validate Home Assistant MQTT Discovery configurations and OBIS parity. Ensures all numeric OBIS entries have discovery specs, verifies device grouping under a single meter serial, and checks retained topic cleanups.
disable-model-invocation: true
---

# ha-discovery-validate

Verify the Home Assistant MQTT Discovery integration contracts:
1. Every numeric OBIS reading parsed by `internal/sml` has a matching sensor entry in `discovery.Sensors`.
2. Readings and bridge diagnostics are grouped together under a single Home Assistant device keyed by the meter serial number.
3. Discovery configs are published with `retain: true`, while state topics (`readings`, `diagnostics`) are `retain: false`.
4. Stale discovery configurations from older bot versions are cleaned up via the retained message sweep.

## 1. Run Discovery Parity Tests

```bash
go test -v ./internal/sml -run TestObisNamesHaveDiscoverySpecs
go test -v ./internal/discovery
go test -v ./internal/output -run "TestDiscovery|TestLegacyBridgeDiscovery|TestEnumerateRetainedConfigs"
```

## 2. Invariants Checklist

When modifying discovery definitions in `internal/discovery/discovery.go`:
- **Identifiers**: Both readings and diagnostics must reference `identifiers: [ dev.MeterSerial ]` (e.g. `["LGZ-81199038"]`).
- **Entity Categories**: Diagnostic sensors (bridge battery voltage, bridge temperature, router WiFi RSSI, meter link RSSI) MUST specify `entity_category: "diagnostic"`.
- **Unique IDs**: Must follow the convention `tibber_pulse_<meter_serial>_<sensor_slug>` (lower-case, sanitized alphanumeric).
- **Parity**: If a new OBIS reading is added in `obisNames`, add a matching `SensorSpec` to `discovery.Sensors` (unless it is a string-valued metadata field like `manufacturer`, `meter_serial`, `device_id`, or `server_id`).
