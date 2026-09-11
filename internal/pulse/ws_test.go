package pulse

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestParseWSFrame(t *testing.T) {
	t.Run("splits header and body at first >", func(t *testing.T) {
		raw := []byte(`<topic:test/sml len:4>` + "\x01\x02\x03\x04")
		f, ok := parseWSFrame(raw)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if f.Header["topic"] != "test/sml" {
			t.Errorf("topic = %q, want test/sml", f.Header["topic"])
		}
		if f.Header["len"] != "4" {
			t.Errorf("len = %q, want 4", f.Header["len"])
		}
		if string(f.Body) != "\x01\x02\x03\x04" {
			t.Errorf("body = %x, want 01020304", f.Body)
		}
	})

	t.Run("body may contain > bytes after the first", func(t *testing.T) {
		raw := []byte(`<a:b>x>y`)
		f, ok := parseWSFrame(raw)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if string(f.Body) != "x>y" {
			t.Errorf("body = %q, want x>y", f.Body)
		}
	})

	t.Run("rejects input without leading <", func(t *testing.T) {
		if _, ok := parseWSFrame([]byte("topic:x>body")); ok {
			t.Error("expected ok=false without leading <")
		}
	})

	t.Run("rejects input without >", func(t *testing.T) {
		if _, ok := parseWSFrame([]byte("<topic:x")); ok {
			t.Error("expected ok=false without >")
		}
	})

	t.Run("rejects too-short input", func(t *testing.T) {
		if _, ok := parseWSFrame([]byte("<")); ok {
			t.Error("expected ok=false for 1-byte input")
		}
	})
}

func TestParseHeaderAttrs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"unquoted pairs", "a:1 b:2", map[string]string{"a": "1", "b": "2"}},
		{"quoted value with spaces", `name:"hello world" x:1`, map[string]string{"name": "hello world", "x": "1"}},
		{"quoted value with colon", `t:"a:b:c"`, map[string]string{"t": "a:b:c"}},
		{"leading and extra spaces", "  a:1   b:2  ", map[string]string{"a": "1", "b": "2"}},
		{"empty value", "a: b:2", map[string]string{"a": "", "b": "2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseHeaderAttrs(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("key %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestIsPeerClose(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"io.EOF", io.EOF, true},
		{"wrapped EOF string", errors.New("ws read: unexpected EOF"), true},
		{"connection reset", errors.New("read tcp: connection reset by peer"), true},
		{"generic error", errors.New("some protocol error"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPeerClose(tt.err); got != tt.want {
				t.Errorf("isPeerClose(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestStreamFramesPermanentErrors(t *testing.T) {
	t.Run("returns ErrFirmwareNoWS on HTTP 404", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))
		defer s.Close()

		host := strings.TrimPrefix(s.URL, "http://")
		client := NewClient(host, "pass", 1)
		err := client.StreamFrames(context.Background(), 1*time.Second, func(f WSFrame) {})
		if !errors.Is(err, ErrFirmwareNoWS) {
			t.Errorf("expected ErrFirmwareNoWS, got: %v", err)
		}
		if !IsPermanent(err) {
			t.Errorf("expected IsPermanent to report true for %v", err)
		}
	})

	t.Run("returns ErrUnauthorized on HTTP 401", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}))
		defer s.Close()

		host := strings.TrimPrefix(s.URL, "http://")
		client := NewClient(host, "pass", 1)
		err := client.StreamFrames(context.Background(), 1*time.Second, func(f WSFrame) {})
		if !errors.Is(err, ErrUnauthorized) {
			t.Errorf("expected ErrUnauthorized, got: %v", err)
		}
		if !IsPermanent(err) {
			t.Errorf("expected IsPermanent to report true for %v", err)
		}
	})
}

func TestStreamFramesStreamingAndIdleTimeout(t *testing.T) {
	t.Run("streams valid frame and completes callback", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()

			ctx := r.Context()
			_ = c.Write(ctx, websocket.MessageText, []byte("<topic:test len:4>test"))
			// Allow client to read before closing
			time.Sleep(50 * time.Millisecond)
		}))
		defer s.Close()

		host := strings.TrimPrefix(s.URL, "http://")
		client := NewClient(host, "pass", 1)

		received := make(chan WSFrame, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		err := client.StreamFrames(ctx, 500*time.Millisecond, func(f WSFrame) {
			received <- f
			cancel() // End stream once frame received
		})

		if !errors.Is(err, context.Canceled) && err != nil && !errors.Is(err, ErrPeerClosed) {
			t.Fatalf("unexpected StreamFrames error: %v", err)
		}

		select {
		case f := <-received:
			if f.Header["topic"] != "test" {
				t.Errorf("topic = %q, want test", f.Header["topic"])
			}
			if string(f.Body) != "test" {
				t.Errorf("body = %q, want test", string(f.Body))
			}
		default:
			t.Fatal("frame was not received")
		}
	})

	t.Run("returns ErrIdleTimeout when server sends no frames", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			// Keep connection open without sending anything
			time.Sleep(500 * time.Millisecond)
		}))
		defer s.Close()

		host := strings.TrimPrefix(s.URL, "http://")
		client := NewClient(host, "pass", 1)

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		err := client.StreamFrames(ctx, 100*time.Millisecond, func(f WSFrame) {})
		if !errors.Is(err, ErrIdleTimeout) {
			t.Fatalf("expected ErrIdleTimeout, got: %v", err)
		}
		if IsPermanent(err) {
			t.Errorf("IsPermanent should be false for ErrIdleTimeout")
		}
	})
}
