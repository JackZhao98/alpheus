// Deterministic sim broker. It is the paper venue, integration target, and
// replay venue. All money and quantities use the same exact domain types as the
// risk gate.
package broker

import (
	"fmt"
	"sync"
	"time"

	"alpheus/kernel/internal/units"
)

type Fake struct {
	mu          sync.Mutex
	cash        units.Micros
	positions   map[string]*Position
	quotes      map[string]Quote
	instruments map[string]Instrument
	orders      map[string]*fakeOrder
	seq         int
}

type fakeOrder struct {
	result     OrderResult
	symbol     string
	side       string
	kind       string
	qty        units.Qty
	limit      units.Micros
	multiplier int64
}

func NewFake(startingCash units.Micros) *Fake {
	now := time.Now().UTC()
	return &Fake{
		cash:      startingCash,
		positions: map[string]*Position{},
		quotes: map[string]Quote{
			"SPY": {
				Symbol: "SPY", Bid: units.MustMicros("0.34"),
				Ask: units.MustMicros("0.35"), OpenInterest: 45_000,
				Source: "fake", AsOf: now,
			},
		},
		instruments: map[string]Instrument{
			"SPY": {Symbol: "SPY", Kind: "option", Multiplier: 100},
		},
		orders: map[string]*fakeOrder{},
	}
}

// SetQuote advances the replay venue: a sane quote that crosses a resting
// limit fills that order synchronously, so integration tests can exercise the
// same mid-first policy as production without bypassing the risk gate.
func (f *Fake) SetQuote(q Quote) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if q.Source == "" {
		q.Source = "fake"
	}
	if q.AsOf.IsZero() {
		q.AsOf = time.Now().UTC()
	}
	f.quotes[q.Symbol] = q
	for _, order := range f.orders {
		if order.symbol != q.Symbol || order.result.State != "submitted" ||
			!marketable(order.side, order.limit, q) {
			continue
		}
		if err := f.fillOrder(order, q); err != nil {
			return err
		}
	}
	return nil
}

func (f *Fake) DeleteQuote(symbol string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.quotes, symbol)
}

func (f *Fake) SetInstrument(instrument Instrument) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instruments[instrument.Symbol] = instrument
}

func (f *Fake) DeleteInstrument(symbol string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.instruments, symbol)
}

func (f *Fake) GetAccount() (AccountState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	equity := f.cash
	known := true
	for _, position := range f.positions {
		quote, ok := f.quotes[position.Symbol]
		if !ok || !quote.Sane() {
			known = false
			continue
		}
		absQty, err := units.AbsQty(position.Qty)
		if err != nil {
			known = false
			continue
		}
		price := quote.Bid
		roundUp := false
		if position.Qty < 0 {
			price = quote.Ask
			// A short liability rounds up, against the account.
			roundUp = true
		}
		value, err := units.MulQtyPrice(absQty, price, position.Multiplier, roundUp)
		if err != nil {
			known = false
			continue
		}
		if position.Qty < 0 {
			value = -value
		}
		equity, err = units.Add(equity, value)
		if err != nil {
			known = false
		}
	}

	return AccountState{
		AccountType: "cash", BuyingPower: f.cash, Equity: equity,
		EquityKnown: known, SettledCash: f.cash,
	}, nil
}

func (f *Fake) GetPositions() ([]Position, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Position, 0, len(f.positions))
	for _, position := range f.positions {
		out = append(out, *position)
	}
	return out, nil
}

func (f *Fake) GetQuote(symbol string) (Quote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	quote, ok := f.quotes[symbol]
	if !ok {
		return Quote{}, fmt.Errorf("market data unavailable for %s", symbol)
	}
	return quote, nil
}

func (f *Fake) GetInstrument(symbol string) (Instrument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	instrument, ok := f.instruments[symbol]
	if !ok {
		return Instrument{}, fmt.Errorf("instrument metadata unavailable for %s", symbol)
	}
	return instrument, nil
}

func (f *Fake) PlaceLimitOrder(symbol, side string, qty units.Qty, limit units.Micros, kind string) (OrderResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if qty <= 0 || limit <= 0 {
		return OrderResult{}, fmt.Errorf("quantity and limit must be positive")
	}
	if side != "buy" && side != "sell" {
		return OrderResult{}, fmt.Errorf("unsupported side %q", side)
	}
	quote, ok := f.quotes[symbol]
	if !ok || !quote.Sane() {
		return OrderResult{}, fmt.Errorf("market data unavailable for %s", symbol)
	}

	multiplier := int64(1)
	if kind == "option" {
		instrument, ok := f.instruments[symbol]
		if !ok || instrument.Kind != "option" || instrument.Multiplier <= 0 {
			return OrderResult{}, fmt.Errorf("instrument metadata unavailable for %s", symbol)
		}
		multiplier = instrument.Multiplier
	}

	f.seq++
	id := fmt.Sprintf("fake-%d", f.seq)
	order := &fakeOrder{
		result: OrderResult{BrokerOrderID: id, State: "submitted"},
		symbol: symbol, side: side, kind: kind, qty: qty, limit: limit,
		multiplier: multiplier,
	}
	if marketable(side, limit, quote) {
		if err := f.fillOrder(order, quote); err != nil {
			return OrderResult{}, err
		}
	}
	f.orders[id] = order
	return order.result, nil
}

func marketable(side string, limit units.Micros, quote Quote) bool {
	return quote.Sane() &&
		((side == "buy" && limit >= quote.Ask) || (side == "sell" && limit <= quote.Bid))
}

func (f *Fake) fillOrder(order *fakeOrder, quote Quote) error {
	price := quote.Ask
	roundUp := true
	if order.side == "sell" {
		price = quote.Bid
		// Sale proceeds round down, against the account.
		roundUp = false
	}
	cashDelta, err := units.MulQtyPrice(order.qty, price, order.multiplier, roundUp)
	if err != nil {
		return err
	}

	signedQty := order.qty
	if order.side == "sell" {
		signedQty = -order.qty
	}
	position, exists := f.positions[order.symbol]
	var nextQty units.Qty
	if exists {
		if position.Kind != order.kind || position.Multiplier != order.multiplier {
			return fmt.Errorf("position metadata mismatch for %s", order.symbol)
		}
		nextQty, err = units.AddQty(position.Qty, signedQty)
		if err != nil {
			return err
		}
	}

	var nextCash units.Micros
	if order.side == "buy" {
		nextCash, err = units.Add(f.cash, -cashDelta)
	} else {
		nextCash, err = units.Add(f.cash, cashDelta)
	}
	if err != nil {
		return err
	}

	f.cash = nextCash
	if exists {
		position.Qty = nextQty
		if nextQty == 0 {
			delete(f.positions, order.symbol)
		}
	} else {
		f.positions[order.symbol] = &Position{
			Symbol: order.symbol, Qty: signedQty, AvgPrice: price,
			Kind: order.kind, Multiplier: order.multiplier,
		}
	}
	order.result.State = "filled"
	order.result.FilledQty = order.qty
	order.result.FilledPrice = price
	return nil
}

func (f *Fake) CancelOrder(id string) (OrderResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	order, ok := f.orders[id]
	if !ok {
		return OrderResult{BrokerOrderID: id, State: "rejected", Reason: "unknown order"}, nil
	}
	if order.result.State == "submitted" {
		order.result.State = "cancelled"
	}
	return order.result, nil
}

func (f *Fake) GetOrder(id string) (OrderResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if order, ok := f.orders[id]; ok {
		return order.result, nil
	}
	return OrderResult{BrokerOrderID: id, State: "rejected", Reason: "unknown order"}, nil
}
