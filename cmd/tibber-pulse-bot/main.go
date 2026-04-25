package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/0Bu/tibber-pulse-bot/internal/output"
	"github.com/0Bu/tibber-pulse-bot/internal/pulse"
	"github.com/0Bu/tibber-pulse-bot/internal/sml"
)

func main() {
	pulseHost := flag.String("pulse-host", "", "Tibber Pulse Bridge IP/hostname (required)")
	pulsePassword := flag.String("pulse-password", os.Getenv("TIBBER_PULSE_PASSWORD"),
		"Bridge admin password (9-char QR code from sticker). Defaults to $TIBBER_PULSE_PASSWORD.")
	pulseNode := flag.Int("pulse-node", 1, "Bridge node id (only used in poll mode)")
	mode := flag.String("mode", "push", "Acquisition mode: 'push' (WebSocket, ~1s live updates) or 'poll' (HTTP, --interval)")
	mqttHost := flag.String("mqtt-host", "", "MQTT broker host. If empty, readings go to stdout.")
	mqttPort := flag.Int("mqtt-port", 1883, "MQTT broker port")
	mqttTopic := flag.String("mqtt-topic", "tibber/pulse", "MQTT topic prefix")
	mqttClientID := flag.String("mqtt-client-id", "tibber-pulse-bot", "MQTT client id")
	haDiscovery := flag.Bool("ha-discovery", false, "Publish Home Assistant MQTT-Discovery configs (retain=true)")
	haDiscoveryPrefix := flag.String("ha-discovery-prefix", "homeassistant", "Topic prefix HA listens on for discovery")
	interval := flag.Duration("interval", 10*time.Second, "Poll interval (only used in poll mode)")
	idleTimeout := flag.Duration("ws-idle-timeout", 60*time.Second, "Reconnect WS if no message arrives within this window (push mode)")
	reconnectDelay := flag.Duration("reconnect-delay", 1*time.Second, "Delay before reconnecting after WS disconnect")
	verbose := flag.Bool("v", false, "Verbose: log every WS reconnect (default: only real errors)")
	quiet := flag.Bool("quiet", false, "When --mqtt-host is set, suppress the per-update stdout line")
	metricsInterval := flag.Duration("metrics-interval", 60*time.Second, "Bridge metrics poll interval (set 0 to disable)")
	flag.Parse()

	if *pulseHost == "" {
		log.Fatal("--pulse-host is required")
	}
	if *pulsePassword == "" {
		log.Fatal("--pulse-password (or $TIBBER_PULSE_PASSWORD) is required — admin password printed on the bridge sticker")
	}
	if *mode != "push" && *mode != "poll" {
		log.Fatalf("--mode must be 'push' or 'poll', got %q", *mode)
	}

	client := pulse.NewClient(*pulseHost, *pulsePassword, *pulseNode)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var sink output.Sink
	var mqttSink *output.MQTTSink
	if *mqttHost == "" {
		sink = output.NewStdoutSink(os.Stdout)
		log.Printf("mode=%s, host=%s, output=stdout", *mode, *pulseHost)
	} else {
		discoveryPrefix := ""
		if *haDiscovery {
			discoveryPrefix = *haDiscoveryPrefix
		}
		s, err := output.NewMQTTSink(*mqttHost, *mqttPort, *mqttClientID, *mqttTopic, discoveryPrefix)
		if err != nil {
			log.Fatalf("mqtt connect: %v", err)
		}
		mqttSink = s
		mqttSink.SetBridgeHost(*pulseHost)
		if *quiet {
			sink = mqttSink
		} else {
			sink = output.NewTeeSink(mqttSink, output.NewCompactStdoutSink(os.Stdout))
		}
		log.Printf("mode=%s, host=%s, output=mqtt://%s:%d/%s%s",
			*mode, *pulseHost, *mqttHost, *mqttPort, *mqttTopic,
			map[bool]string{true: " (quiet)", false: " + compact stdout"}[*quiet])
	}
	defer sink.Close()

	if *metricsInterval > 0 {
		go runMetrics(ctx, client, mqttSink, *metricsInterval)
	}

	if *mode == "poll" {
		runPoll(ctx, client, sink, *interval)
	} else {
		runPush(ctx, client, sink, *idleTimeout, *reconnectDelay, *verbose)
	}
}

func runPoll(ctx context.Context, c *pulse.Client, sink output.Sink, interval time.Duration) {
	if err := pollOnce(ctx, c, sink); err != nil {
		log.Printf("first poll: %v", err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := pollOnce(ctx, c, sink); err != nil {
				log.Printf("poll: %v", err)
			}
		}
	}
}

func pollOnce(ctx context.Context, c *pulse.Client, sink output.Sink) error {
	body, err := c.FetchData(ctx)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	readings, err := sml.ParseFrames(body)
	if err != nil && len(readings) == 0 {
		return fmt.Errorf("sml parse (got %d bytes): %w", len(body), err)
	}
	if len(readings) == 0 {
		return fmt.Errorf("no readings in %d byte SML payload", len(body))
	}
	return sink.Publish(ctx, readings)
}

// runMetrics polls /metrics.json on a fixed cadence and forwards the
// values to MQTT (when mqttSink is non-nil) or stdout (otherwise). One
// failure logs and then keeps trying on the next tick — bridge metrics
// are diagnostic, not critical for the SML stream.
func runMetrics(ctx context.Context, c *pulse.Client, mqttSink *output.MQTTSink, interval time.Duration) {
	pollAndPublish := func() {
		fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		m, err := c.FetchMetrics(fctx)
		if err != nil {
			log.Printf("metrics fetch: %v", err)
			return
		}
		if mqttSink != nil {
			if err := mqttSink.PublishBridge(m); err != nil {
				log.Printf("metrics publish: %v", err)
			}
			return
		}
		log.Printf("bridge metrics: V=%.3fV T=%.1f°C RSSI=%.0fdBm uptime=%dh pkg_recv=%d corrupt=%d fw=%s",
			m.BatteryVoltage, m.Temperature, m.AvgRSSI,
			m.UptimeMS/3_600_000, m.MeterPkgCountRecv, m.MeterCorruptCountRecv, m.NodeVersion)
	}
	pollAndPublish()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pollAndPublish()
		}
	}
}

func runPush(ctx context.Context, c *pulse.Client, sink output.Sink, idle, backoff time.Duration, verbose bool) {
	for ctx.Err() == nil {
		err := c.StreamFrames(ctx, idle, func(f pulse.WSFrame) {
			topic := f.Header["topic"]
			// Only SML telegrams carry parseable payload here. Other topics
			// (e.g. metrics/status) we silently ignore for now.
			if !strings.Contains(topic, "sml") && len(f.Body) < 8 {
				return
			}
			readings, err := sml.ParseFrames(f.Body)
			if err != nil && len(readings) == 0 {
				log.Printf("ws sml parse (%d bytes, topic=%q): %v", len(f.Body), topic, err)
				return
			}
			if len(readings) == 0 {
				return
			}
			if err := sink.Publish(ctx, readings); err != nil {
				log.Printf("publish: %v", err)
			}
		})
		if ctx.Err() != nil {
			return
		}
		// Bridge tears down the WS every 30–60 s — that's normal, only log
		// real protocol/network errors. Use -v to see every reconnect.
		if errors.Is(err, pulse.ErrPeerClosed) {
			if verbose {
				log.Printf("ws peer closed, reconnecting in %s", backoff)
			}
		} else {
			log.Printf("ws error: %v — reconnecting in %s", err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}
