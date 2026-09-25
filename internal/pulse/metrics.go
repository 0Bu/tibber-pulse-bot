package pulse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Metrics is the parsed JSON from /node_metrics.json?node_id=N or /metrics.json?node_id=N.
//
// Only fields used by the reduced diagnostics document are decoded. Unknown
// JSON fields are intentionally ignored instead of becoming MQTT/HA noise.
type Metrics struct {
	BatteryVoltage        float64 `json:"node_battery_voltage"`
	Temperature           float64 `json:"node_temperature"`
	AvgRSSI               float64 `json:"node_avg_rssi"`
	MeterCorruptCountRecv int     `json:"meter_corrupt_reading_count_recv"`
}

func (m *Metrics) UnmarshalJSON(data []byte) error {
	type rawMetrics struct {
		BatteryVoltage        *float64 `json:"battery_voltage"`
		NodeBatteryVoltage    *float64 `json:"node_battery_voltage"`
		Temperature           *float64 `json:"temperature"`
		NodeTemperature       *float64 `json:"node_temperature"`
		AvgRSSI               *float64 `json:"avg_rssi"`
		NodeAvgRSSI           *float64 `json:"node_avg_rssi"`
		MeterCorruptCountRecv int      `json:"meter_corrupt_reading_count_recv"`
	}
	var raw rawMetrics
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.BatteryVoltage != nil {
		m.BatteryVoltage = *raw.BatteryVoltage
	} else if raw.NodeBatteryVoltage != nil {
		m.BatteryVoltage = *raw.NodeBatteryVoltage
	}
	if raw.Temperature != nil {
		m.Temperature = *raw.Temperature
	} else if raw.NodeTemperature != nil {
		m.Temperature = *raw.NodeTemperature
	}
	if raw.AvgRSSI != nil {
		m.AvgRSSI = *raw.AvgRSSI
	} else if raw.NodeAvgRSSI != nil {
		m.AvgRSSI = *raw.NodeAvgRSSI
	}
	m.MeterCorruptCountRecv = raw.MeterCorruptCountRecv
	return nil
}

// metricsEnvelope matches the on-the-wire shape with the outer sections:
// - Legacy firmware: "node_status" and "hub_attachments"
// - Modern firmware: "node", "ir", and "hub"
type metricsEnvelope struct {
	NodeStatus     *Metrics `json:"node_status"`
	HubAttachments *struct {
		MeterCorruptReadingCountRecv *int `json:"meter_corrupt_reading_count_recv"`
	} `json:"hub_attachments"`

	Node *Metrics `json:"node"`
	IR   *struct {
		InvalidMeterReadingsCount *int `json:"invalid_meter_readings_count"`
	} `json:"ir"`
	Hub *struct {
		MeterCorruptReadingCountRecv      *int `json:"meter_corrupt_reading_count_recv"`
		MeterCorruptReadingCountRecvDelta *int `json:"meter_corrupt_reading_count_received_delta"`
	} `json:"hub"`
}

func parseMetrics(body []byte) (Metrics, error) {
	var env metricsEnvelope
	if err := json.Unmarshal(body, &env); err == nil {
		if env.Node != nil {
			m := *env.Node
			if env.IR != nil && env.IR.InvalidMeterReadingsCount != nil {
				m.MeterCorruptCountRecv = *env.IR.InvalidMeterReadingsCount
			} else if env.Hub != nil && env.Hub.MeterCorruptReadingCountRecv != nil {
				m.MeterCorruptCountRecv = *env.Hub.MeterCorruptReadingCountRecv
			} else if env.Hub != nil && env.Hub.MeterCorruptReadingCountRecvDelta != nil {
				m.MeterCorruptCountRecv = *env.Hub.MeterCorruptReadingCountRecvDelta
			}
			return m, nil
		}
		if env.NodeStatus != nil {
			m := *env.NodeStatus
			if env.HubAttachments != nil && env.HubAttachments.MeterCorruptReadingCountRecv != nil {
				m.MeterCorruptCountRecv = *env.HubAttachments.MeterCorruptReadingCountRecv
			}
			return m, nil
		}
	}
	var m Metrics
	if err := json.Unmarshal(body, &m); err != nil {
		return Metrics{}, fmt.Errorf("metrics decode: %w (%d bytes)", err, len(body))
	}
	return m, nil
}

// FetchMetrics polls /node_metrics.json?node_id=N (modern) or /metrics.json?node_id=N (legacy).
// Returns a flattened struct. Fields not present in either section stay at their Go zero value.
func (c *Client) FetchMetrics(ctx context.Context) (Metrics, error) {
	c.mu.Lock()
	cached := c.metricsEndpoint
	c.mu.Unlock()

	var candidates []string
	if cached != "" {
		candidates = append(candidates, cached)
		if cached == "/node_metrics.json" {
			candidates = append(candidates, "/metrics.json")
		} else {
			candidates = append(candidates, "/node_metrics.json")
		}
	} else {
		candidates = []string{"/node_metrics.json", "/metrics.json"}
	}

	var lastErr error
	for _, ep := range candidates {
		url := fmt.Sprintf("http://%s%s?node_id=%d", c.host, ep, c.nodeID)
		body, err := c.get(ctx, url)
		if err != nil {
			lastErr = err
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return Metrics{}, err
		}

		m, err := parseMetrics(body)
		if err != nil {
			return Metrics{}, err
		}

		c.mu.Lock()
		c.metricsEndpoint = ep
		c.mu.Unlock()

		return m, nil
	}
	return Metrics{}, lastErr
}
