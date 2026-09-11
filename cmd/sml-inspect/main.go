package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/0Bu/tibber-pulse-bot/internal/sml"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sml-inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	hexArg := fs.String("hex", "", "Hex-encoded SML telegram to decode")
	fileArg := fs.String("file", "", "Path to SML file (binary or hex)")
	jsonOutput := fs.Bool("json", false, "Output readings as JSON")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	var payload []byte
	var err error

	switch {
	case *hexArg != "":
		cleaned := strings.Join(strings.Fields(*hexArg), "")
		payload, err = hex.DecodeString(cleaned)
		if err != nil {
			fmt.Fprintf(stderr, "error decoding hex string: %v\n", err)
			return 1
		}
	case *fileArg != "":
		data, err := os.ReadFile(*fileArg)
		if err != nil {
			fmt.Fprintf(stderr, "error reading file %s: %v\n", *fileArg, err)
			return 1
		}
		cleaned := strings.TrimSpace(string(data))
		cleanedNoSpace := strings.Join(strings.Fields(cleaned), "")
		if decoded, err := hex.DecodeString(cleanedNoSpace); err == nil && len(decoded) >= 16 {
			payload = decoded
		} else {
			payload = data
		}
	default:
		if fs.NArg() > 0 {
			// First non-flag positional argument treated as hex string
			cleaned := strings.Join(strings.Fields(fs.Arg(0)), "")
			payload, err = hex.DecodeString(cleaned)
			if err != nil {
				fmt.Fprintf(stderr, "error decoding hex argument: %v\n", err)
				return 1
			}
		} else if data, err := readStdinWithTimeout(stdin); err == nil && len(data) > 0 {
			cleaned := strings.TrimSpace(string(data))
			cleanedNoSpace := strings.Join(strings.Fields(cleaned), "")
			if decoded, err := hex.DecodeString(cleanedNoSpace); err == nil && len(decoded) >= 16 {
				payload = decoded
			} else {
				payload = data
			}
		} else {
			fmt.Fprintf(stderr, "Usage: sml-inspect [-hex <hex-string> | -file <path> | stdin | <hex-string>]\n")
			fs.PrintDefaults()
			return 1
		}
	}

	readings, err := sml.ParseFrames(payload)
	if len(readings) == 0 {
		if err != nil {
			fmt.Fprintf(stderr, "sml parse error (payload %d bytes): %v\n", len(payload), err)
		} else {
			fmt.Fprintf(stderr, "no SML readings found in %d bytes payload\n", len(payload))
		}
		return 1
	}

	if *jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(readings); err != nil {
			fmt.Fprintf(stderr, "json encode error: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Fprintf(stdout, "Decoded %d readings from %d bytes payload:\n", len(readings), len(payload))
	fmt.Fprintf(stdout, "%-24s %-20s %-15s %s\n", "NAME", "OBIS", "VALUE", "RAW")
	fmt.Fprintln(stdout, strings.Repeat("-", 75))
	for _, r := range readings {
		valStr := ""
		if r.Raw == "" {
			if r.Unit != "" {
				valStr = fmt.Sprintf("%.3f %s", r.Value, r.Unit)
			} else {
				valStr = fmt.Sprintf("%.3f", r.Value)
			}
		}
		fmt.Fprintf(stdout, "%-24s %-20s %-15s %s\n", r.Name, r.OBIS, valStr, r.Raw)
	}
	return 0
}

func readStdinWithTimeout(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, errors.New("stdin is nil")
	}
	if f, isFile := r.(*os.File); isFile {
		stat, err := f.Stat()
		if err == nil && (stat.Mode()&os.ModeCharDevice) != 0 {
			return nil, errors.New("interactive terminal")
		}
	}
	ch := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		data, err := io.ReadAll(r)
		if err != nil {
			errCh <- err
			return
		}
		ch <- data
	}()
	select {
	case data := <-ch:
		return data, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(3 * time.Second):
		return nil, errors.New("stdin read timeout")
	}
}
