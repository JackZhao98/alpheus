package rhmcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// DecodeData enforces the committed v1 MCP envelope. It deliberately does not
// search nested JSON for familiar field names: moving data is schema drift.
func DecodeData(raw json.RawMessage, dst any) error {
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Guide *string         `json:"guide"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("provider envelope drift")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("provider envelope drift")
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" || envelope.Guide == nil {
		return fmt.Errorf("provider response missing data")
	}
	if err := json.Unmarshal(envelope.Data, dst); err != nil {
		return fmt.Errorf("provider data drift")
	}
	return nil
}

// DecodeExactWhole accepts provider integers in either JSON number form or
// decimal-string form with only zero fractional digits. Robinhood commonly
// represents exact multipliers as "100.0000"; accepting that is exact, while
// accepting a non-zero fraction or exponent would silently change semantics.
func DecodeExactWhole(raw []byte) (int64, error) {
	if len(raw) == 0 || len(raw) > 64 {
		return 0, fmt.Errorf("invalid exact whole number")
	}
	value := string(raw)
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, fmt.Errorf("invalid exact whole number")
		}
	}
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "eE") || strings.HasPrefix(value, "+") {
		return 0, fmt.Errorf("invalid exact whole number")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("invalid exact whole number")
	}
	if len(parts) == 2 {
		if parts[1] == "" || strings.Trim(parts[1], "0") != "" {
			return 0, fmt.Errorf("invalid exact whole number")
		}
	}
	parsed, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid exact whole number")
	}
	return parsed, nil
}

// DecodeExactDecimal normalizes a provider decimal to at most scale fractional
// digits. Extra digits are accepted only when they are all zero, so provider
// strings such as "0.65000000" remain exactly representable as micro-dollars
// while "0.65000001" fails instead of being rounded.
func DecodeExactDecimal(raw []byte, scale int) (string, error) {
	if len(raw) == 0 || len(raw) > 128 || scale < 0 || scale > 18 {
		return "", fmt.Errorf("invalid exact decimal")
	}
	value := string(raw)
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("invalid exact decimal")
		}
	}
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "eE") || strings.HasPrefix(value, "+") {
		return "", fmt.Errorf("invalid exact decimal")
	}
	prefix := ""
	unsigned := value
	if strings.HasPrefix(unsigned, "-") {
		prefix, unsigned = "-", unsigned[1:]
	}
	parts := strings.Split(unsigned, ".")
	if len(parts) > 2 || parts[0] == "" {
		return "", fmt.Errorf("invalid exact decimal")
	}
	for _, part := range parts {
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return "", fmt.Errorf("invalid exact decimal")
			}
		}
	}
	if len(parts) == 1 {
		return prefix + parts[0], nil
	}
	if parts[1] == "" {
		return "", fmt.Errorf("invalid exact decimal")
	}
	fraction := parts[1]
	if len(fraction) > scale {
		if strings.Trim(fraction[scale:], "0") != "" {
			return "", fmt.Errorf("exact decimal exceeds scale")
		}
		fraction = fraction[:scale]
	}
	if fraction == "" {
		return prefix + parts[0], nil
	}
	return prefix + parts[0] + "." + fraction, nil
}
