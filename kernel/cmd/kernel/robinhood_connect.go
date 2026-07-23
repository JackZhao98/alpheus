package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/marketdata"
	"alpheus/kernel/internal/rhmcp"
	"alpheus/kernel/internal/store"
)

const robinhoodMCPSecretName = "robinhood_mcp"

type robinhoodOAuthFlowStore interface {
	CreateRobinhoodOAuthFlow(store.RobinhoodOAuthFlow) error
	ConsumeRobinhoodOAuthFlow(stateDigest string) (*store.RobinhoodOAuthFlow, error)
}

type robinhoodConnection struct {
	Version        int              `json:"version"`
	Token          rhmcp.OAuthToken `json:"token"`
	BoundAccountID string           `json:"bound_account_id,omitempty"`
}

func (c robinhoodConnection) valid() bool {
	return c.Version == 1 && c.Token.Version == 1 && strings.TrimSpace(c.Token.AccessToken) != "" && strings.TrimSpace(c.Token.ClientID) != ""
}

func robinhoodStateDigest(state string) string {
	digest := sha256.Sum256([]byte(state))
	return hex.EncodeToString(digest[:])
}

func (s *server) robinhoodFlowStore() (robinhoodOAuthFlowStore, bool) {
	flows, ok := s.store.(robinhoodOAuthFlowStore)
	return flows, ok
}

func (s *server) loadRobinhoodConnection() (robinhoodConnection, error) {
	raw, err := s.loadAgentSecret(robinhoodMCPSecretName)
	if err != nil {
		return robinhoodConnection{}, err
	}
	var connection robinhoodConnection
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&connection); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !connection.valid() {
		return robinhoodConnection{}, fmt.Errorf("Robinhood connection is invalid")
	}
	return connection, nil
}

func (s *server) saveRobinhoodConnection(connection robinhoodConnection) error {
	if !connection.valid() {
		return fmt.Errorf("Robinhood connection is invalid")
	}
	connection.BoundAccountID = strings.TrimSpace(connection.BoundAccountID)
	raw, err := json.Marshal(connection)
	if err != nil {
		return fmt.Errorf("encode Robinhood connection")
	}
	ciphertext, err := sealAgentSecret(s.mode.AgentWebSessionKey, robinhoodMCPSecretName, string(raw))
	if err != nil {
		return err
	}
	return s.store.PutAgentSecret(robinhoodMCPSecretName, ciphertext)
}

// databaseRobinhoodTokenStore keeps token refreshes in the encrypted database
// record while retaining the separately-confirmed account binding.
type databaseRobinhoodTokenStore struct {
	server *server
	mu     sync.Mutex
}

func (t *databaseRobinhoodTokenStore) LoadOAuthToken() (rhmcp.OAuthToken, error) {
	if t == nil || t.server == nil {
		return rhmcp.OAuthToken{}, fmt.Errorf("Robinhood connection is unavailable")
	}
	connection, err := t.server.loadRobinhoodConnection()
	if err != nil {
		return rhmcp.OAuthToken{}, err
	}
	return connection.Token, nil
}

