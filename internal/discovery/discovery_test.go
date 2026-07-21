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
	dev := Device{MeterSerial: "LGZ-81199038", Manufacturer: "LGZ", BridgeHost: "192.168.1.5"}
	spec := Sensors["power_total"]
	cfg := BuildConfig("power_total", spec, dev, "tibber/pulse/readings")

	if cfg["unique_id"] != "tibber_pulse_lgz_81199038_power_total" {
		t.Errorf("unique_id = %v", cfg["unique_id"])
	}
	if cfg["state_topic"] != "tibber/pulse/readings" {
		t.Errorf("state_topic = %v", cfg["state_topic"])
	}
	if cfg["value_template"] != "{{ value_json.power_total }}" {
		t.Errorf("value_template = %v", cfg["value_template"])
	}
	device := cfg["device"].(map[string]any)
	if device["configuration_url"] != "http://192.168.1.5/" {
		t.Errorf("configuration_url = %v", device["configuration_url"])
	}
	if _, hasVia := device["via_device"]; hasVia {
		t.Error("combined device must not have via_device")
	}
}

func TestDiagnosticConfig(t *testing.T) {
	dev := Device{MeterSerial: "LGZ-81199038"}
	spec := Diagnostics["bridge_available"]
	cfg := BuildConfig("bridge_available", spec, dev, "tibber/pulse/diagnostics")
	if cfg["entity_category"] != "diagnostic" {
		t.Errorf("entity_category = %v", cfg["entity_category"])
	}
	if cfg["value_template"] != "{{ 'ON' if value_json.bridge_available else 'OFF' }}" {
		t.Errorf("value_template = %v", cfg["value_template"])
	}
	if _, ok := cfg["unit_of_measurement"]; ok {
		t.Error("empty unit must be omitted")
	}
}

func TestConfigTopic(t *testing.T) {
	dev := Device{MeterSerial: "LGZ-81199038"}
	got := ConfigTopic("homeassistant", "power_total", Sensors["power_total"], dev)
	want := "homeassistant/sensor/tibber_pulse_lgz_81199038_power_total/config"
	if got != want {
		t.Errorf("ConfigTopic = %q, want %q", got, want)
	}
	got = ConfigTopic("homeassistant", "bridge_available", Diagnostics["bridge_available"], dev)
	want = "homeassistant/binary_sensor/tibber_pulse_lgz_81199038_bridge_available/config"
	if got != want {
		t.Errorf("diagnostic ConfigTopic = %q, want %q", got, want)
	}
}
