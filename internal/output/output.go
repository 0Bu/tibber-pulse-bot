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
	discoveryPrefix  string
	discovered       map[string]bool // sensor name → already announced
	device           discovery.Device
	bridge           discovery.BridgeDevice
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

// BridgeUpdate bundles all four bridge data sources for a single publish.
// Node/Status/OTA may be nil/empty if their endpoint failed; the publisher
// degrades gracefully and only emits sensors backed by present data.
type BridgeUpdate struct {
	Metrics pulse.Metrics
	Node    *pulse.Node
	Status  *pulse.Status
	OTA     []pulse.OTAEntry
}

// PublishBridgeUpdate publishes bridge metrics + node/status/OTA derived
// values under <prefix>/bridge/<name>. On the first call after EUI is
// known, retained HA discovery configs are emitted (or refreshed) for every
// sensor that has metadata in discovery.BridgeSensors.
func (m *MQTTSink) PublishBridgeUpdate(u BridgeUpdate) error {
	if m.bridge.Host == "" {
		return fmt.Errorf("bridge host not set")
	}
	m.bridge.NodeVersion = u.Metrics.NodeVersion
	if u.Status != nil {
		m.bridge.ESPVersion = u.Status.Firmware.ESP
		m.bridge.EFRVersion = u.Status.Firmware.EFR
	}
	if u.Node != nil {
		// EUI transition: clear the legacy IP-based discovery topics from
		// v1.0.4 (HA picks up empty retained payload as "remove device"),
		// then re-publish under the new EUI-based identifier.
		if m.bridge.EUI != u.Node.EUI {
			if m.bridge.EUI == "" {
				// First time we learn the EUI — always best-effort sweep
				// of legacy IP-based topics, idempotent if nothing's there.
				m.cleanupOldBridgeDiscovery()
			}
			m.bridge.EUI = u.Node.EUI
			m.bridge.ProductModel = u.Node.ProductModel
			m.bridge.NodeModel = u.Node.Model
			m.bridgeDiscovered = map[string]bool{}
		}
	}

	values, dyn := bridgeState(u)

	if m.discoveryPrefix != "" {
		m.announceBridge(values, dyn)
	}

	for name, raw := range values {
		topic := m.prefix + "/bridge/" + name
		payload := formatStatePayload(raw)
		if payload == "" {
			continue
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

// formatStatePayload renders bool → "ON"/"OFF" (HA binary_sensor default),
// numeric → 3-decimal/integer string, string → verbatim. An empty string (or
// unknown type) yields "" so the caller skips publishing it.
func formatStatePayload(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "ON"
		}
		return "OFF"
	case float64:
		return fmt.Sprintf("%.3f", x)
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case string:
		return x
	}
	return ""
}

// bridgeState merges every decoded field from the four bridge endpoints into
// one flat map keyed by the same names BridgeSensors uses for HA discovery.
// Values are bool (binary_sensor), float64/int (numeric) or string (text).
// The second return value holds discovery specs for the dynamically-named
// per-OTA-component sensors, which can't live in the static BridgeSensors map
// because their names embed the component model.
//
// Existing counter/measurement fields keep their float64 type so their MQTT
// payloads (and HA history) are unchanged; only newly added integer
// identifiers use int.
func bridgeState(u BridgeUpdate) (map[string]any, map[string]discovery.SensorSpec) {
	m := u.Metrics
	out := map[string]any{
		"battery_voltage":            m.BatteryVoltage,
		"temperature":                m.Temperature,
		"rssi":                       m.AvgRSSI,
		"lqi":                        m.AvgLQI,
		"radio_tx_power":             m.RadioTxPower,
		"uptime":                     float64(m.UptimeMS) / 1000.0,
		"meter_msg_sent":             float64(m.MeterMsgCountSent),
		"pkg_sent":                   float64(m.MeterPkgCountSent),
		"pkg_received":               float64(m.MeterPkgCountRecv),
		"readings_received":          float64(m.MeterReadingCountRecv),
		"corrupt_readings":           float64(m.MeterCorruptCountRecv),
		"invalid_readings":           float64(m.InvalidMeterReadings),
		"compression_error_readings": float64(m.CompressionErrorReadings),
		"meter_mode":                 m.MeterMode,
		"bootloader_version":         m.BootloaderVersion,
		"product_id":                 m.ProductID,
		"node_version":               m.NodeVersion,
	}
	if n := u.Node; n != nil {
		out["node_id"] = n.NodeID
		out["eui"] = n.EUI
		out["product_model"] = n.ProductModel
		out["model"] = n.Model
		out["version"] = n.Version
		out["available"] = n.Available
		out["paired"] = n.Paired
		out["last_seen_age"] = float64(n.LastSeenMS) / 1000.0
		out["last_data_age"] = float64(n.LastDataMS) / 1000.0
		out["average_rssi"] = float64(n.AverageRSSI)
		out["average_lqi"] = float64(n.AverageLQI)
		out["ota_distribute_status"] = n.OTADistributeStatus
	}
	if s := u.Status; s != nil {
		out["pairing_status"] = s.PairingStatus
		out["bridge_uptime"] = float64(s.UpTime)
		out["firmware_esp"] = s.Firmware.ESP
		out["firmware_efr"] = s.Firmware.EFR
		out["wifi_ip"] = s.WiFi.IP
		out["wifi_ssid"] = s.WiFi.SSID
		out["wifi_bssid"] = s.WiFi.BSSID
		out["wifi_rssi"] = float64(s.WiFi.RSSI)
		out["wifi_connected"] = s.WiFi.Connected
		out["cloud_mqtt"] = s.MQTT.Connected
		out["cloud_mqtt_subscribed"] = s.MQTT.Subscribed
		out["ota_update_running"] = s.OTAUpdateRunning
	}
	dyn := map[string]discovery.SensorSpec{}
	if len(u.OTA) > 0 {
		anyOutdated := false
		for _, e := range u.OTA {
			if !e.Up2Date {
				anyOutdated = true
			}
			slug, label := otaKey(e)
			cv := "ota_" + slug + "_current_version"
			mv := "ota_" + slug + "_manifest_version"
			ud := "ota_" + slug + "_up2date"
			out[cv], out[mv], out[ud] = e.CurrentVersion, e.ManifestVersion, e.Up2Date
			dyn[cv] = discovery.SensorSpec{FriendlyName: "OTA " + label + " Current Version"}
			dyn[mv] = discovery.SensorSpec{FriendlyName: "OTA " + label + " Manifest Version"}
			dyn[ud] = discovery.SensorSpec{FriendlyName: "OTA " + label + " Up To Date", Component: "binary_sensor"}
		}
		out["update_available"] = anyOutdated
	}
	// Drop empty string fields so HA only gets an entity once a value exists
	// (mirrors the lazy-announce behaviour for newly appearing sensors).
	for k, v := range out {
		if s, ok := v.(string); ok && s == "" {
			delete(out, k)
			delete(dyn, k)
		}
	}
	return out, dyn
}

// otaKey derives a topic-safe slug and a human label for one OTA component,
// falling back to the numeric index when the model is empty so every
// component still gets unique topics.
func otaKey(e pulse.OTAEntry) (slug, label string) {
	label = e.Model
	if label == "" {
		label = fmt.Sprintf("component %d", e.OTAIndex)
	}
	return discovery.Sanitize(label), label
}

func (m *MQTTSink) announceBridge(values map[string]any, dyn map[string]discovery.SensorSpec) {
	for name := range values {
		if m.bridgeDiscovered[name] {
			continue
		}
		spec, ok := discovery.BridgeSensors[name]
		if !ok {
			spec, ok = dyn[name]
		}
		if !ok {
			continue
		}
		stateTopic := m.prefix + "/bridge/" + name
		topic := discovery.BridgeConfigTopic(m.discoveryPrefix, name, spec, m.bridge)
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

// cleanupOldBridgeDiscovery is called once when we transition from an
// IP-based bridge identifier (v1.0.4) to an EUI-based one (v1.0.5+). It
// publishes empty retained payloads to the legacy topics so HA garbage
// collects the orphan device card. Best-effort — failures are ignored.
func (m *MQTTSink) cleanupOldBridgeDiscovery() {
	oldDev := discovery.BridgeDevice{Host: m.bridge.Host} // EUI empty → IP-based
	for name, spec := range discovery.BridgeSensors {
		topic := discovery.BridgeConfigTopic(m.discoveryPrefix, name, spec, oldDev)
		_ = m.client.Publish(topic, 0, true, "")
	}
}
