package broker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fixtureCaller map[string]json.RawMessage

func (f fixtureCaller) Call(_ context.Context, tool string, _ map[string]any) (json.RawMessage, error) {
	return f[tool], nil
}

func accountFixture(accounts string) json.RawMessage {
	return json.RawMessage(`{"data":{"accounts":` + accounts + `},"guide":"fixture"}`)
}

func validAccount(number string) string {
	return `{"account_number":"` + number + `","rhs_account_number":"rhs-` + number + `","type":"cash","brokerage_account_type":"individual","is_default":false,"agentic_allowed":true,"option_level":"option_level_2","state":"active","deactivated":false,"permanently_deactivated":false}`
}

func portfolioFixture(total string) json.RawMessage {
	return json.RawMessage(`{"data":{"total_value":"` + total + `","equity_value":"0","options_value":"0","futures_value":"0","event_contracts_value":"0","crypto_value":"0","cash":"` + total + `","pending_deposits":"0","mutual_funds_value":"0","fixed_income_value":"0","currency":"USD","buying_power":{"buying_power":"` + total + `","unleveraged_buying_power":"` + total + `","display_currency":"USD"}},"guide":"fixture"}`)
}

func TestRobinhoodAccountMatchesExactAllowlistAndRedactsID(t *testing.T) {
	provider, err := NewRobinhood(fixtureCaller{
		"get_accounts":  accountFixture(`[` + validAccount("other") + `,` + validAccount("wanted") + `]`),
		"get_portfolio": portfolioFixture("401.16"),
	}, "wanted")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := any(provider).(ExecutionProvider); ok {
		t.Fatal("production read provider unexpectedly exposes execution capability")
	}
	account, err := provider.Account(context.Background())
	if err != nil || account.ExternalID != "wanted" || account.Source != robinhoodSource {
		t.Fatalf("account=%+v err=%v", account, err)
	}
	raw, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "wanted") || strings.Contains(string(raw), "account_number") {
		t.Fatalf("account id leaked in API JSON: %s", raw)
	}
}

func TestRobinhoodAccountFailsClosedOnAmbiguityAndPrecisionDrift(t *testing.T) {
	for name, caller := range map[string]fixtureCaller{
		"ambiguous": {
			"get_accounts": accountFixture(`[` + validAccount("wanted") + `,` + validAccount("wanted") + `]`),
		},
		"missing_account_field": {
			"get_accounts": accountFixture(`[{"account_number":"wanted"}]`),
		},
		"portfolio_precision": {
			"get_accounts":  accountFixture(`[` + validAccount("wanted") + `]`),
			"get_portfolio": json.RawMessage(`{"data":{"total_value":"1.0000001"},"guide":"fixture"}`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			provider, err := NewRobinhood(caller, "wanted")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.Account(context.Background()); err == nil {
				t.Fatal("schema/account drift was accepted")
			}
		})
	}
}

func TestRobinhoodPositionsRequireStableIDsAndKnownMultiplier(t *testing.T) {
	accounts := accountFixture(`[` + validAccount("wanted") + `]`)
	for name, caller := range map[string]fixtureCaller{
		"missing_equity_id": {
			"get_accounts":         accounts,
			"get_equity_positions": json.RawMessage(`{"data":{"positions":[{"instrument_id":"instrument","symbol":"SPY","quantity":"1","average_buy_price":"1"}]},"guide":"fixture"}`),
		},
		"unknown_option_multiplier": {
			"get_accounts":         accounts,
			"get_equity_positions": json.RawMessage(`{"data":{"positions":[]},"guide":"fixture"}`),
			"get_option_positions": json.RawMessage(`{"data":{"positions":[{"id":"position","option_id":"option","symbol":"SPY-C","type":"long","quantity":"1","average_price":"1","trade_value_multiplier":"99"}]},"guide":"fixture"}`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			provider, err := NewRobinhood(caller, "wanted")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.Positions(context.Background()); err == nil {
				t.Fatal("invalid production position was accepted")
			}
		})
	}
}
