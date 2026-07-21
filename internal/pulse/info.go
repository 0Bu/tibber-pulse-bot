package pulse

import (
	"context"
	"encoding/json"
	"fmt"
)

// Node mirrors one entry of /nodes.json.
type Node struct {
	NodeID     *int   `json:"node_id"`
	EUI        string `json:"eui"`
	Available  bool   `json:"available"`
	LastDataMS int64  `json:"last_data_ms"`
}

// Status decodes only the router-link RSSI used by diagnostics.
type Status struct {
	WiFi struct {
		RSSI int `json:"rssi"`
	} `json:"wifi_status"`
}

// FetchNode returns the bridge's view of the configured node (the pulse
// attached to the meter). Returns an error if the node is missing.
func (c *Client) FetchNode(ctx context.Context) (Node, error) {
	url := fmt.Sprintf("http://%s/nodes.json", c.host)
	body, err := c.get(ctx, url)
	if err != nil {
		return Node{}, err
	}
	var nodes []Node
	if err := json.Unmarshal(body, &nodes); err != nil {
		return Node{}, fmt.Errorf("nodes decode: %w", err)
	}
	for _, n := range nodes {
		if n.NodeID != nil && *n.NodeID == c.nodeID {
			return n, nil
		}
	}
	return Node{}, fmt.Errorf("node %d not found in /nodes.json", c.nodeID)
}

func (c *Client) FetchStatus(ctx context.Context) (Status, error) {
	url := fmt.Sprintf("http://%s/status.json?timeout=0", c.host)
	body, err := c.get(ctx, url)
	if err != nil {
		return Status{}, err
	}
	var s Status
	if err := json.Unmarshal(body, &s); err != nil {
		return Status{}, fmt.Errorf("status decode: %w", err)
	}
	return s, nil
}
