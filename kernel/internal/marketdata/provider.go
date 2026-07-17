// Package marketdata is the read-only market-data capability boundary. It is
// deliberately separate from broker execution so production read modes cannot
// gain order methods by construction.
package marketdata

import (
	"context"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/units"
)

const (
	MaxChainWindowPct = 15
	MaxBarDays        = 30
	MaxMovers         = 10
)

type InstrumentSpec = broker.Instrument

type OptionQuote struct {
	Instrument InstrumentSpec `json:"instrument"`
	Underlying string         `json:"underlying"`
	Expiry     string         `json:"expiry"`
	OptionType string         `json:"option_type"`
	Strike     units.Micros   `json:"strike"`
	Quote      broker.Quote   `json:"quote"`
}

type Bar struct {
	Symbol string       `json:"symbol"`
	Open   units.Micros `json:"open"`
	High   units.Micros `json:"high"`
	Low    units.Micros `json:"low"`
	Close  units.Micros `json:"close"`
	Volume units.Qty    `json:"volume"`
	Source string       `json:"source"`
	AsOf   time.Time    `json:"as_of"`
}

type Mover struct {
	Symbol        string              `json:"symbol"`
	Price         units.Micros        `json:"price"`
	ChangePercent units.PercentMicros `json:"change_percent"`
	Direction     string              `json:"direction"`
	Source        string              `json:"source"`
	AsOf          time.Time           `json:"as_of"`
}

type MarketHours struct {
	Date       string    `json:"date"`
	IsOpen     bool      `json:"is_open"`
	OpensAt    time.Time `json:"opens_at"`
	ClosesAt   time.Time `json:"closes_at"`
	ExtendedAt time.Time `json:"extended_closes_at,omitempty"`
	Source     string    `json:"source"`
	AsOf       time.Time `json:"as_of"`
}

type Provider interface {
	Quote(ctx context.Context, symbol string) (broker.Quote, error)
	Instrument(ctx context.Context, symbol string) (InstrumentSpec, error)
	Chain(ctx context.Context, underlying, expiry string, window units.PercentMicros) ([]OptionQuote, error)
	Expirations(ctx context.Context, underlying string) ([]string, error)
	Bars(ctx context.Context, symbol string, days int) ([]Bar, error)
	Movers(ctx context.Context, direction string, n int) ([]Mover, error)
	Hours(ctx context.Context) (MarketHours, error)
}

type Status struct {
	Connected          bool      `json:"connected"`
	Source             string    `json:"source"`
	SnapshotVersion    string    `json:"snapshot_version"`
	LastSuccessfulRead time.Time `json:"last_successful_read,omitempty"`
	LastError          string    `json:"last_error,omitempty"`
	SchemaDrift        bool      `json:"schema_drift"`
}

type StatusProvider interface {
	ProviderStatus() Status
}
