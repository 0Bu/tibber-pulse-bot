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
	"sync"
	"syscall"
	"time"

	"github.com/0Bu/tibber-pulse-bot/internal/output"
	"github.com/0Bu/tibber-pulse-bot/internal/pulse"
	"github.com/0Bu/tibber-pulse-bot/internal/sml"
)

// Build-time injected via -ldflags. Falls back to "dev"/"unknown" for
// local `go build` and `go run`.
var (
	version = "dev"     // overridden by -ldflags at build time
	commit  = "unknown" // overridden by -ldflags at build time
)

func main() {
	showVersion := flag.Bool("version", false, "Print version and exit")
	pulseHost := flag.String("pulse-host", "", "Tibber Pulse Bridge IP/hostname (required)")
	pulsePassword := flag.String("pulse-password", os.Getenv("TIBBER_PULSE_PASSWORD"),
		"Bridge admin password (9-char QR code from sticker). Defaults to $TIBBER_PULSE_PASSWORD.")
	pulseNode := flag.Int("pulse-node", 1, "Bridge node id (used for node metrics and poll mode)")
	mode := flag.String("mode", "push", "Acquisition mode: 'push' (WebSocket, ~1s live updates) or 'poll' (HTTP, --interval)")
	mqttHost := flag.String("mqtt-host", "", "MQTT broker host. If empty, readings go to stdout.")
	mqttPort := flag.Int("mqtt-port", 1883, "MQTT broker port")
	mqttTopic := flag.String("mqtt-topic", "tibber/pulse", "MQTT topic prefix")
	mqttClientID := flag.String("mqtt-client-id", "tibber-pulse-bot", "MQTT client id")
	haDiscovery := flag.Bool("ha-discovery", false, "Publish Home Assistant MQTT-Discovery configs (retain=true)")
	haDiscoveryPrefix := flag.String("ha-discovery-prefix", "homeassistant", "Topic prefix HA listens on for discovery")
	interval := flag.Duration("interval", 10*time.Second, "Poll interval (only used in poll mode)")
	idleTimeout := flag.Duration("ws-idle-timeout", 60*time.Second, "Reconnect WS if no message arrives within this window (push mode)")
	reconnectDelay := flag.Duration("reconnect-delay", 100*time.Millisecond, "Delay before reconnecting after WS disconnect (100ms prevents missed telegrams during bridge idle socket drops)")
	verbose := flag.Bool("v", false, "Verbose: log every WS reconnect (default: only real errors)")
	quiet := flag.Bool("quiet", false, "When --mqtt-host is set, suppress the per-update stdout line")
	metricsInterval := flag.Duration("metrics-interval", 60*time.Second, "Bridge diagnostics poll interval (set 0 to disable)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("tibber-pulse-bot version=%s commit=%s\n", version, commit)
		return
	}
	log.Printf("tibber-pulse-bot version=%s commit=%s", version, commit)

	cfg := botConfig{
		pulseHost:         *pulseHost,
		pulsePassword:     *pulsePassword,
		pulseNode:         *pulseNode,
		mode:              *mode,
		interval:          *interval,
		idleTimeout:       *idleTimeout,
		reconnectDelay:    *reconnectDelay,
		metricsInterval:   *metricsInterval,
		haDiscovery:       *haDiscovery,
		haDiscoveryPrefix: *haDiscoveryPrefix,
		mqttHost:          *mqttHost,
		mqttPort:          *mqttPort,
		mqttTopic:         *mqttTopic,
		mqttClientID:      *mqttClientID,
	}
	if err := validateConfig(cfg); err != nil {
		log.Fatal(err)
	}

	client := pulse.NewClient(*pulseHost, *pulsePassword, *pulseNode)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var sink output.Sink
	var mqttSink *output.MQTTSink
	if *mqttHost == "" {
		sink = output.NewStdoutSink(os.Stdout)
		log.Printf("mode=%s, host=%s, output=stdout", *mode, client.Host())
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
		mqttSink.SetBridgeHost(client.Host())
		if *quiet {
			sink = mqttSink
		} else {
			sink = output.NewTeeSink(mqttSink, output.NewCompactStdoutSink(os.Stdout))
		}
		log.Printf("mode=%s, host=%s, output=mqtt://%s:%d/%s%s",
			*mode, client.Host(), *mqttHost, *mqttPort, *mqttTopic,
			map[bool]string{true: " (quiet)", false: " + compact stdout"}[*quiet])
	}
	defer sink.Close()

	var wg sync.WaitGroup
	if *metricsInterval > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runMetrics(ctx, client, mqttSink, *metricsInterval)
		}()
	}

	var runErr error
	if *mode == "poll" {
		runErr = runPoll(ctx, client, sink, *interval)
	} else {
		runErr = runPush(ctx, client, sink, *idleTimeout, *reconnectDelay, *verbose)
	}

	cancel()
	wg.Wait()
	if runErr != nil {
		sink.Close()
		os.Exit(1)
	}
}

