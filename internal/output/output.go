package output

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

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
}

func NewMQTTSink(host string, port int, clientID, topicPrefix string) (*MQTTSink, error) {
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
	return &MQTTSink{client: c, prefix: strings.TrimRight(topicPrefix, "/")}, nil
}

func (m *MQTTSink) Publish(_ context.Context, readings []sml.Reading) error {
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

func (m *MQTTSink) Close() {
	m.client.Disconnect(500)
}
