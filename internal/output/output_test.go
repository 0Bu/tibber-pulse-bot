package output

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/0Bu/tibber-pulse-bot/internal/discovery"
	"github.com/0Bu/tibber-pulse-bot/internal/pulse"
	"github.com/0Bu/tibber-pulse-bot/internal/sml"
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
			MeterMsgCountSent: 42, CompressionErrorReadings: 3,
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
	// newly added integer counters render as int ("42"), not float64 ("42.000")
	if v, ok := values["meter_msg_sent"].(int); !ok || v != 42 {
		t.Errorf("meter_msg_sent = %v (%T), want int 42", values["meter_msg_sent"], values["meter_msg_sent"])
	}
	if v, ok := values["compression_error_readings"].(int); !ok || v != 3 {
		t.Errorf("compression_error_readings = %v (%T), want int 3", values["compression_error_readings"], values["compression_error_readings"])
	}
	// per-component OTA topics with dynamic discovery specs; the slug is
	// prefixed with the OTAIndex (0 here) to avoid same-model collisions.
	cv := "ota_0_tibber_pulse_ir_hub_esp32_current_version"
	ud := "ota_0_tibber_pulse_ir_hub_esp32_up2date"
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

// TestBridgeStateOTAIndexDisambiguates guards against the collision where two
// OTA components share a model: the OTAIndex must keep their topics distinct
// so neither component's version data is silently overwritten.
func TestBridgeStateOTAIndexDisambiguates(t *testing.T) {
	u := BridgeUpdate{
		OTA: []pulse.OTAEntry{
			{Model: "efr32", OTAIndex: 1, CurrentVersion: "1.0"},
			{Model: "efr32", OTAIndex: 2, CurrentVersion: "2.0"},
		},
	}
	values, _ := bridgeState(u)
	if values["ota_1_efr32_current_version"] != "1.0" {
		t.Errorf("index 1 = %v, want 1.0", values["ota_1_efr32_current_version"])
	}
	if values["ota_2_efr32_current_version"] != "2.0" {
		t.Errorf("index 2 = %v, want 2.0", values["ota_2_efr32_current_version"])
	}
}

// TestBridgeObjectID covers parsing the HA object_id out of a discovery config
// topic and rejecting non-bridge / non-config topics.
func TestBridgeObjectID(t *testing.T) {
	cases := []struct {
		topic, prefix, wantID string
		wantOK                bool
	}{
		{"homeassistant/sensor/tibber-pulse-bridge-abc_rssi/config", "homeassistant", "tibber-pulse-bridge-abc_rssi", true},
		{"homeassistant/binary_sensor/tibber-pulse-bridge-abc_available/config", "homeassistant", "tibber-pulse-bridge-abc_available", true},
		{"homeassistant/sensor/tibber-pulse-lgz_81199038_power_total/config", "homeassistant", "", false}, // meter, not bridge
		{"homeassistant/sensor/tibber-pulse-bridge-abc_rssi/state", "homeassistant", "", false},           // not a config topic
		{"ha/discovery/sensor/tibber-pulse-bridge-x_rssi/config", "ha/discovery", "tibber-pulse-bridge-x_rssi", true},
	}
	for _, c := range cases {
		got, ok := bridgeObjectID(c.topic, c.prefix)
		if ok != c.wantOK || got != c.wantID {
			t.Errorf("bridgeObjectID(%q,%q) = (%q,%v), want (%q,%v)", c.topic, c.prefix, got, ok, c.wantID, c.wantOK)
		}
	}
}

