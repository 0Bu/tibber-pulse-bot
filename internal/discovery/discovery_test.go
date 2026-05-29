package discovery

import "testing"

func TestSanitize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"LGZ-81199038", "lgz_81199038"},
		{"abc123", "abc123"},
		{"A.B:C*D", "a_b_c_d"},
		{"30FB10FFFE9326A9", "30fb10fffe9326a9"},
	}
	for _, tt := range tests {
		if got := sanitize(tt.in); got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestManufacturerName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"LGZ", "Landis+Gyr"},
		{"lgz", "Landis+Gyr"},
		{"ESY", "EasyMeter"},
		{"XYZ", "XYZ"}, // unknown passes through
	}
	for _, tt := range tests {
		if got := manufacturerName(tt.in); got != tt.want {
			t.Errorf("manufacturerName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBuildConfig(t *testing.T) {
	dev := Device{MeterSerial: "LGZ-81199038", Manufacturer: "LGZ"}
	spec := Sensors["power_total"]
	cfg := BuildConfig("power_total", spec, dev, "tibber/pulse/power_total", "")

	if cfg["unique_id"] != "tibber_pulse_lgz_81199038_power_total" {
		t.Errorf("unique_id = %v", cfg["unique_id"])
	}
	if cfg["state_topic"] != "tibber/pulse/power_total" {
		t.Errorf("state_topic = %v", cfg["state_topic"])
	}
	if _, hasVia := cfg["device"].(map[string]any)["via_device"]; hasVia {
		t.Error("via_device must be absent when bridgeIdentifier is empty")
	}

	cfg2 := BuildConfig("power_total", spec, dev, "tibber/pulse/power_total", "tibber-pulse-bridge-abc")
	if cfg2["device"].(map[string]any)["via_device"] != "tibber-pulse-bridge-abc" {
		t.Error("via_device must be set when bridgeIdentifier is provided")
	}
}

func TestConfigTopic(t *testing.T) {
	dev := Device{MeterSerial: "LGZ-81199038"}
	got := ConfigTopic("homeassistant", "power_total", dev)
	want := "homeassistant/sensor/tibber_pulse_lgz_81199038_power_total/config"
	if got != want {
		t.Errorf("ConfigTopic = %q, want %q", got, want)
	}
}
