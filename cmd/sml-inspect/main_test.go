package main

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/0Bu/tibber-pulse-bot/internal/sml"
	gosml "github.com/andig/gosml"
)

func buildSampleSMLFrame() []byte {
	msg := []byte{
		0x76,
		0x01,
		0x62, 0x00,
		0x62, 0x00,
		0x72,
		0x65, 0x00, 0x00, 0x07, 0x01,
		0x77,
		0x01,
		0x0B, 0x0A, 0x01, 'L', 'G', 'Z', 0x01, 0x04, 0xD6, 0xFF, 0xBE,
		0x01,
		0x01,
		0x72,
		0x77,
		0x07, 0x01, 0x00, 0x60, 0x01, 0x00, 0xFF,
		0x01,
		0x01,
		0x01,
		0x01,
		0x0B, 0x0A, 0x01, 'L', 'G', 'Z', 0x01, 0x04, 0xD6, 0xFF, 0xBE,
		0x01,
		0x77,
		0x07, 0x01, 0x00, 0x10, 0x07, 0x00, 0xFF,
		0x01,
		0x01,
		0x62, 0x1B,
		0x52, 0x00,
		0x63, 0x01, 0x2C,
		0x01,
		0x01,
		0x01,
		0x63, 0x00, 0x00,
		0x00,
	}
	crc := gosml.Crc16Calculate(msg[:len(msg)-4], len(msg)-4)
	msg[len(msg)-3] = byte(crc >> 8)
	msg[len(msg)-2] = byte(crc & 0xFF)

	pad := (4 - (len(msg) % 4)) % 4
	for i := 0; i < pad; i++ {
		msg = append(msg, 0x00)
	}

	startSeq := []byte{0x1b, 0x1b, 0x1b, 0x1b, 0x01, 0x01, 0x01, 0x01}
	endSeq := []byte{0x1b, 0x1b, 0x1b, 0x1b, 0x1a, byte(pad), 0x00, 0x00}

	frame := append(startSeq, msg...)
	frame = append(frame, endSeq...)
	return frame
}

func TestSMLInspectHexDecode(t *testing.T) {
	frame := buildSampleSMLFrame()
	hexStr := hex.EncodeToString(frame)
	if len(hexStr) == 0 {
		t.Fatal("empty hex string")
	}

	decoded, err := hex.DecodeString(hexStr)
	if err != nil {
		t.Fatalf("hex decode failed: %v", err)
	}

	readings, err := sml.ParseFrames(decoded)
	if err != nil {
		t.Fatalf("ParseFrames failed: %v", err)
	}

	if len(readings) != 4 {
		t.Fatalf("got %d readings, want 4", len(readings))
	}
	if readings[1].Name != "manufacturer" || readings[1].Raw != "LGZ" {
		t.Errorf("manufacturer = %+v, want LGZ", readings[1])
	}
	if readings[2].Name != "meter_serial" || readings[2].Raw != "LGZ-81199038" {
		t.Errorf("meter_serial = %+v, want LGZ-81199038", readings[2])
	}
	if readings[3].Name != "power_total" || readings[3].Value != 300 {
		t.Errorf("power_total = %+v, want 300", readings[3])
	}
}

