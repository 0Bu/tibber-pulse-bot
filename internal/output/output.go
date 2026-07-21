package output

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/0Bu/tibber-pulse-bot/internal/discovery"
	"github.com/0Bu/tibber-pulse-bot/internal/pulse"
	"github.com/0Bu/tibber-pulse-bot/internal/sml"
)

// Sink consumes a batch of readings produced by one Pulse poll.
type Sink interface {
	Publish(ctx context.Context, readings []sml.Reading) error
	Close()
}

// --- stdout sink ---------------------------------------------------------

type StdoutSink struct {
	w io.Writer
}

func NewStdoutSink(w io.Writer) *StdoutSink { return &StdoutSink{w: w} }

func (s *StdoutSink) Publish(_ context.Context, readings []sml.Reading) error {
	ts := time.Now().Format(time.RFC3339)
	fmt.Fprintf(s.w, "--- %s (%d readings) ---\n", ts, len(readings))
	for _, r := range readings {
		name := r.Name
		if name == "" {
			name = r.OBIS
		}
		if r.Raw != "" {
			fmt.Fprintf(s.w, "  %-22s %s = %s\n", name, r.OBIS, r.Raw)
			continue
		}
		fmt.Fprintf(s.w, "  %-22s %s = %.3f %s\n", name, r.OBIS, r.Value, r.Unit)
	}
	return nil
}

func (s *StdoutSink) Close() {}

// --- compact one-line stdout sink ---------------------------------------

// CompactStdoutSink prints one line per publish, container-log friendly.
// Format: "<timestamp>  power=3.000W import=2423174.800Wh export=253615.500Wh"
type CompactStdoutSink struct {
	w    io.Writer
	keys []string // ordered list of names to include; others are dropped
}

func NewCompactStdoutSink(w io.Writer) *CompactStdoutSink {
	return &CompactStdoutSink{
		w: w,
		keys: []string{
			"power_total",
			"power_l1", "power_l2", "power_l3",
			"energy_import_total", "energy_export_total",
			"voltage_l1", "voltage_l2", "voltage_l3",
			"current_l1", "current_l2", "current_l3",
			"frequency",
		},
	}
}

func (s *CompactStdoutSink) Publish(_ context.Context, readings []sml.Reading) error {
	idx := make(map[string]sml.Reading, len(readings))
	for _, r := range readings {
		if r.Name != "" {
			idx[r.Name] = r
		}
	}
	var b strings.Builder
	b.WriteString(time.Now().Format("15:04:05"))
	for _, k := range s.keys {
		r, ok := idx[k]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, " %s=%.3f%s", short(k), r.Value, r.Unit)
	}
	b.WriteByte('\n')
	_, err := s.w.Write([]byte(b.String()))
	return err
}

func (s *CompactStdoutSink) Close() {}

// short produces compact column headers for the one-line output.
func short(k string) string {
	switch k {
	case "power_total":
		return "P"
	case "power_l1":
		return "P1"
	case "power_l2":
		return "P2"
	case "power_l3":
		return "P3"
	case "energy_import_total":
		return "Eimp"
	case "energy_export_total":
		return "Eexp"
	case "voltage_l1":
		return "U1"
	case "voltage_l2":
		return "U2"
	case "voltage_l3":
		return "U3"
	case "current_l1":
		return "I1"
	case "current_l2":
		return "I2"
	case "current_l3":
		return "I3"
	case "frequency":
		return "f"
	}
	return k
}

// --- tee sink (fan-out) -------------------------------------------------

// TeeSink publishes to all wrapped sinks, returning the first error but
// always invoking every sink (so a slow MQTT broker doesn't suppress logs).
type TeeSink struct{ sinks []Sink }

func NewTeeSink(sinks ...Sink) *TeeSink { return &TeeSink{sinks: sinks} }

