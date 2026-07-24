package main

import (
	"net/http"
	"time"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/store"
)

const agentConsoleOperationLimit = 20

type agentConsoleEnvironment struct {
	Selected         string `json:"selected"`
	DataScope        string `json:"data_scope"`
	KernelMode       string `json:"kernel_mode"`
	PaperAvailable   bool   `json:"paper_available"`
	LiveAvailable    bool   `json:"live_available"`
	ExecutionEnabled bool   `json:"execution_enabled"`
}

type agentConsoleAutonomy struct {
	Selected  string   `json:"selected"`
	Available []string `json:"available"`
}

type agentConsolePortfolio struct {
	Available bool      `json:"available"`
	ErrorCode string    `json:"error_code,omitempty"`
	Account   any       `json:"account,omitempty"`
	Positions any       `json:"positions,omitempty"`
	Orders    any       `json:"open_orders,omitempty"`
	AsOf      time.Time `json:"as_of,omitempty"`
	Source    string    `json:"source,omitempty"`
}

type agentConsoleActivity struct {
	Available  bool                 `json:"available"`
	ErrorCode  string               `json:"error_code,omitempty"`
	Operations []store.OperationRow `json:"operations"`
}

func (s *server) agentConsoleEnvironment() agentConsoleEnvironment {
	mode := s.tradingMode()
	paper := mode == config.ModeSim || mode == config.ModeShadow
	live := s.robinhoodEnabled
	selected := "paper"
	dataScope := "paper"
	if live {
		selected = "live"
		dataScope = "live"
	}
	return agentConsoleEnvironment{
		Selected:         selected,
		DataScope:        dataScope,
		KernelMode:       mode,
		PaperAvailable:   paper,
		LiveAvailable:    live,
		ExecutionEnabled: mode == config.ModeLive || mode == config.ModeSim || mode == config.ModeShadow,
	}
}

func (s *server) getAgentConsoleSnapshot(w http.ResponseWriter, r *http.Request) {
	portfolio := agentConsolePortfolio{}
	if snapshot, err := s.captureProviderSnapshot(r.Context(), "read_model"); err != nil {
		portfolio.ErrorCode = "portfolio_unavailable"
	} else {
		portfolio.Available = true
		portfolio.Account = snapshot.Account
		portfolio.Positions = snapshot.Positions
		portfolio.Orders = snapshot.Orders
		portfolio.AsOf = snapshot.Observation.CompletedAt
		portfolio.Source = snapshot.Observation.Source
	}

	activity := agentConsoleActivity{Operations: []store.OperationRow{}}
	if operations, err := s.store.ListOperations("", agentConsoleOperationLimit, nil); err != nil {
		activity.ErrorCode = "operations_unavailable"
	} else {
		activity.Available = true
		activity.Operations = operations
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"environment": s.agentConsoleEnvironment(),
		"autonomy": agentConsoleAutonomy{
			Selected: "observe", Available: []string{"observe"},
		},
		"portfolio": portfolio,
		"activity":  activity,
		"triggers": map[string]any{
			"available": false,
			"items":     []any{},
			"reason":    "trigger_registry_not_installed",
		},
		"generated_at": time.Now().UTC(),
		"source":       "kernel_console_projection",
	})
}
