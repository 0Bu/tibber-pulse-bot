package output

import (
	"context"
	"fmt"
	"io"
	"strings"
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

	// HA MQTT-Discovery — when discoveryPrefix is non-empty, the sink emits
	// one retained config message per known sensor the first time it sees
	// the sensor in a published batch. Requires the meter_serial reading to
	// be present (used as the HA device identifier).
	discoveryPrefix string
	discovered      map[string]bool // sensor name → already announced
	device          discovery.Device
	bridge          discovery.BridgeDevice
	bridgeDiscovered map[string]bool
}

func NewMQTTSink(host string, port int, clientID, topicPrefix, discoveryPrefix string) (*MQTTSink, error) {
	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:%d", host, port)).
		SetClientID(clientID).
		SetAutoReconnect(true).
		SetConnectTimeout(10 * time.Second).
		SetCleanSession(true)

	c := mqtt.NewClient(opts)
	t := c.Connect()
	if !t.WaitTimeout(10 * time.Second) {
		return nil, fmt.Errorf("mqtt: connect timeout to %s:%d", host, port)
	}
	if err := t.Error(); err != nil {
		return nil, fmt.Errorf("mqtt: connect %s:%d: %w", host, port, err)
	}
	return &MQTTSink{
		client:           c,
		prefix:           strings.TrimRight(topicPrefix, "/"),
		discoveryPrefix:  strings.TrimRight(discoveryPrefix, "/"),
		discovered:       map[string]bool{},
		bridgeDiscovered: map[string]bool{},
	}, nil
}

// SetBridgeHost records the bridge host so meter discovery payloads can
// link to it via via_device. Call once before the first Publish.
func (m *MQTTSink) SetBridgeHost(host string) {
	m.bridge.Host = host
}

func (m *MQTTSink) Publish(_ context.Context, readings []sml.Reading) error {
	if m.discoveryPrefix != "" {
		m.maybeAnnounce(readings)
	}
	for _, r := range readings {
		suffix := r.Name
		if suffix == "" {
			suffix = "obis/" + strings.ReplaceAll(r.OBIS, "*", "_")
		}
		topic := m.prefix + "/" + suffix
		var payload string
		if r.Raw != "" {
			payload = r.Raw
		} else {
			payload = fmt.Sprintf("%.3f", r.Value)
		}
		t := m.client.Publish(topic, 0, false, payload)
		if !t.WaitTimeout(5 * time.Second) {
			return fmt.Errorf("mqtt: publish timeout for %s", topic)
		}
		if err := t.Error(); err != nil {
			return fmt.Errorf("mqtt: publish %s: %w", topic, err)
		}
	}
	return nil
}

// maybeAnnounce publishes HA discovery configs for any newly seen sensors.
// Discovery messages are retained so HA can rebuild its device registry on
// restart. We can only announce once we've seen the meter_serial — that's
// the HA device identifier and ties all entities to one Device card.
func (m *MQTTSink) maybeAnnounce(readings []sml.Reading) {
	if m.device.MeterSerial == "" {
		for _, r := range readings {
			switch r.Name {
			case "meter_serial":
				m.device.MeterSerial = r.Raw
			case "manufacturer":
				m.device.Manufacturer = r.Raw
			}
		}
		if m.device.MeterSerial == "" {
			return // wait for next frame
		}
	}
	for _, r := range readings {
		if r.Name == "" || m.discovered[r.Name] {
			continue
		}
		spec, ok := discovery.Sensors[r.Name]
		if !ok {
			continue // no HA metadata for this OBIS — skip discovery
		}
		stateTopic := m.prefix + "/" + r.Name
		topic := discovery.ConfigTopic(m.discoveryPrefix, r.Name, m.device)
		via := ""
		if m.bridge.Host != "" {
			via = m.bridge.Identifier()
		}
		payload, err := discovery.MarshalConfig(discovery.BuildConfig(r.Name, spec, m.device, stateTopic, via))
		if err != nil {
			continue
		}
		t := m.client.Publish(topic, 0, true, payload) // retain=true is required by HA
		if !t.WaitTimeout(5 * time.Second) {
			return
		}
		m.discovered[r.Name] = true
	}
}

func (m *MQTTSink) Close() {
	m.client.Disconnect(500)
}

// PublishBridge publishes one set of bridge metrics under
// <prefix>/bridge/<name> and (if discovery is enabled) emits retained
// HA discovery configs for each metric on the first publish.
func (m *MQTTSink) PublishBridge(metrics pulse.Metrics) error {
	if m.bridge.Host == "" {
		// shouldn't happen — caller must SetBridgeHost first
		return fmt.Errorf("bridge host not set")
	}
	m.bridge.SWVersion = metrics.NodeVersion

	values := bridgeMetricMap(metrics)

	if m.discoveryPrefix != "" {
		m.announceBridge(values)
	}

	for name, val := range values {
		topic := m.prefix + "/bridge/" + name
		payload := fmt.Sprintf("%.3f", val)
		t := m.client.Publish(topic, 0, false, payload)
		if !t.WaitTimeout(5 * time.Second) {
			return fmt.Errorf("mqtt: publish timeout for %s", topic)
		}
		if err := t.Error(); err != nil {
			return fmt.Errorf("mqtt: publish %s: %w", topic, err)
		}
	}
	return nil
}

// bridgeMetricMap maps the wire fields onto the discovery sensor names.
// Uptime is converted from ms to seconds (HA duration unit).
func bridgeMetricMap(m pulse.Metrics) map[string]float64 {
	return map[string]float64{
		"battery_voltage":   m.BatteryVoltage,
		"temperature":       m.Temperature,
		"rssi":              m.AvgRSSI,
		"lqi":               m.AvgLQI,
		"uptime":            float64(m.UptimeMS) / 1000.0,
		"pkg_sent":          float64(m.MeterPkgCountSent),
		"pkg_received":      float64(m.MeterPkgCountRecv),
		"readings_received": float64(m.MeterReadingCountRecv),
		"corrupt_readings":  float64(m.MeterCorruptCountRecv),
		"invalid_readings":  float64(m.InvalidMeterReadings),
	}
}

func (m *MQTTSink) announceBridge(values map[string]float64) {
	for name := range values {
		if m.bridgeDiscovered[name] {
			continue
		}
		spec, ok := discovery.BridgeSensors[name]
		if !ok {
			continue
		}
		stateTopic := m.prefix + "/bridge/" + name
		topic := discovery.BridgeConfigTopic(m.discoveryPrefix, name, m.bridge)
		payload, err := discovery.MarshalConfig(discovery.BuildBridgeConfig(name, spec, m.bridge, stateTopic))
		if err != nil {
			continue
		}
		t := m.client.Publish(topic, 0, true, payload)
		if !t.WaitTimeout(5 * time.Second) {
			return
		}
		m.bridgeDiscovered[name] = true
	}
}