func (t *TeeSink) Publish(ctx context.Context, readings []sml.Reading) error {
	var firstErr error
	for _, s := range t.sinks {
		if err := s.Publish(ctx, readings); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (t *TeeSink) Close() {
	for _, s := range t.sinks {
		s.Close()
	}
}

// --- MQTT sink -----------------------------------------------------------

type MQTTSink struct {
	client mqtt.Client
	prefix string

	discoveryPrefix       string
	mu                    sync.Mutex
	readingsDiscovered    map[string]bool
	diagnosticsDiscovered map[string]bool
	legacyCleaned         map[string]bool
	device                discovery.Device
	diagnostics           map[string]any
	legacyEUI             string
}

func NewMQTTSink(host string, port int, clientID, topicPrefix, discoveryPrefix string) (*MQTTSink, error) {
	m := &MQTTSink{
		prefix:                strings.TrimRight(topicPrefix, "/"),
		discoveryPrefix:       strings.TrimRight(discoveryPrefix, "/"),
		readingsDiscovered:    map[string]bool{},
		diagnosticsDiscovered: map[string]bool{},
		legacyCleaned:         map[string]bool{},
		diagnostics:           map[string]any{},
	}
	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:%d", host, port)).
		SetClientID(clientID).
		SetAutoReconnect(true).
		SetConnectTimeout(10 * time.Second).
		SetCleanSession(true).
		SetOnConnectHandler(func(mqtt.Client) {
			m.mu.Lock()
			m.readingsDiscovered = map[string]bool{}
			m.diagnosticsDiscovered = map[string]bool{}
			m.mu.Unlock()
		})

	c := mqtt.NewClient(opts)
	t := c.Connect()
	if !t.WaitTimeout(10 * time.Second) {
		return nil, fmt.Errorf("mqtt: connect timeout to %s:%d", host, port)
	}
	if err := t.Error(); err != nil {
		return nil, fmt.Errorf("mqtt: connect %s:%d: %w", host, port, err)
	}
	m.client = c
	return m, nil
}

// SetBridgeHost records the bridge URL shown on the combined HA device.
func (m *MQTTSink) SetBridgeHost(host string) {
	m.mu.Lock()
	m.device.BridgeHost = host
	m.mu.Unlock()
}

func (m *MQTTSink) Publish(_ context.Context, readings []sml.Reading) error {
	if m.discoveryPrefix != "" {
		if err := m.maybeAnnounceReadings(readings); err != nil {
			return err
		}
	}
	payload, err := json.Marshal(readingState(readings))
	if err != nil {
		return fmt.Errorf("mqtt: encode readings: %w", err)
	}
	return m.publish(m.prefix+"/readings", false, payload)
}

// readingState collapses one SML telegram into a single JSON document. Known
// readings are top-level fields; unknown OBIS values stay available in a nested
// object without creating an unbounded set of MQTT topics.
func readingState(readings []sml.Reading) map[string]any {
	out := make(map[string]any, len(readings))
	unknown := map[string]any{}
	for _, r := range readings {
		var value any = r.Value
		if r.Raw != "" {
			value = r.Raw
		}
		if r.Name != "" {
			out[r.Name] = value
			continue
		}
		unknown[r.OBIS] = value
	}
	if len(unknown) > 0 {
		out["obis"] = unknown
	}
	return out
}

func (m *MQTTSink) maybeAnnounceReadings(readings []sml.Reading) error {
	m.mu.Lock()
	for _, r := range readings {
		switch r.Name {
		case "meter_serial":
			m.device.MeterSerial = r.Raw
		case "manufacturer":
			m.device.Manufacturer = r.Raw
		}
	}
	dev := m.device
	m.mu.Unlock()
	if dev.MeterSerial == "" {
		return nil
	}

	for _, r := range readings {
		spec, ok := discovery.Sensors[r.Name]
		if !ok {
			continue
		}
		m.mu.Lock()
		seen := m.readingsDiscovered[r.Name]
		m.mu.Unlock()
		if seen {
			continue
		}
		if err := m.announce(r.Name, spec, dev, m.prefix+"/readings"); err != nil {
			return err
		}
		m.mu.Lock()
		m.readingsDiscovered[r.Name] = true
		m.mu.Unlock()
	}
	return m.maybeAnnounceDiagnostics()
}

func (m *MQTTSink) Close() {
	m.client.Disconnect(500)
}

// BridgeUpdate bundles the three bridge data sources used by the reduced
// diagnostics set. Node and Status may be nil when an endpoint fails.
type BridgeUpdate struct {
	Metrics pulse.Metrics
	Node    *pulse.Node
	Status  *pulse.Status
}

// PublishBridgeUpdate publishes the reduced bridge health document under one
// diagnostics topic and groups its HA entities with the meter readings.
func (m *MQTTSink) PublishBridgeUpdate(u BridgeUpdate) error {
	values := diagnosticState(u)
	m.mu.Lock()
	if m.device.BridgeHost == "" {
		m.mu.Unlock()
		return fmt.Errorf("bridge host not set")
	}
	m.diagnostics = values
	if u.Node != nil && u.Node.EUI != "" {
		m.legacyEUI = u.Node.EUI
	}
	host, eui := m.device.BridgeHost, m.legacyEUI
	m.mu.Unlock()

	if m.discoveryPrefix != "" {
		m.cleanupLegacyBridgeDiscovery(host, eui)
		if err := m.maybeAnnounceDiagnostics(); err != nil {
			return err
		}
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("mqtt: encode diagnostics: %w", err)
	}
	return m.publish(m.prefix+"/diagnostics", false, payload)
}

func diagnosticState(u BridgeUpdate) map[string]any {
	m := u.Metrics
	out := map[string]any{
		"bridge_battery_voltage": m.BatteryVoltage,
		"bridge_temperature":     m.Temperature,
		"meter_link_rssi":        m.AvgRSSI,
		"corrupt_readings":       m.MeterCorruptCountRecv,
	}
	if n := u.Node; n != nil {
		out["bridge_available"] = n.Available
		out["last_data_age"] = float64(n.LastDataMS) / 1000.0
	}
	if s := u.Status; s != nil {
		out["wifi_rssi"] = float64(s.WiFi.RSSI)
	}
	return out
}

func (m *MQTTSink) maybeAnnounceDiagnostics() error {
	m.mu.Lock()
	dev := m.device
	values := make(map[string]any, len(m.diagnostics))
	for k, v := range m.diagnostics {
		values[k] = v
	}
	m.mu.Unlock()
	if dev.MeterSerial == "" {
		return nil
	}
	for name := range values {
		spec, ok := discovery.Diagnostics[name]
		if !ok {
			continue
		}
		m.mu.Lock()
		seen := m.diagnosticsDiscovered[name]
		m.mu.Unlock()
		if seen {
			continue
		}
		if err := m.announce(name, spec, dev, m.prefix+"/diagnostics"); err != nil {
			return err
		}
		m.mu.Lock()
		m.diagnosticsDiscovered[name] = true
		m.mu.Unlock()
	}
	return nil
}

func (m *MQTTSink) announce(name string, spec discovery.SensorSpec, dev discovery.Device, stateTopic string) error {
	topic := discovery.ConfigTopic(m.discoveryPrefix, name, spec, dev)
	payload, err := discovery.MarshalConfig(discovery.BuildConfig(name, spec, dev, stateTopic))
	if err != nil {
		return fmt.Errorf("mqtt: encode discovery %s: %w", name, err)
	}
	return m.publish(topic, true, payload)
}

func (m *MQTTSink) publish(topic string, retain bool, payload any) error {
	t := m.client.Publish(topic, 0, retain, payload)
	if !t.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("mqtt: publish timeout for %s", topic)
	}
	if err := t.Error(); err != nil {
		return fmt.Errorf("mqtt: publish %s: %w", topic, err)
	}
	return nil
}

func (m *MQTTSink) cleanupLegacyBridgeDiscovery(host, eui string) {
	devices := []discovery.LegacyBridgeDevice{{Host: host}}
	if eui != "" {
		devices = append(devices, discovery.LegacyBridgeDevice{Host: host, EUI: eui})
	}
	pending := make([]discovery.LegacyBridgeDevice, 0, len(devices))
	m.mu.Lock()
	for _, dev := range devices {
		if !m.legacyCleaned[dev.Identifier()] {
			pending = append(pending, dev)
		}
	}
	m.mu.Unlock()
	if len(pending) == 0 {
		return
	}

	observed, ok := m.enumerateRetainedConfigs()
	if ok {
		for topic := range observed {
			oid, valid := bridgeObjectID(topic, m.discoveryPrefix)
			if !valid {
				continue
			}
			for _, dev := range pending {
				if strings.HasPrefix(oid, dev.Identifier()+"_") {
					_ = m.client.Publish(topic, 0, true, "")
					break
				}
			}
		}
	} else {
		for _, dev := range pending {
			for name, component := range discovery.LegacyBridgeSensors {
				topic := discovery.LegacyBridgeConfigTopic(m.discoveryPrefix, name, component, dev)
				_ = m.client.Publish(topic, 0, true, "")
			}
		}
	}
	m.mu.Lock()
	for _, dev := range pending {
		m.legacyCleaned[dev.Identifier()] = true
	}
	m.mu.Unlock()
}

// bridgeObjectID extracts the HA object_id from a discovery config topic of the
// form <prefix>/<component>/<object_id>/config and reports whether it belongs
// to a legacy bridge sensor. Meter configs and non-config topics are rejected.
func bridgeObjectID(topic, discoveryPrefix string) (string, bool) {
	if !strings.HasPrefix(topic, discoveryPrefix+"/") || !strings.HasSuffix(topic, "/config") {
		return "", false
	}
	mid := topic[len(discoveryPrefix)+1 : len(topic)-len("/config")]
	i := strings.LastIndex(mid, "/")
	if i < 0 {
		return "", false
	}
	oid := mid[i+1:]
	if !strings.HasPrefix(oid, discovery.BridgePrefix) {
		return "", false
	}
	return oid, true
}

// enumerateRetainedConfigs subscribes to the HA discovery config topics and
// collects the non-empty bridge configs the broker replays from its retained
// store. MQTT has no end-of-retained signal, so it collects until a short
// quiet window elapses (hard-capped). The bool reports whether the SUBSCRIBE
// succeeded; on failure the caller uses the publish-only legacy cleanup.
func (m *MQTTSink) enumerateRetainedConfigs() (map[string]struct{}, bool) {
	found := make(map[string]struct{})
	var mu sync.Mutex
	activity := make(chan struct{}, 1024)
	filter := m.discoveryPrefix + "/+/+/config"
	tok := m.client.Subscribe(filter, 0, func(_ mqtt.Client, msg mqtt.Message) {
		if !msg.Retained() || len(msg.Payload()) == 0 {
			return
		}
		if _, ok := bridgeObjectID(msg.Topic(), m.discoveryPrefix); ok {
			mu.Lock()
			found[msg.Topic()] = struct{}{}
			mu.Unlock()
		}
		select {
		case activity <- struct{}{}:
		default:
		}
	})
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		m.client.Unsubscribe(filter)
		return copyStringSet(&mu, found), false
	}
	deadline := time.After(3 * time.Second)
	// Don't arm the short inter-message quiet window until the first retained
	// config actually arrives: a loaded broker can take longer than the window
	// to start replaying its retained store, and settling before then would
	// return an empty set that the caller wrongly trusts as "nothing stale".
	// Until first activity, only the hard deadline can end the sweep (a receive
	// from a nil channel blocks forever).
	var quiet *time.Timer
	var quietC <-chan time.Time
	for done := false; !done; {
		select {
		case <-activity:
			if quiet == nil {
				quiet = time.NewTimer(500 * time.Millisecond)
				quietC = quiet.C
			} else {
				if !quiet.Stop() {
					<-quiet.C
				}
				quiet.Reset(500 * time.Millisecond)
			}
		case <-quietC:
			done = true
		case <-deadline:
			done = true
		}
	}
	if quiet != nil {
		quiet.Stop()
	}
	m.client.Unsubscribe(filter)
	return copyStringSet(&mu, found), true
}

// copyStringSet returns a snapshot of src taken under mu, so the caller never
// touches the map the subscribe callback may still be writing to.
func copyStringSet(mu *sync.Mutex, src map[string]struct{}) map[string]struct{} {
	mu.Lock()
	defer mu.Unlock()
	out := make(map[string]struct{}, len(src))
	for k := range src {
		out[k] = struct{}{}
	}
	return out
}
