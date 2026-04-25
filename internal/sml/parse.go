package sml

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	gosml "github.com/andig/gosml"
)

// Reading is a single OBIS value extracted from an SML frame.
type Reading struct {
	OBIS  string  // human-readable OBIS code, e.g. "1-0:1.8.0*255"
	Name  string  // mnemonic name (e.g. "energy_import_total"), empty if unknown
	Value float64 // scaled numeric value, when numeric
	Raw   string  // hex string for octet-string values (e.g. serial number)
	Unit  string  // unit symbol (W, Wh, V, A, Hz, ...) or empty
}

// ParseFrames consumes a stream of SML transport frames and returns all
// readings from every GetListResponse it finds. Partial/garbled trailing
// frames are skipped silently — the Pulse Bridge web buffer is known to
// truncate occasionally.
func ParseFrames(payload []byte) ([]Reading, error) {
	r := bufio.NewReader(bytes.NewReader(payload))
	var out []Reading
	var firstErr error
	frames := 0
	for {
		buf, err := gosml.TransportRead(r)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		frames++
		// Strip 8-byte start escape + 4-byte end + 4-byte CRC pad.
		if len(buf) < 16 {
			continue
		}
		messages, err := gosml.FileParse(buf[8 : len(buf)-8])
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for _, msg := range messages {
			list, ok := msg.MessageBody.Data.(gosml.GetListResponse)
			if !ok {
				continue
			}
			for _, e := range list.ValList {
				r := entryToReading(e)
				out = append(out, r)
				if extras := derivedReadings(r, e); len(extras) > 0 {
					out = append(out, extras...)
				}
			}
		}
	}
	if frames == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func entryToReading(e gosml.ListEntry) Reading {
	obis := obisString(e.ObjName)
	r := Reading{
		OBIS: obis,
		Name: obisName(e.ObjName),
		Unit: unitSymbol(e.Unit),
	}
	switch e.Value.Typ & gosml.TYPEFIELD {
	case gosml.TYPEINTEGER, gosml.TYPEUNSIGNED:
		scaler := int(e.Scaler)
		r.Value = float64(e.Value.DataInt) * math.Pow10(scaler)
	default:
		if len(e.Value.DataBytes) > 0 {
			b := strings.Builder{}
			for _, x := range e.Value.DataBytes {
				fmt.Fprintf(&b, "%02x", x)
			}
			r.Raw = b.String()
		}
	}
	return r
}

// derivedReadings produces synthetic readings from raw octet-string values
// that carry structured content. Currently: decodes the FNN 10-byte server-ID
// (OBIS 1-0:96.1.0) into manufacturer (3-letter ASCII) and serial number.
func derivedReadings(r Reading, e gosml.ListEntry) []Reading {
	if r.Name != "device_id" || len(e.Value.DataBytes) < 10 {
		return nil
	}
	b := e.Value.DataBytes
	// FNN server-ID layout: [prefix][medium][3 ASCII manufacturer][version][4-byte serial BE]
	manufacturer := string(b[2:5])
	for _, c := range manufacturer {
		if c < 'A' || c > 'Z' {
			return nil
		}
	}
	serial := uint32(b[6])<<24 | uint32(b[7])<<16 | uint32(b[8])<<8 | uint32(b[9])
	return []Reading{
		{Name: "manufacturer", OBIS: r.OBIS, Raw: manufacturer},
		{Name: "meter_serial", OBIS: r.OBIS, Raw: fmt.Sprintf("%s-%d", manufacturer, serial)},
	}
}

func obisString(o gosml.OctetString) string {
	if len(o) < 6 {
		return fmt.Sprintf("%x", []byte(o))
	}
	return fmt.Sprintf("%d-%d:%d.%d.%d*%d", o[0], o[1], o[2], o[3], o[4], o[5])
}

// DLMS unit codes (subset relevant for electricity meters).
func unitSymbol(u uint8) string {
	switch u {
	case 0x1B:
		return "W"
	case 0x1D:
		return "var"
	case 0x1E:
		return "Wh"
	case 0x1F:
		return "varh"
	case 0x21:
		return "A"
	case 0x23:
		return "V"
	case 0x2C:
		return "Hz"
	case 0x09:
		return "s"
	case 0x06:
		return "min"
	}
	return ""
}

// obisName returns a stable short identifier for common electricity OBIS codes.
// Empty string if the code is not in the known list — callers should fall back
// to the numeric OBIS code as topic suffix.
func obisName(o gosml.OctetString) string {
	if len(o) < 6 {
		return ""
	}
	key := [6]byte{o[0], o[1], o[2], o[3], o[4], o[5]}
	if name, ok := obisNames[key]; ok {
		return name
	}
	return ""
}

var obisNames = map[[6]byte]string{
	{1, 0, 0, 0, 9, 255}:   "device_id",
	{1, 0, 96, 1, 0, 255}:  "device_id",
	{1, 0, 1, 8, 0, 255}:   "energy_import_total",
	{1, 0, 1, 8, 1, 255}:   "energy_import_t1",
	{1, 0, 1, 8, 2, 255}:   "energy_import_t2",
	{1, 0, 2, 8, 0, 255}:   "energy_export_total",
	{1, 0, 2, 8, 1, 255}:   "energy_export_t1",
	{1, 0, 2, 8, 2, 255}:   "energy_export_t2",
	{1, 0, 16, 7, 0, 255}:  "power_total",
	{1, 0, 36, 7, 0, 255}:  "power_l1",
	{1, 0, 56, 7, 0, 255}:  "power_l2",
	{1, 0, 76, 7, 0, 255}:  "power_l3",
	{1, 0, 32, 7, 0, 255}:  "voltage_l1",
	{1, 0, 52, 7, 0, 255}:  "voltage_l2",
	{1, 0, 72, 7, 0, 255}:  "voltage_l3",
	{1, 0, 31, 7, 0, 255}:  "current_l1",
	{1, 0, 51, 7, 0, 255}:  "current_l2",
	{1, 0, 71, 7, 0, 255}:  "current_l3",
	{1, 0, 14, 7, 0, 255}:  "frequency",
	{129, 129, 199, 130, 3, 255}: "manufacturer",
}
