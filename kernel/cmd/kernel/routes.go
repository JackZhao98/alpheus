package main

import (
	"net/http"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
)

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", serveCockpitFile("index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /cockpit", serveCockpitFile("index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /assets/cockpit.css", serveCockpitFile("style.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /assets/cockpit.js", serveCockpitFile("app.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("GET /agent-lab", serveCockpitFile("agent-lab.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /assets/agent-lab.css", serveCockpitFile("agent-lab.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /assets/agent-lab.js", serveCockpitFile("agent-lab.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("GET /gex", serveCockpitFile("gex.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /assets/gex.css", serveCockpitFile("gex.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /assets/gex.js", serveCockpitFile("gex.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("GET /gex/data", s.getPublicGEXDashboard)
	mux.HandleFunc("POST /agent/auth/login", s.postAgentLogin)
	mux.HandleFunc("GET /agent/auth/session", s.getAgentSession)
	mux.HandleFunc("POST /agent/auth/logout", s.postAgentLogout)
	mux.HandleFunc("GET /agent/secrets", s.authorizeAgentWeb(s.getAgentSecrets))
	mux.HandleFunc("PUT /agent/secrets/{name}", s.authorizeAgentWeb(s.putAgentSecret))
	mux.HandleFunc("DELETE /agent/secrets/{name}", s.authorizeAgentWeb(s.deleteAgentSecret))
	mux.HandleFunc("POST /agent/gexbot/test", s.authorizeAgentWeb(s.postGEXBotTest))
	mux.HandleFunc("GET /agent/robinhood/connection", s.authorizeAgentWeb(s.getRobinhoodConnection))
	mux.HandleFunc("GET /agent/robinhood/capabilities", s.authorizeAgentWeb(s.getRobinhoodCapabilities))
	mux.HandleFunc("POST /agent/robinhood/connect", s.authorizeAgentWeb(s.postRobinhoodConnect))
	// OAuth returns from Robinhood without the Agent Lab session cookie in some
	// browser configurations; the one-time PKCE state is the callback proof.
	mux.HandleFunc("GET /agent/robinhood/callback", s.getRobinhoodCallback)
	mux.HandleFunc("GET /agent/robinhood/accounts", s.authorizeAgentWeb(s.getRobinhoodAccounts))
	mux.HandleFunc("POST /agent/robinhood/bind", s.authorizeAgentWeb(s.postRobinhoodBind))
	mux.HandleFunc("GET /limits", s.authorize(permissionRead, s.getLimits))
	mux.HandleFunc("GET /auth/capabilities", s.authorize(permissionRead, s.getAuthCapabilities))
	mux.HandleFunc("GET /state", s.authorize(permissionRead, s.getState))
	mux.HandleFunc("GET /operations", s.authorize(permissionRead, s.listOperations))
	mux.HandleFunc("GET /operations/{id}", s.authorize(permissionRead, s.getOperation))
	mux.HandleFunc("GET /control/warnings", s.authorize(permissionRead, s.getControlWarnings))
	mux.HandleFunc("GET /lessons", s.authorize(permissionRead, s.getLessons))
	mux.HandleFunc("GET /blackboard/{day}", s.authorize(permissionRead, s.getBlackboard))
	mux.HandleFunc("GET /market/quote/{symbol}", s.authorize(permissionRead, s.getMarketQuote))
	mux.HandleFunc("GET /market/authority-quote/{symbol}", s.authorize(permissionRead, s.getAuthorityMarketQuote))
	mux.HandleFunc("GET /market/chain/{underlying}", s.authorize(permissionRead, s.getMarketChain))
	mux.HandleFunc("GET /market/expirations/{underlying}", s.authorize(permissionRead, s.getMarketExpirations))
	mux.HandleFunc("GET /market/bars/{symbol}", s.authorize(permissionRead, s.getMarketBars))
	mux.HandleFunc("GET /market/movers", s.authorize(permissionRead, s.getMarketMovers))
	mux.HandleFunc("GET /market/hours", s.authorize(permissionRead, s.getMarketHours))
	mux.HandleFunc("GET /research/news/{symbol}", s.authorize(permissionRead, s.getResearchNews))
	mux.HandleFunc("GET /research/search", s.authorize(permissionRead, s.getResearchWebSearch))
	mux.HandleFunc("GET /research/fetch", s.authorize(permissionRead, s.getResearchWebFetch))
	mux.HandleFunc("GET /provider/status", s.authorize(permissionRead, s.getProviderStatus))
	mux.HandleFunc("GET /mcp/read-tools", s.authorize(permissionRead, s.getMCPReadTools))
	mux.HandleFunc("POST /mcp/read-query", s.authorize(permissionRead, s.postMCPReadQuery))
	// This is a deliberately narrow internal fact bridge, not a second generic
	// MCP endpoint. Cortex Input may request published earnings facts only.
	mux.HandleFunc("POST /internal/v1/cortex-tools/earnings-results", s.postCortexEarningsResults)
	// The reviewed read registry may name only a server-side allowlisted Tool
	// and arguments. Kernel still injects the bound account and blocks all MCP
	// mutation tools.
	mux.HandleFunc("POST /internal/v1/cortex-tools/read", s.postCortexKernelRead)
	// Historical agent_query_job rows remain readable for audit/migration, but
	// the legacy write path is retired. New requests enter Cortex directly.
	mux.HandleFunc("POST /agent/query", s.authorizeAgentWeb(legacyAgentQueryGone))
	mux.HandleFunc("GET /agent/query-jobs/{id}", s.authorizeAgentWeb(s.getAgentQueryJob))
	mux.HandleFunc("POST /agent/cortex-requests", s.authorizeAgentWeb(s.postCortexRequest))
	mux.HandleFunc("GET /agent/cortex-runs/{id}", s.authorizeAgentWeb(s.getCortexRun))
	mux.HandleFunc("POST /agent/cortex-runs/{id}/cancel", s.authorizeAgentWeb(s.postCortexRunCancellation))
	mux.HandleFunc("GET /agent/cortex-conversations/{id}", s.authorizeAgentWeb(s.getCortexConversation))
	mux.HandleFunc("GET /agent/cortex-operations", s.authorizeAgentWeb(s.getCortexOperations))
	mux.HandleFunc("POST /agent/room-requests", s.authorizeAgentWeb(s.postAgentRoomRequest))
	mux.HandleFunc("GET /agent/rooms", s.authorizeAgentWeb(s.getAgentRooms))
	mux.HandleFunc("GET /agent/rooms/{id}", s.authorizeAgentWeb(s.getAgentRoom))
	mux.HandleFunc("PATCH /agent/rooms/{id}", s.authorizeAgentWeb(s.patchAgentRoom))

	if s.tradingMode() == config.ModeReadOnly {
		mux.HandleFunc("POST /operations", methodNotAllowed)
		mux.HandleFunc("POST /operations/{id}/review", methodNotAllowed)
		mux.HandleFunc("POST /execution-attempts/{id}/adopt-candidate", methodNotAllowed)
		mux.HandleFunc("POST /journal", methodNotAllowed)
		mux.HandleFunc("PUT /blackboard/{day}", methodNotAllowed)
		mux.HandleFunc("POST /telemetry", methodNotAllowed)
		mux.HandleFunc("POST /halt", methodNotAllowed)
		mux.HandleFunc("POST /halt/resume", methodNotAllowed)
		mux.HandleFunc("POST /breaker/resume", methodNotAllowed)
		return mux
	}

	mux.HandleFunc("POST /operations", s.authorize(permissionRuntime, s.propose))
	mux.HandleFunc("POST /operations/{id}/review", s.authorize(permissionAdmin, s.requireConsoleOrigin(s.review)))
	mux.HandleFunc("POST /execution-attempts/{id}/adopt-candidate", s.authorize(permissionAdmin, s.requireConsoleOrigin(s.adoptExecutionCandidate)))
	mux.HandleFunc("POST /journal", s.authorize(permissionRuntime, s.postJournal))
	mux.HandleFunc("PUT /blackboard/{day}", s.authorize(permissionRuntime, s.putBlackboard))
	mux.HandleFunc("POST /telemetry", s.authorize(permissionRuntime, s.postTelemetry))
	mux.HandleFunc("POST /halt", s.authorize(permissionAdmin, s.requireConsoleOrigin(s.postHalt)))
	mux.HandleFunc("POST /halt/resume", s.authorize(permissionAdmin, s.requireConsoleOrigin(s.postHaltResume)))
	mux.HandleFunc("POST /breaker/resume", s.authorize(permissionAdmin, s.requireConsoleOrigin(s.postBreakerResume)))
	if s.tradingMode() == config.ModeSim || s.tradingMode() == config.ModeShadow {
		if _, fakeBroker := s.broker.(*broker.Fake); s.simVenue != nil || fakeBroker {
			mux.HandleFunc("POST /sim/quote", s.authorize(permissionAdmin, s.requireConsoleOrigin(s.simQuote)))
		}
	}
	return mux
}

func methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "read-only mode"})
}
