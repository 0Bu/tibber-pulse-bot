package discovery

import (
	"fmt"
	"strings"
)

// BridgeDevice identifies the Tibber Pulse Bridge (the ESP32 hardware) as
// a separate HA device from the meter it reads. The bridge's identifier
// is derived from its host address so it stays stable across restarts.
type BridgeDevice struct {
	Host       string // e.g. "192.168.107.118"
	SWVersion  string // node_version, e.g. "1201-f4b8d10b"
}

// BridgeIdentifier is also returned to be used as via_device on the
// meter sensors so HA links the meter card to the bridge card.
func (b BridgeDevice) Identifier() string {
	return "tibber-pulse-bridge-" + sanitize(b.Host)
}

// BridgeSensors maps the metric field name to its HA discovery metadata.
// "name" here is the entity-suffix; with has_entity_name=true HA renders
// each as "<Bridge> <name>".
var BridgeSensors = map[string]SensorSpec{
	"battery_voltage":     {"Battery Voltage", "voltage", "measurement", "V"},
	"temperature":         {"Temperature", "temperature", "measurement", "°C"},
	"rssi":                {"RSSI", "signal_strength", "measurement", "dBm"},
	"lqi":                 {"Link Quality", "", "measurement", ""},
	"uptime":              {"Uptime", "duration", "measurement", "s"},
	"pkg_sent":            {"Packets Sent", "", "total_increasing", ""},
	"pkg_received":        {"Packets Received", "", "total_increasing", ""},
	"readings_received":   {"Readings Received", "", "total_increasing", ""},
	"corrupt_readings":    {"Corrupt Readings", "", "total_increasing", ""},
	"invalid_readings":    {"Invalid Readings", "", "total_increasing", ""},
}

// BuildBridgeConfig produces the discovery payload for one bridge metric.
func BuildBridgeConfig(name string, spec SensorSpec, b BridgeDevice, stateTopic string) Config {
	bid := b.Identifier()
	uniqueID := fmt.Sprintf("%s_%s", bid, name)
	dev := map[string]any{
		"identifiers":   []string{bid},
		"name":          "Tibber Pulse Bridge " + b.Host,
		"manufacturer":  "Tibber",
		"model":         "Pulse Bridge",
		"configuration_url": "http://" + b.Host + "/",
	}
	if b.SWVersion != "" {
		dev["sw_version"] = b.SWVersion
	}
	cfg := Config{
		"name":            spec.FriendlyName,
		"has_entity_name": true,
		"unique_id":       uniqueID,
		"object_id":       uniqueID,
		"state_topic":     stateTopic,
		"state_class":     spec.StateClass,
		"entity_category": "diagnostic",
		"device":          dev,
	}
	if spec.DeviceClass != "" {
		cfg["device_class"] = spec.DeviceClass
	}
	if spec.Unit != "" {
		cfg["unit_of_measurement"] = spec.Unit
	}
	return cfg
}

// BridgeConfigTopic returns the topic at which the bridge discovery
// payload must be retained.
func BridgeConfigTopic(discoveryPrefix, name string, b BridgeDevice) string {
	uniqueID := fmt.Sprintf("%s_%s", b.Identifier(), name)
	return fmt.Sprintf("%s/sensor/%s/config",
		strings.TrimRight(discoveryPrefix, "/"), uniqueID)
}