func runPoll(ctx context.Context, c *pulse.Client, sink output.Sink, interval time.Duration) error {
	if err := pollOnce(ctx, c, sink); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		if pulse.IsPermanent(err) {
			log.Printf("poll fatal: %v", err)
			return err
		}
		log.Printf("first poll: %v", err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := pollOnce(ctx, c, sink); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				if pulse.IsPermanent(err) {
					log.Printf("poll fatal: %v", err)
					return err
				}
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

// runMetrics polls the three endpoints that back the reduced diagnostics set.
// Each source is independent; only /metrics.json is required.
func runMetrics(ctx context.Context, c *pulse.Client, mqttSink *output.MQTTSink, interval time.Duration) {
	pollAndPublish := func() {
		fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		var u output.BridgeUpdate
		var err error

		u.Metrics, err = c.FetchMetrics(fctx)
		if err != nil {
			log.Printf("metrics fetch: %v", err)
			return // Metrics is the only one we hard-require
		}
		if n, err := c.FetchNode(fctx); err != nil {
			log.Printf("node fetch: %v", err)
		} else {
			u.Node = &n
		}
		if s, err := c.FetchStatus(fctx); err != nil {
			log.Printf("status fetch: %v", err)
		} else {
			u.Status = &s
		}
		if mqttSink != nil {
			if err := mqttSink.PublishBridgeUpdate(u); err != nil {
				log.Printf("diagnostics publish: %v", err)
			}
			return
		}
		logBridgeStdout(u)
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

func logBridgeStdout(u output.BridgeUpdate) {
	m := u.Metrics
	wifiRSSI := "n/a"
	if u.Status != nil {
		wifiRSSI = fmt.Sprintf("%ddBm", u.Status.WiFi.RSSI)
	}
	avail := "?"
	lastData := int64(-1)
	if u.Node != nil {
		if u.Node.Available {
			avail = "yes"
		} else {
			avail = "no"
		}
		lastData = u.Node.LastDataMS / 1000
	}
	log.Printf("bridge: V=%.3fV T=%.1f°C meterRSSI=%.0fdBm wifiRSSI=%s corrupt=%d available=%s lastData=%ds",
		m.BatteryVoltage, m.Temperature, m.AvgRSSI, wifiRSSI,
		m.MeterCorruptCountRecv, avail, lastData)
}

func runPush(ctx context.Context, c *pulse.Client, sink output.Sink, idle, reconnectDelay time.Duration, verbose bool) error {
	currentBackoff := reconnectDelay
	for ctx.Err() == nil {
		receivedFrames := 0
		err := c.StreamFrames(ctx, idle, func(f pulse.WSFrame) {
			receivedFrames++
			topic := f.Header["topic"]
			// Only SML telegrams carry parseable payload here. Other topics
			// (e.g. metrics/status) we silently ignore for now.
			if !strings.Contains(topic, "sml") || len(f.Body) < 16 {
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
			if err := sink.Publish(ctx, readings); err != nil && ctx.Err() == nil {
				log.Printf("publish: %v", err)
			}
		})
		if ctx.Err() != nil {
			return nil
		}
		if pulse.IsPermanent(err) {
			log.Printf("ws fatal: %v", err)
			return err
		}
		if receivedFrames > 0 {
			currentBackoff = reconnectDelay
		}
		// Bridge tears down the WS every ~1.1 s when idle — that's expected firmware behavior.
		// Reconnect immediately after reconnectDelay (default 100ms) to avoid dropping meter frames.
		var sleepDuration time.Duration
		if errors.Is(err, pulse.ErrPeerClosed) || errors.Is(err, pulse.ErrIdleTimeout) {
			sleepDuration = reconnectDelay
			currentBackoff = reconnectDelay
			if verbose {
				log.Printf("%v, reconnecting in %s", err, sleepDuration)
			}
		} else {
			sleepDuration = currentBackoff
			log.Printf("ws error: %v — reconnecting in %s", err, sleepDuration)
			currentBackoff *= 2
			if currentBackoff > 5*time.Second {
				currentBackoff = 5 * time.Second
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(sleepDuration):
		}
	}
	return nil
}

type botConfig struct {
	pulseHost         string
	pulsePassword     string
	pulseNode         int
	mode              string
	interval          time.Duration
	idleTimeout       time.Duration
	reconnectDelay    time.Duration
	metricsInterval   time.Duration
	haDiscovery       bool
	haDiscoveryPrefix string
	mqttHost          string
	mqttPort          int
	mqttTopic         string
	mqttClientID      string
}

func validateConfig(cfg botConfig) error {
	if strings.TrimSpace(cfg.pulseHost) == "" {
		return errors.New("--pulse-host is required")
	}
	if strings.TrimSpace(cfg.pulsePassword) == "" {
		return errors.New("--pulse-password (or $TIBBER_PULSE_PASSWORD) is required — admin password printed on the bridge sticker")
	}
	if cfg.mode != "push" && cfg.mode != "poll" {
		return fmt.Errorf("--mode must be 'push' or 'poll', got %q", cfg.mode)
	}
	if cfg.pulseNode < 1 {
		return fmt.Errorf("--pulse-node must be >= 1, got %d", cfg.pulseNode)
	}
	if cfg.mode == "poll" && cfg.interval <= 0 {
		return fmt.Errorf("--interval must be positive in poll mode, got %s", cfg.interval)
	}
	if cfg.idleTimeout < 0 {
		return fmt.Errorf("--ws-idle-timeout cannot be negative, got %s", cfg.idleTimeout)
	}
	if cfg.reconnectDelay <= 0 {
		return fmt.Errorf("--reconnect-delay must be positive, got %s", cfg.reconnectDelay)
	}
	if cfg.metricsInterval < 0 {
		return fmt.Errorf("--metrics-interval cannot be negative, got %s", cfg.metricsInterval)
	}
	if cfg.haDiscovery && strings.TrimSpace(cfg.haDiscoveryPrefix) == "" {
		return errors.New("--ha-discovery-prefix cannot be empty when --ha-discovery is enabled")
	}
	if cfg.mqttHost != "" {
		if cfg.mqttPort <= 0 || cfg.mqttPort > 65535 {
			return fmt.Errorf("--mqtt-port must be between 1 and 65535, got %d", cfg.mqttPort)
		}
		if strings.TrimSpace(cfg.mqttTopic) == "" {
			return errors.New("--mqtt-topic cannot be empty when --mqtt-host is set")
		}
		if strings.TrimSpace(cfg.mqttClientID) == "" {
			return errors.New("--mqtt-client-id cannot be empty when --mqtt-host is set")
		}
	}
	return nil
}
