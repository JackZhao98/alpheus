// Package broker is the venue port. Agents NEVER see this interface — only the
// kernel does. Credentials live behind it and nowhere else.
package broker

import (
	"context"
	"errors"
	"time"

	"alpheus/kernel/internal/units"
)

var ErrNotFound = errors.New("broker object not found")

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
	Symbol         string       `json:"symbol"`
	InstrumentID   string       `json:"instrument_id"`
	Kind           string       `json:"kind"`
	Multiplier     int64        `json:"multiplier"`
	PriceTick      units.Micros `json:"price_tick"`
	BelowPriceTick units.Micros `json:"below_price_tick,omitempty"`
	TickCutoff     units.Micros `json:"tick_cutoff,omitempty"`
	QtyIncrement   units.Qty    `json:"qty_increment"`
	Source         string       `json:"source"`
	AsOf           time.Time    `json:"as_of"`
}

// TickForPrice returns the exact provider tick for a price. A zero below-tick
// and cutoff mean the instrument has one fixed tick at every supported price.
func (i Instrument) TickForPrice(price units.Micros) units.Micros {
	if i.BelowPriceTick > 0 && i.TickCutoff > 0 && price <= i.TickCutoff {
		return i.BelowPriceTick
	}
	return i.PriceTick
}

func (i Instrument) PrecisionSane() bool {
	if i.PriceTick <= 0 || i.QtyIncrement <= 0 {
		return false
	}
	variable := i.BelowPriceTick != 0 || i.TickCutoff != 0
	return !variable || (i.BelowPriceTick > 0 && i.TickCutoff > 0)
}

func (i Instrument) SupportsPrice(price units.Micros) bool {
	tick := i.TickForPrice(price)
	return i.PrecisionSane() && price > 0 && tick > 0 && price%tick == 0
}

type Position struct {
	PositionID    string       `json:"position_id"`
	InstrumentID  string       `json:"instrument_id"`
	Symbol        string       `json:"symbol"`
	Qty           units.Qty    `json:"qty"`
	AvgPrice      units.Micros `json:"avg_price"`
	AvgPriceKnown bool         `json:"avg_price_known"`
	Kind          string       `json:"kind"`
	Multiplier    int64        `json:"multiplier"`
	Source        string       `json:"source"`
	AsOf          time.Time    `json:"as_of"`
}

type AccountState struct {
	ExternalID    string       `json:"-"`
	AccountType   string       `json:"account_type"`
	BuyingPower   units.Micros `json:"buying_power"`
	Equity        units.Micros `json:"equity"`
	EquityKnown   bool         `json:"equity_known"`
	DayTradesUsed int          `json:"day_trades_used"`
	Cash          units.Micros `json:"cash"`
	CashKnown     bool         `json:"cash_known"`
	Source        string       `json:"source"`
	AsOf          time.Time    `json:"as_of"`
}

type ReadOrder struct {
	BrokerOrderID   string       `json:"broker_order_id"`
	ClientOrderID   string       `json:"client_order_id,omitempty"`
	InstrumentID    string       `json:"instrument_id"`
	Symbol          string       `json:"symbol"`
	Side            string       `json:"side"`
	State           string       `json:"state"`
	Qty             units.Qty    `json:"qty"`
	FilledQty       units.Qty    `json:"filled_qty"`
	LimitPrice      units.Micros `json:"limit_price"`
	LimitPriceKnown bool         `json:"limit_price_known"`
	Source          string       `json:"source"`
	AsOf            time.Time    `json:"as_of"`
}

type ReadFill struct {
	FillID        string       `json:"fill_id"`
	BrokerOrderID string       `json:"broker_order_id"`
	InstrumentID  string       `json:"instrument_id"`
	Symbol        string       `json:"symbol"`
	Side          string       `json:"side"`
	Qty           units.Qty    `json:"qty"`
	Price         units.Micros `json:"price"`
	Fees          units.Micros `json:"fees"`
	Source        string       `json:"source"`
	AsOf          time.Time    `json:"as_of"`
}

