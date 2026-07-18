package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/rhmcp"
	"alpheus/kernel/internal/units"
)

const robinhoodSource = "robinhood-mcp"

var RobinhoodReadTools = []string{
	"get_accounts", "get_portfolio", "get_equity_positions", "get_option_positions",
	"get_equity_orders", "get_option_orders", "get_equity_quotes", "get_option_quotes",
	"get_equity_tradability", "get_equity_price_book", "get_option_chains", "get_option_instruments", "get_equity_historicals",
	"get_realized_pnl", "get_pnl_trade_history", "search",
}

type RobinhoodProvider struct {
	caller rhmcp.Caller
	status interface {
		Status() rhmcp.Status
		MarkSchemaDrift()
		MarkDataError()
	}
	snapshotVersion string
}

func NewRobinhoodProvider(caller rhmcp.Caller, status interface {
	Status() rhmcp.Status
	MarkSchemaDrift()
	MarkDataError()
}, snapshotVersion string) (*RobinhoodProvider, error) {
	if caller == nil || status == nil || snapshotVersion == "" {
		return nil, fmt.Errorf("Robinhood market provider is incomplete")
	}
	return &RobinhoodProvider{caller: caller, status: status, snapshotVersion: snapshotVersion}, nil
}

func (p *RobinhoodProvider) schemaError(message string) error {
	p.status.MarkSchemaDrift()
	return fmt.Errorf("%s", message)
}

func (p *RobinhoodProvider) dataError(message string) error {
	p.status.MarkDataError()
	return fmt.Errorf("%s", message)
}

func (p *RobinhoodProvider) decodeData(raw json.RawMessage, dst any) error {
	err := rhmcp.DecodeData(raw, dst)
	if err != nil {
		p.status.MarkSchemaDrift()
	}
	return err
}

type exactWhole int64

func (v *exactWhole) UnmarshalJSON(raw []byte) error {
	parsed, err := rhmcp.DecodeExactWhole(raw)
	if err != nil {
		return fmt.Errorf("invalid whole number")
	}
	*v = exactWhole(parsed)
	return nil
}

type equityQuoteRecord struct {
	Symbol       string        `json:"symbol"`
	BidPrice     *units.Micros `json:"bid_price"`
	AskPrice     *units.Micros `json:"ask_price"`
	VenueBidTime string        `json:"venue_bid_time"`
	VenueAskTime string        `json:"venue_ask_time"`
	HasTraded    *bool         `json:"has_traded"`
	State        string        `json:"state"`
}

type optionQuoteRecord struct {
	InstrumentID string        `json:"instrument_id"`
	BidPrice     *units.Micros `json:"bid_price"`
	AskPrice     *units.Micros `json:"ask_price"`
	OpenInterest *exactWhole   `json:"open_interest"`
	UpdatedAt    string        `json:"updated_at"`
}

func parseProviderTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.IsZero() {
		return time.Time{}, fmt.Errorf("provider timestamp drift")
	}
	return parsed.UTC(), nil
}

func normalizedEquityQuote(record equityQuoteRecord) (broker.Quote, error) {
	if record.Symbol == "" || record.BidPrice == nil || record.AskPrice == nil ||
		record.VenueBidTime == "" || record.VenueAskTime == "" || record.HasTraded == nil || record.State == "" {
		return broker.Quote{}, fmt.Errorf("quote schema drift")
	}
	if !*record.HasTraded || record.State != "active" {
		return broker.Quote{}, fmt.Errorf("quote data is not executable")
	}
	bidAsOf, err := parseProviderTime(record.VenueBidTime)
	if err != nil {
		return broker.Quote{}, fmt.Errorf("quote timestamp drift")
	}
	askAsOf, err := parseProviderTime(record.VenueAskTime)
	if err != nil {
		return broker.Quote{}, fmt.Errorf("quote timestamp drift")
	}
	asOf := bidAsOf
	if askAsOf.Before(asOf) {
		asOf = askAsOf
	}
	quote := broker.Quote{
		Symbol: record.Symbol, Bid: *record.BidPrice, Ask: *record.AskPrice,
		Source: robinhoodSource, AsOf: asOf,
	}
	if !quote.Sane() {
		return broker.Quote{}, fmt.Errorf("quote data is not executable")
	}
	return quote, nil
}

