package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0Bu/tibber-pulse-bot/internal/output"
	"github.com/0Bu/tibber-pulse-bot/internal/pulse"
	"github.com/0Bu/tibber-pulse-bot/internal/sml"
)

type memorySink struct {
	readings [][]sml.Reading
}

func (s *memorySink) Publish(_ context.Context, r []sml.Reading) error {
	s.readings = append(s.readings, r)
	return nil
}

func (s *memorySink) Close() {}

func TestPollOnce(t *testing.T) {
	t.Run("fails when server returns 500", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "server error", http.StatusInternalServerError)
		}))
		defer ts.Close()

		client := pulse.NewClient(ts.URL, "pass", 1)
		sink := &memorySink{}

		err := pollOnce(context.Background(), client, sink)
		if err == nil {
			t.Fatal("want error from pollOnce when server errors, got nil")
		}
	})

	t.Run("fails when payload contains no readings", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not-sml-data"))
		}))
		defer ts.Close()

		client := pulse.NewClient(ts.URL, "pass", 1)
		sink := &memorySink{}

		err := pollOnce(context.Background(), client, sink)
		if err == nil {
			t.Fatal("want error for empty SML readings, got nil")
		}
	})
}

func TestLogBridgeStdout(t *testing.T) {
	origOutput := log.Writer()
	defer log.SetOutput(origOutput)

	t.Run("formats wifiRSSI as n/a when status is nil", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)

		u := output.BridgeUpdate{
			Metrics: pulse.Metrics{
				BatteryVoltage:        3.6,
				Temperature:           20.0,
				AvgRSSI:               -70.0,
				MeterCorruptCountRecv: 1,
			},
			Node:   nil,
			Status: nil,
		}
		logBridgeStdout(u)

		logged := buf.String()
		if !strings.Contains(logged, "wifiRSSI=n/a") {
			t.Errorf("expected wifiRSSI=n/a, got: %s", logged)
		}
		if strings.Contains(logged, "wifiRSSI=0dBm") {
			t.Errorf("should not contain wifiRSSI=0dBm, got: %s", logged)
		}
	})

	t.Run("formats wifiRSSI with dBm when status is present", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)

		st := pulse.Status{}
		st.WiFi.RSSI = -62
		u := output.BridgeUpdate{
			Metrics: pulse.Metrics{
				BatteryVoltage:        3.6,
				Temperature:           20.0,
				AvgRSSI:               -70.0,
				MeterCorruptCountRecv: 0,
			},
			Status: &st,
		}
		logBridgeStdout(u)

		logged := buf.String()
		if !strings.Contains(logged, "wifiRSSI=-62dBm") {
			t.Errorf("expected wifiRSSI=-62dBm, got: %s", logged)
		}
	})
}

func TestRunPushPermanentErrors(t *testing.T) {
	t.Run("terminates on HTTP 401 Unauthorized", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}))
		defer ts.Close()

		host := strings.TrimPrefix(ts.URL, "http://")
		client := pulse.NewClient(host, "badpass", 1)
		sink := &memorySink{}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err := runPush(ctx, client, sink, 1*time.Second, 100*time.Millisecond, false)
		if !errors.Is(err, pulse.ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized, got: %v", err)
		}
	})

	t.Run("terminates on HTTP 404 Not Found", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))
		defer ts.Close()

		host := strings.TrimPrefix(ts.URL, "http://")
		client := pulse.NewClient(host, "pass", 1)
		sink := &memorySink{}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err := runPush(ctx, client, sink, 1*time.Second, 100*time.Millisecond, false)
		if !errors.Is(err, pulse.ErrFirmwareNoWS) {
			t.Fatalf("expected ErrFirmwareNoWS, got: %v", err)
		}
	})
}

func TestRunPollPermanentErrors(t *testing.T) {
	t.Run("terminates on HTTP 401 Unauthorized", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}))
		defer ts.Close()

		host := strings.TrimPrefix(ts.URL, "http://")
		client := pulse.NewClient(host, "badpass", 1)
		sink := &memorySink{}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err := runPoll(ctx, client, sink, 50*time.Millisecond)
		if !errors.Is(err, pulse.ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized, got: %v", err)
		}
	})

	t.Run("terminates cleanly on context cancellation", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		host := strings.TrimPrefix(ts.URL, "http://")
		client := pulse.NewClient(host, "pass", 1)
		sink := &memorySink{}

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		err := runPoll(ctx, client, sink, 50*time.Millisecond)
		if err != nil {
			t.Fatalf("expected nil error on cancellation, got: %v", err)
		}
	})
}

