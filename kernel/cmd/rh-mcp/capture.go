package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/marketdata"
	"alpheus/kernel/internal/rhmcp"
)

type privateCapture struct {
	name string
	args map[string]any
}

func captureReadFixtures(ctx context.Context, tokenFile, bindingFile, privateDir string) error {
	accountID, err := loadPrivateValue(bindingFile)
	if err != nil {
		return err
	}
	client, err := rhmcp.New(rhmcp.Config{TokenFile: tokenFile, AllowedTools: marketdata.RobinhoodReadTools})
	if err != nil {
		return err
	}
	defer client.Close()

	choices, err := broker.RobinhoodAccountChoices(ctx, client)
	if err != nil {
		return err
	}
	var selected *broker.RobinhoodAccountChoice
	for i := range choices {
		if choices[i].AccountNumber == accountID {
			if selected != nil {
				return fmt.Errorf("account binding is ambiguous")
			}
			selected = &choices[i]
		}
	}
	if selected == nil || !selected.AgenticAllowed || selected.State != "active" || selected.Deactivated || selected.PermanentlyDeactivated {
		return fmt.Errorf("bound account is not an active agentic account")
	}

	start := time.Now().UTC().AddDate(0, 0, -7).Format(time.RFC3339)
	captures := []privateCapture{
		{name: "accounts", args: map[string]any{}},
		{name: "portfolio", args: map[string]any{"account_number": selected.AccountNumber}},
		{name: "equity_positions", args: map[string]any{"account_number": selected.AccountNumber}},
		{name: "option_positions", args: map[string]any{"account_number": selected.AccountNumber, "nonzero": true}},
		{name: "equity_orders", args: map[string]any{"account_number": selected.AccountNumber}},
		{name: "option_orders", args: map[string]any{"account_number": selected.AccountNumber}},
		{name: "realized_pnl", args: map[string]any{
			"account_number": selected.RHSAccountNumber,
			"span":           "year",
			"timezone":       "America/New_York",
			// The published schema says this can be omitted, but the live service
			// currently rejects an unspecified asset class. Be explicit so the
			// discovery fixture covers the complete brokerage result.
			"asset_classes": []string{"equity", "option", "crypto"},
		}},
		{name: "pnl_trade_history", args: map[string]any{"account_number": selected.RHSAccountNumber, "span": "month"}},
		{name: "equity_quotes", args: map[string]any{"symbols": []string{"SPY"}}},
		{name: "equity_tradability", args: map[string]any{"account_number": selected.AccountNumber, "symbols": []string{"SPY"}}},
		{name: "equity_price_book", args: map[string]any{"symbols": []string{"SPY"}}},
		{name: "equity_historicals", args: map[string]any{"symbols": []string{"SPY"}, "start_time": start, "interval": "day", "bounds": "regular"}},
		{name: "option_chains", args: map[string]any{"underlying_symbol": "SPY"}},
	}
	toolNames := map[string]string{
		"accounts": "get_accounts", "portfolio": "get_portfolio",
		"equity_positions": "get_equity_positions", "option_positions": "get_option_positions",
		"equity_orders": "get_equity_orders", "option_orders": "get_option_orders",
		"realized_pnl": "get_realized_pnl", "pnl_trade_history": "get_pnl_trade_history",
		"equity_quotes": "get_equity_quotes", "equity_tradability": "get_equity_tradability",
		"equity_price_book": "get_equity_price_book", "equity_historicals": "get_equity_historicals",
		"option_chains": "get_option_chains",
	}
	var chainRaw []byte
	for _, capture := range captures {
		raw, err := client.Call(ctx, toolNames[capture.name], capture.args)
		if err != nil {
			return fmt.Errorf("capture %s failed", capture.name)
		}
		if err := savePrivateBytes(filepath.Join(privateDir, capture.name+".json"), append(raw, '\n')); err != nil {
			return err
		}
		if capture.name == "option_chains" {
			chainRaw = raw
		}
	}

	chainID, expiry, err := selectFixtureChain(chainRaw, time.Now().UTC())
	if err != nil {
		return err
	}
	instrumentsRaw, err := client.Call(ctx, "get_option_instruments", map[string]any{
		"chain_id": chainID, "expiration_dates": expiry, "state": "active", "tradability": "tradable", "type": "call",
	})
	if err != nil {
		return fmt.Errorf("capture option instruments failed")
	}
	if err := savePrivateBytes(filepath.Join(privateDir, "option_instruments.json"), append(instrumentsRaw, '\n')); err != nil {
		return err
	}
	instrumentID, err := selectFixtureInstrument(instrumentsRaw)
	if err != nil {
		return err
	}
	quoteRaw, err := client.Call(ctx, "get_option_quotes", map[string]any{"instrument_ids": []string{instrumentID}})
	if err != nil {
		return fmt.Errorf("capture option quote failed")
	}
	if err := savePrivateBytes(filepath.Join(privateDir, "option_quotes.json"), append(quoteRaw, '\n')); err != nil {
		return err
	}
	fmt.Printf("captured 15 private read-only fixtures for %s\n", selected.MaskedAccount)
	return nil
}

func selectFixtureChain(raw []byte, now time.Time) (string, string, error) {
	var data struct {
		Chains []struct {
			ID              string   `json:"id"`
			Symbol          string   `json:"symbol"`
			CanOpenPosition bool     `json:"can_open_position"`
			ExpirationDates []string `json:"expiration_dates"`
		} `json:"chains"`
	}
	if err := rhmcp.DecodeData(raw, &data); err != nil {
		return "", "", err
	}
	// Avoid same/next-day contracts because the provider may already have
	// disabled openings around its sellout window even while the chain still
	// advertises the expiration.
	earliestStableExpiry := now.AddDate(0, 0, 2).Format(time.DateOnly)
	for _, chain := range data.Chains {
		if chain.ID == "" || chain.Symbol != "SPY" || !chain.CanOpenPosition {
			continue
		}
		sort.Strings(chain.ExpirationDates)
		for _, expiry := range chain.ExpirationDates {
			if expiry >= earliestStableExpiry {
				return chain.ID, expiry, nil
			}
		}
	}
	return "", "", fmt.Errorf("no active SPY option chain fixture available")
}

func selectFixtureInstrument(raw []byte) (string, error) {
	var data struct {
		Instruments []struct {
			ID          string `json:"id"`
			State       string `json:"state"`
			Tradability string `json:"tradability"`
		} `json:"instruments"`
	}
	if err := rhmcp.DecodeData(raw, &data); err != nil {
		return "", err
	}
	for _, instrument := range data.Instruments {
		if instrument.ID != "" && instrument.State == "active" && instrument.Tradability == "tradable" {
			return instrument.ID, nil
		}
	}
	return "", fmt.Errorf("no active tradable SPY option fixture available")
}
