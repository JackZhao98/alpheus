package marketdata

import (
	"context"
	"fmt"
	"strings"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/units"
)

// FakeProvider and Fake share one quote map through the Fake venue. Updating a
// replay quote therefore changes both the risk view and simulated fills.
type FakeProvider struct {
	venue *broker.Fake
}

func NewFakeProvider(venue *broker.Fake) *FakeProvider {
	return &FakeProvider{venue: venue}
}

func (f *FakeProvider) Quote(_ context.Context, symbol string) (broker.Quote, error) {
	return f.venue.GetQuote(symbol)
}

func (f *FakeProvider) Instrument(_ context.Context, symbol string) (InstrumentSpec, error) {
	return f.venue.GetInstrument(symbol)
}

func (f *FakeProvider) Chain(ctx context.Context, underlying, expiry string, window units.PercentMicros) ([]OptionQuote, error) {
	if window < 0 || window > units.MustPercent("15") {
		return nil, fmt.Errorf("window must be between 0 and 15 percentage points")
	}
	quote, err := f.Quote(ctx, underlying)
	if err != nil {
		return []OptionQuote{}, nil
	}
	instrument, err := f.Instrument(ctx, underlying)
	if err != nil || instrument.Kind != "option" {
		return []OptionQuote{}, nil
	}
	return []OptionQuote{{
		Instrument: instrument, Underlying: underlying, Expiry: expiry,
		OptionType: "call", Strike: quote.Mid(), Quote: quote,
	}}, nil
}

func (f *FakeProvider) Expirations(_ context.Context, _ string) ([]string, error) {
	return []string{}, nil
}

func (f *FakeProvider) Bars(ctx context.Context, symbol string, days int) ([]Bar, error) {
	if days < 1 || days > MaxBarDays {
		return nil, fmt.Errorf("days must be between 1 and %d", MaxBarDays)
	}
	quote, err := f.Quote(ctx, symbol)
	if err != nil {
		return nil, err
	}
	mid := quote.Mid()
	return []Bar{{
		Symbol: symbol, Open: mid, High: quote.Ask, Low: quote.Bid, Close: mid,
		Source: "fake", AsOf: quote.AsOf,
	}}, nil
}

func (f *FakeProvider) Movers(_ context.Context, direction string, n int) ([]Mover, error) {
	direction = strings.ToLower(direction)
	if direction != "up" && direction != "down" {
		return nil, fmt.Errorf("direction must be up or down")
	}
	if n < 1 || n > MaxMovers {
		return nil, fmt.Errorf("n must be between 1 and %d", MaxMovers)
	}
	return []Mover{}, nil
}

func (f *FakeProvider) Hours(_ context.Context) (MarketHours, error) {
	now := time.Now().UTC()
	return MarketHours{Date: now.Format(time.DateOnly), Source: "fake", AsOf: now}, nil
}

func (f *FakeProvider) ProviderStatus() Status {
	return Status{Connected: true, Source: "fake", SnapshotVersion: "fake-v1", LastSuccessfulRead: time.Now().UTC()}
}

func (f *FakeProvider) SetQuote(quote broker.Quote) error { return f.venue.SetQuote(quote) }
func (f *FakeProvider) DeleteQuote(symbol string)         { f.venue.DeleteQuote(symbol) }