func normalizedOptionQuote(record optionQuoteRecord) (broker.Quote, error) {
	if record.InstrumentID == "" || record.BidPrice == nil || record.AskPrice == nil ||
		record.OpenInterest == nil || record.UpdatedAt == "" {
		return broker.Quote{}, fmt.Errorf("quote schema drift")
	}
	if *record.OpenInterest < 0 || uint64(*record.OpenInterest) > uint64(^uint(0)>>1) {
		return broker.Quote{}, fmt.Errorf("quote open interest drift")
	}
	asOf, err := parseProviderTime(record.UpdatedAt)
	if err != nil {
		return broker.Quote{}, fmt.Errorf("quote timestamp drift")
	}
	quote := broker.Quote{
		Symbol: record.InstrumentID, Bid: *record.BidPrice, Ask: *record.AskPrice,
		OpenInterest: int(*record.OpenInterest), Source: robinhoodSource, AsOf: asOf,
	}
	if !quote.Sane() {
		return broker.Quote{}, fmt.Errorf("quote data is not executable")
	}
	return quote, nil
}

func (p *RobinhoodProvider) normalizedEquityQuote(record equityQuoteRecord) (broker.Quote, error) {
	quote, err := normalizedEquityQuote(record)
	if err == nil {
		return quote, nil
	}
	if strings.Contains(err.Error(), "drift") {
		return broker.Quote{}, p.schemaError("quote schema drift")
	}
	return broker.Quote{}, p.dataError("quote data is not executable")
}

func (p *RobinhoodProvider) normalizedOptionQuote(record optionQuoteRecord) (broker.Quote, error) {
	quote, err := normalizedOptionQuote(record)
	if err == nil {
		return quote, nil
	}
	if strings.Contains(err.Error(), "drift") {
		return broker.Quote{}, p.schemaError("quote schema drift")
	}
	return broker.Quote{}, p.dataError("quote data is not executable")
}

func (p *RobinhoodProvider) equityQuote(ctx context.Context, symbol string) (broker.Quote, error) {
	raw, err := p.caller.Call(ctx, "get_equity_quotes", map[string]any{"symbols": []string{symbol}})
	if err != nil {
		return broker.Quote{}, fmt.Errorf("quote data unavailable")
	}
	var data struct {
		Results []*struct {
			Quote *equityQuoteRecord `json:"quote"`
		} `json:"results"`
	}
	if err := p.decodeData(raw, &data); err != nil {
		return broker.Quote{}, err
	}
	var match *equityQuoteRecord
	for _, result := range data.Results {
		if result == nil || result.Quote == nil {
			return broker.Quote{}, p.schemaError("quote schema drift")
		}
		if result.Quote.Symbol != symbol {
			continue
		}
		if match != nil {
			return broker.Quote{}, p.schemaError("quote match is ambiguous")
		}
		match = result.Quote
	}
	if match == nil {
		return broker.Quote{}, p.dataError("quote not found")
	}
	return p.normalizedEquityQuote(*match)
}

func (p *RobinhoodProvider) optionQuotes(ctx context.Context, instrumentIDs []string) (map[string]optionQuoteRecord, error) {
	if len(instrumentIDs) == 0 || len(instrumentIDs) > 20 {
		return nil, fmt.Errorf("option quote request exceeds provider contract")
	}
	raw, err := p.caller.Call(ctx, "get_option_quotes", map[string]any{"instrument_ids": instrumentIDs})
	if err != nil {
		return nil, fmt.Errorf("quote data unavailable")
	}
	var data struct {
		Results []*struct {
			Quote *optionQuoteRecord `json:"quote"`
		} `json:"results"`
	}
	if err := p.decodeData(raw, &data); err != nil {
		return nil, err
	}
	out := make(map[string]optionQuoteRecord, len(data.Results))
	for _, result := range data.Results {
		if result == nil || result.Quote == nil || result.Quote.InstrumentID == "" {
			return nil, p.schemaError("quote schema drift")
		}
		key := strings.ToLower(result.Quote.InstrumentID)
		if _, exists := out[key]; exists {
			return nil, p.schemaError("quote match is ambiguous")
		}
		out[key] = *result.Quote
	}
	return out, nil
}

func looksLikeUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func (p *RobinhoodProvider) Quote(ctx context.Context, symbol string) (broker.Quote, error) {
	symbol = strings.TrimSpace(symbol)
	if looksLikeUUID(symbol) {
		id := strings.ToLower(symbol)
		records, err := p.optionQuotes(ctx, []string{id})
		if err != nil {
			return broker.Quote{}, err
		}
		record, ok := records[id]
		if !ok {
			return broker.Quote{}, p.dataError("quote not found")
		}
		return p.normalizedOptionQuote(record)
	}
	return p.equityQuote(ctx, strings.ToUpper(symbol))
}

type tickRules struct {
	Above  *units.Micros `json:"above_tick"`
	Below  *units.Micros `json:"below_tick"`
	Cutoff *units.Micros `json:"cutoff_price"`
}

func fixedTick(rules *tickRules) (units.Micros, error) {
	if rules == nil || rules.Above == nil || rules.Below == nil || rules.Cutoff == nil {
		return 0, fmt.Errorf("tick schema drift")
	}
	if *rules.Above <= 0 || *rules.Below <= 0 || *rules.Cutoff < 0 {
		return 0, fmt.Errorf("tick schema drift")
	}
	if *rules.Above != *rules.Below {
		return 0, fmt.Errorf("variable tick schedule is unsupported")
	}
	return *rules.Above, nil
}

type optionChainRecord struct {
	ID                    string          `json:"id"`
	Symbol                string          `json:"symbol"`
	CanOpenPosition       *bool           `json:"can_open_position"`
	CashComponent         json.RawMessage `json:"cash_component"`
	ExpirationDates       []string        `json:"expiration_dates"`
	TradeValueMultiplier  *exactWhole     `json:"trade_value_multiplier"`
	MinTicks              *tickRules      `json:"min_ticks"`
	UnderlyingInstruments []struct {
		Instrument string `json:"instrument"`
		Symbol     string `json:"symbol"`
	} `json:"underlying_instruments"`
}

func (p *RobinhoodProvider) loadOptionChains(ctx context.Context, args map[string]any) ([]optionChainRecord, error) {
	raw, err := p.caller.Call(ctx, "get_option_chains", args)
	if err != nil {
		return nil, fmt.Errorf("option chain data unavailable")
	}
	var data struct {
		Chains []*optionChainRecord `json:"chains"`
	}
	if err := p.decodeData(raw, &data); err != nil {
		return nil, err
	}
	out := make([]optionChainRecord, 0, len(data.Chains))
	for _, chain := range data.Chains {
		if chain == nil {
			return nil, p.schemaError("option chain schema drift")
		}
		out = append(out, *chain)
	}
	return out, nil
}

func (p *RobinhoodProvider) standardOptionChain(chain optionChainRecord) (bool, units.Micros, error) {
	if chain.ID == "" || chain.Symbol == "" || chain.CanOpenPosition == nil ||
		chain.TradeValueMultiplier == nil || chain.MinTicks == nil || len(chain.CashComponent) == 0 {
		return false, 0, p.schemaError("option chain schema drift")
	}
	tick, err := fixedTick(chain.MinTicks)
	if err != nil {
		if strings.Contains(err.Error(), "schema drift") {
			return false, 0, p.schemaError("option tick schema drift")
		}
		return false, 0, nil
	}
	standardDeliverable := string(chain.CashComponent) == "null" && len(chain.UnderlyingInstruments) == 1 &&
		chain.UnderlyingInstruments[0].Instrument != ""
	if !*chain.CanOpenPosition || int64(*chain.TradeValueMultiplier) != 100 || !standardDeliverable {
		return false, 0, nil
	}
	return true, tick, nil
}

