package output

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/0Bu/tibber-pulse-bot/internal/discovery"
	"github.com/0Bu/tibber-pulse-bot/internal/pulse"
	"github.com/0Bu/tibber-pulse-bot/internal/sml"
)

func TestReadingState(t *testing.T) {
	got := readingState([]sml.Reading{
		{Name: "power_total", Value: 3.125},
		{Name: "meter_serial", Raw: "LGZ-81199038"},
		{OBIS: "1-0:96.50.1*1", Value: 7},
	})
	if got["power_total"] != 3.125 {
		t.Errorf("power_total = %v", got["power_total"])
	}
	if got["meter_serial"] != "LGZ-81199038" {
		t.Errorf("meter_serial = %v", got["meter_serial"])
	}
	unknown, ok := got["obis"].(map[string]any)
	if !ok || unknown["1-0:96.50.1*1"] != float64(7) {
		t.Errorf("obis = %#v", got["obis"])
	}
}

func TestDiagnosticStateIsReduced(t *testing.T) {
	var status pulse.Status
	status.WiFi.RSSI = -55
	u := BridgeUpdate{
		Metrics: pulse.Metrics{
			BatteryVoltage: 3.3, Temperature: 21.5, AvgRSSI: -67,
			MeterCorruptCountRecv: 2,
		},
		Node:   &pulse.Node{Available: true, LastDataMS: 2500},
		Status: &status,
	}
	got := diagnosticState(u)
	wantKeys := []string{
		"bridge_available", "last_data_age", "meter_link_rssi", "wifi_rssi",
		"bridge_battery_voltage", "bridge_temperature", "corrupt_readings",
	}
	if len(got) != len(wantKeys) {
		t.Fatalf("diagnostics has %d values, want %d: %#v", len(got), len(wantKeys), got)
	}
	for _, key := range wantKeys {
		if _, ok := got[key]; !ok {
			t.Errorf("missing diagnostic %q", key)
		}
		if _, ok := discovery.Diagnostics[key]; !ok {
			t.Errorf("diagnostic %q has no HA metadata", key)
		}
	}
	if got["last_data_age"] != 2.5 {
		t.Errorf("last_data_age = %v, want 2.5", got["last_data_age"])
	}
	for _, removed := range []string{"lqi", "uptime", "node_id", "wifi_ssid", "cloud_mqtt", "update_available"} {
		if _, ok := got[removed]; ok {
			t.Errorf("overloaded diagnostic %q is still present", removed)
		}
	}
}

func TestDiagnosticStateOmitsFailedOptionalEndpoints(t *testing.T) {
	got := diagnosticState(BridgeUpdate{Metrics: pulse.Metrics{BatteryVoltage: 3.3}})
	for _, key := range []string{"bridge_available", "last_data_age", "wifi_rssi"} {
		if _, ok := got[key]; ok {
			t.Errorf("%q should be absent without its endpoint", key)
		}
	}
}

func TestShort(t *testing.T) {
	tests := map[string]string{
		"power_total": "P", "energy_import_total": "Eimp",
		"energy_export_total": "Eexp", "voltage_l1": "U1",
		"current_l3": "I3", "frequency": "f", "other": "other",
	}
	for in, want := range tests {
		if got := short(in); got != want {
			t.Errorf("short(%q) = %q, want %q", in, got, want)
		}
	}
}

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

type publishedMessage struct {
	payload string
	retain  bool
}

type fakeMQTTClient struct {
	mu        sync.Mutex
	published map[string]publishedMessage
	subFunc   func(topic string, qos byte, callback mqtt.MessageHandler) mqtt.Token
}

func newFakeMQTTClient() *fakeMQTTClient {
	return &fakeMQTTClient{published: map[string]publishedMessage{}}
}

func (c *fakeMQTTClient) message(topic string) (publishedMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.published[topic]
	return p, ok
}

func (c *fakeMQTTClient) stateTopics(prefix string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var topics []string
	for topic := range c.published {
		if strings.HasPrefix(topic, prefix+"/") {
			topics = append(topics, topic)
		}
	}
	return topics
}

func (c *fakeMQTTClient) Publish(topic string, _ byte, retain bool, payload any) mqtt.Token {
	var raw string
	switch p := payload.(type) {
	case string:
		raw = p
	case []byte:
		raw = string(p)
	}
	c.mu.Lock()
	c.published[topic] = publishedMessage{payload: raw, retain: retain}
	c.mu.Unlock()
	return &fakeToken{}
}