type PlaceRequest struct {
	ClientOrderID  string       `json:"client_order_id"`
	Symbol         string       `json:"symbol"`
	Side           string       `json:"side"`
	PositionEffect string       `json:"position_effect"`
	Qty            units.Qty    `json:"qty"`
	Limit          units.Micros `json:"limit"`
	Kind           string       `json:"kind"`
}

// ProviderPlaceIntent is the canonical, provider-visible identity of one
// place mutation. It is persisted before the first live send and is also the
// only shape exact candidate reconciliation may use.
type ProviderPlaceIntent struct {
	Kind           string       `json:"kind"`
	InstrumentID   string       `json:"instrument_id"`
	Symbol         string       `json:"symbol"`
	Side           string       `json:"side"`
	PositionEffect string       `json:"position_effect"`
	Qty            units.Qty    `json:"qty"`
	Limit          units.Micros `json:"limit"`
	OrderType      string       `json:"order_type"`
	Trigger        string       `json:"trigger"`
	TimeInForce    string       `json:"time_in_force"`
	MarketHours    string       `json:"market_hours"`
}

type ExactPlaceCandidateQuery struct {
	AccountID     string
	ClientOrderID string
	Intent        ProviderPlaceIntent
	WindowStart   time.Time
	WindowEnd     time.Time
}

type ExactPlaceCandidateProvider interface {
	FindExactPlaceCandidates(ctx context.Context, query ExactPlaceCandidateQuery) ([]OrderResult, error)
}

// OrderKindSupport lets a production adapter expose a narrower mutation
// surface than its read provider. The kernel consults it before persisting a
// live grant and again before marking a provider call sent.
type OrderKindSupport interface {
	SupportsOrderKind(kind string) bool
}

// InstrumentReader is the minimum read capability an execution adapter needs
// to revalidate the persisted order against current provider metadata before
// any mutation. marketdata.Provider satisfies it without creating a package
// dependency from broker back to marketdata.
type InstrumentReader interface {
	Instrument(ctx context.Context, symbol string) (Instrument, error)
}

type OrderResult struct {
	BrokerOrderID string       `json:"broker_order_id"`
	ClientOrderID string       `json:"client_order_id,omitempty"`
	State         string       `json:"state"`
	FilledQty     units.Qty    `json:"filled_qty"`
	FilledPrice   units.Micros `json:"filled_price"`
	Fills         []ReadFill   `json:"fills,omitempty"`
	Reason        string       `json:"reason,omitempty"`
}

type AccountProvider interface {
	Account(ctx context.Context) (AccountState, error)
	Positions(ctx context.Context) ([]Position, error)
	OpenOrders(ctx context.Context) ([]ReadOrder, error)
	RecentFills(ctx context.Context, since time.Time) ([]ReadFill, error)
	AccountID(ctx context.Context) (string, error)
}

type RealizedPnLSnapshot struct {
	Total  units.Micros
	Source string
	AsOf   time.Time
}

// RealizedPnLProvider is an optional read-only account capability. Simulation
// and shadow ledgers use the kernel's durable FIFO ledger and need not
// implement it; the production Robinhood reader does.
type RealizedPnLProvider interface {
	RealizedPnL(ctx context.Context, marketDay time.Time, marketTZ string) (RealizedPnLSnapshot, error)
}

type ExecutionProvider interface {
	PlaceLimitOrder(ctx context.Context, req PlaceRequest) (OrderResult, error)
	CancelOrder(ctx context.Context, brokerOrderID string) (OrderResult, error)
	GetOrder(ctx context.Context, brokerOrderID string) (OrderResult, error)
}

// ClientOrderFinder is the preferred recovery capability when a provider
// exposes the client-generated identity in its read API. Robinhood currently
// does not, so its bounded recovery uses ExactPlaceCandidateProvider instead.
type ClientOrderFinder interface {
	FindOrderByClientID(ctx context.Context, clientOrderID string) (OrderResult, error)
}

// Adapter is intentionally implemented only by the simulation venue. The
// production read-only provider must never satisfy this interface before M11.
type Adapter interface {
	AccountProvider
	ExecutionProvider
}