// TestStaleBridgeConfigs covers the reconcile decision: clear old-identity and
// current-but-undesired configs, never a desired one, another bridge's, or a
// meter config.
func TestStaleBridgeConfigs(t *testing.T) {
	const prefix = "homeassistant"
	const oldID = "tibber-pulse-bridge-192_168_1_5" // host-based legacy id
	const curID = "tibber-pulse-bridge-30fb10fffe9326a9"
	topic := func(comp, oid string) string { return prefix + "/" + comp + "/" + oid + "/config" }

	desiredKeep := topic("sensor", curID+"_battery_voltage")
	curStale := topic("sensor", curID+"_ota_9_gone_current_version")
	oldLegacy := topic("sensor", oldID+"_temperature")
	otherBridge := topic("sensor", "tibber-pulse-bridge-deadbeef_battery_voltage")
	meterConfig := topic("sensor", "tibber-pulse-lgz_81199038_power_total")

	observed := map[string]struct{}{
		desiredKeep: {}, curStale: {}, oldLegacy: {}, otherBridge: {}, meterConfig: {},
	}
	desired := map[string]struct{}{desiredKeep: {}}

	cleared := map[string]bool{}
	for _, tpc := range staleBridgeConfigs(observed, desired, prefix, oldID, curID, true) {
		cleared[tpc] = true
	}
	if !cleared[curStale] {
		t.Errorf("should clear stale current-id config %s", curStale)
	}
	if !cleared[oldLegacy] {
		t.Errorf("should clear old-identity config %s", oldLegacy)
	}
	if cleared[desiredKeep] {
		t.Error("must not clear a desired config")
	}
	if cleared[otherBridge] {
		t.Error("must not clear another bridge's config")
	}
	if cleared[meterConfig] {
		t.Error("must not clear a meter config")
	}

	// With curIDComplete=false (manifest not fetched this cycle), current-id
	// configs are left alone, but the departed identity is still swept.
	cleared = map[string]bool{}
	for _, tpc := range staleBridgeConfigs(observed, desired, prefix, oldID, curID, false) {
		cleared[tpc] = true
	}
	if cleared[curStale] {
		t.Error("must not clear current-id config when manifest is incomplete")
	}
	if !cleared[oldLegacy] {
		t.Error("old-identity sweep must run regardless of manifest completeness")
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

func TestReconcileSettled(t *testing.T) {
	tests := []struct {
		name            string
		swept, haveOTA  bool
		attempts, fails int
		want            bool
	}{
		{"manifest reconciled settles at once", true, true, 1, 0, true},
		{"ota-less waits out the grace", true, false, 1, 0, false},
		{"ota-less waits mid-grace", true, false, reconcileOTALessSweeps - 1, 0, false},
		{"ota-less settles after grace", true, false, reconcileOTALessSweeps, 0, true},
		{"transient ota miss does not settle early", true, false, 3, 0, false},
		{"single subscribe failure keeps trying", false, false, 1, 1, false},
		{"repeated subscribe failure gives up", false, false, 2, reconcileMaxFailures, true},
		{"hard cap backstops", false, false, reconcileMaxAttempts, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reconcileSettled(tt.swept, tt.haveOTA, tt.attempts, tt.fails); got != tt.want {
				t.Errorf("reconcileSettled(%v,%v,%d,%d) = %v, want %v",
					tt.swept, tt.haveOTA, tt.attempts, tt.fails, got, tt.want)
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

// --- meter via_device lifecycle (regression for #71) --------------------

// errFakeNoSub makes the fake broker's SUBSCRIBE fail so the reconcile sweep
// inside PublishBridgeUpdate bails immediately instead of blocking on the 3s
// retained-enumeration deadline — the sweep is irrelevant to what these tests
// assert (the meter via_device), so short-circuiting it keeps them fast.
var errFakeNoSub = errors.New("fake: subscribe unsupported")

type fakeToken struct{ err error }

func (t *fakeToken) Wait() bool                     { return true }
func (t *fakeToken) WaitTimeout(time.Duration) bool { return true }
func (t *fakeToken) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
func (t *fakeToken) Error() error { return t.err }

// fakeMQTTClient records the last payload published to each topic so a test
// can inspect the discovery configs the sink emits. Subscribe fails on purpose
// (see errFakeNoSub); the remaining interface methods are unused no-ops.
type fakeMQTTClient struct {
	mu        sync.Mutex
	published map[string]string
}

func newFakeMQTTClient() *fakeMQTTClient {
	return &fakeMQTTClient{published: map[string]string{}}
}

func (c *fakeMQTTClient) payload(topic string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.published[topic]
	return p, ok
}

func (c *fakeMQTTClient) Publish(topic string, _ byte, _ bool, payload any) mqtt.Token {
	c.mu.Lock()
	switch p := payload.(type) {
	case string:
		c.published[topic] = p
	case []byte:
		c.published[topic] = string(p)
	}
	c.mu.Unlock()
	return &fakeToken{}
}

func (c *fakeMQTTClient) Subscribe(string, byte, mqtt.MessageHandler) mqtt.Token {
	return &fakeToken{err: errFakeNoSub}
}
func (c *fakeMQTTClient) Unsubscribe(...string) mqtt.Token { return &fakeToken{} }
func (c *fakeMQTTClient) Disconnect(uint)                  {}

func (c *fakeMQTTClient) IsConnected() bool      { return true }
func (c *fakeMQTTClient) IsConnectionOpen() bool { return true }
func (c *fakeMQTTClient) Connect() mqtt.Token    { return &fakeToken{} }
func (c *fakeMQTTClient) SubscribeMultiple(map[string]byte, mqtt.MessageHandler) mqtt.Token {
	return &fakeToken{}
}
func (c *fakeMQTTClient) AddRoute(string, mqtt.MessageHandler)    {}
func (c *fakeMQTTClient) OptionsReader() mqtt.ClientOptionsReader { return mqtt.ClientOptionsReader{} }

// meterVia returns the device.via_device of the retained discovery config the
// sink published for one meter sensor, and whether that config exists at all.
func meterVia(t *testing.T, fc *fakeMQTTClient, serial, sensor string) (string, bool) {
	t.Helper()
	topic := discovery.ConfigTopic("homeassistant", sensor, discovery.Device{MeterSerial: serial})
	raw, ok := fc.payload(topic)
	if !ok {
		return "", false
	}
	var cfg struct {
		Device struct {
			ViaDevice string `json:"via_device"`
		} `json:"device"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal meter config for %s: %v", sensor, err)
	}
	return cfg.Device.ViaDevice, true
}

func newDiscoverySink(fc *fakeMQTTClient, host string) *MQTTSink {
	m := &MQTTSink{
		client:           fc,
		prefix:           "tibber/pulse",
		discoveryPrefix:  "homeassistant",
		discovered:       map[string]bool{},
		bridgeDiscovered: map[string]bool{},
	}
	m.SetBridgeHost(host)
	return m
}

// TestMeterViaDeviceReannouncesOnEUILearn is the regression guard for #71: a
// meter announced before the bridge EUI is known links via the host-based
// bridge id, and once the EUI is learned the meter re-announces with a
// via_device pointing at the EUI-based bridge device that actually gets
// published — never leaving HA on "Connected via Unnamed device".
func TestMeterViaDeviceReannouncesOnEUILearn(t *testing.T) {
	fc := newFakeMQTTClient()
	m := newDiscoverySink(fc, "192.168.1.5")

	const serial = "LGZ-81199038"
	readings := []sml.Reading{
		{Name: "meter_serial", Raw: serial},
		{Name: "manufacturer", Raw: "LGZ"},
		{Name: "power_total", Value: 3, Unit: "W"},
	}

	// Before the EUI is learned the meter links to the host-based bridge id.
	m.maybeAnnounce(readings)
	via, ok := meterVia(t, fc, serial, "power_total")
	if !ok {
		t.Fatal("meter config not published before EUI learn")
	}
	if want := "tibber-pulse-bridge-192_168_1_5"; via != want {
		t.Fatalf("via_device before EUI = %q, want host-based %q", via, want)
	}

	// EUI arrives on the metrics path — the identifier transition must reset
	// the meter announce latch so the next frame re-announces.
	if err := m.PublishBridgeUpdate(BridgeUpdate{
		Node: &pulse.Node{NodeID: 1, EUI: "30FB10FFFE9326A9"},
	}); err != nil {
		t.Fatalf("PublishBridgeUpdate: %v", err)
	}

	// Next meter frame re-announces with the EUI-based via_device.
	m.maybeAnnounce(readings)
	via, _ = meterVia(t, fc, serial, "power_total")
	if want := "tibber-pulse-bridge-30fb10fffe9326a9"; via != want {
		t.Fatalf("via_device after EUI = %q, want EUI-based %q", via, want)
	}

	// The bridge device that via_device now names must actually exist on the
	// broker (announceBridge published it under the same id), else the link
	// would dangle exactly as #71 describes.
	bridgeCfg := discovery.BridgeConfigTopic("homeassistant", "rssi",
		discovery.BridgeSensors["rssi"],
		discovery.BridgeDevice{Host: "192.168.1.5", EUI: "30FB10FFFE9326A9"})
	if _, ok := fc.payload(bridgeCfg); !ok {
		t.Fatal("EUI-based bridge device never published — meter via_device would dangle")
	}
}

// TestMeterAnnouncesWithoutEUI covers #71 acceptance criterion 2: a bridge
// whose /nodes.json never yields an EUI still gets its meter entities into HA,
// linked via the host-based bridge id (the only id published in that case).
func TestMeterAnnouncesWithoutEUI(t *testing.T) {
	fc := newFakeMQTTClient()
	m := newDiscoverySink(fc, "10.0.0.9")

	m.maybeAnnounce([]sml.Reading{
		{Name: "meter_serial", Raw: "LGZ-1"},
		{Name: "power_total", Value: 1, Unit: "W"},
	})

	via, ok := meterVia(t, fc, "LGZ-1", "power_total")
	if !ok {
		t.Fatal("meter entity not announced when bridge has no EUI")
	}
	if want := "tibber-pulse-bridge-10_0_0_9"; via != want {
		t.Fatalf("via_device = %q, want host-based %q", via, want)
	}
}