type optionInstrumentRecord struct {
	ID              string        `json:"id"`
	ChainID         string        `json:"chain_id"`
	ChainSymbol     string        `json:"chain_symbol"`
	UnderlyingType  string        `json:"underlying_type"`
	ExpirationDate  string        `json:"expiration_date"`
	SelloutDatetime string        `json:"sellout_datetime"`
	StrikePrice     *units.Micros `json:"strike_price"`
	Type            string        `json:"type"`
	State           string        `json:"state"`
	Tradability     string        `json:"tradability"`
	MinTicks        *tickRules    `json:"min_ticks"`
}

func providerNextCursor(next string) (string, error) {
	if next == "" {
		return "", nil
	}
	parsed, err := url.Parse(next)
	if err != nil {
		return "", err
	}
	values, ok := parsed.Query()["cursor"]
	if !ok || len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", fmt.Errorf("invalid provider pagination")
	}
	return values[0], nil
}

func cloneArgs(input map[string]any) map[string]any {
	out := make(map[string]any, len(input)+1)
	for key, value := range input {
		out[key] = value
	}
	return out
}

func (p *RobinhoodProvider) loadOptionInstruments(ctx context.Context, baseArgs map[string]any) ([]optionInstrumentRecord, error) {
	out := []optionInstrumentRecord{}
	cursor := ""
	for page := 0; page < 100; page++ {
		args := cloneArgs(baseArgs)
		if cursor != "" {
			args["cursor"] = cursor
		}
		raw, err := p.caller.Call(ctx, "get_option_instruments", args)
		if err != nil {
			return nil, fmt.Errorf("option instrument data unavailable")
		}
		var data struct {
			Instruments []*optionInstrumentRecord `json:"instruments"`
			Next        string                    `json:"next"`
		}
		if err := p.decodeData(raw, &data); err != nil {
			return nil, err
		}
		for _, instrument := range data.Instruments {
			if instrument == nil {
				return nil, p.schemaError("option instrument schema drift")
			}
			out = append(out, *instrument)
			if len(out) > 10_000 {
				return nil, fmt.Errorf("option instrument result exceeds safety bound")
			}
		}
		cursor, err = providerNextCursor(data.Next)
		if err != nil {
			return nil, p.schemaError("option instrument pagination drift")
		}
		if cursor == "" {
			return out, nil
		}
	}
	return nil, fmt.Errorf("option instrument pagination exceeds safety bound")
}

func (p *RobinhoodProvider) normalizedInstrument(record optionInstrumentRecord, chain optionChainRecord, tick units.Micros) (InstrumentSpec, error) {
	if record.ID == "" || record.ChainID == "" || record.ChainSymbol == "" || record.ExpirationDate == "" ||
		record.SelloutDatetime == "" || record.StrikePrice == nil || record.Type == "" || record.State == "" ||
		record.Tradability == "" || record.UnderlyingType == "" || record.MinTicks == nil {
		return InstrumentSpec{}, p.schemaError("option instrument schema drift")
	}
	if record.ChainID != chain.ID || (record.Type != "call" && record.Type != "put") ||
		record.State != "active" || record.Tradability != "tradable" || record.UnderlyingType != "equity" || *record.StrikePrice <= 0 {
		return InstrumentSpec{}, p.dataError("option instrument is unsupported")
	}
	if _, err := time.Parse(time.DateOnly, record.ExpirationDate); err != nil {
		return InstrumentSpec{}, p.schemaError("option expiration schema drift")
	}
	if _, err := parseProviderTime(record.SelloutDatetime); err != nil {
		return InstrumentSpec{}, p.schemaError("option sellout timestamp drift")
	}
	instrumentTick, err := fixedTick(record.MinTicks)
	if err != nil || instrumentTick != tick {
		if err != nil && strings.Contains(err.Error(), "unsupported") {
			return InstrumentSpec{}, p.dataError("variable option tick schedule is unsupported")
		}
		return InstrumentSpec{}, p.schemaError("option tick schema drift")
	}
	return InstrumentSpec{
		Symbol: record.ChainSymbol, InstrumentID: record.ID, Kind: "option", Multiplier: 100,
		PriceTick: tick, QtyIncrement: units.MustQty("1"), Source: robinhoodSource, AsOf: time.Now().UTC(),
	}, nil
}

