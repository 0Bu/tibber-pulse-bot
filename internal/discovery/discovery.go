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
	// Component is the HA discovery component, default "sensor". Set to
	// "binary_sensor" for boolean states (HA payload "ON"/"OFF").
	Component string
	// EntityCategory groups operational values under the HA device's
	// Diagnostics section instead of mixing them with meter measurements.
	EntityCategory string
}

// component returns "sensor" when unset.
func (s SensorSpec) component() string {
	if s.Component == "" {
		return "sensor"
	}
	return s.Component
}

// Sensors maps the bot's reading.Name to the HA discovery metadata.
// Keep in sync with internal/sml/parse.go obisNames.
var Sensors = map[string]SensorSpec{
	"power_total":         {FriendlyName: "Power", DeviceClass: "power", StateClass: "measurement", Unit: "W"},
	"power_l1":            {FriendlyName: "Power L1", DeviceClass: "power", StateClass: "measurement", Unit: "W"},
	"power_l2":            {FriendlyName: "Power L2", DeviceClass: "power", StateClass: "measurement", Unit: "W"},
	"power_l3":            {FriendlyName: "Power L3", DeviceClass: "power", StateClass: "measurement", Unit: "W"},
	"energy_import_total": {FriendlyName: "Energy Import", DeviceClass: "energy", StateClass: "total_increasing", Unit: "Wh"},
	"energy_import_t1":    {FriendlyName: "Energy Import T1", DeviceClass: "energy", StateClass: "total_increasing", Unit: "Wh"},
	"energy_import_t2":    {FriendlyName: "Energy Import T2", DeviceClass: "energy", StateClass: "total_increasing", Unit: "Wh"},
	"energy_export_total": {FriendlyName: "Energy Export", DeviceClass: "energy", StateClass: "total_increasing", Unit: "Wh"},
	"energy_export_t1":    {FriendlyName: "Energy Export T1", DeviceClass: "energy", StateClass: "total_increasing", Unit: "Wh"},
	"energy_export_t2":    {FriendlyName: "Energy Export T2", DeviceClass: "energy", StateClass: "total_increasing", Unit: "Wh"},
	"voltage_l1":          {FriendlyName: "Voltage L1", DeviceClass: "voltage", StateClass: "measurement", Unit: "V"},
	"voltage_l2":          {FriendlyName: "Voltage L2", DeviceClass: "voltage", StateClass: "measurement", Unit: "V"},
	"voltage_l3":          {FriendlyName: "Voltage L3", DeviceClass: "voltage", StateClass: "measurement", Unit: "V"},
	"current_l1":          {FriendlyName: "Current L1", DeviceClass: "current", StateClass: "measurement", Unit: "A"},
	"current_l2":          {FriendlyName: "Current L2", DeviceClass: "current", StateClass: "measurement", Unit: "A"},
	"current_l3":          {FriendlyName: "Current L3", DeviceClass: "current", StateClass: "measurement", Unit: "A"},
	"frequency":           {FriendlyName: "Frequency", DeviceClass: "frequency", StateClass: "measurement", Unit: "Hz"},
}

// Diagnostics is intentionally small: these values are enough to judge
// whether the bridge and its two radio links are healthy without exposing
// every firmware counter and identifier as a separate HA entity.
var Diagnostics = map[string]SensorSpec{
	"bridge_available":       {FriendlyName: "Bridge Available", DeviceClass: "connectivity", Component: "binary_sensor", EntityCategory: "diagnostic"},
	"last_data_age":          {FriendlyName: "Last Data Age", DeviceClass: "duration", StateClass: "measurement", Unit: "s", EntityCategory: "diagnostic"},
	"meter_link_rssi":        {FriendlyName: "Meter Link RSSI", DeviceClass: "signal_strength", StateClass: "measurement", Unit: "dBm", EntityCategory: "diagnostic"},
	"wifi_rssi":              {FriendlyName: "WiFi RSSI", DeviceClass: "signal_strength", StateClass: "measurement", Unit: "dBm", EntityCategory: "diagnostic"},
	"bridge_battery_voltage": {FriendlyName: "Bridge Battery Voltage", DeviceClass: "voltage", StateClass: "measurement", Unit: "V", EntityCategory: "diagnostic"},
	"bridge_temperature":     {FriendlyName: "Bridge Temperature", DeviceClass: "temperature", StateClass: "measurement", Unit: "°C", EntityCategory: "diagnostic"},
	"corrupt_readings":       {FriendlyName: "Corrupt Readings", StateClass: "total_increasing", EntityCategory: "diagnostic"},
}

// Device groups all sensors of one physical meter under a single HA Device.
type Device struct {
	MeterSerial  string // e.g. "LGZ-81199038" — used as device identifier
	Manufacturer string // raw 3-letter mfg code (e.g. "LGZ")
	BridgeHost   string // used for the bridge configuration link
}

// Config is the JSON payload we publish to
// <prefix>/sensor/<unique_id>/config. We compose it via map[string]any so
// it stays compact (omits null fields) without needing omitempty per field.
type Config = map[string]any

// BuildConfig produces the discovery payload for one reading or diagnostic.
// All entities read one field from a shared JSON state topic.
func BuildConfig(name string, spec SensorSpec, dev Device, stateTopic string) Config {
	uniqueID := fmt.Sprintf("tibber_pulse_%s_%s", sanitize(dev.MeterSerial), name)
	device := map[string]any{
		"identifiers":  []string{dev.MeterSerial},
		"name":         "Tibber Pulse " + dev.MeterSerial,
		"manufacturer": manufacturerName(dev.Manufacturer),
		"model":        "SML 1.04 meter",
	}
	if dev.BridgeHost != "" {
		device["configuration_url"] = "http://" + dev.BridgeHost + "/"
	}
	cfg := Config{
		"name":            spec.FriendlyName,
		"has_entity_name": true,
		"unique_id":       uniqueID,
		"object_id":       uniqueID,
		"state_topic":     stateTopic,
		"value_template":  valueTemplate(name, spec),
		"device":          device,
	}
	if spec.Unit != "" {
		cfg["unit_of_measurement"] = spec.Unit
	}
	if spec.DeviceClass != "" {
		cfg["device_class"] = spec.DeviceClass
	}
	if spec.StateClass != "" {
		cfg["state_class"] = spec.StateClass
	}
	if spec.EntityCategory != "" {
		cfg["entity_category"] = spec.EntityCategory
	}
	return cfg
}

func valueTemplate(name string, spec SensorSpec) string {
	if spec.component() == "binary_sensor" {
		return fmt.Sprintf("{{ 'ON' if value_json.%s else 'OFF' }}", name)
	}
	return fmt.Sprintf("{{ value_json.%s }}", name)
}

// ConfigTopic is where the discovery payload must be published.
func ConfigTopic(discoveryPrefix, name string, spec SensorSpec, dev Device) string {
	uniqueID := fmt.Sprintf("tibber_pulse_%s_%s", sanitize(dev.MeterSerial), name)
	return fmt.Sprintf("%s/%s/%s/config",
		strings.TrimRight(discoveryPrefix, "/"), spec.component(), uniqueID)
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
