package pulse

import (
	"context"
	"encoding/json"
	"fmt"
)

// Metrics is the parsed JSON from /metrics.json?node_id=N.
//
// Only fields used by the reduced diagnostics document are decoded. Unknown
// JSON fields are intentionally ignored instead of becoming MQTT/HA noise.
type Metrics struct {
	BatteryVoltage        float64 `json:"node_battery_voltage"`
	Temperature           float64 `json:"node_temperature"`
	AvgRSSI               float64 `json:"node_avg_rssi"`
	MeterCorruptCountRecv int     `json:"meter_corrupt_reading_count_recv"`
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
	body, err := c.get(ctx, url)
	if err != nil {
		return Metrics{}, err
	}
	var env metricsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return Metrics{}, fmt.Errorf("metrics decode: %w (%d bytes)", err, len(body))
	}
	// The hardware fields live in node_status; the corrupt-reading counter is
	// reported in hub_attachments.
	m := env.NodeStatus
	m.MeterCorruptCountRecv = env.HubAttachments.MeterCorruptCountRecv
	return m, nil
}
