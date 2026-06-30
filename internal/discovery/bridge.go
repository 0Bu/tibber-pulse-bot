package discovery

import (
	"fmt"
	"strings"
)

// BridgeDevice identifies the Tibber Pulse Bridge (the ESP32 hardware) as
// a separate HA device from the meter it reads. EUI (the radio hardware
// address from /nodes.json) is the preferred identifier — stable across
// IP changes and reboots. Host stays as a fallback so older deployments
// still produce a usable identifier when /nodes.json hasn't been read yet.
type BridgeDevice struct {
	Host         string // e.g. "192.168.107.118"
	EUI          string // e.g. "30fb10fffe9326a9"
	ESPVersion   string // /status.json firmware.esp
	EFRVersion   string // /status.json firmware.efr
	NodeVersion  string // /metrics.json node_version (from hub_attachments)
	ProductModel string // /nodes.json product_model, e.g. "TFD01"
	NodeModel    string // /nodes.json model, e.g. "tibber-pulse-ir-node-efr32"
}

// Identifier returns the stable HA device identifier. EUI takes precedence;
// if missing (e.g. /nodes.json fetch hasn't succeeded yet), falls back to
// the host so behaviour matches v1.0.4.
func (b BridgeDevice) Identifier() string {
	if b.EUI != "" {
		return "tibber-pulse-bridge-" + sanitize(b.EUI)
	}
	return "tibber-pulse-bridge-" + sanitize(b.Host)
}

// BridgeSensors maps the metric field name to its HA discovery metadata.
// "name" here is the entity-suffix; with has_entity_name=true HA renders
// each as "<Bridge> <name>". Order is preserved per stdlib map iteration
// only for tests — production logic is map-iteration-order-agnostic.
var BridgeSensors = map[string]SensorSpec{
	// /metrics.json
	"battery_voltage": {"Battery Voltage", "voltage", "measurement", "V", ""},
	"temperature":     {"Temperature", "temperature", "measurement", "°C", ""},
	"rssi":            {"Meter RSSI", "signal_strength", "measurement", "dBm", ""},
	"lqi":             {"Meter Link Quality", "", "measurement", "", ""},
	// Raw EFR32 radio TX power figure (live bridge reports 67). Not a valid
	// dBm signal_strength (those are negative RSSI); the true unit is most
	// likely deci-dBm but unverifiable from a single sample, so we publish
	// the raw value with no device_class/unit rather than mislabel it.
	"radio_tx_power":             {"Radio TX Power", "", "measurement", "", ""},
	"uptime":                     {"Node Uptime", "duration", "measurement", "s", ""},
	"meter_msg_sent":             {"Meter Messages Sent", "", "total_increasing", "", ""},
	"pkg_sent":                   {"Packets Sent", "", "total_increasing", "", ""},
	"pkg_received":               {"Packets Received", "", "total_increasing", "", ""},
	"readings_received":          {"Readings Received", "", "total_increasing", "", ""},
	"corrupt_readings":           {"Corrupt Readings", "", "total_increasing", "", ""},
	"invalid_readings":           {"Invalid Readings", "", "total_increasing", "", ""},
	"compression_error_readings": {"Compression Error Readings", "", "total_increasing", "", ""},
	"meter_mode":                 {"Meter Mode", "", "", "", ""},
	"bootloader_version":         {"Bootloader Version", "", "", "", ""},
	"product_id":                 {"Product ID", "", "", "", ""},
	"node_version":               {"Node Firmware", "", "", "", ""},
	// /nodes.json
	"node_id":               {"Node ID", "", "", "", ""},
	"eui":                   {"EUI", "", "", "", ""},
	"product_model":         {"Product Model", "", "", "", ""},
	"model":                 {"Node Model", "", "", "", ""},
	"version":               {"Node Version", "", "", "", ""},
	"last_seen_age":         {"Last Seen Age", "duration", "measurement", "s", ""},
	"last_data_age":         {"Last Data Age", "duration", "measurement", "s", ""},
	"average_rssi":          {"Node Average RSSI", "signal_strength", "measurement", "dBm", ""},
	"average_lqi":           {"Node Average Link Quality", "", "measurement", "", ""},
	"ota_distribute_status": {"OTA Distribute Status", "", "", "", ""},
	// /status.json
	"pairing_status": {"Pairing Status", "", "", "", ""},
	"bridge_uptime":  {"Bridge Uptime", "duration", "measurement", "s", ""},
	"firmware_esp":   {"Firmware ESP", "", "", "", ""},
	"firmware_efr":   {"Firmware EFR", "", "", "", ""},
	"wifi_ip":        {"WiFi IP", "", "", "", ""},
	"wifi_ssid":      {"WiFi SSID", "", "", "", ""},
	"wifi_bssid":     {"WiFi BSSID", "", "", "", ""},
	"wifi_rssi":      {"WiFi RSSI", "signal_strength", "measurement", "dBm", ""},
	// binary sensors (HA payload "ON"/"OFF")
	"available":             {"Available", "connectivity", "", "", "binary_sensor"},
	"paired":                {"Paired", "", "", "", "binary_sensor"},
	"wifi_connected":        {"WiFi Connected", "connectivity", "", "", "binary_sensor"},
	"cloud_mqtt":            {"Tibber Cloud MQTT", "connectivity", "", "", "binary_sensor"},
	"cloud_mqtt_subscribed": {"Tibber Cloud MQTT Subscribed", "connectivity", "", "", "binary_sensor"},
	"ota_update_running":    {"OTA Update Running", "running", "", "", "binary_sensor"},
	"update_available":      {"Update Available", "update", "", "", "binary_sensor"},
}

