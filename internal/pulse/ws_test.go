package pulse

import (
	"errors"
	"io"
	"testing"
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
