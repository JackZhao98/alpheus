package config

import (
	"fmt"
	"os"
	"strings"
)

const (
	ModeSim      = "sim"
	ModeShadow   = "shadow"
	ModeReadOnly = "read_only"
	ModeLive     = "live"
)

// ModeConfig owns process-level safety boundaries. Secret/binding fields are
// explicitly excluded from JSON/YAML so an accidental marshal cannot turn the
// config into an API, event, or persistence leak.
type ModeConfig struct {
	TradingMode        string
	RuntimeToken       string `json:"-" yaml:"-"`
	AdminToken         string `json:"-" yaml:"-"`
	KernelToken        string `json:"-" yaml:"-"`
	LiveTradingEnabled bool
	LiveAccountID      string `json:"-" yaml:"-"`
}

func LoadModeConfig() (ModeConfig, error) {
	mode := strings.TrimSpace(Env("TRADING_MODE", ModeSim))
	liveAccountID, err := loadLiveAccountID()
	if err != nil {
		return ModeConfig{}, err
	}
	cfg := ModeConfig{
		TradingMode:        mode,
		RuntimeToken:       osValue("RUNTIME_TOKEN"),
		AdminToken:         osValue("ADMIN_TOKEN"),
		KernelToken:        osValue("KERNEL_TOKEN"),
		LiveTradingEnabled: strings.EqualFold(strings.TrimSpace(osValue("LIVE_TRADING_ENABLED")), "true"),
		LiveAccountID:      liveAccountID,
	}
	if err := cfg.Validate(); err != nil {
		return ModeConfig{}, err
	}
	return cfg, nil
}

func loadLiveAccountID() (string, error) {
	direct := strings.TrimSpace(osValue("LIVE_ACCOUNT_ID"))
	path := strings.TrimSpace(osValue("LIVE_ACCOUNT_ID_FILE"))
	if direct != "" && path != "" {
		return "", fmt.Errorf("set only one of LIVE_ACCOUNT_ID or LIVE_ACCOUNT_ID_FILE")
	}
	if path == "" {
		return direct, nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() < 1 || info.Size() > 256 {
		return "", fmt.Errorf("LIVE_ACCOUNT_ID_FILE must be a private regular file with mode 0600")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read LIVE_ACCOUNT_ID_FILE")
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, "\r\n\t ") {
		return "", fmt.Errorf("LIVE_ACCOUNT_ID_FILE is invalid")
	}
	return value, nil
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
