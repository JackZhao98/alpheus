package marketdata

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/rhmcp"
	"alpheus/kernel/internal/units"
)

// TestRobinhoodLiveReadContract is intentionally env-gated. It exercises only
// the committed read allowlist and never discovers or invokes a mutation tool.
func TestRobinhoodLiveReadContract(t *testing.T) {
	if os.Getenv("RH_MCP_INTEGRATION") != "1" {
		t.Skip("set RH_MCP_INTEGRATION=1 for the authenticated read-only contract")
	}
	tokenFile := os.Getenv("RH_MCP_TOKEN_FILE")
	bindingFile := os.Getenv("LIVE_ACCOUNT_ID_FILE")
	if tokenFile == "" || bindingFile == "" {
		t.Fatal("RH_MCP_TOKEN_FILE and LIVE_ACCOUNT_ID_FILE are required")
	}
	rawAccountID, err := os.ReadFile(bindingFile)
	if err != nil {
		t.Fatal("read account binding")
	}
	accountID := strings.TrimSpace(string(rawAccountID))
	if accountID == "" {
		t.Fatal("account binding is empty")
	}
	client, err := rhmcp.New(rhmcp.Config{
		TokenFile: tokenFile, AllowedTools: RobinhoodReadTools,
		CallTimeout: 30 * time.Second, ConnectWait: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	account, err := broker.NewRobinhood(client, accountID)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewRobinhoodProvider(client, client, "live-contract")
	if err != nil {
		t.Fatal(err)
	}

	runReadContract(t, account, provider)
	if _, err := provider.Bars(context.Background(), "SPY", 7); err != nil {
		t.Fatalf("bars: %v", err)
	}
	equity, err := provider.Instrument(context.Background(), "SPY")
	if err != nil || equity.Kind != "equity" || equity.Multiplier != 1 ||
		equity.InstrumentID == "" || equity.PriceTick != units.MustMicros("0.01") ||
		equity.BelowPriceTick != units.MustMicros("0.0001") ||
		equity.TickCutoff != units.MustMicros("1") || equity.QtyIncrement != units.MustQty("1") {
		t.Fatalf("equity instrument contract failed: instrument=%+v err=%v", equity, err)
	}
	expirations, err := provider.Expirations(context.Background(), "SPY")
	if err != nil || len(expirations) == 0 {
		t.Fatalf("expirations=%d err=%v", len(expirations), err)
	}
	stableDate := time.Now().UTC().AddDate(0, 0, 2).Format(time.DateOnly)
	expiry := ""
	for _, candidate := range expirations {
		if candidate >= stableDate {
			expiry = candidate
			break
		}
	}
	if expiry == "" {
		t.Fatal("no stable expiration available")
	}
	chain, err := provider.Chain(context.Background(), "SPY", expiry, units.MustPercent("0.5"))
	if err != nil || len(chain) == 0 {
		t.Fatalf("chain=%d err=%v", len(chain), err)
	}
	instrument, err := provider.Instrument(context.Background(), chain[0].Instrument.InstrumentID)
	if err != nil || instrument.InstrumentID != chain[0].Instrument.InstrumentID || instrument.Multiplier != 100 {
		t.Fatalf("instrument contract failed: %v", err)
	}
}

func TestRobinhoodLiveEquityInstrumentContract(t *testing.T) {
	if os.Getenv("RH_MCP_INTEGRATION") != "1" {
		t.Skip("set RH_MCP_INTEGRATION=1 for the authenticated read-only contract")
	}
	tokenFile := os.Getenv("RH_MCP_TOKEN_FILE")
	if tokenFile == "" {
		t.Fatal("RH_MCP_TOKEN_FILE is required")
	}
	client, err := rhmcp.New(rhmcp.Config{
		TokenFile: tokenFile, AllowedTools: RobinhoodReadTools,
		CallTimeout: 30 * time.Second, ConnectWait: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	provider, err := NewRobinhoodProvider(client, client, "live-equity-contract")
	if err != nil {
		t.Fatal(err)
	}
	equity, err := provider.Instrument(context.Background(), "SPY")
	if err != nil || equity.Kind != "equity" || equity.Multiplier != 1 ||
		equity.InstrumentID != "8f92e76f-1e0e-4478-8580-16a6ffcfaef5" ||
		equity.PriceTick != units.MustMicros("0.01") ||
		equity.BelowPriceTick != units.MustMicros("0.0001") ||
		equity.TickCutoff != units.MustMicros("1") || equity.QtyIncrement != units.MustQty("1") {
		t.Fatalf("equity instrument contract failed: instrument=%+v err=%v", equity, err)
	}
}
