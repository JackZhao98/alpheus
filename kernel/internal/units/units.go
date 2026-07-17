// Package units defines the exact fixed-point types used on the kernel money
// path. Public JSON and YAML remain human-readable decimal units; the internal
// representation is one millionth of a dollar, share, contract, percentage
// point, or whole ratio depending on the type.
package units

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"

	"gopkg.in/yaml.v3"
)

const Scale int64 = 1_000_000

type Micros int64
type Qty int64
type PercentMicros int64
type RatioMicros int64

func decimalTokenJSON(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty decimal")
	}
	if data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return "", fmt.Errorf("invalid decimal string")
		}
		return value, nil
	}
	return string(data), nil
}

func parseScaled(value string) (int64, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return 0, fmt.Errorf("invalid decimal %q", value)
	}
	if strings.ContainsAny(value, "eE") {
		return 0, fmt.Errorf("exponent form is not allowed")
	}

	negative := false
	switch value[0] {
	case '-':
		negative = true
		value = value[1:]
	case '+':
		value = value[1:]
	}
	if value == "" {
		return 0, fmt.Errorf("invalid decimal")
	}

	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("invalid decimal")
	}
	if len(parts) == 2 && (parts[1] == "" || len(parts[1]) > 6) {
		return 0, fmt.Errorf("decimal must have at most 6 fractional digits")
	}
	for _, part := range parts {
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return 0, fmt.Errorf("invalid decimal")
			}
		}
	}

	whole := new(big.Int)
	if _, ok := whole.SetString(parts[0], 10); !ok {
		return 0, fmt.Errorf("invalid decimal")
	}
	scaled := new(big.Int).Mul(whole, big.NewInt(Scale))
	if len(parts) == 2 {
		fractionText := parts[1] + strings.Repeat("0", 6-len(parts[1]))
		fraction := new(big.Int)
		if _, ok := fraction.SetString(fractionText, 10); !ok {
			return 0, fmt.Errorf("invalid decimal")
		}
		scaled.Add(scaled, fraction)
	}
	if negative {
		scaled.Neg(scaled)
	}
	if !scaled.IsInt64() {
		return 0, fmt.Errorf("decimal is out of range")
	}
	return scaled.Int64(), nil
}

func formatScaled(value int64) string {
	n := big.NewInt(value)
	negative := n.Sign() < 0
	if negative {
		n.Abs(n)
	}
	divisor := big.NewInt(Scale)
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(n, divisor, fraction)
	if fraction.Sign() == 0 {
		if negative {
			return "-" + whole.String()
		}
		return whole.String()
	}
	fractionText := fmt.Sprintf("%06d", fraction.Int64())
	fractionText = strings.TrimRight(fractionText, "0")
	prefix := ""
	if negative {
		prefix = "-"
	}
	return prefix + whole.String() + "." + fractionText
}

func unmarshalJSONScaled(data []byte, target *int64) error {
	value, err := decimalTokenJSON(bytes.TrimSpace(data))
	if err != nil {
		return err
	}
	scaled, err := parseScaled(value)
	if err != nil {
		return err
	}
	*target = scaled
	return nil
}

func unmarshalYAMLScaled(node *yaml.Node, target *int64) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("decimal must be a scalar")
	}
	scaled, err := parseScaled(node.Value)
	if err != nil {
		return err
	}
	*target = scaled
	return nil
}

func (m Micros) String() string        { return formatScaled(int64(m)) }
func (q Qty) String() string           { return formatScaled(int64(q)) }
func (p PercentMicros) String() string { return formatScaled(int64(p)) }
func (r RatioMicros) String() string   { return formatScaled(int64(r)) }

func (m Micros) MarshalJSON() ([]byte, error)        { return []byte(m.String()), nil }
func (q Qty) MarshalJSON() ([]byte, error)           { return []byte(q.String()), nil }
func (p PercentMicros) MarshalJSON() ([]byte, error) { return []byte(p.String()), nil }
func (r RatioMicros) MarshalJSON() ([]byte, error)   { return []byte(r.String()), nil }

func (m *Micros) UnmarshalJSON(data []byte) error {
	var value int64
	if err := unmarshalJSONScaled(data, &value); err != nil {
		return err
	}
	*m = Micros(value)
	return nil
}

func (q *Qty) UnmarshalJSON(data []byte) error {
	var value int64
	if err := unmarshalJSONScaled(data, &value); err != nil {
		return err
	}
	*q = Qty(value)
	return nil
}

func (p *PercentMicros) UnmarshalJSON(data []byte) error {
	var value int64
	if err := unmarshalJSONScaled(data, &value); err != nil {
		return err
	}
	*p = PercentMicros(value)
	return nil
}

func (r *RatioMicros) UnmarshalJSON(data []byte) error {
	var value int64
	if err := unmarshalJSONScaled(data, &value); err != nil {
		return err
	}
	*r = RatioMicros(value)
	return nil
}