func (t *databaseRobinhoodTokenStore) SaveOAuthToken(token rhmcp.OAuthToken) error {
	if t == nil || t.server == nil {
		return fmt.Errorf("Robinhood connection is unavailable")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	connection, err := t.server.loadRobinhoodConnection()
	if err != nil {
		return err
	}
	connection.Token = token
	return t.server.saveRobinhoodConnection(connection)
}

func (s *server) robinhoodTokenStore() *databaseRobinhoodTokenStore {
	return &databaseRobinhoodTokenStore{server: s}
}

func (s *server) robinhoodCallbackURL() string {
	return strings.TrimRight(s.consoleOrigin, "/") + "/agent/robinhood/callback"
}

func (s *server) postRobinhoodConnect(w http.ResponseWriter, r *http.Request) {
	if !s.robinhoodEnabled {
		writeError(w, http.StatusConflict, "robinhood_not_enabled", "Kernel is not configured for Robinhood")
		return
	}
	flows, ok := s.robinhoodFlowStore()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "robinhood_connection_store_unavailable", "connection store unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	start, err := s.beginRobinhoodAuthorization(ctx, s.robinhoodCallbackURL())
	if err != nil {
		writeInternalError(w, "start Robinhood authorization", err)
		return
	}
	flowID := store.NewID()
	verifier, err := sealAgentSecret(s.mode.AgentWebSessionKey, "robinhood_oauth_flow:"+flowID, start.Verifier)
	if err != nil {
		writeInternalError(w, "encrypt Robinhood OAuth verifier", err)
		return
	}
	if err := flows.CreateRobinhoodOAuthFlow(store.RobinhoodOAuthFlow{
		ID: flowID, StateDigest: robinhoodStateDigest(start.State), VerifierCiphertext: verifier,
		ClientID: start.ClientID, RedirectURI: start.RedirectURI, Subject: authenticatedSubject(r),
		ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	}); err != nil {
		writeStoreError(w, "store Robinhood OAuth flow", err)
		return
	}
	s.store.Event("robinhood_connect_started", map[string]string{"subject": authenticatedSubject(r)})
	writeJSON(w, http.StatusOK, map[string]string{"authorization_url": start.AuthorizationURL})
}

func (s *server) getRobinhoodCallback(w http.ResponseWriter, r *http.Request) {
	if !s.robinhoodEnabled || r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" || r.URL.Query().Get("state") == "" {
		s.renderRobinhoodCallback(w, false)
		return
	}
	flows, ok := s.robinhoodFlowStore()
	if !ok {
		s.renderRobinhoodCallback(w, false)
		return
	}
	flow, err := flows.ConsumeRobinhoodOAuthFlow(robinhoodStateDigest(r.URL.Query().Get("state")))
	if err != nil || flow == nil {
		if err != nil {
			writeInternalError(w, "consume Robinhood OAuth flow", err)
			return
		}
		s.renderRobinhoodCallback(w, false)
		return
	}
	verifier, err := openAgentSecret(s.mode.AgentWebSessionKey, "robinhood_oauth_flow:"+flow.ID, flow.VerifierCiphertext)
	if err != nil {
		writeInternalError(w, "decrypt Robinhood OAuth verifier", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	token, err := s.exchangeRobinhoodAuthorization(ctx, flow.ClientID, r.URL.Query().Get("code"), verifier, flow.RedirectURI)
	if err != nil {
		s.store.Event("robinhood_connect_failed", map[string]string{"subject": flow.Subject})
		s.renderRobinhoodCallback(w, false)
		return
	}
	connection := robinhoodConnection{Version: 1, Token: token}
	// Reauthorization refreshes credentials but never silently changes an
	// explicitly confirmed account. Changing the bound account requires an
	// equally explicit reset/bind workflow.
	if existing, loadErr := s.loadRobinhoodConnection(); loadErr == nil {
		connection.BoundAccountID = existing.BoundAccountID
	}
	if err := s.saveRobinhoodConnection(connection); err != nil {
		writeInternalError(w, "store Robinhood connection", err)
		return
	}
	s.store.Event("robinhood_connect_completed", map[string]string{"subject": flow.Subject})
	s.renderRobinhoodCallback(w, true)
}

func (s *server) renderRobinhoodCallback(w http.ResponseWriter, connected bool) {
	state := "failed"
	message := "Robinhood connection was not completed. Return to Agent Lab and try again."
	if connected {
		state = "connected"
		message = "Robinhood connected. Return to Agent Lab to select the account explicitly."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = fmt.Fprintf(w, "<!doctype html><meta http-equiv=\"refresh\" content=\"0;url=/agent-lab?robinhood=%s\"><title>Robinhood connection</title><p>%s</p>", state, html.EscapeString(message))
}

func (s *server) getRobinhoodConnection(w http.ResponseWriter, _ *http.Request) {
	if !s.robinhoodEnabled {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "status": "unavailable"})
		return
	}
	connection, err := s.loadRobinhoodConnection()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "status": "disconnected"})
		return
	}
	bound := strings.TrimSpace(connection.BoundAccountID)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "status": map[bool]string{true: "connected", false: "needs_account"}[bound != ""],
		"bound": bound != "", "account": maskedAccountID(bound), "provider_ready": s.robinhoodProviderReady(),
	})
}

