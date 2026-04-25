package pulse

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	host     string
	password string
	nodeID   int
	http     *http.Client
}

func NewClient(host, password string, nodeID int) *Client {
	return &Client{
		host:     host,
		password: password,
		nodeID:   nodeID,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

// FetchData returns the raw response body of /data.json?node_id=<n>.
// For SML meters this is a binary SML 1.04 stream containing one or more frames.
func (c *Client) FetchData(ctx context.Context) ([]byte, error) {
	url := fmt.Sprintf("http://%s/data.json?node_id=%d", c.host, c.nodeID)
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
