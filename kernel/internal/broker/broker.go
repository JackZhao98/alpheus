// Package broker is the venue port. Agents NEVER see this interface — only the
// kernel does. Credentials live behind it and nowhere else.
package broker

import (
	"fmt"
	"os"
	"time"

	"alpheus/kernel/internal/units"
)

type Quote struct {
	Symbol       string       `json:"symbol"`
	Bid          units.Micros `json:"bid"`
	Ask          units.Micros `json:"ask"`
	OpenInterest int          `json:"open_interest"` // options only; 0 = unknown
	Source       string       `json:"source,omitempty"`
	AsOf         time.Time    `json:"as_of,omitempty"`
}

// Mid rounds down to a micro-dollar. For a buy working price this is in the
// account's interest; approval and reservation still use the ask/cap.
func (q Quote) Mid() units.Micros {
	return q.Bid + (q.Ask-q.Bid)/2
}

func (q Quote) Sane() bool {
	return q.Bid > 0 && q.Ask > q.Bid
}

func (q Quote) Fresh(maxAgeSec int, now time.Time) bool {
	if maxAgeSec <= 0 {
		return true
	}
	if q.AsOf.IsZero() || q.AsOf.After(now) {
		return false
	}
	return now.Sub(q.AsOf) <= time.Duration(maxAgeSec)*time.Second
}

func (q Quote) Usable(maxAgeSec int, now time.Time) bool {
	return q.Sane() && q.Fresh(maxAgeSec, now)
}

type Instrument struct {
	Symbol     string `json:"symbol"`
	Kind       string `json:"kind"`
	Multiplier int64  `json:"multiplier"`
}

type Position struct {
	Symbol     string       `json:"symbol"`
	Qty        units.Qty    `json:"qty"`
	AvgPrice   units.Micros `json:"avg_price"`
	Kind       string       `json:"kind"`
	Multiplier int64        `json:"multiplier"`
}

type AccountState struct {
	AccountType   string       `json:"account_type"`
	BuyingPower   units.Micros `json:"buying_power"`
	Equity        units.Micros `json:"equity"`
	EquityKnown   bool         `json:"equity_known"`
	DayTradesUsed int          `json:"day_trades_used"`
	SettledCash   units.Micros `json:"settled_cash"`
}

type OrderResult struct {
	BrokerOrderID string       `json:"broker_order_id"`
	State         string       `json:"state"`
	FilledQty     units.Qty    `json:"filled_qty"`
	FilledPrice   units.Micros `json:"filled_price"`
	Reason        string       `json:"reason,omitempty"`
}

type Adapter interface {
	AccountID() (string, error)
	GetAccount() (AccountState, error)
	GetPositions() ([]Position, error)
	GetQuote(symbol string) (Quote, error)
	GetInstrument(symbol string) (Instrument, error)
	PlaceLimitOrder(symbol, side string, qty units.Qty, limit units.Micros, kind string) (OrderResult, error)
	CancelOrder(brokerOrderID string) (OrderResult, error)
	GetOrder(brokerOrderID string) (OrderResult, error)
}

func New() (Adapter, error) {
	switch os.Getenv("BROKER") {
	case "", "fake":
		return NewFake(units.MustMicros("300")), nil
	case "robinhood":
		return &Robinhood{}, nil
	default:
		return nil, fmt.Errorf("unknown BROKER %q", os.Getenv("BROKER"))
	}
}
