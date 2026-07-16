// Deterministic sim broker. Three jobs:
//  1. paper trading Robinhood doesn't offer (shadow mode fills here),
//  2. integration-test target,
//  3. backtest venue when historical quotes are replayed into SetQuote.
package broker

import (
	"fmt"
	"sync"
)

type Fake struct {
	mu        sync.Mutex
	cash      float64
	positions map[string]*Position
	quotes    map[string]Quote
	orders    map[string]*OrderResult
	seq       int
}

func NewFake(startingCash float64) *Fake {
	return &Fake{
		cash:      startingCash,
		positions: map[string]*Position{},
		quotes:    map[string]Quote{"SPY": {Symbol: "SPY", Bid: 623.10, Ask: 623.14, OpenInterest: 45000}},
		orders:    map[string]*OrderResult{},
	}
}

// SetQuote is the sim control surface (exposed via kernel /sim/quote).
func (f *Fake) SetQuote(q Quote) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.quotes[q.Symbol] = q
}

func (f *Fake) GetAccount() (AccountState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return AccountState{AccountType: "cash", BuyingPower: f.cash, Equity: f.cash, SettledCash: f.cash}, nil
}

func (f *Fake) GetPositions() ([]Position, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Position, 0, len(f.positions))
	for _, p := range f.positions {
		out = append(out, *p)
	}
	return out, nil
}

func (f *Fake) GetQuote(symbol string) (Quote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q, ok := f.quotes[symbol]
	if !ok {
		q = Quote{Symbol: symbol, Bid: 100.0, Ask: 100.1, OpenInterest: 1000}
		f.quotes[symbol] = q
	}
	return q, nil
}

func (f *Fake) PlaceLimitOrder(symbol, side string, qty, limit float64, kind string) (OrderResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q, ok := f.quotes[symbol]
	if !ok {
		q = Quote{Symbol: symbol, Bid: 100.0, Ask: 100.1, OpenInterest: 1000}
		f.quotes[symbol] = q
	}
	f.seq++
	id := fmt.Sprintf("fake-%d", f.seq)
	marketable := (side == "buy" && limit >= q.Ask) || (side == "sell" && limit <= q.Bid)
	res := &OrderResult{BrokerOrderID: id, State: "submitted"}
	if marketable {
		px := q.Ask
		if side == "sell" {
			px = q.Bid
		}
		mult := 1.0
		if kind == "option" {
			mult = 100.0
		}
		delta := qty * px * mult
		if side == "buy" {
			f.cash -= delta
		} else {
			f.cash += delta
		}
		signed := qty
		if side == "sell" {
			signed = -qty
		}
		if p, exists := f.positions[symbol]; exists {
			p.Qty += signed
			if p.Qty == 0 {
				delete(f.positions, symbol)
			}
		} else {
			f.positions[symbol] = &Position{Symbol: symbol, Qty: signed, AvgPrice: px, Kind: kind}
		}
		res.State, res.FilledQty, res.FilledPrice = "filled", qty, px
	}
	f.orders[id] = res
	return *res, nil
}

func (f *Fake) CancelOrder(id string) (OrderResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[id]
	if !ok {
		return OrderResult{BrokerOrderID: id, State: "rejected", Reason: "unknown order"}, nil
	}
	if o.State == "submitted" {
		o.State = "cancelled"
	}
	return *o, nil
}

func (f *Fake) GetOrder(id string) (OrderResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o, ok := f.orders[id]; ok {
		return *o, nil
	}
	return OrderResult{BrokerOrderID: id, State: "rejected", Reason: "unknown order"}, nil
}
