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
		{"known device_id", gosml.OctetString{1, 0, 96, 1, 0, 255}, "device_id"},
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
	if r.Name != "device_id" {
		t.Errorf("Name = %q, want device_id", r.Name)
	}
}

func TestDerivedReadings(t *testing.T) {
	// FNN server-ID: [0x0A][0x01]["LGZ"][version]["serial BE 4 bytes"]
	// serial 81199038 = 0x04D6FFBE
	valid := gosml.OctetString{0x0A, 0x01, 'L', 'G', 'Z', 0x01, 0x04, 0xD6, 0xFF, 0xBE}

	t.Run("valid server-id decodes manufacturer and serial", func(t *testing.T) {
		r := Reading{Name: "device_id", OBIS: "1-0:96.1.0*255"}
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

	t.Run("non device_id reading is ignored", func(t *testing.T) {
		r := Reading{Name: "power_total"}
		e := gosml.ListEntry{Value: gosml.Value{DataBytes: valid}}
		if got := derivedReadings(r, e); got != nil {
			t.Errorf("want nil for non-device_id, got %v", got)
		}
	})

	t.Run("too few bytes is ignored", func(t *testing.T) {
		r := Reading{Name: "device_id"}
		e := gosml.ListEntry{Value: gosml.Value{DataBytes: gosml.OctetString{0x0A, 0x01, 'L'}}}
		if got := derivedReadings(r, e); got != nil {
			t.Errorf("want nil for short payload, got %v", got)
		}
	})

	t.Run("non A-Z manufacturer is ignored", func(t *testing.T) {
		bad := gosml.OctetString{0x0A, 0x01, 'L', '2', 'Z', 0x01, 0x04, 0xD6, 0xFF, 0xBE}
		r := Reading{Name: "device_id"}
		e := gosml.ListEntry{Value: gosml.Value{DataBytes: bad}}
		if got := derivedReadings(r, e); got != nil {
			t.Errorf("want nil for non-A-Z manufacturer, got %v", got)
		}
	})
}