// getRobinhoodCapabilities exposes only secret-free schemas for the committed
// read allowlist. It exists so operators can review provider drift without
// exporting OAuth material or enabling a generic Tool call surface.
func (s *server) getRobinhoodCapabilities(w http.ResponseWriter, r *http.Request) {
	if !s.robinhoodEnabled {
		writeError(w, http.StatusConflict, "robinhood_not_enabled", "Kernel is not configured for Robinhood")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.brokerTimeout)
	defer cancel()
	tools, err := s.discoverRobinhoodCapabilities(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "robinhood_capabilities_unavailable", "Robinhood capability discovery is unavailable")
		return
	}
	allowed := make(map[string]struct{}, len(rhmcp.SafeQueryTools))
	for _, name := range rhmcp.SafeQueryTools {
		allowed[name] = struct{}{}
	}
	safe := make([]rhmcp.ToolSchema, 0, len(allowed))
	for _, tool := range tools {
		if _, ok := allowed[tool.Name]; ok {
			safe = append(safe, tool)
		}
	}
	if len(safe) != len(allowed) {
		writeError(w, http.StatusBadGateway, "robinhood_capabilities_incomplete", "Robinhood read capability discovery is incomplete")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": "robinhood-mcp", "generated_at": time.Now().UTC(), "tools": safe,
	})
}

func (s *server) discoverRobinhoodCapabilities(ctx context.Context) ([]rhmcp.ToolSchema, error) {
	if s.robinhoodDiscover != nil {
		return s.robinhoodDiscover(ctx)
	}
	if _, err := s.loadRobinhoodConnection(); err != nil {
		return nil, err
	}
	client, err := rhmcp.New(rhmcp.Config{TokenStore: s.robinhoodTokenStore(), AllowedTools: rhmcp.SafeQueryTools})
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.Discover(ctx)
}

