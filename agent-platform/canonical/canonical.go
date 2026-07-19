// Package canonical owns Alpheus's versioned authority-bearing JSON profile.
//
// Profile v1 accepts only JSON null, booleans, strings, arrays, objects, and
// base-10 integers. Objects reject duplicate keys and serialize keys in UTF-8
// lexical order. Strings use minimal JSON escapes. Floats, exponents, invalid
// UTF-8, trailing values, and non-canonical integers fail closed. Digests add a
// fixed profile prefix and an explicit domain before SHA-256 hashing.
package canonical

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"unicode/utf8"
)

const Profile = "alpheus-c14n-v1"

var (
	domainPattern  = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	integerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
)

// JSON returns the canonical profile-v1 representation of one strict JSON
// value. Numbers are integers only so authority-bearing values cannot lose
// precision in another implementation.
func JSON(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("canonical JSON is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := parseValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("canonical JSON contains trailing data")
		}
		return nil, fmt.Errorf("canonical JSON trailing data: %w", err)
	}
	var output bytes.Buffer
	if err := appendValue(&output, value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// Marshal canonicalizes a typed value after ordinary JSON encoding. Callers
// still validate their semantic contract separately.
func Marshal(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical value: %w", err)
	}
	return JSON(raw)
}

// Digest hashes a typed value with profile and domain separation.
func Digest(domain string, value any) (string, error) {
	canonical, err := Marshal(value)
	if err != nil {
		return "", err
	}
	return DigestJSON(domain, canonical)
}

// DigestJSON hashes strict JSON with profile and domain separation.
func DigestJSON(domain string, raw []byte) (string, error) {
	if !domainPattern.MatchString(domain) {
		return "", fmt.Errorf("invalid canonical digest domain")
	}
	canonical, err := JSON(raw)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(Profile))
	_, _ = hash.Write([]byte{'\n'})
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{'\n'})
	_, _ = hash.Write(canonical)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func parseValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode canonical JSON: %w", err)
	}
	switch value := token.(type) {
	case nil, bool, string, json.Number:
		return value, nil
	case json.Delim:
		switch value {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, fmt.Errorf("decode canonical object key: %w", err)
				}
				key, ok := keyToken.(string)
				if !ok || !utf8.ValidString(key) {
					return nil, fmt.Errorf("canonical object key is invalid")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("canonical object has duplicate key %q", key)
				}
				child, err := parseValue(decoder)
				if err != nil {
					return nil, err
				}
				object[key] = child
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("canonical object is not closed")
			}
			return object, nil
		case '[':
			array := []any{}
			for decoder.More() {
				child, err := parseValue(decoder)
				if err != nil {
					return nil, err
				}
				array = append(array, child)
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("canonical array is not closed")
			}
			return array, nil
		default:
			return nil, fmt.Errorf("unexpected canonical delimiter %q", value)
		}
	default:
		return nil, fmt.Errorf("unsupported canonical JSON value")
	}
}

func appendValue(output *bytes.Buffer, value any) error {
	switch value := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		if value {
			output.WriteString("true")
		} else {
			output.WriteString("false")
		}
	case string:
		return appendString(output, value)
	case json.Number:
		text := value.String()
		if !integerPattern.MatchString(text) || text == "-0" {
			return fmt.Errorf("canonical number %q is not a base-10 integer", text)
		}
		output.WriteString(text)
	case []any:
		output.WriteByte('[')
		for index := range value {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := appendValue(output, value[index]); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := appendString(output, key); err != nil {
				return err
			}
			output.WriteByte(':')
			if err := appendValue(output, value[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical value type %T", value)
	}
	return nil
}

func appendString(output *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("canonical string is not valid UTF-8")
	}
	const hexDigits = "0123456789abcdef"
	output.WriteByte('"')
	for _, char := range value {
		switch char {
		case '"', '\\':
			output.WriteByte('\\')
			output.WriteRune(char)
		case '\b':
			output.WriteString(`\b`)
		case '\f':
			output.WriteString(`\f`)
		case '\n':
			output.WriteString(`\n`)
		case '\r':
			output.WriteString(`\r`)
		case '\t':
			output.WriteString(`\t`)
		default:
			if char < 0x20 {
				output.WriteString(`\u00`)
				output.WriteByte(hexDigits[byte(char)>>4])
				output.WriteByte(hexDigits[byte(char)&0x0f])
			} else {
				output.WriteRune(char)
			}
		}
	}
	output.WriteByte('"')
	return nil
}
