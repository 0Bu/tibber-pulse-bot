package output

import (
	"testing"

	"github.com/0Bu/tibber-pulse-bot/internal/discovery"
	"github.com/0Bu/tibber-pulse-bot/internal/pulse"
)

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
		{"string verbatim", "1.2.3", "1.2.3"},
		{"empty string skipped", "", ""},
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

func TestBridgeState(t *testing.T) {
	var st pulse.Status
	st.PairingStatus = "PAIRED"
	st.UpTime = 12345 // 10ms ticks → 123.45 s
	st.WiFi.IP = "192.168.1.5"
	st.WiFi.SSID = "home"
	st.WiFi.RSSI = -55
	st.WiFi.Connected = true

	u := BridgeUpdate{
		Metrics: pulse.Metrics{
			BatteryVoltage: 3.3, RadioTxPower: 14, MeterMode: 2,
			ProductID: 7, NodeVersion: "n9",
		},
		Node: &pulse.Node{
			NodeID: 1, EUI: "30FB10FFFE9326A9", Model: "node-efr32",
			Version: "", Available: true, Paired: true, AverageRSSI: -70,
		},
		Status: &st,
		OTA: []pulse.OTAEntry{
			{Model: "tibber-pulse-ir-hub-esp32", OTAIndex: 0, CurrentVersion: "1.2", ManifestVersion: "1.3", Up2Date: false},
		},
	}
	values, dyn := bridgeState(u)

	// scalar fields from each source
	for _, k := range []string{
		"battery_voltage", "radio_tx_power", "meter_mode", "product_id", "node_version",
		"node_id", "eui", "model", "available", "paired", "average_rssi",
		"pairing_status", "wifi_ip", "wifi_ssid", "wifi_rssi", "wifi_connected",
		"update_available",
	} {
		if _, ok := values[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	// empty string fields are dropped (Node.Version is "")
	if _, ok := values["version"]; ok {
		t.Error("empty string field 'version' should be dropped")
	}
	// integer identifiers stay int, not float64
	if v, ok := values["product_id"].(int); !ok || v != 7 {
		t.Errorf("product_id = %v (%T), want int 7", values["product_id"], values["product_id"])
	}
	// per-component OTA topics with dynamic discovery specs
	cv := "ota_tibber_pulse_ir_hub_esp32_current_version"
	ud := "ota_tibber_pulse_ir_hub_esp32_up2date"
	if values[cv] != "1.2" {
		t.Errorf("%s = %v, want 1.2", cv, values[cv])
	}
	if _, ok := dyn[cv]; !ok {
		t.Errorf("missing dynamic spec for %q", cv)
	}
	if dyn[ud].Component != "binary_sensor" {
		t.Errorf("%s component = %q, want binary_sensor", ud, dyn[ud].Component)
	}
	if values["update_available"] != true {
		t.Error("update_available should be true when a component is out of date")
	}
	// bridge up_time is 10ms FreeRTOS ticks → seconds (÷100), not raw/ms
	if v, ok := values["bridge_uptime"].(float64); !ok || v != 123.45 {
		t.Errorf("bridge_uptime = %v (%T), want float64 123.45", values["bridge_uptime"], values["bridge_uptime"])
	}
}

// TestBridgeStateHasDiscoverySpecs is the bridge-side counterpart of
// internal/sml.TestObisNamesHaveDiscoverySpecs: every field bridgeState can
// emit must have HA discovery metadata, either a static entry in
// discovery.BridgeSensors or a dynamic spec returned alongside the values
// (the per-OTA-component sensors). A field published without a spec is
// silently invisible in Home Assistant — the regression CLAUDE.md warns about.
func TestBridgeStateHasDiscoverySpecs(t *testing.T) {
	// Populate every decoded field with a non-empty value so none of the
	// string fields are dropped — we want the full key set under test.
	u := BridgeUpdate{
		Metrics: pulse.Metrics{
			BatteryVoltage: 3.3, Temperature: 21, AvgRSSI: -60, AvgLQI: 200,
			RadioTxPower: 14, UptimeMS: 1000, MeterMsgCountSent: 1,
			MeterPkgCountSent: 1, InvalidMeterReadings: 0, MeterMode: 2,
			BootloaderVersion: 5, ProductID: 7, MeterPkgCountRecv: 1,
			MeterReadingCountRecv: 1, MeterCorruptCountRecv: 0,
			CompressionErrorReadings: 0, NodeVersion: "n9",
		},
		Node: &pulse.Node{
			NodeID: 1, EUI: "30FB10FFFE9326A9", ProductModel: "TFD01",
			Model: "node-efr32", Version: "v1", Available: true,
			LastSeenMS: 1000, LastDataMS: 1000, AverageRSSI: -70,
			AverageLQI: 180, OTADistributeStatus: "idle", Paired: true,
		},
		Status: func() *pulse.Status {
			var s pulse.Status
			s.PairingStatus = "PAIRED"
			s.UpTime = 12345
			s.Firmware.ESP = "1.2"
			s.Firmware.EFR = "3.4"
			s.WiFi.IP = "192.168.1.5"
			s.WiFi.SSID = "home"
			s.WiFi.BSSID = "aa:bb:cc:dd:ee:ff"
			s.WiFi.RSSI = -55
			s.WiFi.Connected = true
			s.MQTT.Connected = true
			s.MQTT.Subscribed = true
			s.OTAUpdateRunning = false
			return &s
		}(),
		OTA: []pulse.OTAEntry{
			{Model: "tibber-pulse-ir-hub-esp32", OTAIndex: 0, CurrentVersion: "1.2", ManifestVersion: "1.3", Up2Date: false},
		},
	}
	values, dyn := bridgeState(u)
	for name := range values {
		if _, ok := discovery.BridgeSensors[name]; ok {
			continue
		}
		if _, ok := dyn[name]; ok {
			continue
		}
		t.Errorf("bridgeState emits %q but neither discovery.BridgeSensors nor the dynamic spec map covers it — HA won't surface it", name)
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
