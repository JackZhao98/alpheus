// Package broker is the venue port. Agents NEVER see this interface —
// only the kernel does. Credentials live behind it and nowhere else.
// A new venue = a new adapter; nothing above this line changes.
package broker

import (
	"fmt"
	"math"
	"os"
)

type Quote struct {
	Symbol       string  `json:"symbol"`
	Bid          float64 `json:"bid"`
	Ask          float64 `json:"ask"`
	OpenInterest int     `json:"open_interest"` // options only; 0 = unknown
}

func (q Quote) Mid() float64 { return math.Round((q.Bid+q.Ask)/2*100) / 100 }

// Sane is the minimum invariant a quote must satisfy before it can support a
// risk or execution decision. Locked, crossed, non-positive, NaN, and infinite
// markets are not executable evidence and must fail closed upstream.
func (q Quote) Sane() bool {
	return !math.IsNaN(q.Bid) && !math.IsInf(q.Bid, 0) &&
		!math.IsNaN(q.Ask) && !math.IsInf(q.Ask, 0) &&
		q.Bid > 0 && q.Ask > q.Bid
}

func (q Quote) RelativeSpread() float64 {
	if !q.Sane() {
		return math.Inf(1)
	}
	m := (q.Bid + q.Ask) / 2
	return (q.Ask - q.Bid) / m
}

type Position struct {
	Symbol   string  `json:"symbol"` // underlying or OCC option symbol
	Qty      float64 `json:"qty"`
	AvgPrice float64 `json:"avg_price"`
	Kind     string  `json:"kind"` // equity | option
}

type AccountState struct {
	AccountType   string  `json:"account_type"` // cash | margin
	BuyingPower   float64 `json:"buying_power"`
	Equity        float64 `json:"equity"`
	DayTradesUsed int     `json:"day_trades_used"` // margin/PDT bookkeeping
	SettledCash   float64 `json:"settled_cash"`    // cash-account/GFV bookkeeping
}

type OrderResult struct {
	BrokerOrderID string  `json:"broker_order_id"`
	State         string  `json:"state"` // submitted | filled | cancelled | rejected
	FilledQty     float64 `json:"filled_qty"`
	FilledPrice   float64 `json:"filled_price"`
	Reason        string  `json:"reason,omitempty"`
}

type Adapter interface {
	GetAccount() (AccountState, error)
	GetPositions() ([]Position, error)
	GetQuote(symbol string) (Quote, error)
	PlaceLimitOrder(symbol, side string, qty, limit float64, kind string) (OrderResult, error)
	CancelOrder(brokerOrderID string) (OrderResult, error)
	GetOrder(brokerOrderID string) (OrderResult, error)
}

func New() (Adapter, error) {
	switch os.Getenv("BROKER") {
	case "", "fake":
		return NewFake(300), nil
	case "robinhood":
		return &Robinhood{}, nil
	default:
		return nil, fmt.Errorf("unknown BROKER %q", os.Getenv("BROKER"))
	}
}
