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

func (c *fakeMQTTClient) Subscribe(string, byte, mqtt.MessageHandler) mqtt.Token {
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
