package config

import (
	"fmt"
	"strings"
)

const (
	ModeSim      = "sim"
	ModeShadow   = "shadow"
	ModeReadOnly = "read_only"
	ModeLive     = "live"
)

// ModeConfig owns process-level safety boundaries. Tokens deliberately have
// no JSON/YAML tags: they must never become API, event, or persistence data.
type ModeConfig struct {
	TradingMode        string
	RuntimeToken       string
	AdminToken         string
	KernelToken        string
	LiveTradingEnabled bool
	LiveAccountID      string
}

func LoadModeConfig() (ModeConfig, error) {
	mode := strings.TrimSpace(Env("TRADING_MODE", ModeSim))
	cfg := ModeConfig{
		TradingMode:        mode,
		RuntimeToken:       osValue("RUNTIME_TOKEN"),
		AdminToken:         osValue("ADMIN_TOKEN"),
		KernelToken:        osValue("KERNEL_TOKEN"),
		LiveTradingEnabled: strings.EqualFold(strings.TrimSpace(osValue("LIVE_TRADING_ENABLED")), "true"),
		LiveAccountID:      strings.TrimSpace(osValue("LIVE_ACCOUNT_ID")),
	}
	if err := cfg.Validate(); err != nil {
		return ModeConfig{}, err
	}
	return cfg, nil
}

func osValue(key string) string {
	return strings.TrimSpace(Env(key, ""))
}

func (c ModeConfig) Validate() error {
	switch c.TradingMode {
	case ModeSim, ModeShadow, ModeReadOnly, ModeLive:
	default:
		return fmt.Errorf("TRADING_MODE must be sim, shadow, read_only, or live")
	}

	if c.TradingMode != ModeSim {
		missing := make([]string, 0, 3)
		if c.RuntimeToken == "" {
			missing = append(missing, "RUNTIME_TOKEN")
		}
		if c.AdminToken == "" {
			missing = append(missing, "ADMIN_TOKEN")
		}
		if c.KernelToken == "" {
			missing = append(missing, "KERNEL_TOKEN")
		}
		if len(missing) > 0 {
			return fmt.Errorf("%s required outside sim mode", strings.Join(missing, ", "))
		}
		if c.RuntimeToken == c.AdminToken || c.RuntimeToken == c.KernelToken || c.AdminToken == c.KernelToken {
			return fmt.Errorf("RUNTIME_TOKEN, ADMIN_TOKEN, and KERNEL_TOKEN must be distinct")
		}
	}

	if c.TradingMode == ModeLive {
		if !c.LiveTradingEnabled {
			return fmt.Errorf("LIVE_TRADING_ENABLED must be true in live mode")
		}
		if c.LiveAccountID == "" {
			return fmt.Errorf("LIVE_ACCOUNT_ID required in live mode")
		}
	}
	return nil
}

func (c ModeConfig) ValidateBroker(brokerName string) error {
	brokerName = strings.TrimSpace(brokerName)
	if brokerName == "" {
		brokerName = "fake"
	}
	if c.TradingMode == ModeSim && brokerName != "fake" {
		return fmt.Errorf("sim mode requires BROKER=fake")
	}
	if c.TradingMode == ModeLive && brokerName == "fake" {
		return fmt.Errorf("live mode refuses BROKER=fake")
	}
	return nil
}
