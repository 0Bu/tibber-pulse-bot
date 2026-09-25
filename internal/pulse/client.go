package pulse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrNotFound indicates the requested bridge endpoint was not found (HTTP 404).
var ErrNotFound = errors.New("pulse: endpoint not found (HTTP 404)")

type Client struct {
	host     string
	password string
	nodeID   int
	http     *http.Client

	mu              sync.Mutex
	dataEndpoint    string
	metricsEndpoint string
}

func cleanHost(host string) string {
	host = strings.TrimSpace(host)
	lower := strings.ToLower(host)
	for _, prefix := range []string{"http://", "https://", "ws://", "wss://"} {
		if strings.HasPrefix(lower, prefix) {
			host = host[len(prefix):]
			break
		}
	}
	host = strings.TrimRight(host, "/")
	return host
}

func NewClient(host, password string, nodeID int) *Client {
	return &Client{
		host:     cleanHost(host),
		password: password,
		nodeID:   nodeID,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

// Host returns the sanitized bridge host or IP.
func (c *Client) Host() string {
	return c.host
}

// get issues an authenticated GET against the bridge and returns the raw
// response body. Shared by every endpoint (data/metrics/nodes/status);
// the payload may be binary SML or JSON depending on the URL.
func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
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
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, ErrUnauthorized
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("pulse %s: %w", url, ErrNotFound)
		}
		return nil, fmt.Errorf("pulse %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// FetchData returns the raw response body of /node_data.json?node_id=<n> (modern)
// or /data.json?node_id=<n> (legacy).
// For SML meters this is a binary SML 1.04 stream containing one or more frames.
func (c *Client) FetchData(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	cached := c.dataEndpoint
	c.mu.Unlock()

	var candidates []string
	if cached != "" {
		candidates = append(candidates, cached)
		if cached == "/node_data.json" {
			candidates = append(candidates, "/data.json")
		} else {
			candidates = append(candidates, "/node_data.json")
		}
	} else {
		candidates = []string{"/node_data.json", "/data.json"}
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
			return nil, err
		}

		c.mu.Lock()
		c.dataEndpoint = ep
		c.mu.Unlock()

		return body, nil
	}
	return nil, lastErr
}
