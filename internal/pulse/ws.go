package pulse

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// ErrPeerClosed is returned by StreamFrames when the bridge closed the WS
// without an error (typical: TCP RST/EOF every 30–60 s, no close frame).
// Callers should reconnect quietly without alarming the operator.
var ErrPeerClosed = errors.New("ws: peer closed connection")

// WSFrame is one push message from the bridge: the parsed header attributes
// and the raw body bytes (for SML mode, this is a single SML 1.04 telegram).
type WSFrame struct {
	Header map[string]string
	Body   []byte
}

// StreamFrames opens ws://<host>/ws with Basic auth and invokes onFrame for
// every push message. Blocks until ctx is cancelled or the connection drops
// with a non-recoverable error. Reconnects are the caller's responsibility.
//
// idleTimeout closes the connection if no message is received within the
// window — the bridge does not reliably emit close frames when the meter
// goes silent. Set to 0 to disable.
func (c *Client) StreamFrames(ctx context.Context, idleTimeout time.Duration, onFrame func(WSFrame)) error {
	url := fmt.Sprintf("ws://%s/ws", c.host)

	hdr := http.Header{}
	cred := base64.StdEncoding.EncodeToString([]byte("admin:" + c.password))
	hdr.Set("Authorization", "Basic "+cred)

	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader:      hdr,
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("ws: bridge firmware too old for /ws push (HTTP 404) — use --mode poll")
		}
		return fmt.Errorf("ws dial: %w", err)
	}
	defer conn.CloseNow()

	for {
		readCtx := ctx
		var cancel context.CancelFunc
		if idleTimeout > 0 {
			readCtx, cancel = context.WithTimeout(ctx, idleTimeout)
		}
		_, data, err := conn.Read(readCtx)
		if cancel != nil {
			cancel()
		}
		if err != nil {
			if errors.Is(err, context.Canceled) && ctx.Err() != nil {
				return ctx.Err()
			}
			if isPeerClose(err) {
				return ErrPeerClosed
			}
			return fmt.Errorf("ws read: %w", err)
		}

		frame, ok := parseWSFrame(data)
		if !ok {
			continue
		}
		onFrame(frame)
	}
}

// isPeerClose reports whether err means the bridge dropped the connection
// without protocol-level error — either a clean WS close, an EOF, or a
// CloseAbnormalClosure (1006), all expected periodically with this firmware.
func isPeerClose(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	if status := websocket.CloseStatus(err); status != -1 {
		return true
	}
	// coder/websocket reports the underlying read error wrapped in a string.
	s := err.Error()
	return strings.Contains(s, "EOF") || strings.Contains(s, "connection reset")
}

// parseWSFrame splits "<key:value key:\"value\" ...>BODY..." into header map
// and body slice. The body starts after the first '>' byte.
func parseWSFrame(b []byte) (WSFrame, bool) {
	if len(b) < 2 || b[0] != '<' {
		return WSFrame{}, false
	}
	end := bytes.IndexByte(b, '>')
	if end < 0 {
		return WSFrame{}, false
	}
	hdr := parseHeaderAttrs(string(b[1:end]))
	return WSFrame{Header: hdr, Body: b[end+1:]}, true
}

// parseHeaderAttrs handles `key:value` pairs separated by spaces, with values
// optionally quoted ("...") to allow embedded spaces/colons.
func parseHeaderAttrs(s string) map[string]string {
	out := map[string]string{}
	i := 0
	for i < len(s) {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		keyStart := i
		for i < len(s) && s[i] != ':' && s[i] != ' ' {
			i++
		}
		if i >= len(s) || s[i] != ':' {
			break
		}
		key := s[keyStart:i]
		i++
		var val string
		if i < len(s) && s[i] == '"' {
			i++
			valStart := i
			for i < len(s) && s[i] != '"' {
				i++
			}
			val = s[valStart:i]
			if i < len(s) {
				i++
			}
		} else {
			valStart := i
			for i < len(s) && s[i] != ' ' {
				i++
			}
			val = s[valStart:i]
		}
		out[key] = val
	}
	return out
}
