package discovery

import "testing"

func TestBridgeIdentifier(t *testing.T) {
	t.Run("prefers EUI", func(t *testing.T) {
		b := BridgeDevice{Host: "192.168.1.5", EUI: "30FB10FFFE9326A9"}
		want := "tibber-pulse-bridge-30fb10fffe9326a9"
		if got := b.Identifier(); got != want {
			t.Errorf("Identifier = %q, want %q", got, want)
		}
	})
	t.Run("falls back to host when EUI missing", func(t *testing.T) {
		b := BridgeDevice{Host: "192.168.1.5"}
		want := "tibber-pulse-bridge-192_168_1_5"
		if got := b.Identifier(); got != want {
			t.Errorf("Identifier = %q, want %q", got, want)
		}
	})
}

func TestFormatEUI(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"30FB10FFFE9326A9", "30:fb:10:ff:fe:93:26:a9"},
		{"shorthex", "shorthex"}, // not 16 chars → unchanged
		{"", ""},
	}
	for _, tt := range tests {
		if got := formatEUI(tt.in); got != tt.want {
			t.Errorf("formatEUI(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBestModel(t *testing.T) {
	if got := (BridgeDevice{}).bestModel(); got != "Pulse Bridge" {
		t.Errorf("bestModel empty = %q, want Pulse Bridge", got)
	}
	if got := (BridgeDevice{ProductModel: "TFD01"}).bestModel(); got != "Pulse Bridge TFD01" {
		t.Errorf("bestModel = %q, want Pulse Bridge TFD01", got)
	}
}

func TestComposedSWVersion(t *testing.T) {
	tests := []struct {
		name string
		b    BridgeDevice
		want string
	}{
		{"both ESP and EFR", BridgeDevice{ESPVersion: "1.2", EFRVersion: "3.4"}, "ESP 1.2 / EFR 3.4"},
		{"node version only", BridgeDevice{NodeVersion: "n9"}, "n9"},
		{"esp only", BridgeDevice{ESPVersion: "1.2"}, "ESP 1.2"},
		{"none", BridgeDevice{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.b.composedSWVersion(); got != tt.want {
				t.Errorf("composedSWVersion = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBridgeConfigTopic(t *testing.T) {
	b := BridgeDevice{EUI: "30FB10FFFE9326A9"}
	t.Run("sensor component default", func(t *testing.T) {
		got := BridgeConfigTopic("homeassistant", "rssi", BridgeSensors["rssi"], b)
		want := "homeassistant/sensor/tibber-pulse-bridge-30fb10fffe9326a9_rssi/config"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("binary_sensor component", func(t *testing.T) {
		got := BridgeConfigTopic("homeassistant", "available", BridgeSensors["available"], b)
		want := "homeassistant/binary_sensor/tibber-pulse-bridge-30fb10fffe9326a9_available/config"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
