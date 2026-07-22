package config

import (
	"fmt"
	"strings"
)

const (
	ModeSim              = "sim"
	ModeShadow           = "shadow"
	ModeReadOnly         = "read_only"
	ModeLive             = "live"
	AgentWebAuthPassword = "password"
	AgentWebAuthLocal    = "local"
)

// ModeConfig owns process-level safety boundaries. Secret/binding fields are
// explicitly excluded from JSON/YAML so an accidental marshal cannot turn the
// config into an API, event, or persistence leak.
type ModeConfig struct {
	TradingMode        string
	RuntimeToken       string `json:"-" yaml:"-"`
	AdminToken         string `json:"-" yaml:"-"`
	KernelToken        string `json:"-" yaml:"-"`
	AgentWebPassword   string `json:"-" yaml:"-"`
	AgentWebSessionKey string `json:"-" yaml:"-"`
	AgentWebAuthMode   string `json:"-" yaml:"-"`
	LiveTradingEnabled bool
	// LiveAccountID is retained only as an in-process test seam. LoadModeConfig
	// never reads it from an environment variable or file; production account
	// binding is loaded from Kernel's encrypted database connection record.
	LiveAccountID string `json:"-" yaml:"-"`
}

func LoadModeConfig() (ModeConfig, error) {
	mode := strings.TrimSpace(Env("TRADING_MODE", ModeSim))
	cfg := ModeConfig{
		TradingMode:        mode,
		RuntimeToken:       osValue("RUNTIME_TOKEN"),
		AdminToken:         osValue("ADMIN_TOKEN"),
		KernelToken:        osValue("KERNEL_TOKEN"),
		AgentWebPassword:   osValue("AGENT_WEB_PASSWORD"),
		AgentWebSessionKey: osValue("AGENT_WEB_SESSION_KEY"),
		AgentWebAuthMode:   strings.TrimSpace(Env("AGENT_WEB_AUTH_MODE", AgentWebAuthPassword)),
		LiveTradingEnabled: strings.EqualFold(strings.TrimSpace(osValue("LIVE_TRADING_ENABLED")), "true"),
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
	}
	authMode := c.AgentWebAuthMode
	if authMode == "" {
		authMode = AgentWebAuthPassword
	}
	if authMode != AgentWebAuthPassword && authMode != AgentWebAuthLocal {
		return fmt.Errorf("AGENT_WEB_AUTH_MODE must be password or local")
	}
	if authMode == AgentWebAuthPassword && (c.AgentWebPassword == "") != (c.AgentWebSessionKey == "") {
		return fmt.Errorf("AGENT_WEB_PASSWORD and AGENT_WEB_SESSION_KEY must be set together")
	}
	if authMode == AgentWebAuthLocal && c.AgentWebSessionKey == "" {
		return fmt.Errorf("AGENT_WEB_SESSION_KEY is required for local Agent Lab access")
	}
	if c.AgentWebPassword != "" && len(c.AgentWebPassword) < 12 {
		return fmt.Errorf("AGENT_WEB_PASSWORD must contain at least 12 bytes")
	}
	if c.AgentWebSessionKey != "" && len(c.AgentWebSessionKey) < 32 {
		return fmt.Errorf("AGENT_WEB_SESSION_KEY must contain at least 32 bytes")
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
