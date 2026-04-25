package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Metrics is the parsed JSON from /metrics.json?node_id=N.
//
// Field names mirror the bridge wire format (snake_case). All numeric
// fields stay as the bridge sends them — float64 for voltages/RSSI, int
// for counters. The two outer sections (`node_status`, `hub_attachments`)
// are flattened in the Go struct to keep callers terse.
type Metrics struct {
	// node_status: hardware/radio metrics from the ESP32 plugged into the meter
	BatteryVoltage           float64 `json:"node_battery_voltage"`
	Temperature              float64 `json:"node_temperature"`
	AvgRSSI                  float64 `json:"node_avg_rssi"`
	AvgLQI                   float64 `json:"node_avg_lqi"`
	RadioTxPower             int     `json:"radio_tx_power"`
	UptimeMS                 int64   `json:"node_uptime_ms"`
	MeterMsgCountSent        int     `json:"meter_msg_count_sent"`
	MeterPkgCountSent        int     `json:"meter_pkg_count_sent"`
	InvalidMeterReadings     int     `json:"invalid_meter_readings_count"`
	MeterMode                int     `json:"meter_mode"`
	BootloaderVersion        int64   `json:"bootloader_version"`
	ProductID                int     `json:"product_id"`

	// hub_attachments: bridge-side counters
	MeterPkgCountRecv        int    `json:"meter_pkg_count_recv"`
	MeterReadingCountRecv    int    `json:"meter_reading_count_recv"`
	MeterCorruptCountRecv    int    `json:"meter_corrupt_reading_count_recv"`
	CompressionErrorReadings int    `json:"compression_error_readings_count"`
	NodeVersion              string `json:"node_version"`
}

// metricsEnvelope matches the on-the-wire shape with the two outer
// sections, just to peel them off into a flat Metrics.
type metricsEnvelope struct {
	NodeStatus     Metrics `json:"node_status"`
	HubAttachments Metrics `json:"hub_attachments"`
}

// FetchMetrics polls /metrics.json?node_id=N. Returns a flattened struct.
// Fields not present in either section stay at their Go zero value.
func (c *Client) FetchMetrics(ctx context.Context) (Metrics, error) {
	url := fmt.Sprintf("http://%s/metrics.json?node_id=%d", c.host, c.nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Metrics{}, err
	}
	req.SetBasicAuth("admin", c.password)

	resp, err := c.http.Do(req)
	if err != nil {
		return Metrics{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Metrics{}, fmt.Errorf("pulse %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Metrics{}, err
	}
	var env metricsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return Metrics{}, fmt.Errorf("metrics decode: %w (%d bytes)", err, len(body))
	}
	// Merge: hub_attachments overrides only the fields it carries.
	m := env.NodeStatus
	if env.HubAttachments.MeterPkgCountRecv != 0 {
		m.MeterPkgCountRecv = env.HubAttachments.MeterPkgCountRecv
	}
	if env.HubAttachments.MeterReadingCountRecv != 0 {
		m.MeterReadingCountRecv = env.HubAttachments.MeterReadingCountRecv
	}
	m.MeterCorruptCountRecv = env.HubAttachments.MeterCorruptCountRecv
	m.CompressionErrorReadings = env.HubAttachments.CompressionErrorReadings
	if env.HubAttachments.NodeVersion != "" {
		m.NodeVersion = env.HubAttachments.NodeVersion
	}
	return m, nil
}