func (m *Micros) UnmarshalYAML(node *yaml.Node) error {
	var value int64
	if err := unmarshalYAMLScaled(node, &value); err != nil {
		return err
	}
	*m = Micros(value)
	return nil
}

func (q *Qty) UnmarshalYAML(node *yaml.Node) error {
	var value int64
	if err := unmarshalYAMLScaled(node, &value); err != nil {
		return err
	}
	*q = Qty(value)
	return nil
}

func (p *PercentMicros) UnmarshalYAML(node *yaml.Node) error {
	var value int64
	if err := unmarshalYAMLScaled(node, &value); err != nil {
		return err
	}
	*p = PercentMicros(value)
	return nil
}

func (r *RatioMicros) UnmarshalYAML(node *yaml.Node) error {
	var value int64
	if err := unmarshalYAMLScaled(node, &value); err != nil {
		return err
	}
	*r = RatioMicros(value)
	return nil
}

func MustMicros(value string) Micros {
	scaled, err := parseScaled(value)
	if err != nil {
		panic(err)
	}
	return Micros(scaled)
}

func MustQty(value string) Qty {
	scaled, err := parseScaled(value)
	if err != nil {
		panic(err)
	}
	return Qty(scaled)
}

func MustPercent(value string) PercentMicros {
	scaled, err := parseScaled(value)
	if err != nil {
		panic(err)
	}
	return PercentMicros(scaled)
}

func MustRatio(value string) RatioMicros {
	scaled, err := parseScaled(value)
	if err != nil {
		panic(err)
	}
	return RatioMicros(scaled)
}

// MulQtyPrice multiplies a non-negative quantity by a non-negative unit price
// and multiplier, dividing the quantity micro-unit scale exactly once.
func MulQtyPrice(qty Qty, price Micros, multiplier int64, roundUp bool) (Micros, error) {
	if qty < 0 || price < 0 || multiplier <= 0 {
		return 0, fmt.Errorf("quantity, price, and multiplier must be positive")
	}
	numerator := new(big.Int).Mul(big.NewInt(int64(qty)), big.NewInt(int64(price)))
	numerator.Mul(numerator, big.NewInt(multiplier))
	result, remainder := new(big.Int), new(big.Int)
	result.QuoRem(numerator, big.NewInt(Scale), remainder)
	if roundUp && remainder.Sign() != 0 {
		result.Add(result, big.NewInt(1))
	}
	if !result.IsInt64() {
		return 0, fmt.Errorf("money calculation overflows int64")
	}
	return Micros(result.Int64()), nil
}

func Add(a, b Micros) (Micros, error) {
	result := new(big.Int).Add(big.NewInt(int64(a)), big.NewInt(int64(b)))
	if !result.IsInt64() {
		return 0, fmt.Errorf("money addition overflows int64")
	}
	return Micros(result.Int64()), nil
}

func AddQty(a, b Qty) (Qty, error) {
	result := new(big.Int).Add(big.NewInt(int64(a)), big.NewInt(int64(b)))
	if !result.IsInt64() {
		return 0, fmt.Errorf("quantity addition overflows int64")
	}
	return Qty(result.Int64()), nil
}

func PercentFloor(amount Micros, percent PercentMicros) (Micros, error) {
	if amount < 0 || percent < 0 {
		return 0, fmt.Errorf("percent input must be non-negative")
	}
	result := new(big.Int).Mul(big.NewInt(int64(amount)), big.NewInt(int64(percent)))
	result.Quo(result, big.NewInt(100*Scale))
	if !result.IsInt64() {
		return 0, fmt.Errorf("percentage calculation overflows int64")
	}
	return Micros(result.Int64()), nil
}

func SumLessOrEqual(a, b, limit Micros) bool {
	sum := new(big.Int).Add(big.NewInt(int64(a)), big.NewInt(int64(b)))
	return sum.Cmp(big.NewInt(int64(limit))) <= 0
}

func DifferenceExceeds(a, b, tolerance Micros) bool {
	diff := new(big.Int).Sub(big.NewInt(int64(a)), big.NewInt(int64(b)))
	diff.Abs(diff)
	return diff.Cmp(big.NewInt(int64(tolerance))) > 0
}

func SpreadWithin(bid, ask Micros, maximum RatioMicros) bool {
	if bid <= 0 || ask <= bid || maximum < 0 {
		return false
	}
	left := new(big.Int).Sub(big.NewInt(int64(ask)), big.NewInt(int64(bid)))
	left.Mul(left, big.NewInt(2*Scale))
	right := new(big.Int).Add(big.NewInt(int64(ask)), big.NewInt(int64(bid)))
	right.Mul(right, big.NewInt(int64(maximum)))
	return left.Cmp(right) <= 0
}

func AbsQty(qty Qty) (Qty, error) {
	if int64(qty) == math.MinInt64 {
		return 0, fmt.Errorf("quantity magnitude overflows int64")
	}
	if qty < 0 {
		return -qty, nil
	}
	return qty, nil
}