func (p *RobinhoodProvider) Instrument(ctx context.Context, symbol string) (InstrumentSpec, error) {
	if !looksLikeUUID(symbol) {
		return p.equityInstrument(ctx, strings.ToUpper(strings.TrimSpace(symbol)))
	}
	id := strings.ToLower(symbol)
	instruments, err := p.loadOptionInstruments(ctx, map[string]any{"ids": id, "state": "active", "tradability": "tradable"})
	if err != nil {
		return InstrumentSpec{}, err
	}
	if len(instruments) != 1 || !strings.EqualFold(instruments[0].ID, id) {
		return InstrumentSpec{}, p.dataError("option instrument match is not exact")
	}
	chains, err := p.loadOptionChains(ctx, map[string]any{"ids": instruments[0].ChainID})
	if err != nil {
		return InstrumentSpec{}, err
	}
	if len(chains) != 1 || chains[0].ID != instruments[0].ChainID {
		return InstrumentSpec{}, p.schemaError("option chain match is not exact")
	}
	standard, tick, err := p.standardOptionChain(chains[0])
	if err != nil {
		return InstrumentSpec{}, err
	}
	if !standard {
		return InstrumentSpec{}, p.dataError("non-standard option contract is unsupported")
	}
	return p.normalizedInstrument(instruments[0], chains[0], tick)
}

type equitySearchRecord struct {
	InstrumentID string `json:"instrument_id"`
	Symbol       string `json:"symbol"`
	Name         string `json:"name"`
}

func (p *RobinhoodProvider) equityInstrument(ctx context.Context, symbol string) (InstrumentSpec, error) {
	if symbol == "" {
		return InstrumentSpec{}, p.dataError("equity instrument symbol is empty")
	}
	raw, err := p.caller.Call(ctx, "search", map[string]any{"query": symbol, "limit": 10})
	if err != nil {
		return InstrumentSpec{}, p.dataError("equity instrument identity unavailable")
	}
	var data struct {
		Results []*equitySearchRecord `json:"results"`
	}
	if err := p.decodeData(raw, &data); err != nil {
		return InstrumentSpec{}, err
	}
	var match *equitySearchRecord
	for _, result := range data.Results {
		if result == nil || result.InstrumentID == "" || result.Symbol == "" || result.Name == "" {
			return InstrumentSpec{}, p.schemaError("equity instrument search schema drift")
		}
		if result.Symbol != symbol {
			continue
		}
		if match != nil {
			return InstrumentSpec{}, p.schemaError("equity instrument identity is ambiguous")
		}
		match = result
	}
	if match == nil || !looksLikeUUID(match.InstrumentID) {
		return InstrumentSpec{}, p.dataError("equity instrument identity is not exact")
	}
	quote, err := p.equityQuote(ctx, symbol)
	if err != nil {
		return InstrumentSpec{}, err
	}
	instrument := InstrumentSpec{
		Symbol: symbol, InstrumentID: strings.ToLower(match.InstrumentID), Kind: "equity", Multiplier: 1,
		PriceTick: units.MustMicros("0.01"), BelowPriceTick: units.MustMicros("0.0001"),
		TickCutoff: units.MustMicros("1"), QtyIncrement: units.MustQty("1"),
		Source: robinhoodSource, AsOf: quote.AsOf,
	}
	if !instrument.SupportsPrice(quote.Bid) || !instrument.SupportsPrice(quote.Ask) {
		return InstrumentSpec{}, p.dataError("equity quote violates certified tick schedule")
	}
	return instrument, nil
}

