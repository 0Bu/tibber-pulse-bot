package pulse

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCleanHost(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"192.168.1.50", "192.168.1.50"},
		{"http://192.168.1.50", "192.168.1.50"},
		{"HTTP://192.168.1.50/", "192.168.1.50"},
		{"http://192.168.1.50/", "192.168.1.50"},
		{"https://bridge.local:8080/", "bridge.local:8080"},
		{"ws://10.0.0.1/ws", "10.0.0.1/ws"},
		{"  192.168.1.100  ", "192.168.1.100"},
	}
	for _, tc := range cases {
		if got := cleanHost(tc.in); got != tc.want {
			t.Errorf("cleanHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHTTPClientEndpoints(t *testing.T) {
	const testPass = "secret123"
	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:"+testPass))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != expectedAuth {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/data.json":
			if r.URL.Query().Get("node_id") != "1" {
				http.Error(w, "Missing node_id", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("fake-sml-data"))

		case "/metrics.json":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"node_status": {
					"node_battery_voltage": 3.61,
					"node_temperature": 21.5,
					"node_avg_rssi": -65.0
				},
				"hub_attachments": {
					"meter_corrupt_reading_count_recv": 4
				}
			}`))

		case "/nodes.json":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{
					"node_id": 1,
					"eui": "0011223344556677",
					"available": true,
					"last_data_ms": 2500
				},
				{
					"node_id": 2,
					"eui": "8899aabbccddeeff",
					"available": false,
					"last_data_ms": 10000
				}
			]`))

		case "/status.json":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"wifi_status": {
					"rssi": -55
				}
			}`))

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Test with server URL (testing prefix stripping)
	client := NewClient(server.URL, testPass, 1)

	ctx := context.Background()

	t.Run("FetchData", func(t *testing.T) {
		data, err := client.FetchData(ctx)
		if err != nil {
			t.Fatalf("FetchData error: %v", err)
		}
		if string(data) != "fake-sml-data" {
			t.Errorf("FetchData payload = %q, want fake-sml-data", string(data))
		}
	})

	t.Run("FetchMetrics", func(t *testing.T) {
		metrics, err := client.FetchMetrics(ctx)
		if err != nil {
			t.Fatalf("FetchMetrics error: %v", err)
		}
		if metrics.BatteryVoltage != 3.61 {
			t.Errorf("BatteryVoltage = %v, want 3.61", metrics.BatteryVoltage)
		}
		if metrics.Temperature != 21.5 {
			t.Errorf("Temperature = %v, want 21.5", metrics.Temperature)
		}
		if metrics.AvgRSSI != -65.0 {
			t.Errorf("AvgRSSI = %v, want -65.0", metrics.AvgRSSI)
		}
		if metrics.MeterCorruptCountRecv != 4 {
			t.Errorf("MeterCorruptCountRecv = %d, want 4", metrics.MeterCorruptCountRecv)
		}
	})

	t.Run("FetchNode existing", func(t *testing.T) {
		node, err := client.FetchNode(ctx)
		if err != nil {
			t.Fatalf("FetchNode error: %v", err)
		}
		if node.EUI != "0011223344556677" {
			t.Errorf("EUI = %q, want 0011223344556677", node.EUI)
		}
		if !node.Available {
			t.Errorf("Available = false, want true")
		}
		if node.LastDataMS != 2500 {
			t.Errorf("LastDataMS = %d, want 2500", node.LastDataMS)
		}
	})

	t.Run("FetchNode missing nodeID", func(t *testing.T) {
		clientNode3 := NewClient(server.URL, testPass, 3)
		_, err := clientNode3.FetchNode(ctx)
		if err == nil {
			t.Fatal("want error for missing node 3, got nil")
		}
	})

	t.Run("FetchStatus", func(t *testing.T) {
		status, err := client.FetchStatus(ctx)
		if err != nil {
			t.Fatalf("FetchStatus error: %v", err)
		}
		if status.WiFi.RSSI != -55 {
			t.Errorf("WiFi.RSSI = %d, want -55", status.WiFi.RSSI)
		}
	})

	t.Run("Authentication failure", func(t *testing.T) {
		badClient := NewClient(server.URL, "wrongpass", 1)
		_, err := badClient.FetchData(ctx)
		if err == nil {
			t.Fatal("want HTTP 401 error, got nil")
		}
		if !errors.Is(err, ErrUnauthorized) {
			t.Errorf("expected ErrUnauthorized, got: %v", err)
		}
		if !IsPermanent(err) {
			t.Errorf("expected IsPermanent to report true for %v", err)
		}
	})
}

func TestFetchMetricsHardwareFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"node_status": {
				"battery_voltage": 2.973,
				"temperature": 25.33,
				"avg_rssi": -60.58
			},
			"hub_attachments": {
				"meter_corrupt_reading_count_recv": 7
			}
		}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "pass", 1)
	m, err := client.FetchMetrics(context.Background())
	if err != nil {
		t.Fatalf("FetchMetrics error: %v", err)
	}
	if m.BatteryVoltage != 2.973 {
		t.Errorf("BatteryVoltage = %v, want 2.973", m.BatteryVoltage)
	}
	if m.Temperature != 25.33 {
		t.Errorf("Temperature = %v, want 25.33", m.Temperature)
	}
	if m.AvgRSSI != -60.58 {
		t.Errorf("AvgRSSI = %v, want -60.58", m.AvgRSSI)
	}
	if m.MeterCorruptCountRecv != 7 {
		t.Errorf("MeterCorruptCountRecv = %d, want 7", m.MeterCorruptCountRecv)
	}
}

func TestFetchNodeSingleObjectFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"node_id": 1,
			"eui": "0011223344556677",
			"available": true,
			"last_data_ms": 3200
		}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "pass", 1)
	node, err := client.FetchNode(context.Background())
	if err != nil {
		t.Fatalf("FetchNode error: %v", err)
	}
	if node.EUI != "0011223344556677" {
		t.Errorf("EUI = %q, want 0011223344556677", node.EUI)
	}
	if !node.Available {
		t.Errorf("Available = false, want true")
	}
	if node.LastDataMS != 3200 {
		t.Errorf("LastDataMS = %d, want 3200", node.LastDataMS)
	}
}

func TestFetchNodeSingleObjectFormatMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"node_id": 2,
			"eui": "0011223344556677",
			"available": true,
			"last_data_ms": 3200
		}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "pass", 1)
	_, err := client.FetchNode(context.Background())
	if err == nil {
		t.Fatal("want error for mismatched nodeID in single object, got nil")
	}
	if !strings.Contains(err.Error(), "node 1 not found in /nodes.json") {
		t.Errorf("error = %v, want 'node 1 not found in /nodes.json'", err)
	}
}

func TestIsPeerCloseNilSafety(t *testing.T) {
	if isPeerClose(nil) {
		t.Error("isPeerClose(nil) must return false")
	}
}

func TestModernFirmwareEndpoints(t *testing.T) {
	nodeDataHits := 0
	nodeMetricsHits := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/node_data.json":
			nodeDataHits++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("modern-sml-data"))
		case "/node_metrics.json":
			nodeMetricsHits++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"node": {
					"node_id": 1,
					"battery_voltage": 2.95,
					"temperature": 23.67,
					"avg_rssi": -55.9
				},
				"ir": {
					"invalid_meter_readings_count": 0,
					"valid_meter_readings_count": 300
				},
				"hub": {
					"meter_pkg_count_received": 28495,
					"meter_msg_count_received": 17534,
					"meter_corrupt_reading_count_received_delta": 0
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "pass", 1)
	ctx := context.Background()

	// 1. FetchData hits /node_data.json
	data, err := client.FetchData(ctx)
	if err != nil {
		t.Fatalf("FetchData error: %v", err)
	}
	if string(data) != "modern-sml-data" {
		t.Errorf("FetchData payload = %q, want modern-sml-data", string(data))
	}
	if nodeDataHits != 1 {
		t.Errorf("nodeDataHits = %d, want 1", nodeDataHits)
	}

	// 2. Subsequent FetchData uses cached endpoint
	_, err = client.FetchData(ctx)
	if err != nil {
		t.Fatalf("second FetchData error: %v", err)
	}
	if nodeDataHits != 2 {
		t.Errorf("nodeDataHits = %d, want 2", nodeDataHits)
	}

	// 3. FetchMetrics hits /node_metrics.json and parses new schema
	m, err := client.FetchMetrics(ctx)
	if err != nil {
		t.Fatalf("FetchMetrics error: %v", err)
	}
	if m.BatteryVoltage != 2.95 {
		t.Errorf("BatteryVoltage = %v, want 2.95", m.BatteryVoltage)
	}
	if m.Temperature != 23.67 {
		t.Errorf("Temperature = %v, want 23.67", m.Temperature)
	}
	if m.AvgRSSI != -55.9 {
		t.Errorf("AvgRSSI = %v, want -55.9", m.AvgRSSI)
	}
	if m.MeterCorruptCountRecv != 0 {
		t.Errorf("MeterCorruptCountRecv = %d, want 0", m.MeterCorruptCountRecv)
	}
	if nodeMetricsHits != 1 {
		t.Errorf("nodeMetricsHits = %d, want 1", nodeMetricsHits)
	}

	// 4. Subsequent FetchMetrics uses cached endpoint
	_, err = client.FetchMetrics(ctx)
	if err != nil {
		t.Fatalf("second FetchMetrics error: %v", err)
	}
	if nodeMetricsHits != 2 {
		t.Errorf("nodeMetricsHits = %d, want 2", nodeMetricsHits)
	}
}

func TestLegacyFirmwareFallback(t *testing.T) {
	legacyDataHits := 0
	legacyMetricsHits := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data.json":
			legacyDataHits++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("legacy-sml-data"))
		case "/metrics.json":
			legacyMetricsHits++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"node_status": {
					"battery_voltage": 3.61,
					"temperature": 21.5,
					"avg_rssi": -65.0
				},
				"hub_attachments": {
					"meter_corrupt_reading_count_recv": 3
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "pass", 1)
	ctx := context.Background()

	// Modern endpoint fails (404), falls back to legacy /data.json
	data, err := client.FetchData(ctx)
	if err != nil {
		t.Fatalf("FetchData error: %v", err)
	}
	if string(data) != "legacy-sml-data" {
		t.Errorf("FetchData payload = %q, want legacy-sml-data", string(data))
	}
	if legacyDataHits != 1 {
		t.Errorf("legacyDataHits = %d, want 1", legacyDataHits)
	}

	// Subsequent FetchData uses cached /data.json without testing /node_data.json
	_, err = client.FetchData(ctx)
	if err != nil {
		t.Fatalf("second FetchData error: %v", err)
	}
	if legacyDataHits != 2 {
		t.Errorf("legacyDataHits = %d, want 2", legacyDataHits)
	}

	// Modern endpoint fails (404), falls back to legacy /metrics.json
	m, err := client.FetchMetrics(ctx)
	if err != nil {
		t.Fatalf("FetchMetrics error: %v", err)
	}
	if m.BatteryVoltage != 3.61 {
		t.Errorf("BatteryVoltage = %v, want 3.61", m.BatteryVoltage)
	}
	if m.MeterCorruptCountRecv != 3 {
		t.Errorf("MeterCorruptCountRecv = %d, want 3", m.MeterCorruptCountRecv)
	}
	if legacyMetricsHits != 1 {
		t.Errorf("legacyMetricsHits = %d, want 1", legacyMetricsHits)
	}
}

func TestEndpointCacheInvalidationOn404(t *testing.T) {
	modernActive := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !modernActive {
			// Legacy firmware active
			if r.URL.Path == "/data.json" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("legacy"))
				return
			}
		} else {
			// Firmware updated to modern
			if r.URL.Path == "/node_data.json" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("modern"))
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(server.URL, "pass", 1)
	ctx := context.Background()

	// Initial poll caches /data.json
	data, err := client.FetchData(ctx)
	if err != nil || string(data) != "legacy" {
		t.Fatalf("expected legacy, got %s, err=%v", string(data), err)
	}

	// Firmware update occurs: /data.json now returns 404, /node_data.json is active
	modernActive = true

	data, err = client.FetchData(ctx)
	if err != nil || string(data) != "modern" {
		t.Fatalf("expected modern after invalidation, got %s, err=%v", string(data), err)
	}
}
