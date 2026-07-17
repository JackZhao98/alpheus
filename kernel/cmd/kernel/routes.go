package main

import (
	"net/http"

	"alpheus/kernel/internal/config"
)

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /limits", s.authorize(permissionRead, s.getLimits))
	mux.HandleFunc("GET /state", s.authorize(permissionRead, s.getState))
	mux.HandleFunc("GET /operations/{id}", s.authorize(permissionRead, s.getOperation))
	mux.HandleFunc("GET /lessons", s.authorize(permissionRead, s.getLessons))
	mux.HandleFunc("GET /blackboard/{day}", s.authorize(permissionRead, s.getBlackboard))

	if s.tradingMode() == config.ModeReadOnly {
		mux.HandleFunc("POST /operations", methodNotAllowed)
		mux.HandleFunc("POST /operations/{id}/review", methodNotAllowed)
		mux.HandleFunc("POST /journal", methodNotAllowed)
		mux.HandleFunc("PUT /blackboard/{day}", methodNotAllowed)
		mux.HandleFunc("POST /halt", methodNotAllowed)
		return mux
	}

	mux.HandleFunc("POST /operations", s.authorize(permissionRuntime, s.propose))
	mux.HandleFunc("POST /operations/{id}/review", s.authorize(permissionAdmin, s.review))
	mux.HandleFunc("POST /journal", s.authorize(permissionRuntime, s.postJournal))
	mux.HandleFunc("PUT /blackboard/{day}", s.authorize(permissionRuntime, s.putBlackboard))
	mux.HandleFunc("POST /halt", s.authorize(permissionAdmin, s.postHalt))
	if s.tradingMode() == config.ModeSim {
		mux.HandleFunc("POST /sim/quote", s.authorize(permissionAdmin, s.simQuote))
	}
	return mux
}

func methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "read-only mode"})
}
