package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Node mirrors one entry of /nodes.json.
type Node struct {
	NodeID              int    `json:"node_id"`
	EUI                 string `json:"eui"`
	ProductModel        string `json:"product_model"`
	Model               string `json:"model"`
	Version             string `json:"version"`
	Available           bool   `json:"available"`
	LastSeenMS          int64  `json:"last_seen_ms"`
	LastDataMS          int64  `json:"last_data_ms"`
	AverageRSSI         int    `json:"average_rssi"`
	AverageLQI          int    `json:"average_lqi"`
	OTADistributeStatus string `json:"ota_distribute_status"`
	Paired              bool   `json:"paired"`
}

// Status mirrors /status.json (subset of what we surface).
type Status struct {
	PairingStatus string `json:"pairing_status"`
	UpTime        int64  `json:"up_time"`
	Firmware      struct {
		ESP string `json:"esp"`
		EFR string `json:"efr"`
	} `json:"firmware"`
	WiFi struct {
		IP        string `json:"ip"`
		SSID      string `json:"ssid"`
		BSSID     string `json:"bssid"`
		RSSI      int    `json:"rssi"`
		Connected bool   `json:"connected"`
	} `json:"wifi_status"`
	MQTT struct {
		Connected  bool `json:"connected"`
		Subscribed bool `json:"subscribed"`
	} `json:"mqtt_status"`
	OTAUpdateRunning bool `json:"ota_update_running"`
}

// OTAEntry mirrors one entry of /ota_manifest.json (subset).
type OTAEntry struct {
	Model           string `json:"model"`
	OTAIndex        int    `json:"ota_index"`
	CurrentVersion  string `json:"current_version"`
	ManifestVersion string `json:"manifest_version"`
	Up2Date         bool   `json:"up2date"`
}

// FetchNode returns the bridge's view of the configured node (the pulse
// attached to the meter). Returns an error if the node is missing.
func (c *Client) FetchNode(ctx context.Context) (Node, error) {
	url := fmt.Sprintf("http://%s/nodes.json", c.host)
	body, err := c.fetchJSON(ctx, url)
	if err != nil {
		return Node{}, err
	}
	var nodes []Node
	if err := json.Unmarshal(body, &nodes); err != nil {
		return Node{}, fmt.Errorf("nodes decode: %w", err)
	}
	for _, n := range nodes {
		if n.NodeID == c.nodeID {
			return n, nil
		}
	}
	return Node{}, fmt.Errorf("node %d not found in /nodes.json", c.nodeID)
}

// FetchStatus returns the bridge's runtime status (WiFi, cloud-MQTT,
// firmware versions, OTA flag).
func (c *Client) FetchStatus(ctx context.Context) (Status, error) {
	url := fmt.Sprintf("http://%s/status.json?timeout=0", c.host)
	body, err := c.fetchJSON(ctx, url)
	if err != nil {
		return Status{}, err
	}
	var s Status
	if err := json.Unmarshal(body, &s); err != nil {
		return Status{}, fmt.Errorf("status decode: %w", err)
	}
	return s, nil
}

// FetchOTAManifest returns the per-component OTA state. Aggregating its
// up2date flags tells whether any component has a pending firmware update.
func (c *Client) FetchOTAManifest(ctx context.Context) ([]OTAEntry, error) {
	url := fmt.Sprintf("http://%s/ota_manifest.json", c.host)
	body, err := c.fetchJSON(ctx, url)
	if err != nil {
		return nil, err
	}
	var entries []OTAEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("ota decode: %w", err)
	}
	return entries, nil
}

func (c *Client) fetchJSON(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("admin", c.password)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pulse %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