func (s *server) getRobinhoodAccounts(w http.ResponseWriter, r *http.Request) {
	if !s.robinhoodEnabled {
		writeError(w, http.StatusConflict, "robinhood_not_enabled", "Kernel is not configured for Robinhood")
		return
	}
	if _, err := s.loadRobinhoodConnection(); err != nil {
		writeError(w, http.StatusConflict, "robinhood_not_connected", "connect Robinhood first")
		return
	}
	client, err := rhmcp.New(rhmcp.Config{TokenStore: s.robinhoodTokenStore(), AllowedTools: rhmcp.SafeQueryTools})
	if err != nil {
		writeInternalError(w, "open Robinhood account reader", err)
		return
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(r.Context(), s.brokerTimeout)
	defer cancel()
	accounts, err := broker.RobinhoodAccountChoices(ctx, client)
	if err != nil {
		writeError(w, http.StatusBadGateway, "robinhood_accounts_unavailable", "Robinhood account list is unavailable; reconnect and retry")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

func (s *server) postRobinhoodBind(w http.ResponseWriter, r *http.Request) {
	if !s.robinhoodEnabled {
		writeError(w, http.StatusConflict, "robinhood_not_enabled", "Kernel is not configured for Robinhood")
		return
	}
	var input struct {
		MaskedAccount string `json:"masked_account"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	input.MaskedAccount = strings.TrimSpace(input.MaskedAccount)
	if !strings.HasPrefix(input.MaskedAccount, "••••") || len([]rune(input.MaskedAccount)) != 8 {
		writeError(w, http.StatusBadRequest, "robinhood_account_invalid", "select an account returned by Robinhood")
		return
	}
	connection, err := s.loadRobinhoodConnection()
	if err != nil {
		writeError(w, http.StatusConflict, "robinhood_not_connected", "connect Robinhood first")
		return
	}
	client, err := rhmcp.New(rhmcp.Config{TokenStore: s.robinhoodTokenStore(), AllowedTools: rhmcp.SafeQueryTools})
	if err != nil {
		writeInternalError(w, "open Robinhood account reader", err)
		return
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(r.Context(), s.brokerTimeout)
	defer cancel()
	accounts, err := broker.RobinhoodAccountChoices(ctx, client)
	if err != nil {
		writeError(w, http.StatusBadGateway, "robinhood_accounts_unavailable", "Robinhood account list is unavailable; reconnect and retry")
		return
	}
	var selected *broker.RobinhoodAccountChoice
	for index := range accounts {
		choice := &accounts[index]
		if choice.MaskedAccount != input.MaskedAccount {
			continue
		}
		if selected != nil {
			writeError(w, http.StatusConflict, "robinhood_account_ambiguous", "more than one account has that suffix; connection is unchanged")
			return
		}
		selected = choice
	}
	if selected == nil || !selected.AgenticAllowed || selected.State != "active" || selected.Deactivated || selected.PermanentlyDeactivated {
		writeError(w, http.StatusBadRequest, "robinhood_account_not_eligible", "choose an active Agentic Trading account")
		return
	}
	connection.BoundAccountID = selected.AccountNumber
	if err := s.saveRobinhoodConnection(connection); err != nil {
		writeInternalError(w, "save Robinhood account binding", err)
		return
	}
	if err := s.activateRobinhood(context.Background()); err != nil {
		s.store.Event("robinhood_account_binding_pending", map[string]string{"subject": authenticatedSubject(r)})
		writeError(w, http.StatusBadGateway, "robinhood_provider_unavailable", "account saved, but provider is unavailable; retry shortly")
		return
	}
	if err := s.activateM3AIfNeeded(context.Background()); err != nil {
		s.store.Event("robinhood_account_binding_pending", map[string]string{"subject": authenticatedSubject(r)})
		writeError(w, http.StatusBadGateway, "robinhood_baseline_unavailable", "account saved, but the initial account baseline is unavailable; retry shortly")
		return
	}
	s.store.Event("robinhood_account_bound", map[string]string{"subject": authenticatedSubject(r)})
	writeJSON(w, http.StatusOK, map[string]any{"bound": true, "account": selected.MaskedAccount, "provider_ready": true})
}

func (s *server) beginRobinhoodAuthorization(ctx context.Context, redirectURI string) (rhmcp.AuthorizationStart, error) {
	if s.robinhoodBegin != nil {
		return s.robinhoodBegin(ctx, redirectURI)
	}
	return rhmcp.BeginAuthorization(ctx, redirectURI, nil, rhmcp.DefaultOAuthEndpoints())
}

func (s *server) exchangeRobinhoodAuthorization(ctx context.Context, clientID, code, verifier, redirectURI string) (rhmcp.OAuthToken, error) {
	if s.robinhoodExchange != nil {
		return s.robinhoodExchange(ctx, clientID, code, verifier, redirectURI)
	}
	return rhmcp.ExchangeAuthorizationCode(ctx, clientID, code, verifier, redirectURI, nil, rhmcp.DefaultOAuthEndpoints())
}

func (s *server) robinhoodProviderReady() bool {
	s.providerMu.RLock()
	defer s.providerMu.RUnlock()
	return s.account != nil && s.mcpLab != nil
}

func (s *server) boundRobinhoodAccountID() string {
	s.providerMu.RLock()
	bound := s.robinhoodAccountID
	s.providerMu.RUnlock()
	if bound != "" {
		return bound
	}
	// Unit-test servers deliberately bypass the production Robinhood connection
	// path and provide this in-memory seam. It is never populated by env/file.
	return s.mode.LiveAccountID
}

// activateRobinhood builds only the read capability in read-only mode. Live
// execution remains guarded by the existing explicit live/canary boundaries.
func (s *server) activateRobinhood(ctx context.Context) error {
	if !s.robinhoodEnabled {
		return fmt.Errorf("Robinhood is not enabled")
	}
	connection, err := s.loadRobinhoodConnection()
	if err != nil {
		return err
	}
	if strings.TrimSpace(connection.BoundAccountID) == "" {
		return fmt.Errorf("Robinhood account selection is required")
	}
	client, err := rhmcp.New(rhmcp.Config{TokenStore: s.robinhoodTokenStore(), AllowedTools: rhmcp.SafeQueryTools})
	if err != nil {
		return err
	}
	closeClient := true
	defer func() {
		if closeClient {
			_ = client.Close()
		}
	}()
	if err := rhmcp.ValidateSnapshot(ctx, client, s.robinhoodSnapshot, rhmcp.SafeQueryTools); err != nil {
		return fmt.Errorf("capability snapshot validation failed: %w", err)
	}
	readAccount, err := broker.NewRobinhood(client, connection.BoundAccountID)
	if err != nil {
		return err
	}
	authorityClient, err := rhmcp.NewAuthorityClient(client)
	if err != nil {
		return err
	}
	authorityAccount, err := broker.NewRobinhood(authorityClient, connection.BoundAccountID)
	if err != nil {
		return err
	}
	readLab, err := newMCPReadLab(client, connection.BoundAccountID, s.robinhoodSnapshot)
	if err != nil {
		return err
	}
	readMarket, err := marketdata.NewRobinhoodProvider(client, client, s.robinhoodSnapshot.Version)
	if err != nil {
		return err
	}
	authorityMarket, err := marketdata.NewRobinhoodProvider(authorityClient, client, s.robinhoodSnapshot.Version)
	if err != nil {
		return err
	}
	var execution broker.ExecutionProvider
	if s.mode.TradingMode == config.ModeLive {
		mutation, mutationErr := rhmcp.NewMutation(rhmcp.Config{TokenStore: s.robinhoodTokenStore()}, connection.BoundAccountID)
		if mutationErr != nil {
			return mutationErr
		}
		execution, err = broker.NewRobinhoodExecution(authorityAccount, mutation, authorityMarket)
		if err != nil {
			_ = mutation.Close()
			return err
		}
	}
	bindingCtx, cancel := context.WithTimeout(ctx, s.brokerTimeout)
	actual, bindingErr := readAccount.AccountID(bindingCtx)
	cancel()
	if bindingErr != nil || actual != connection.BoundAccountID {
		return fmt.Errorf("account binding failed")
	}
	s.providerMu.Lock()
	s.account = readAccount
	s.authorityAccount = authorityAccount
	s.execution = execution
	s.market = readMarket
	s.authorityMarket = authorityMarket
	s.mcpLab = readLab
	s.robinhoodAccountID = connection.BoundAccountID
	s.providerMu.Unlock()
	closeClient = false
	return nil
}

func (s *server) activateM3AIfNeeded(ctx context.Context) error {
	active, err := s.store.FeatureActive("m3a")
	if err != nil {
		return fmt.Errorf("M3A activation marker: %w", err)
	}
	if active {
		return nil
	}
	account := s.accountProvider()
	if account == nil {
		return fmt.Errorf("M3A activation: account provider unavailable")
	}
	activationCtx, cancel := context.WithTimeout(ctx, s.brokerTimeout)
	accountSnapshot, accountErr := account.Account(activationCtx)
	cancel()
	if accountErr != nil || !accountSnapshot.EquityKnown {
		return fmt.Errorf("M3A activation: account snapshot unavailable")
	}
	activationCtx, cancel = context.WithTimeout(ctx, s.brokerTimeout)
	positionsSnapshot, positionsErr := account.Positions(activationCtx)
	cancel()
	if positionsErr != nil {
		return fmt.Errorf("M3A activation: position snapshot unavailable")
	}
	positions := make([]store.ActivationPosition, 0, len(positionsSnapshot))
	for _, position := range positionsSnapshot {
		positions = append(positions, store.ActivationPosition{Symbol: position.Symbol, Kind: position.Kind, Multiplier: position.Multiplier, Qty: position.Qty})
	}
	if err := s.store.ActivateM3A(store.M3AActivationSnapshot{Equity: accountSnapshot.Equity, BuyingPower: accountSnapshot.BuyingPower, Positions: positions}); err != nil {
		return fmt.Errorf("M3A activation: %w", err)
	}
	return nil
}
