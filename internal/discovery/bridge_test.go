package discovery

import "testing"

func TestLegacyBridgeIdentifier(t *testing.T) {
	tests := []struct {
		name string
		dev  LegacyBridgeDevice
		want string
	}{
		{"prefers EUI", LegacyBridgeDevice{Host: "192.168.1.5", EUI: "30FB10FFFE9326A9"}, "tibber-pulse-bridge-30fb10fffe9326a9"},
		{"falls back to host", LegacyBridgeDevice{Host: "192.168.1.5"}, "tibber-pulse-bridge-192_168_1_5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.dev.Identifier(); got != tt.want {
				t.Errorf("Identifier = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLegacyBridgeConfigTopic(t *testing.T) {
	got := LegacyBridgeConfigTopic("homeassistant", "available", "binary_sensor",
		LegacyBridgeDevice{EUI: "30FB10FFFE9326A9"})
	want := "homeassistant/binary_sensor/tibber-pulse-bridge-30fb10fffe9326a9_available/config"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
