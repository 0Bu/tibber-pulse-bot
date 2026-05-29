package output

import "testing"

func TestFormatStatePayload(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"bool true", true, "ON"},
		{"bool false", false, "OFF"},
		{"float64", 3.14159, "3.142"},
		{"int", 42, "42"},
		{"int64", int64(123), "123"},
		{"unknown type skipped", "string", ""},
		{"nil skipped", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatStatePayload(tt.in); got != tt.want {
				t.Errorf("formatStatePayload(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestShort(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"power_total", "P"},
		{"energy_import_total", "Eimp"},
		{"energy_export_total", "Eexp"},
		{"voltage_l1", "U1"},
		{"current_l3", "I3"},
		{"frequency", "f"},
		{"unmapped_key", "unmapped_key"},
	}
	for _, tt := range tests {
		if got := short(tt.in); got != tt.want {
			t.Errorf("short(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