func containsDate(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (p *RobinhoodProvider) Chain(ctx context.Context, underlying, expiry string, window units.PercentMicros) ([]OptionQuote, error) {
	if window < 0 || window > units.MustPercent("15") {
		return nil, fmt.Errorf("chain window exceeds provider contract")
	}
	if parsed, err := time.Parse(time.DateOnly, expiry); err != nil || parsed.Format(time.DateOnly) != expiry {
		return nil, fmt.Errorf("expiry must be YYYY-MM-DD")
	}
	underlying = strings.ToUpper(strings.TrimSpace(underlying))
	chains, err := p.loadOptionChains(ctx, map[string]any{"underlying_symbol": underlying})
	if err != nil {
		return nil, err
	}
	var selected *optionChainRecord
	var chainTick units.Micros
	for i := range chains {
		standard, tick, err := p.standardOptionChain(chains[i])
		if err != nil {
			return nil, err
		}
		if !standard || !containsDate(chains[i].ExpirationDates, expiry) {
			continue
		}
		if selected != nil {
			return nil, p.dataError("option chain match is ambiguous")
		}
		selected, chainTick = &chains[i], tick
	}
	if selected == nil {
		return []OptionQuote{}, nil
	}
	underlyingQuote, err := p.equityQuote(ctx, underlying)
	if err != nil {
		return nil, err
	}
	delta, err := units.PercentFloor(underlyingQuote.Mid(), window)
	if err != nil {
		return nil, p.dataError("chain window calculation failed")
	}
	lower, err := units.Add(underlyingQuote.Mid(), -delta)
	if err != nil {
		return nil, p.dataError("chain window calculation failed")
	}
	upper, err := units.Add(underlyingQuote.Mid(), delta)
	if err != nil {
		return nil, p.dataError("chain window calculation failed")
	}
	instruments, err := p.loadOptionInstruments(ctx, map[string]any{
		"chain_id": selected.ID, "expiration_dates": expiry, "state": "active", "tradability": "tradable",
	})
	if err != nil {
		return nil, err
	}
	type normalizedContract struct {
		record     optionInstrumentRecord
		instrument InstrumentSpec
	}
	contracts := []normalizedContract{}
	for _, record := range instruments {
		if record.ChainID != selected.ID || record.ExpirationDate != expiry || record.StrikePrice == nil {
			return nil, p.schemaError("option instrument filter drift")
		}
		if *record.StrikePrice < lower || *record.StrikePrice > upper {
			continue
		}
		instrument, err := p.normalizedInstrument(record, *selected, chainTick)
		if err != nil {
			return nil, err
		}
		contracts = append(contracts, normalizedContract{record: record, instrument: instrument})
	}
	quotes := make(map[string]optionQuoteRecord, len(contracts))
	for start := 0; start < len(contracts); start += 20 {
		end := start + 20
		if end > len(contracts) {
			end = len(contracts)
		}
		ids := make([]string, 0, end-start)
		for _, contract := range contracts[start:end] {
			ids = append(ids, contract.record.ID)
		}
		batch, err := p.optionQuotes(ctx, ids)
		if err != nil {
			return nil, err
		}
		for id, quote := range batch {
			quotes[id] = quote
		}
	}
	out := []OptionQuote{}
	for _, contract := range contracts {
		record, ok := quotes[strings.ToLower(contract.record.ID)]
		if !ok {
			continue
		}
		quote, err := normalizedOptionQuote(record)
		if err != nil {
			if strings.Contains(err.Error(), "drift") {
				return nil, p.schemaError("option quote schema drift")
			}
			// Zero/locked/crossed quotes are not executable. Omitting them from a
			// discovery list is fail closed; direct Quote still returns an error.
			continue
		}
		out = append(out, OptionQuote{
			Instrument: contract.instrument, Underlying: underlying, Expiry: expiry,
			OptionType: contract.record.Type, Strike: *contract.record.StrikePrice, Quote: quote,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Strike != out[j].Strike {
			return out[i].Strike < out[j].Strike
		}
		return out[i].OptionType < out[j].OptionType
	})
	return out, nil
}

func (p *RobinhoodProvider) Expirations(ctx context.Context, underlying string) ([]string, error) {
	underlying = strings.ToUpper(strings.TrimSpace(underlying))
	chains, err := p.loadOptionChains(ctx, map[string]any{"underlying_symbol": underlying})
	if err != nil {
		return nil, err
	}
	dates := map[string]struct{}{}
	for _, chain := range chains {
		standard, _, err := p.standardOptionChain(chain)
		if err != nil {
			return nil, err
		}
		if !standard {
			continue
		}
		for _, expiry := range chain.ExpirationDates {
			parsed, err := time.Parse(time.DateOnly, expiry)
			if err != nil || parsed.Format(time.DateOnly) != expiry {
				return nil, p.schemaError("option expiration schema drift")
			}
			dates[expiry] = struct{}{}
		}
	}
	out := make([]string, 0, len(dates))
	for expiry := range dates {
		out = append(out, expiry)
	}
	sort.Strings(out)
	return out, nil
}

func (p *RobinhoodProvider) Bars(ctx context.Context, symbol string, days int) ([]Bar, error) {
	if days < 1 || days > MaxBarDays {
		return nil, fmt.Errorf("bar window exceeds provider contract")
	}
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	raw, err := p.caller.Call(ctx, "get_equity_historicals", map[string]any{
		"symbols": []string{symbol}, "start_time": time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339),
		"interval": "day", "bounds": "regular",
	})
	if err != nil {
		return nil, fmt.Errorf("historical data unavailable")
	}
	type barRecord struct {
		Open     *units.Micros `json:"open_price"`
		High     *units.Micros `json:"high_price"`
		Low      *units.Micros `json:"low_price"`
		Close    *units.Micros `json:"close_price"`
		Volume   *units.Qty    `json:"volume"`
		BeginsAt string        `json:"begins_at"`
	}
	var data struct {
		Results []*struct {
			Symbol   string       `json:"symbol"`
			Interval string       `json:"interval"`
			Bounds   string       `json:"bounds"`
			Bars     []*barRecord `json:"bars"`
		} `json:"results"`
	}
	if err := p.decodeData(raw, &data); err != nil {
		return nil, err
	}
	var match *struct {
		Symbol   string       `json:"symbol"`
		Interval string       `json:"interval"`
		Bounds   string       `json:"bounds"`
		Bars     []*barRecord `json:"bars"`
	}
	for _, result := range data.Results {
		if result == nil {
			return nil, p.schemaError("historical schema drift")
		}
		if result.Symbol != symbol {
			continue
		}
		if match != nil {
			return nil, p.schemaError("historical match is ambiguous")
		}
		match = result
	}
	if match == nil {
		return nil, p.dataError("historical data not found")
	}
	if match.Interval != "day" || match.Bounds != "regular" {
		return nil, p.schemaError("historical request echo drift")
	}
	out := make([]Bar, 0, len(match.Bars))
	for _, item := range match.Bars {
		if item == nil || item.Open == nil || item.High == nil || item.Low == nil || item.Close == nil ||
			item.Volume == nil || item.BeginsAt == "" {
			return nil, p.schemaError("historical schema drift")
		}
		asOf, err := parseProviderTime(item.BeginsAt)
		if err != nil || *item.Low <= 0 || *item.High < *item.Low || *item.Open < *item.Low ||
			*item.Open > *item.High || *item.Close < *item.Low || *item.Close > *item.High || *item.Volume < 0 {
			return nil, p.schemaError("historical schema drift")
		}
		if len(out) > 0 && !asOf.After(out[len(out)-1].AsOf) {
			return nil, p.schemaError("historical ordering drift")
		}
		out = append(out, Bar{
			Symbol: symbol, Open: *item.Open, High: *item.High, Low: *item.Low, Close: *item.Close,
			Volume: *item.Volume, Source: robinhoodSource, AsOf: asOf,
		})
	}
	return out, nil
}

func (p *RobinhoodProvider) Movers(ctx context.Context, direction string, n int) ([]Mover, error) {
	if n < 1 || n > MaxMovers || (direction != "up" && direction != "down") {
		return nil, fmt.Errorf("mover request exceeds provider contract")
	}
	_ = ctx
	return nil, p.dataError("movers capability is not documented by the provider")
}

func (p *RobinhoodProvider) Hours(ctx context.Context) (MarketHours, error) {
	_ = ctx
	return MarketHours{}, p.dataError("market hours capability is not documented by the provider")
}

func (p *RobinhoodProvider) ProviderStatus() Status {
	status := p.status.Status()
	return Status{
		Connected: status.Connected, Source: robinhoodSource,
		SnapshotVersion: p.snapshotVersion, LastSuccessfulRead: status.LastSuccessfulRead,
		LastError: status.LastError, SchemaDrift: status.SchemaDrift,
	}
}

var _ Provider = (*RobinhoodProvider)(nil)
var _ StatusProvider = (*RobinhoodProvider)(nil)