func (c *fakeMQTTClient) Subscribe(topic string, qos byte, h mqtt.MessageHandler) mqtt.Token {
	if c.subFunc != nil {
		return c.subFunc(topic, qos, h)
	}
	return &fakeToken{err: errFakeNoSub}
}
func (c *fakeMQTTClient) Unsubscribe(...string) mqtt.Token { return &fakeToken{} }
func (c *fakeMQTTClient) Disconnect(uint)                  {}
func (c *fakeMQTTClient) IsConnected() bool                { return true }
func (c *fakeMQTTClient) IsConnectionOpen() bool           { return true }
func (c *fakeMQTTClient) Connect() mqtt.Token              { return &fakeToken{} }
func (c *fakeMQTTClient) SubscribeMultiple(map[string]byte, mqtt.MessageHandler) mqtt.Token {
	return &fakeToken{}
}
func (c *fakeMQTTClient) AddRoute(string, mqtt.MessageHandler)    {}
func (c *fakeMQTTClient) OptionsReader() mqtt.ClientOptionsReader { return mqtt.ClientOptionsReader{} }

func newTestMQTTSink(client *fakeMQTTClient, discoveryPrefix string) *MQTTSink {
	return &MQTTSink{
		client:                client,
		prefix:                "tibber/pulse",
		discoveryPrefix:       discoveryPrefix,
		readingsDiscovered:    map[string]bool{},
		diagnosticsDiscovered: map[string]bool{},
		legacyCleaned:         map[string]bool{},
		diagnostics:           map[string]any{},
		device:                discovery.Device{BridgeHost: "192.168.1.5"},
	}
}

