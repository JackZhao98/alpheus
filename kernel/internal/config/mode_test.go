package config

import "testing"

func validNonSim(mode string) ModeConfig {
	return ModeConfig{
		TradingMode: mode, RuntimeToken: "runtime", AdminToken: "admin",
		KernelToken: "kernel", LiveTradingEnabled: true, LiveAccountID: "account",
	}
}

func TestModeConfigFailClosed(t *testing.T) {
	tests := []struct {
		name string
		cfg  ModeConfig
	}{
		{"unknown mode", ModeConfig{TradingMode: "paper"}},
		{"live missing admin", ModeConfig{TradingMode: ModeLive, RuntimeToken: "runtime", KernelToken: "kernel", LiveTradingEnabled: true, LiveAccountID: "account"}},
		{"live disabled", func() ModeConfig { c := validNonSim(ModeLive); c.LiveTradingEnabled = false; return c }()},
		{"live missing account", func() ModeConfig { c := validNonSim(ModeLive); c.LiveAccountID = ""; return c }()},
		{"tokens overlap", func() ModeConfig { c := validNonSim(ModeShadow); c.AdminToken = c.RuntimeToken; return c }()},
		{"agent password without key", ModeConfig{TradingMode: ModeSim, AgentWebPassword: "long-enough-password"}},
		{"agent password too short", ModeConfig{TradingMode: ModeSim, AgentWebPassword: "short", AgentWebSessionKey: "12345678901234567890123456789012"}},
		{"unknown agent web auth mode", ModeConfig{TradingMode: ModeSim, AgentWebAuthMode: "none"}},
		{"local agent web missing key", ModeConfig{TradingMode: ModeSim, AgentWebAuthMode: AgentWebAuthLocal}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); err == nil {
				t.Fatalf("config=%+v passed validation", tc.cfg)
			}
		})
	}
}

func TestModeConfigAcceptsSafeModes(t *testing.T) {
	if err := (ModeConfig{TradingMode: ModeSim}).Validate(); err != nil {
		t.Fatalf("sim: %v", err)
	}
	if err := (ModeConfig{TradingMode: ModeSim, AgentWebPassword: "long-enough-password", AgentWebSessionKey: "12345678901234567890123456789012"}).Validate(); err != nil {
		t.Fatalf("agent web config: %v", err)
	}
	if err := (ModeConfig{TradingMode: ModeSim, AgentWebAuthMode: AgentWebAuthLocal, AgentWebSessionKey: "12345678901234567890123456789012"}).Validate(); err != nil {
		t.Fatalf("local agent web config: %v", err)
	}
	for _, mode := range []string{ModeShadow, ModeReadOnly, ModeLive} {
		if err := validNonSim(mode).Validate(); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
	}
}

func TestModeBrokerBoundary(t *testing.T) {
	if err := (ModeConfig{TradingMode: ModeSim}).ValidateBroker("robinhood"); err == nil {
		t.Fatal("sim accepted a production broker")
	}
	if err := validNonSim(ModeLive).ValidateBroker("fake"); err == nil {
		t.Fatal("live accepted the fake broker")
	}
}
