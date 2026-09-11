package sml

import (
	"testing"

	gosml "github.com/andig/gosml"

	"github.com/0Bu/tibber-pulse-bot/internal/discovery"
)

// stringReadings are OBIS names that carry text (decoded separately), not a
// numeric value — they intentionally have no HA sensor discovery entry.
var stringReadings = map[string]bool{
	"device_id":    true,
	"server_id":    true,
	"manufacturer": true,
}

// TestObisNamesHaveDiscoverySpecs guards the invariant from CLAUDE.md: every
// numeric OBIS name the parser can emit must have HA discovery metadata, or
// HA silently never surfaces it.
func TestObisNamesHaveDiscoverySpecs(t *testing.T) {
	for _, name := range obisNames {
		if stringReadings[name] {
			continue
		}
		if _, ok := discovery.Sensors[name]; !ok {
			t.Errorf("obisNames has %q but discovery.Sensors does not — HA won't surface it", name)
		}
	}
}

func TestObisString(t *testing.T) {
	tests := []struct {
		name string
		in   gosml.OctetString
		want string
	}{
		{"power_total", gosml.OctetString{1, 0, 16, 7, 0, 255}, "1-0:16.7.0*255"},
		{"energy_import", gosml.OctetString{1, 0, 1, 8, 0, 255}, "1-0:1.8.0*255"},
		{"short falls back to hex", gosml.OctetString{0xAB, 0xCD}, "abcd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := obisString(tt.in); got != tt.want {
				t.Errorf("obisString(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestObisName(t *testing.T) {
	tests := []struct {
		name string
		in   gosml.OctetString
		want string
	}{
		{"known power_total", gosml.OctetString{1, 0, 16, 7, 0, 255}, "power_total"},
		{"known server_id", gosml.OctetString{1, 0, 96, 1, 0, 255}, "server_id"},
		{"known device_id", gosml.OctetString{1, 0, 0, 0, 9, 255}, "device_id"},
		{"unknown returns empty", gosml.OctetString{1, 0, 99, 99, 0, 255}, ""},
		{"too short returns empty", gosml.OctetString{1, 0, 16}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := obisName(tt.in); got != tt.want {
				t.Errorf("obisName(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUnitSymbol(t *testing.T) {
	tests := []struct {
		in   uint8
		want string
	}{
		{0x1B, "W"},
		{0x1E, "Wh"},
		{0x21, "A"},
		{0x23, "V"},
		{0x2C, "Hz"},
		{0xFF, ""}, // unknown
	}
	for _, tt := range tests {
		if got := unitSymbol(tt.in); got != tt.want {
			t.Errorf("unitSymbol(0x%02X) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEntryToReadingScaling(t *testing.T) {
	tests := []struct {
		name   string
		typ    uint8
		dataI  int64
		scaler int8
		want   float64
	}{
		{"negative scaler", gosml.TYPEINTEGER, 23456, -1, 2345.6},
		{"zero scaler", gosml.TYPEUNSIGNED, 42, 0, 42},
		{"positive scaler", gosml.TYPEUNSIGNED, 5, 2, 500},
		{"negative value", gosml.TYPEINTEGER, -1500, -1, -150},
		{"unsigned 16-bit with MSB set", gosml.TYPEUNSIGNED | 2, -25536, 0, 40000},
		{"unsigned 32-bit with MSB set", gosml.TYPEUNSIGNED | 4, -1, 0, 4294967295},
		{"unsigned 8-bit with MSB set", gosml.TYPEUNSIGNED | 1, -1, 0, 255},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := gosml.ListEntry{
				ObjName: gosml.OctetString{1, 0, 16, 7, 0, 255},
				Unit:    0x1B,
				Scaler:  tt.scaler,
				Value:   gosml.Value{Typ: tt.typ, DataInt: tt.dataI},
			}
			r := entryToReading(e)
			if r.Value != tt.want {
				t.Errorf("entryToReading value = %v, want %v", r.Value, tt.want)
			}
			if r.Name != "power_total" || r.Unit != "W" {
				t.Errorf("entryToReading name/unit = %q/%q, want power_total/W", r.Name, r.Unit)
			}
		})
	}
}

func TestEntryToReadingOctetString(t *testing.T) {
	e := gosml.ListEntry{
		ObjName: gosml.OctetString{1, 0, 96, 1, 0, 255},
		Value:   gosml.Value{Typ: 0x00, DataBytes: gosml.OctetString{0x0A, 0x01, 0xDE, 0xAD}},
	}
	r := entryToReading(e)
	if r.Raw != "0a01dead" {
		t.Errorf("Raw = %q, want 0a01dead", r.Raw)
	}
	if r.Name != "server_id" {
		t.Errorf("Name = %q, want server_id", r.Name)
	}

	eDev := gosml.ListEntry{
		ObjName: gosml.OctetString{1, 0, 0, 0, 9, 255},
		Value:   gosml.Value{Typ: 0x00, DataBytes: gosml.OctetString{0x01, 0x02, 0x03}},
	}
	rDev := entryToReading(eDev)
	if rDev.Name != "device_id" {
		t.Errorf("Name = %q, want device_id", rDev.Name)
	}
}

func TestDerivedReadings(t *testing.T) {
	// FNN server-ID: [0x0A][0x01]["LGZ"][version]["serial BE 4 bytes"]
	// serial 81199038 = 0x04D6FFBE
	valid := gosml.OctetString{0x0A, 0x01, 'L', 'G', 'Z', 0x01, 0x04, 0xD6, 0xFF, 0xBE}

	t.Run("valid server_id decodes manufacturer and serial", func(t *testing.T) {
		r := Reading{Name: "server_id", OBIS: "1-0:96.1.0*255"}
		e := gosml.ListEntry{Value: gosml.Value{DataBytes: valid}}
		got := derivedReadings(r, e)
		if len(got) != 2 {
			t.Fatalf("got %d derived readings, want 2", len(got))
		}
		if got[0].Name != "manufacturer" || got[0].Raw != "LGZ" {
			t.Errorf("manufacturer = %+v, want LGZ", got[0])
		}
		if got[1].Name != "meter_serial" || got[1].Raw != "LGZ-81199038" {
			t.Errorf("meter_serial = %q, want LGZ-81199038", got[1].Raw)
		}
	})

	t.Run("device_id reading is ignored by derivedReadings (Bug 9)", func(t *testing.T) {
		r := Reading{Name: "device_id", OBIS: "1-0:0.0.9*255"}
		e := gosml.ListEntry{Value: gosml.Value{DataBytes: valid}}
		if got := derivedReadings(r, e); got != nil {
			t.Errorf("want nil for device_id, got %v", got)
		}
	})

	t.Run("non server_id reading is ignored", func(t *testing.T) {
		r := Reading{Name: "power_total"}
		e := gosml.ListEntry{Value: gosml.Value{DataBytes: valid}}
		if got := derivedReadings(r, e); got != nil {
			t.Errorf("want nil for non-server_id, got %v", got)
		}
	})

	t.Run("too few bytes is ignored", func(t *testing.T) {
		r := Reading{Name: "server_id"}
		e := gosml.ListEntry{Value: gosml.Value{DataBytes: gosml.OctetString{0x0A, 0x01, 'L'}}}
		if got := derivedReadings(r, e); got != nil {
			t.Errorf("want nil for short payload, got %v", got)
		}
	})

	t.Run("non A-Z manufacturer is ignored", func(t *testing.T) {
		bad := gosml.OctetString{0x0A, 0x01, 'L', '2', 'Z', 0x01, 0x04, 0xD6, 0xFF, 0xBE}
		r := Reading{Name: "server_id"}
		e := gosml.ListEntry{Value: gosml.Value{DataBytes: bad}}
		if got := derivedReadings(r, e); got != nil {
			t.Errorf("want nil for non-A-Z manufacturer, got %v", got)
		}
	})

	t.Run("missing DIN 43863-5 FNN prefix (0x0A 0x01) is ignored", func(t *testing.T) {
		badPrefix := gosml.OctetString{0x00, 0x00, 'L', 'G', 'Z', 0x01, 0x04, 0xD6, 0xFF, 0xBE}
		r := Reading{Name: "server_id"}
		e := gosml.ListEntry{Value: gosml.Value{DataBytes: badPrefix}}
		if got := derivedReadings(r, e); got != nil {
			t.Errorf("want nil for missing 0x0A 0x01 prefix, got %v", got)
		}
	})
}

func TestManufacturerDecoding(t *testing.T) {
	t.Run("uppercase ASCII preserved", func(t *testing.T) {
		e := gosml.ListEntry{
			ObjName: gosml.OctetString{129, 129, 199, 130, 3, 255},
			Value:   gosml.Value{Typ: 0x00, DataBytes: gosml.OctetString{'L', 'G', 'Z'}},
		}
		r := entryToReading(e)
		if r.Name != "manufacturer" {
			t.Fatalf("Name = %q, want manufacturer", r.Name)
		}
		if r.Raw != "LGZ" {
			t.Errorf("Raw = %q, want LGZ (not hex 4c475a)", r.Raw)
		}
	})

	t.Run("lowercase ASCII normalized to uppercase", func(t *testing.T) {
		e := gosml.ListEntry{
			ObjName: gosml.OctetString{129, 129, 199, 130, 3, 255},
			Value:   gosml.Value{Typ: 0x00, DataBytes: gosml.OctetString{'e', 'm', 'h'}},
		}
		r := entryToReading(e)
		if r.Name != "manufacturer" {
			t.Fatalf("Name = %q, want manufacturer", r.Name)
		}
		if r.Raw != "EMH" {
			t.Errorf("Raw = %q, want EMH", r.Raw)
		}
	})

	t.Run("null-terminated ASCII trimmed and preserved", func(t *testing.T) {
		e := gosml.ListEntry{
			ObjName: gosml.OctetString{129, 129, 199, 130, 3, 255},
			Value:   gosml.Value{Typ: 0x00, DataBytes: gosml.OctetString{'I', 'S', 'K', 0x00}},
		}
		r := entryToReading(e)
		if r.Name != "manufacturer" {
			t.Fatalf("Name = %q, want manufacturer", r.Name)
		}
		if r.Raw != "ISK" {
			t.Errorf("Raw = %q, want ISK (trimmed null byte)", r.Raw)
		}
	})
}

func buildTestSMLFrame() []byte {
	msg := []byte{
		0x76,       // List of 6 (Message)
		0x01,       // TransactionID (optional skipped)
		0x62, 0x00, // GroupID (unsigned 0)
		0x62, 0x00, // AbortOnError (unsigned 0)
		0x72,                         // MessageBody: List of 2
		0x65, 0x00, 0x00, 0x07, 0x01, // Tag = 0x00000701 (GetListResponse)
		0x77,                                                          // GetListResponse: List of 7
		0x01,                                                          // ClientID (skipped)
		0x0B, 0x0A, 0x01, 'L', 'G', 'Z', 0x01, 0x04, 0xD6, 0xFF, 0xBE, // ServerID: 10 bytes FNN
		0x01, // ListName (skipped)
		0x01, // ActSensorTime (skipped)
		0x72, // ValList: List of 2
		// Entry 1: server_id (1-0:96.1.0*255)
		0x77,                                     // ListEntry: List of 7
		0x07, 0x01, 0x00, 0x60, 0x01, 0x00, 0xFF, // ObjName: 1-0:96.1.0*255
		0x01,                                                          // Status (skipped)
		0x01,                                                          // ValTime (skipped)
		0x01,                                                          // Unit (skipped)
		0x01,                                                          // Scaler (skipped)
		0x0B, 0x0A, 0x01, 'L', 'G', 'Z', 0x01, 0x04, 0xD6, 0xFF, 0xBE, // Value: OctetString 10 bytes FNN
		0x01, // ValueSignature (skipped)
		// Entry 2: power_total (1-0:16.7.0*255)
		0x77,                                     // ListEntry: List of 7
		0x07, 0x01, 0x00, 0x10, 0x07, 0x00, 0xFF, // ObjName: 1-0:16.7.0*255 (power_total)
		0x01,       // Status (skipped)
		0x01,       // ValTime (skipped)
		0x62, 0x1B, // Unit: 0x1B (W)
		0x52, 0x00, // Scaler: 0
		0x63, 0x01, 0x2C, // Value: unsigned 16-bit 300
		0x01,             // ValueSignature (skipped)
		0x01,             // ListSignature (skipped)
		0x01,             // ActGatewayTime (skipped)
		0x63, 0x00, 0x00, // CRC placeholder (3 bytes)
		0x00, // End of message (1 byte)
	}

	// Calculate message CRC for bytes up to before 0x63 CRC field
	crc := gosml.Crc16Calculate(msg[:len(msg)-4], len(msg)-4)
	msg[len(msg)-3] = byte(crc >> 8)
	msg[len(msg)-2] = byte(crc & 0xFF)

	pad := (4 - (len(msg) % 4)) % 4
	for i := 0; i < pad; i++ {
		msg = append(msg, 0x00)
	}

	startSeq := []byte{0x1b, 0x1b, 0x1b, 0x1b, 0x01, 0x01, 0x01, 0x01}
	endSeq := []byte{0x1b, 0x1b, 0x1b, 0x1b, 0x1a, byte(pad), 0x00, 0x00}

	frame := append(startSeq, msg...)
	frame = append(frame, endSeq...)
	return frame
}

func TestParseFrames(t *testing.T) {
	t.Run("empty payload returns nil nil", func(t *testing.T) {
		readings, err := ParseFrames(nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(readings) != 0 {
			t.Errorf("got %d readings, want 0", len(readings))
		}
	})

	t.Run("truncated payload without start escape yields empty readings without crash", func(t *testing.T) {
		readings, err := ParseFrames([]byte{0x1b, 0x1b, 0x1b})
		if err != nil {
			t.Errorf("unexpected error for partial frame: %v", err)
		}
		if len(readings) != 0 {
			t.Errorf("got %d readings, want 0", len(readings))
		}
	})

	t.Run("corrupted frame body returns error (Bug 2 regression test)", func(t *testing.T) {
		startSeq := []byte{0x1b, 0x1b, 0x1b, 0x1b, 0x01, 0x01, 0x01, 0x01}
		corruptBody := []byte{0xFF, 0xFF, 0xFF, 0xFF}
		endSeq := []byte{0x1b, 0x1b, 0x1b, 0x1b, 0x1a, 0x00, 0x00, 0x00}
		payload := append(startSeq, corruptBody...)
		payload = append(payload, endSeq...)

		readings, err := ParseFrames(payload)
		if err == nil {
			t.Error("want error for corrupted SML body, got nil")
		}
		if len(readings) != 0 {
			t.Errorf("want 0 readings, got %d", len(readings))
		}
	})

	t.Run("valid SML frame produces readings and derived fields", func(t *testing.T) {
		payload := buildTestSMLFrame()
		readings, err := ParseFrames(payload)
		if err != nil {
			t.Fatalf("ParseFrames failed: %v", err)
		}
		if len(readings) < 3 {
			t.Fatalf("got %d readings, want at least 3 (power_total, manufacturer, meter_serial)", len(readings))
		}

		byName := make(map[string]Reading)
		for _, r := range readings {
			byName[r.Name] = r
		}

		power, ok := byName["power_total"]
		if !ok {
			t.Fatal("missing power_total reading")
		}
		if power.Value != 300 {
			t.Errorf("power_total value = %v, want 300", power.Value)
		}
		if power.Unit != "W" {
			t.Errorf("power_total unit = %q, want W", power.Unit)
		}

		mfg, ok := byName["manufacturer"]
		if !ok {
			t.Fatal("missing derived manufacturer reading")
		}
		if mfg.Raw != "LGZ" {
			t.Errorf("manufacturer = %q, want LGZ", mfg.Raw)
		}

		serial, ok := byName["meter_serial"]
		if !ok {
			t.Fatal("missing derived meter_serial reading")
		}
		if serial.Raw != "LGZ-81199038" {
			t.Errorf("meter_serial = %q, want LGZ-81199038", serial.Raw)
		}
	})
}