// BuildBridgeConfig produces the discovery payload for one bridge metric.
func BuildBridgeConfig(name string, spec SensorSpec, b BridgeDevice, stateTopic string) Config {
	bid := b.Identifier()
	uniqueID := fmt.Sprintf("%s_%s", bid, name)
	dev := map[string]any{
		"identifiers":       []string{bid},
		"name":              "Tibber Pulse Bridge " + b.bestLabel(),
		"manufacturer":      "Tibber",
		"model":             b.bestModel(),
		"configuration_url": "http://" + b.Host + "/",
	}
	if v := b.composedSWVersion(); v != "" {
		dev["sw_version"] = v
	}
	if b.EUI != "" {
		dev["connections"] = [][]string{{"mac", formatEUI(b.EUI)}}
	}
	cfg := Config{
		"name":            spec.FriendlyName,
		"has_entity_name": true,
		"unique_id":       uniqueID,
		"object_id":       uniqueID,
		"state_topic":     stateTopic,
		"entity_category": "diagnostic",
		"device":          dev,
	}
	if spec.StateClass != "" {
		cfg["state_class"] = spec.StateClass
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
// payload must be retained. The component (sensor or binary_sensor) is
// derived from the SensorSpec.
func BridgeConfigTopic(discoveryPrefix, name string, spec SensorSpec, b BridgeDevice) string {
	uniqueID := fmt.Sprintf("%s_%s", b.Identifier(), name)
	c := spec.Component
	if c == "" {
		c = "sensor"
	}
	return fmt.Sprintf("%s/%s/%s/config",
		strings.TrimRight(discoveryPrefix, "/"), c, uniqueID)
}

func (b BridgeDevice) bestLabel() string {
	if b.EUI != "" {
		return b.EUI
	}
	return b.Host
}

func (b BridgeDevice) bestModel() string {
	if b.ProductModel != "" {
		return "Pulse Bridge " + b.ProductModel
	}
	return "Pulse Bridge"
}

// composedSWVersion shows both ESP32 hub and EFR32 node firmware so the
// HA device card surfaces the full picture instead of just one half.
func (b BridgeDevice) composedSWVersion() string {
	switch {
	case b.ESPVersion != "" && b.EFRVersion != "":
		return "ESP " + b.ESPVersion + " / EFR " + b.EFRVersion
	case b.NodeVersion != "":
		return b.NodeVersion
	case b.ESPVersion != "":
		return "ESP " + b.ESPVersion
	}
	return ""
}

// formatEUI renders the bare hex EUI as a colon-separated MAC-style string
// so HA's "connections" registry can dedupe with networking integrations.
func formatEUI(eui string) string {
	if len(eui) != 16 {
		return eui
	}
	return strings.ToLower(eui[0:2] + ":" + eui[2:4] + ":" + eui[4:6] + ":" +
		eui[6:8] + ":" + eui[8:10] + ":" + eui[10:12] + ":" + eui[12:14] + ":" + eui[14:16])
}
