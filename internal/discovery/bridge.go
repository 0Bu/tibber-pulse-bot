package discovery

import (
	"fmt"
	"strings"
)

// BridgePrefix identifies discovery configs produced by releases that exposed
// bridge diagnostics as a separate Home Assistant device. It remains only so
// the consolidated publisher can remove those retained legacy entities.
const BridgePrefix = "tibber-pulse-bridge-"

// LegacyBridgeDevice reconstructs the former HA identifier for migration.
// EUI was preferred from v1.0.5 onward; older installs used the bridge host.
type LegacyBridgeDevice struct {
	Host string
	EUI  string
}

func (b LegacyBridgeDevice) Identifier() string {
	if b.EUI != "" {
		return BridgePrefix + sanitize(b.EUI)
	}
	return BridgePrefix + sanitize(b.Host)
}

// LegacyBridgeSensors lists the static discovery configs emitted before the
// two-JSON-topic layout. Values are their HA components. The list is used only
// for a publish-only cleanup when the broker denies discovery subscriptions.
var LegacyBridgeSensors = map[string]string{
	"battery_voltage": "sensor", "temperature": "sensor", "rssi": "sensor",
	"lqi": "sensor", "radio_tx_power": "sensor", "uptime": "sensor",
	"meter_msg_sent": "sensor", "pkg_sent": "sensor", "pkg_received": "sensor",
	"readings_received": "sensor", "corrupt_readings": "sensor",
	"invalid_readings": "sensor", "compression_error_readings": "sensor",
	"meter_mode": "sensor", "bootloader_version": "sensor", "product_id": "sensor",
	"node_version": "sensor", "node_id": "sensor", "eui": "sensor",
	"product_model": "sensor", "model": "sensor", "version": "sensor",
	"last_seen_age": "sensor", "last_data_age": "sensor", "average_rssi": "sensor",
	"average_lqi": "sensor", "ota_distribute_status": "sensor",
	"pairing_status": "sensor", "bridge_uptime": "sensor", "firmware_esp": "sensor",
	"firmware_efr": "sensor", "wifi_ip": "sensor", "wifi_ssid": "sensor",
	"wifi_bssid": "sensor", "wifi_rssi": "sensor", "available": "binary_sensor",
	"paired": "binary_sensor", "wifi_connected": "binary_sensor",
	"cloud_mqtt": "binary_sensor", "cloud_mqtt_subscribed": "binary_sensor",
	"ota_update_running": "binary_sensor", "update_available": "binary_sensor",
}

func LegacyBridgeConfigTopic(discoveryPrefix, name, component string, b LegacyBridgeDevice) string {
	uniqueID := fmt.Sprintf("%s_%s", b.Identifier(), name)
	return fmt.Sprintf("%s/%s/%s/config",
		strings.TrimRight(discoveryPrefix, "/"), component, uniqueID)
}