func TestRunCLI(t *testing.T) {
	frame := buildSampleSMLFrame()
	hexStr := hex.EncodeToString(frame)

	t.Run("with -hex flag outputs table", func(t *testing.T) {
		var stdout, stderr strings.Builder
		code := run([]string{"-hex", hexStr}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "power_total") || !strings.Contains(out, "LGZ-81199038") {
			t.Errorf("unexpected stdout: %s", out)
		}
	})

	t.Run("with -json flag outputs JSON", func(t *testing.T) {
		var stdout, stderr strings.Builder
		code := run([]string{"-hex", hexStr, "-json"}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, `"power_total"`) || !strings.Contains(out, `"LGZ-81199038"`) {
			t.Errorf("unexpected json output: %s", out)
		}
	})

	t.Run("with positional hex argument", func(t *testing.T) {
		var stdout, stderr strings.Builder
		code := run([]string{hexStr}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "power_total") {
			t.Errorf("expected power_total in output: %s", stdout.String())
		}
	})

	t.Run("with stdin input", func(t *testing.T) {
		var stdout, stderr strings.Builder
		stdin := strings.NewReader(hexStr)
		code := run([]string{}, stdin, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "power_total") {
			t.Errorf("expected power_total in output: %s", stdout.String())
		}
	})

	t.Run("with invalid hex string returns code 1", func(t *testing.T) {
		var stdout, stderr strings.Builder
		code := run([]string{"-hex", "not-a-hex-string"}, nil, &stdout, &stderr)
		if code != 1 {
			t.Errorf("want code 1 for invalid hex, got %d", code)
		}
		if !strings.Contains(stderr.String(), "error decoding hex") {
			t.Errorf("unexpected stderr: %s", stderr.String())
		}
	})

	t.Run("with empty input returns code 1", func(t *testing.T) {
		var stdout, stderr strings.Builder
		code := run([]string{}, nil, &stdout, &stderr)
		if code != 1 {
			t.Errorf("want code 1 for empty input, got %d", code)
		}
		if !strings.Contains(stderr.String(), "Usage:") {
			t.Errorf("unexpected stderr: %s", stderr.String())
		}
	})

	t.Run("with -file flag pointing to valid hex file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "sml-test-*.hex")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())
		_, _ = tmpFile.WriteString(hexStr)
		_ = tmpFile.Close()

		var stdout, stderr strings.Builder
		code := run([]string{"-file", tmpFile.Name()}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "power_total") {
			t.Errorf("expected power_total in output: %s", stdout.String())
		}
	})

	t.Run("with -file flag pointing to missing file returns 1", func(t *testing.T) {
		var stdout, stderr strings.Builder
		code := run([]string{"-file", "/nonexistent/sml/file.hex"}, nil, &stdout, &stderr)
		if code != 1 {
			t.Errorf("want code 1 for missing file, got %d", code)
		}
		if !strings.Contains(stderr.String(), "error reading file") {
			t.Errorf("unexpected stderr: %s", stderr.String())
		}
	})

	t.Run("formats zero numeric reading as 0.000 in table output", func(t *testing.T) {
		// Replace power_total value in frame with 0
		frameZero := buildSampleSMLFrame()
		// In buildSampleSMLFrame, 0x63, 0x01, 0x2C is value 300
		for i := 0; i < len(frameZero)-3; i++ {
			if frameZero[i] == 0x63 && frameZero[i+1] == 0x01 && frameZero[i+2] == 0x2C {
				frameZero[i+1] = 0x00
				frameZero[i+2] = 0x00
				// Recalculate message CRC
				msg := frameZero[8 : len(frameZero)-8]
				crc := gosml.Crc16Calculate(msg[:len(msg)-4], len(msg)-4)
				msg[len(msg)-3] = byte(crc >> 8)
				msg[len(msg)-2] = byte(crc & 0xFF)
				break
			}
		}
		hexZero := hex.EncodeToString(frameZero)
		var stdout, stderr strings.Builder
		code := run([]string{"-hex", hexZero}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "0.000 W") {
			t.Errorf("expected 0.000 W in output: %s", stdout.String())
		}
	})

	t.Run("terminal stdin without arguments returns usage cleanly", func(t *testing.T) {
		var stdout, stderr strings.Builder
		// Use os.Stdin to test terminal/unpiped path
		code := run([]string{}, os.Stdin, &stdout, &stderr)
		if code != 1 {
			t.Errorf("want code 1 for terminal stdin, got %d", code)
		}
		if !strings.Contains(stderr.String(), "Usage:") {
			t.Errorf("expected Usage in stderr, got: %s", stderr.String())
		}
	})
}
