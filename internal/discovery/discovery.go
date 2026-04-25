// Package discovery emits Home Assistant MQTT-Discovery configs for the
// readings produced by internal/sml.
//
// HA reference: https://www.home-assistant.io/integrations/mqtt/#mqtt-discovery
package discovery

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SensorSpec is the per-OBIS metadata we know about a value: the HA
// device_class / state_class / unit. Names are the same keys used in
// internal/sml.obisNames so they line up automatically.
type SensorSpec struct {
	FriendlyName string
	DeviceClass  string // "power", "energy", "voltage", "current", "frequency"
	StateClass   string // "measurement" or "total_increasing"
	Unit         string // unit_of_measurement
}

// Sensors maps the bot's reading.Name to the HA discovery metadata.
// Keep in sync with internal/sml/parse.go obisNames.
var Sensors = map[string]SensorSpec{
	"power_total":         {"Power", "power", "measurement", "W"},
	"power_l1":            {"Power L1", "power", "measurement", "W"},
	"power_l2":            {"Power L2", "power", "measurement", "W"},
	"power_l3":            {"Power L3", "power", "measurement", "W"},
	"energy_import_total": {"Energy Import", "energy", "total_increasing", "Wh"},
	"energy_import_t1":    {"Energy Import T1", "energy", "total_increasing", "Wh"},
	"energy_import_t2":    {"Energy Import T2", "energy", "total_increasing", "Wh"},
	"energy_export_total": {"Energy Export", "energy", "total_increasing", "Wh"},
	"energy_export_t1":    {"Energy Export T1", "energy", "total_increasing", "Wh"},
	"energy_export_t2":    {"Energy Export T2", "energy", "total_increasing", "Wh"},
	"voltage_l1":          {"Voltage L1", "voltage", "measurement", "V"},
	"voltage_l2":          {"Voltage L2", "voltage", "measurement", "V"},
	"voltage_l3":          {"Voltage L3", "voltage", "measurement", "V"},
	"current_l1":          {"Current L1", "current", "measurement", "A"},
	"current_l2":          {"Current L2", "current", "measurement", "A"},
	"current_l3":          {"Current L3", "current", "measurement", "A"},
	"frequency":           {"Frequency", "frequency", "measurement", "Hz"},
}

// Device groups all sensors of one physical meter under a single HA Device.
type Device struct {
	MeterSerial  string // e.g. "LGZ-81199038" — used as device identifier
	Manufacturer string // raw 3-letter mfg code (e.g. "LGZ")
}

// Config is the JSON payload we publish to
// <prefix>/sensor/<unique_id>/config. We compose it via map[string]any so
// it stays compact (omits null fields) without needing omitempty per field.
type Config = map[string]any

// BuildConfig produces the discovery payload for one sensor.
// stateTopic is the MQTT topic where the bot publishes the value.
func BuildConfig(name string, spec SensorSpec, dev Device, stateTopic string) Config {
	uniqueID := fmt.Sprintf("tibber_pulse_%s_%s", sanitize(dev.MeterSerial), name)
	cfg := Config{
		"name":                spec.FriendlyName,
		"unique_id":           uniqueID,
		"object_id":           uniqueID,
		"state_topic":         stateTopic,
		"unit_of_measurement": spec.Unit,
		"device_class":        spec.DeviceClass,
		"state_class":         spec.StateClass,
		"device": map[string]any{
			"identifiers":  []string{dev.MeterSerial},
			"name":         "Tibber Pulse " + dev.MeterSerial,
			"manufacturer": manufacturerName(dev.Manufacturer),
			"model":        "SML 1.04 meter",
			"via_device":   "tibber-pulse-bot",
		},
	}
	return cfg
}

// ConfigTopic is where the discovery payload must be published.
func ConfigTopic(discoveryPrefix, name string, dev Device) string {
	uniqueID := fmt.Sprintf("tibber_pulse_%s_%s", sanitize(dev.MeterSerial), name)
	return fmt.Sprintf("%s/sensor/%s/config",
		strings.TrimRight(discoveryPrefix, "/"), uniqueID)
}

// MarshalConfig is a convenience wrapper.
func MarshalConfig(cfg Config) ([]byte, error) {
	return json.Marshal(cfg)
}

// sanitize lower-cases and replaces non-alphanumerics with underscores so
// the unique_id is safe for MQTT topics and HA's entity-id slugifier.
func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			out = append(out, c+32)
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

func manufacturerName(code string) string {
	switch strings.ToUpper(code) {
	case "LGZ":
		return "Landis+Gyr"
	case "ESY":
		return "EasyMeter"
	case "EMH":
		return "EMH metering"
	case "ITZ":
		return "Iskraemeco"
	case "ISK":
		return "Iskraemeco"
	}
	return code
}
