---
name: sml-inspect
description: Inspect, decode, and validate binary SML 1.04 telegrams or hex dumps. Extracts OBIS entries, checks DIN 43863-5 FNN server-ID, manufacturer, meter serial, and calculates CRC16 checksums.
disable-model-invocation: true
---

# sml-inspect

Inspect and decode Smart Message Language (SML 1.04) telegrams from electricity meters.
Use this skill when analyzing meter compatibility, diagnosing corrupted telegrams, or adding support for new OBIS codes.

## 1. Quick decode from hex string

To inspect a hex-encoded SML telegram (e.g. from bridge logs or capture tools):

```bash
go run ./cmd/sml-inspect -hex "1b1b1b1b01010101..."
```

Add `-json` to format decoded readings as JSON:

```bash
go run ./cmd/sml-inspect -json -hex "1b1b1b1b01010101..."
```

You can also run the parser test suite against telegram samples:

```bash
go test -v ./internal/sml -run TestParseFrames
```

## 2. Decode raw SML binary or stream from file or bridge

To decode a binary or hex dump file `telegram.sml`:

```bash
go run ./cmd/sml-inspect -file telegram.sml
```

Or pipe a live reading directly from the local bridge:

```bash
curl -s -u "admin:$TIBBER_PULSE_PASSWORD" "http://<bridge-ip>/node_data.json?node_id=1" | go run ./cmd/sml-inspect
# legacy firmware: /data.json?node_id=1
```

## 3. Verify OBIS code mapping

When adding support for a new OBIS code:
1. Look up the 6-byte OBIS identifier: `A-B:C.D.E*F`
   - `A`: Medium (1 = electricity)
   - `B`: Channel (0 = no channel specified)
   - `C`: Physical value (e.g. 1 = active energy import, 16 = active power)
   - `D`: Measurement type (e.g. 8 = total / time integral, 7 = instantaneous)
   - `E`: Tariff (0 = total, 1 = T1, 2 = T2)
   - `F`: Historical value (255 = current value)
2. Add the code to `obisNames` in `internal/sml/parse.go`.
3. Add the corresponding Home Assistant discovery metadata to `discovery.Sensors` in `internal/discovery/discovery.go`.
4. Run `go test ./internal/sml -run TestObisNamesHaveDiscoverySpecs` to verify parity.

## 4. DIN 43863-5 Server-ID Rules

Electricity meter server-IDs follow DIN 43863-5:
- Exact length: 10 bytes
- Byte 0: `0x0A` (length 10)
- Byte 1: `0x01` (medium electricity)
- Bytes 2-4: 3-character uppercase ASCII manufacturer code (e.g. `LGZ`, `EMH`, `ESY`, `ITZ`, `ISK`)
- Byte 5: Generation/version
- Bytes 6-9: 4-byte big-endian serial number