func TestPublishUsesOneReadingsTopic(t *testing.T) {
	client := newFakeMQTTClient()
	m := newTestMQTTSink(client, "")
	err := m.Publish(context.Background(), []sml.Reading{
		{Name: "power_total", Value: 3},
		{Name: "energy_import_total", Value: 42},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	topics := client.stateTopics("tibber/pulse")
	if len(topics) != 1 || topics[0] != "tibber/pulse/readings" {
		t.Fatalf("state topics = %v, want only readings", topics)
	}
	msg, _ := client.message("tibber/pulse/readings")
	var state map[string]any
	if err := json.Unmarshal([]byte(msg.payload), &state); err != nil {
		t.Fatalf("readings JSON: %v", err)
	}
	if state["power_total"] != float64(3) || state["energy_import_total"] != float64(42) {
		t.Errorf("readings state = %#v", state)
	}
	if msg.retain {
		t.Error("readings state must not be retained")
	}
}

func TestPublishBridgeUpdateUsesOneDiagnosticsTopic(t *testing.T) {
	client := newFakeMQTTClient()
	m := newTestMQTTSink(client, "")
	if err := m.PublishBridgeUpdate(BridgeUpdate{Metrics: pulse.Metrics{BatteryVoltage: 3.3}}); err != nil {
		t.Fatalf("PublishBridgeUpdate: %v", err)
	}
	topics := client.stateTopics("tibber/pulse")
	if len(topics) != 1 || topics[0] != "tibber/pulse/diagnostics" {
		t.Fatalf("state topics = %v, want only diagnostics", topics)
	}
}

func TestDiscoveryGroupsReadingsAndDiagnosticsOnOneDevice(t *testing.T) {
	client := newFakeMQTTClient()
	m := newTestMQTTSink(client, "homeassistant")
	var status pulse.Status
	status.WiFi.RSSI = -55
	if err := m.PublishBridgeUpdate(BridgeUpdate{
		Metrics: pulse.Metrics{BatteryVoltage: 3.3, Temperature: 21, AvgRSSI: -67},
		Node:    &pulse.Node{Available: true, LastDataMS: 1000},
		Status:  &status,
	}); err != nil {
		t.Fatalf("PublishBridgeUpdate: %v", err)
	}
	if err := m.Publish(context.Background(), []sml.Reading{
		{Name: "meter_serial", Raw: "LGZ-81199038"},
		{Name: "manufacturer", Raw: "LGZ"},
		{Name: "power_total", Value: 3},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	dev := discovery.Device{MeterSerial: "LGZ-81199038"}
	powerTopic := discovery.ConfigTopic("homeassistant", "power_total", discovery.Sensors["power_total"], dev)
	diagTopic := discovery.ConfigTopic("homeassistant", "bridge_available", discovery.Diagnostics["bridge_available"], dev)
	power := decodeConfig(t, client, powerTopic)
	diag := decodeConfig(t, client, diagTopic)
	if power["state_topic"] != "tibber/pulse/readings" {
		t.Errorf("power state_topic = %v", power["state_topic"])
	}
	if diag["state_topic"] != "tibber/pulse/diagnostics" {
		t.Errorf("diagnostic state_topic = %v", diag["state_topic"])
	}
	if diag["entity_category"] != "diagnostic" {
		t.Errorf("entity_category = %v", diag["entity_category"])
	}
	if diag["value_template"] != "{{ 'ON' if value_json.bridge_available else 'OFF' }}" {
		t.Errorf("diagnostic value_template = %v", diag["value_template"])
	}
	powerDev := power["device"].(map[string]any)
	diagDev := diag["device"].(map[string]any)
	if powerDev["name"] != diagDev["name"] || powerDev["name"] != "Tibber Pulse LGZ-81199038" {
		t.Errorf("devices differ: power=%v diagnostic=%v", powerDev["name"], diagDev["name"])
	}
	if _, ok := diagDev["via_device"]; ok {
		t.Error("diagnostics must not create or link to a separate bridge device")
	}
	stateTopics := client.stateTopics("tibber/pulse")
	if len(stateTopics) != 2 {
		t.Fatalf("state topics = %v, want readings + diagnostics", stateTopics)
	}
}

func TestLegacyBridgeDiscoveryCleanupWithoutSubscribe(t *testing.T) {
	client := newFakeMQTTClient()
	m := newTestMQTTSink(client, "homeassistant")
	if err := m.PublishBridgeUpdate(BridgeUpdate{
		Node: &pulse.Node{EUI: "30FB10FFFE9326A9"},
	}); err != nil {
		t.Fatalf("PublishBridgeUpdate: %v", err)
	}
	for _, dev := range []discovery.LegacyBridgeDevice{
		{Host: "192.168.1.5"},
		{Host: "192.168.1.5", EUI: "30FB10FFFE9326A9"},
	} {
		topic := discovery.LegacyBridgeConfigTopic("homeassistant", "rssi", "sensor", dev)
		msg, ok := client.message(topic)
		if !ok || msg.payload != "" || !msg.retain {
			t.Errorf("legacy cleanup %q = %#v, published=%v", topic, msg, ok)
		}
	}
}

func TestBridgeObjectID(t *testing.T) {
	tests := []struct {
		topic, prefix, want string
		ok                  bool
	}{
		{"homeassistant/sensor/tibber-pulse-bridge-abc_rssi/config", "homeassistant", "tibber-pulse-bridge-abc_rssi", true},
		{"homeassistant/sensor/tibber_pulse_lgz_power_total/config", "homeassistant", "", false},
		{"homeassistant/sensor/tibber-pulse-bridge-abc_rssi/state", "homeassistant", "", false},
	}
	for _, tt := range tests {
		got, ok := bridgeObjectID(tt.topic, tt.prefix)
		if got != tt.want || ok != tt.ok {
			t.Errorf("bridgeObjectID(%q) = (%q,%v), want (%q,%v)", tt.topic, got, ok, tt.want, tt.ok)
		}
	}
}

func decodeConfig(t *testing.T, client *fakeMQTTClient, topic string) map[string]any {
	t.Helper()
	msg, ok := client.message(topic)
	if !ok {
		t.Fatalf("missing discovery config %q", topic)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(msg.payload), &cfg); err != nil {
		t.Fatalf("decode %q: %v", topic, err)
	}
	if !msg.retain {
		t.Errorf("discovery config %q is not retained", topic)
	}
	return cfg
}

type fakeMQTTMessage struct {
	topic   string
	payload []byte
}

func (m *fakeMQTTMessage) Duplicate() bool   { return false }
func (m *fakeMQTTMessage) Qos() byte         { return 0 }
func (m *fakeMQTTMessage) Retained() bool    { return true }
func (m *fakeMQTTMessage) Topic() string     { return m.topic }
func (m *fakeMQTTMessage) MessageID() uint16 { return 0 }
func (m *fakeMQTTMessage) Payload() []byte   { return m.payload }
func (m *fakeMQTTMessage) Ack()              {}

func TestEnumerateRetainedConfigsSweepSuccess(t *testing.T) {
	client := newFakeMQTTClient()
	client.subFunc = func(topic string, qos byte, callback mqtt.MessageHandler) mqtt.Token {
		go func() {
			time.Sleep(10 * time.Millisecond)
			callback(client, &fakeMQTTMessage{
				topic:   "homeassistant/sensor/tibber-pulse-bridge-192_168_1_5_rssi/config",
				payload: []byte(`{"name":"rssi"}`),
			})
			time.Sleep(10 * time.Millisecond)
			callback(client, &fakeMQTTMessage{
				topic:   "homeassistant/sensor/tibber-pulse-bridge-192_168_1_5_battery/config",
				payload: []byte(`{"name":"battery"}`),
			})
		}()
		return &fakeToken{}
	}

	m := newTestMQTTSink(client, "homeassistant")
	found, ok := m.enumerateRetainedConfigs()
	if !ok {
		t.Fatal("enumerateRetainedConfigs failed")
	}
	if len(found) != 2 {
		t.Fatalf("expected 2 retained topics, got %d: %v", len(found), found)
	}

	// Now verify cleanupLegacyBridgeDiscovery clears them with empty retained message
	m.cleanupLegacyBridgeDiscovery("192.168.1.5", "")
	for top := range found {
		msg, ok := client.message(top)
		if !ok || msg.payload != "" || !msg.retain {
			t.Errorf("stale config %q was not cleared with empty retained message: %+v", top, msg)
		}
	}
}

func TestEnumerateRetainedConfigsEmptyBroker(t *testing.T) {
	client := newFakeMQTTClient()
	client.subFunc = func(topic string, qos byte, callback mqtt.MessageHandler) mqtt.Token {
		// Empty broker: subscription succeeds, but no messages arrive.
		return &fakeToken{}
	}

	m := newTestMQTTSink(client, "homeassistant")
	start := time.Now()
	found, ok := m.enumerateRetainedConfigs()
	elapsed := time.Since(start)

	if !ok {
		t.Fatal("enumerateRetainedConfigs should succeed on empty broker")
	}
	if len(found) != 0 {
		t.Errorf("expected 0 topics, got %d", len(found))
	}
	// Verify that the empty broker deadline was reduced to ~1s (not 3s)
	if elapsed > 2*time.Second {
		t.Errorf("sweep on empty broker took too long: %v (want <= 1.5s)", elapsed)
	}
}

func TestManufacturerHexDoesNotOverwriteASCII(t *testing.T) {
	client := newFakeMQTTClient()
	m := newTestMQTTSink(client, "homeassistant")

	// First telegram establishes meter_serial and manufacturer "LGZ" (ASCII)
	err := m.Publish(context.Background(), []sml.Reading{
		{Name: "meter_serial", Raw: "LGZ-12345678"},
		{Name: "manufacturer", Raw: "LGZ"},
		{Name: "power_total", Value: 100},
	})
	if err != nil {
		t.Fatalf("Publish 1: %v", err)
	}

	m.mu.Lock()
	mfg1 := m.device.Manufacturer
	m.mu.Unlock()
	if mfg1 != "LGZ" {
		t.Fatalf("expected manufacturer LGZ, got %q", mfg1)
	}

	// Second telegram attempts to overwrite with hex string "4c475a"
	err = m.Publish(context.Background(), []sml.Reading{
		{Name: "meter_serial", Raw: "LGZ-12345678"},
		{Name: "manufacturer", Raw: "4c475a"},
		{Name: "power_total", Value: 105},
	})
	if err != nil {
		t.Fatalf("Publish 2: %v", err)
	}

	m.mu.Lock()
	mfg2 := m.device.Manufacturer
	m.mu.Unlock()
	if mfg2 != "LGZ" {
		t.Errorf("hex string 4c475a overwrote ASCII manufacturer: got %q, want LGZ", mfg2)
	}
}

func TestManufacturerHexIgnoredWhenInitiallyEmpty(t *testing.T) {
	client := newFakeMQTTClient()
	m := newTestMQTTSink(client, "homeassistant")

	// First telegram contains hex string "4c475a"
	err := m.Publish(context.Background(), []sml.Reading{
		{Name: "meter_serial", Raw: "LGZ-12345678"},
		{Name: "manufacturer", Raw: "4c475a"},
		{Name: "power_total", Value: 100},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	m.mu.Lock()
	mfg := m.device.Manufacturer
	m.mu.Unlock()
	if mfg != "" {
		t.Errorf("expected manufacturer to remain empty when hex 4c475a received, got %q", mfg)
	}
}

func TestTOCTOUAtomicReservation(t *testing.T) {
	client := newFakeMQTTClient()
	m := newTestMQTTSink(client, "homeassistant")
	m.device.MeterSerial = "LGZ-9999"

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Publish(context.Background(), []sml.Reading{
				{Name: "meter_serial", Raw: "LGZ-9999"},
				{Name: "power_total", Value: 250},
			})
		}()
	}
	wg.Wait()

	// Verify discovery was announced cleanly
	m.mu.Lock()
	discovered := m.readingsDiscovered["power_total"]
	m.mu.Unlock()
	if !discovered {
		t.Error("power_total was not discovered")
	}
}

func TestReadingStatePreservesCleanManufacturer(t *testing.T) {
	t.Run("clean followed by hex preserves clean", func(t *testing.T) {
		readings := []sml.Reading{
			{Name: "manufacturer", Raw: "LGZ"},
			{Name: "manufacturer", Raw: "4c475a"},
		}
		state := readingState(readings)
		if state["manufacturer"] != "LGZ" {
			t.Errorf("readingState manufacturer = %v, want LGZ", state["manufacturer"])
		}
	})

	t.Run("hex followed by clean preserves clean", func(t *testing.T) {
		readings := []sml.Reading{
			{Name: "manufacturer", Raw: "4c475a"},
			{Name: "manufacturer", Raw: "LGZ"},
		}
		state := readingState(readings)
		if state["manufacturer"] != "LGZ" {
			t.Errorf("readingState manufacturer = %v, want LGZ", state["manufacturer"])
		}
	})

	t.Run("hex alone is excluded from state", func(t *testing.T) {
		readings := []sml.Reading{
			{Name: "manufacturer", Raw: "4c475a"},
		}
		state := readingState(readings)
		if _, ok := state["manufacturer"]; ok {
			t.Errorf("hex manufacturer should be excluded, got: %v", state["manufacturer"])
		}
	})
}

func TestNonFNNMeterSerialFallback(t *testing.T) {
	client := newFakeMQTTClient()
	m := newTestMQTTSink(client, "homeassistant")

	// Meter without DIN 43863-5 FNN server-ID sends only server_id hex or device_id
	err := m.Publish(context.Background(), []sml.Reading{
		{Name: "server_id", Raw: "010203040506070809"},
		{Name: "power_total", Value: 200},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	m.mu.Lock()
	serial := m.device.MeterSerial
	m.mu.Unlock()
	if serial != "010203040506070809" {
		t.Errorf("expected MeterSerial to fall back to server_id, got %q", serial)
	}

	m.mu.Lock()
	discovered := m.readingsDiscovered["power_total"]
	m.mu.Unlock()
	if !discovered {
		t.Error("expected power_total discovery to succeed with fallback serial")
	}
}

func TestMeterSerialTransitionReannouncesDiscovery(t *testing.T) {
	client := newFakeMQTTClient()
	m := newTestMQTTSink(client, "homeassistant")

	// First telegram only has fallback serial (e.g. server_id)
	err := m.Publish(context.Background(), []sml.Reading{
		{Name: "server_id", Raw: "010203040506070809"},
		{Name: "power_total", Value: 100},
	})
	if err != nil {
		t.Fatalf("Publish 1: %v", err)
	}

	topicOld := "homeassistant/sensor/tibber_pulse_010203040506070809_power_total/config"
	if _, ok := client.message(topicOld); !ok {
		t.Fatalf("expected discovery on %s", topicOld)
	}

	// Second telegram receives decoded meter_serial
	err = m.Publish(context.Background(), []sml.Reading{
		{Name: "server_id", Raw: "010203040506070809"},
		{Name: "meter_serial", Raw: "LGZ-81199038"},
		{Name: "power_total", Value: 150},
	})
	if err != nil {
		t.Fatalf("Publish 2: %v", err)
	}

	m.mu.Lock()
	serial := m.device.MeterSerial
	m.mu.Unlock()
	if serial != "LGZ-81199038" {
		t.Errorf("expected MeterSerial = LGZ-81199038, got %q", serial)
	}

	topicNew := "homeassistant/sensor/tibber_pulse_lgz_81199038_power_total/config"
	if _, ok := client.message(topicNew); !ok {
		t.Errorf("expected discovery re-announcement on %s after serial transition", topicNew)
	}
	msgOld, ok := client.message(topicOld)
	if !ok || msgOld.payload != "" || !msgOld.retain {
		t.Errorf("expected previous discovery topic %s to be retracted with empty retained payload, got %#v (published=%v)", topicOld, msgOld, ok)
	}
}

func TestSetBridgeHostSanitization(t *testing.T) {
	client := newFakeMQTTClient()
	m := newTestMQTTSink(client, "homeassistant")

	cases := []struct {
		input string
		want  string
	}{
		{"192.168.1.100", "192.168.1.100"},
		{"http://192.168.1.100/", "192.168.1.100"},
		{"https://bridge.local:8080/", "bridge.local:8080"},
		{"  ws://10.0.0.1/  ", "10.0.0.1"},
	}

	for _, tc := range cases {
		m.SetBridgeHost(tc.input)
		m.mu.Lock()
		got := m.device.BridgeHost
		m.mu.Unlock()
		if got != tc.want {
			t.Errorf("SetBridgeHost(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