func TestRunMetrics(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/metrics.json":
			_, _ = w.Write([]byte(`{
				"node_status": {"node_battery_voltage": 3.6, "node_temperature": 21.0, "node_avg_rssi": -68.0},
				"hub_attachments": {"meter_corrupt_reading_count_recv": 0}
			}`))
		case "/nodes.json":
			_, _ = w.Write([]byte(`[{"node_id": 1, "available": true, "last_data_ms": 1500}]`))
		case "/status.json":
			_, _ = w.Write([]byte(`{"wifi_status": {"rssi": -60}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := pulse.NewClient(ts.URL, "pass", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	runMetrics(ctx, client, nil, 50*time.Millisecond)
}

func TestValidateConfig(t *testing.T) {
	valid := botConfig{
		pulseHost:         "192.168.1.50",
		pulsePassword:     "dummy-secret",
		pulseNode:         1,
		mode:              "push",
		interval:          10 * time.Second,
		idleTimeout:       60 * time.Second,
		reconnectDelay:    100 * time.Millisecond,
		metricsInterval:   60 * time.Second,
		haDiscovery:       false,
		haDiscoveryPrefix: "homeassistant",
		mqttHost:          "broker.local",
		mqttPort:          1883,
		mqttTopic:         "tibber/pulse",
		mqttClientID:      "tibber-pulse-bot",
	}

	t.Run("valid configuration passes", func(t *testing.T) {
		if err := validateConfig(valid); err != nil {
			t.Errorf("expected nil error, got: %v", err)
		}
	})

	t.Run("missing pulseHost fails", func(t *testing.T) {
		c := valid
		c.pulseHost = ""
		if err := validateConfig(c); err == nil {
			t.Error("want error for empty pulseHost")
		}
		c.pulseHost = "   "
		if err := validateConfig(c); err == nil {
			t.Error("want error for whitespace pulseHost")
		}
	})

	t.Run("missing pulsePassword fails", func(t *testing.T) {
		c := valid
		c.pulsePassword = ""
		if err := validateConfig(c); err == nil {
			t.Error("want error for empty pulsePassword")
		}
		c.pulsePassword = "   "
		if err := validateConfig(c); err == nil {
			t.Error("want error for whitespace pulsePassword")
		}
	})

	t.Run("invalid mode fails", func(t *testing.T) {
		c := valid
		c.mode = "invalid"
		if err := validateConfig(c); err == nil {
			t.Error("want error for invalid mode")
		}
	})

	t.Run("pulseNode < 1 fails", func(t *testing.T) {
		c := valid
		c.pulseNode = 0
		if err := validateConfig(c); err == nil {
			t.Error("want error for pulseNode < 1")
		}
	})

	t.Run("poll mode with non-positive interval fails", func(t *testing.T) {
		c := valid
		c.mode = "poll"
		c.interval = 0
		if err := validateConfig(c); err == nil {
			t.Error("want error for zero interval in poll mode")
		}
	})

	t.Run("negative idleTimeout fails", func(t *testing.T) {
		c := valid
		c.idleTimeout = -1 * time.Second
		if err := validateConfig(c); err == nil {
			t.Error("want error for negative idleTimeout")
		}
	})

	t.Run("non-positive reconnectDelay fails", func(t *testing.T) {
		c := valid
		c.reconnectDelay = 0
		if err := validateConfig(c); err == nil {
			t.Error("want error for zero reconnectDelay")
		}
	})

	t.Run("negative metricsInterval fails", func(t *testing.T) {
		c := valid
		c.metricsInterval = -1 * time.Second
		if err := validateConfig(c); err == nil {
			t.Error("want error for negative metricsInterval")
		}
	})

	t.Run("empty haDiscoveryPrefix when discovery enabled fails", func(t *testing.T) {
		c := valid
		c.haDiscovery = true
		c.haDiscoveryPrefix = "   "
		if err := validateConfig(c); err == nil {
			t.Error("want error for empty haDiscoveryPrefix with discovery enabled")
		}
	})

	t.Run("invalid mqttPort fails", func(t *testing.T) {
		c := valid
		c.mqttPort = 0
		if err := validateConfig(c); err == nil {
			t.Error("want error for mqttPort = 0")
		}
		c.mqttPort = 70000
		if err := validateConfig(c); err == nil {
			t.Error("want error for mqttPort = 70000")
		}
	})

	t.Run("empty mqttTopic when mqttHost is set fails", func(t *testing.T) {
		c := valid
		c.mqttTopic = "   "
		if err := validateConfig(c); err == nil {
			t.Error("want error for empty mqttTopic when mqttHost is set")
		}
	})

	t.Run("empty mqttClientID when mqttHost is set fails", func(t *testing.T) {
		c := valid
		c.mqttClientID = ""
		if err := validateConfig(c); err == nil {
			t.Error("want error for empty mqttClientID when mqttHost is set")
		}
		c.mqttClientID = "   "
		if err := validateConfig(c); err == nil {
			t.Error("want error for whitespace mqttClientID when mqttHost is set")
		}
	})
}
