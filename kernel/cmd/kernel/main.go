// alpheus kernel — the ONLY surface agents ever see. Broker credentials
// live below this line; prompts live above it; neither crosses.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/marketdata"
	"alpheus/kernel/internal/rhmcp"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

type server struct {
	limits    config.Limits
	mode      config.ModeConfig
	account   broker.AccountProvider
	execution broker.ExecutionProvider
	market    marketdata.Provider
	mcpLab    *mcpReadLab
	simVenue  *broker.Fake
	// broker is a simulation/test compatibility seam. Production construction
	// leaves it nil and registers read and execution capabilities separately.
	broker                 broker.Adapter
	store                  storeAPI
	instanceID             string
	brokerTimeout          time.Duration
	claimTimeout           time.Duration
	attemptStale           time.Duration
	providerDedupeVerified bool
	proposalTTL            time.Duration
	runtimeURL             string
	runtimeHTTP            *http.Client
	consoleOrigin          string
	haltMu                 sync.RWMutex
	halted                 bool
	haltReason             string
}

// storeAPI keeps the HTTP surface testable without adding a database-mocking
// dependency. The production implementation is *store.Store.
type storeAPI interface {
	CountTradesForDay(shadow bool, start, end time.Time) (int, error)
	CountTradesForDayExcluding(shadow bool, start, end time.Time, operationID string) (int, error)
	InsertEvent(kind string, payload any) error
	InsertEventWithID(kind string, payload any) (int64, error)
	FindOperationByIdempotency(subject, key string) (*store.OperationRow, error)
	WithProposalLock(identity *store.IdempotencyIdentity, shadow bool, marketDay *time.Time, fn func(store.OperationGate) error) error
	WithLedgerLock(shadow bool, marketDay time.Time, fn func(store.OperationGate) error) error
	WithReviewLock(id string, fn func(store.OperationGate, *store.OperationRow) error) error
	Event(kind string, payload any)
	SetOperationStatus(id, status string, verdict any) error
	GetOperation(id string) (*store.OperationRow, error)
	ListOperations(status string, limit int, cursor *store.OperationCursor) ([]store.OperationRow, error)
	ListControlWarnings(pendingBefore, claimBefore time.Time, limit int) ([]store.ControlWarning, error)
	InsertJournal(operationID string, hypothesis, outcome, promptVersions any, shadow bool) error
	TopLessons(limit int) ([]store.Lesson, error)
	GetBlackboard(day string) (json.RawMessage, error)
	PutBlackboard(day string, doc json.RawMessage) error
	LoadGlobalHalt() (bool, string, error)
	GetExecutionAttempt(id string) (*store.ExecutionAttempt, error)
	UpdatePendingAttemptLimit(id string, limit units.Micros) (bool, error)
	ClaimPendingAttempt(id, instance string) (*store.ExecutionAttempt, error)
	ClaimRecoverableAttempt(id, instance, expectedState string, expectedToken int, claimBefore time.Time) (*store.ExecutionAttempt, error)
	ListRecoverableAttempts(pendingBefore, claimBefore time.Time, limit int) ([]store.ExecutionAttempt, error)
	ResolveAttempt(id string, fencingToken int, resolution store.AttemptResolution) (bool, error)
	FailPendingAttempt(id, reason string) (bool, error)
	GetCloseReservation(id string) (*store.CloseReservation, error)
	GetOpenReservation(id string) (*store.OpenReservation, error)
	HasTradeGrant(operationID string) (bool, error)
	ListWorkingOrders(limit int) ([]store.Order, error)
	GetOrderByAttempt(attemptID string) (*store.Order, error)
	GetOrderByBrokerID(brokerOrderID string) (*store.Order, error)
	StageRepriceCancel(orderID string) (*store.ExecutionAttempt, error)
	FinalizeRepriceCancel(cancelAttemptID string, fencingToken int, update store.OrderUpdate, replacement *store.RepriceReplacement, policyReason string) (*store.ExecutionAttempt, error)
	ApplyOrderUpdate(update store.OrderUpdate) error
	ListTerminalReservationCandidates(limit int) ([]store.TerminalReservationCandidate, error)
	ReleaseProvenTerminalReservation(candidate store.TerminalReservationCandidate, provenFilledQty units.Qty, terminalProof bool) (bool, error)
	LedgerResources(ledger, excludeOperationID string) (store.LedgerResources, error)
	InsertDayOpen(marketDay time.Time, ledger string, equity units.Micros) error
	FeatureActive(name string) (bool, error)
}

type dayStateReader interface {
	CountTradesForDay(shadow bool, start, end time.Time) (int, error)
	LedgerResources(ledger, excludeOperationID string) (store.LedgerResources, error)
	InsertDayOpen(marketDay time.Time, ledger string, equity units.Micros) error
	EvaluateDayRisk(input store.DayRiskInput) (store.DayRiskStats, error)
}

func main() {
	mode, err := config.LoadModeConfig()
	if err != nil {
		log.Fatalf("mode config: %v", err)
	}
	if err := mode.ValidateBroker(config.Env("BROKER", "fake")); err != nil {
		log.Fatalf("mode config: %v", err)
	}
	consoleOrigin, err := normalizeConsoleOrigin(config.Env("CONSOLE_ORIGIN", "http://localhost:8100"))
	if err != nil {
		log.Fatalf("console origin: %v", err)
	}
	limits, err := config.LoadLimits()
	if err != nil {
		log.Fatalf("limits: %v", err)
	}
	proposalTTL, err := proposalLifetime(limits.ProposalTTLSec)
	if err != nil {
		log.Fatalf("limits: %v", err)
	}
	attemptConfig, err := loadAttemptConfig()
	if err != nil {
		log.Fatalf("execution config: %v", err)
	}
	brokerName := config.Env("BROKER", "fake")
	if err := validateProductionQuoteAge(brokerName, limits.QuoteMaxAgeSec); err != nil {
		log.Fatalf("limits: %v", err)
	}
	var account broker.AccountProvider
	var execution broker.ExecutionProvider
	var market marketdata.Provider
	var mcpLab *mcpReadLab
	var simVenue *broker.Fake
	switch brokerName {
	case "fake":
		simVenue = broker.NewFake(units.MustMicros("300"))
		account, execution = simVenue, simVenue
		market = marketdata.NewFakeProvider(simVenue)
	case "robinhood":
		if mode.TradingMode == config.ModeLive {
			log.Fatalf("broker: production execution is unavailable before M11")
		}
		if mode.LiveAccountID == "" {
			log.Fatalf("broker: LIVE_ACCOUNT_ID is required for Robinhood reads")
		}
		snapshot, err := rhmcp.LoadCapabilitySnapshot(config.Env("RH_MCP_CAPABILITIES_FILE", "../docs/rh_mcp_capabilities.json"))
		if err != nil {
			log.Fatalf("broker: %v", err)
		}
		client, err := rhmcp.New(rhmcp.Config{
			TokenFile: config.Env("RH_MCP_TOKEN_FILE", ""), AllowedTools: rhmcp.SafeQueryTools,
		})
		if err != nil {
			log.Fatalf("broker: %v", err)
		}
		if err := rhmcp.ValidateSnapshot(context.Background(), client, snapshot, rhmcp.SafeQueryTools); err != nil {
			_ = client.Close()
			log.Fatalf("broker: capability snapshot validation failed: %v", err)
		}
		account, err = broker.NewRobinhood(client, mode.LiveAccountID)
		if err != nil {
			_ = client.Close()
			log.Fatalf("broker: %v", err)
		}
		mcpLab, err = newMCPReadLab(client, mode.LiveAccountID, snapshot)
		if err != nil {
			_ = client.Close()
			log.Fatalf("broker: %v", err)
		}
		market, err = marketdata.NewRobinhoodProvider(client, client, snapshot.Version)
		if err != nil {
			_ = client.Close()
			log.Fatalf("broker: %v", err)
		}
	default:
		log.Fatalf("broker: unknown BROKER %q", brokerName)
	}
	dbTimeout, err := databaseTimeout()
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	st, err := store.Open(store.Config{
		URL:           config.Env("DATABASE_URL", "postgresql://alpheus:alpheus@localhost:5432/alpheus?sslmode=disable"),
		MigrationsDir: config.Env("MIGRATIONS_DIR", "../db/migrations"),
		Timeout:       dbTimeout,
		MarketTZ:      config.Env("TZ_MARKET", "America/New_York"),
	})
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	if brokerName == "robinhood" {
		bindingCtx, cancel := context.WithTimeout(context.Background(), attemptConfig.brokerTimeout)
		actual, bindingErr := account.AccountID(bindingCtx)
		cancel()
		if bindingErr != nil || actual != mode.LiveAccountID {
			st.Event("account_binding_violation", map[string]string{
				"reason": "read_provider_binding_failed", "mode": mode.TradingMode,
			})
			log.Fatalf("broker: account binding failed")
		}
	}
	m3aActive, err := st.FeatureActive("m3a")
	if err != nil {
		log.Fatalf("M3A activation marker: %v", err)
	}
	if !m3aActive {
		activationCtx, cancel := context.WithTimeout(context.Background(), attemptConfig.brokerTimeout)
		activationAccount, accountErr := account.Account(activationCtx)
		cancel()
		if accountErr != nil || !activationAccount.EquityKnown {
			log.Fatalf("M3A activation: account snapshot unavailable")
		}
		activationCtx, cancel = context.WithTimeout(context.Background(), attemptConfig.brokerTimeout)
		activationPositions, positionErr := account.Positions(activationCtx)
		cancel()
		if positionErr != nil {
			log.Fatalf("M3A activation: position snapshot unavailable")
		}
		positions := make([]store.ActivationPosition, 0, len(activationPositions))
		for _, position := range activationPositions {
			positions = append(positions, store.ActivationPosition{
				Symbol: position.Symbol, Kind: position.Kind,
				Multiplier: position.Multiplier, Qty: position.Qty,
			})
		}
		if err := st.ActivateM3A(store.M3AActivationSnapshot{
			Equity: activationAccount.Equity, BuyingPower: activationAccount.BuyingPower,
			Positions: positions,
		}); err != nil {
			log.Fatalf("M3A activation: %v", err)
		}
	}
	s := &server{
		limits: limits, mode: mode, account: account, execution: execution, market: market,
		mcpLab: mcpLab, simVenue: simVenue, broker: simVenue, store: st,
		instanceID: store.NewID(), brokerTimeout: attemptConfig.brokerTimeout,
		claimTimeout: attemptConfig.claimTimeout, attemptStale: attemptConfig.attemptStale,
		providerDedupeVerified: brokerName == "fake",
		proposalTTL:            proposalTTL,
		runtimeURL:             config.Env("RUNTIME_URL", "http://agent-runtime:8200"),
		consoleOrigin:          consoleOrigin,
	}
	if err := s.loadGlobalHalt(); err != nil {
		log.Fatalf("halt state: %v", err)
	}

	if _, err := startWatchdog(s); err != nil {
		log.Fatalf("watchdog: %v", err)
	}
	if err := startAttemptReconciler(s); err != nil {
		log.Fatalf("attempt reconciler startup: %v", err)
	}
	if err := startRepricer(s); err != nil {
		log.Fatalf("repricer startup: %v", err)
	}
	st.Event("kernel_start", map[string]string{
		"broker": os.Getenv("BROKER"), "profile": limits.Profile, "mode": mode.TradingMode,
	})

	log.Printf("alpheus-kernel listening on :8100 mode=%s", mode.TradingMode)
	log.Fatal(http.ListenAndServe(":8100", s.routes()))
}

func validateProductionQuoteAge(brokerName string, maxAgeSec int) error {
	if brokerName == "robinhood" && maxAgeSec <= 0 {
		return fmt.Errorf("quote_max_age_sec must be set to a positive human-approved value for Robinhood")
	}
	return nil
}

func databaseTimeout() (time.Duration, error) {
	raw := config.Env("DB_TIMEOUT_MS", "3000")
	milliseconds, err := strconv.Atoi(raw)
	if err != nil || milliseconds <= 0 {
		return 0, fmt.Errorf("DB_TIMEOUT_MS must be a positive integer")
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

type attemptTimingConfig struct {
	brokerTimeout time.Duration
	claimTimeout  time.Duration
	attemptStale  time.Duration
}

func loadAttemptConfig() (attemptTimingConfig, error) {
	brokerTimeout, err := durationFromMillis("BROKER_TIMEOUT_MS", 10_000)
	if err != nil {
		return attemptTimingConfig{}, err
	}
	claimTimeout, err := durationFromMillis("CLAIM_TIMEOUT_MS", 30_000)
	if err != nil {
		return attemptTimingConfig{}, err
	}
	attemptStale, err := durationFromMillis("ATTEMPT_STALE_MS", 3_000)
	if err != nil {
		return attemptTimingConfig{}, err
	}
	if claimTimeout <= brokerTimeout {
		return attemptTimingConfig{}, fmt.Errorf("CLAIM_TIMEOUT_MS must exceed BROKER_TIMEOUT_MS")
	}
	return attemptTimingConfig{brokerTimeout: brokerTimeout, claimTimeout: claimTimeout, attemptStale: attemptStale}, nil
}

func durationFromMillis(key string, fallback int) (time.Duration, error) {
	raw := config.Env(key, strconv.Itoa(fallback))
	milliseconds, err := strconv.ParseInt(raw, 10, 64)
	const maxDurationMillis = int64(1<<63-1) / int64(time.Millisecond)
	if err != nil || milliseconds <= 0 || milliseconds > maxDurationMillis {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func proposalLifetime(seconds int) (time.Duration, error) {
	const maxDurationSeconds = int64(1<<63-1) / int64(time.Second)
	if seconds <= 0 || int64(seconds) > maxDurationSeconds {
		return 0, fmt.Errorf("proposal_ttl_sec must be a positive integer")
	}
	return time.Duration(seconds) * time.Second, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

const maxJSONBodyBytes int64 = 1 << 20
const maxLessonsLimit = 100

// decodeJSONBody is the single boundary for every JSON write endpoint. It
// enforces media type, a bounded body, a strict schema, and exactly one JSON
// value before endpoint-specific validation or side effects can run.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content-type must be application/json"})
		return false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body exceeds 1 MiB"})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		}
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body exceeds 1 MiB"})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must contain exactly one JSON value"})
		}
		return false
	}
	return true
}

func writeInternalError(w http.ResponseWriter, context string, err error) {
	log.Printf("%s: %v", context, err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}

func writeStoreError(w http.ResponseWriter, context string, err error) {
	if errors.Is(err, store.ErrUnavailable) {
		log.Printf("%s: %v", context, err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database unavailable"})
		return
	}
	writeInternalError(w, context, err)
}

func validDate(day string) bool {
	parsed, err := time.Parse(time.DateOnly, day)
	return err == nil && parsed.Format(time.DateOnly) == day
}

func validUUID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	for i := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		c := id[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

type marketWindow struct {
	day        time.Time
	start, end time.Time
}

func currentMarketWindow() (marketWindow, error) {
	return marketDayWindow(time.Now(), config.Env("TZ_MARKET", "America/New_York"))
}

func marketDayWindow(now time.Time, tzName string) (marketWindow, error) {
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return marketWindow{}, fmt.Errorf("market timezone %q: %w", tzName, err)
	}
	localNow := now.In(loc)
	day := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	return marketWindow{day: day, start: day.UTC(), end: day.AddDate(0, 0, 1).UTC()}, nil
}

// spendableBuyingPower treats the account provider's buying_power as the
// authoritative amount the venue will currently permit, then reserves every
// locally entitled open that may not yet be reflected by the provider. A
// provider-visible resting order can therefore be counted twice; that is a
// conservative capacity reduction, never permission to overspend. M11 may add
// back a hold only after matching it to the same durable Alpheus order.
func spendableBuyingPower(authoritative, locallyHeld units.Micros) (units.Micros, error) {
	return units.Add(authoritative, -locallyHeld)
}

func (s *server) dayStateAtAccount(ctx context.Context, gate dayStateReader, shadow bool, account broker.AccountState, window marketWindow, halted bool, haltReason string) (risk.DayState, error) {
	n, err := gate.CountTradesForDay(shadow, window.start, window.end)
	if err != nil {
		return risk.DayState{}, err
	}
	ledger := ledgerName(shadow)
	resources, err := gate.LedgerResources(ledger, "")
	if err != nil {
		return risk.DayState{}, err
	}
	buyingPower, err := spendableBuyingPower(account.BuyingPower, resources.HeldCash)
	if err != nil {
		return risk.DayState{}, err
	}
	if err := gate.InsertDayOpen(window.day, ledger, account.Equity); err != nil {
		return risk.DayState{}, err
	}
	providerPnL, err := s.providerRealizedPnL(ctx, shadow, window.day)
	if err != nil {
		return risk.DayState{}, err
	}
	stats, err := gate.EvaluateDayRisk(store.DayRiskInput{
		Ledger: ledger, MarketDay: window.day, Start: window.start, End: window.end,
		ProviderRealizedPnL:     providerPnL,
		MaxDailyLossPct:         s.limits.HardLimits.MaxDailyLossPct,
		ConsecutiveLossDaysHalt: s.limits.HardLimits.ConsecutiveLossDaysHalt,
		PnLReconciliationLimit:  s.limits.PnLReconciliationTolerance,
	})
	if err != nil {
		return risk.DayState{}, err
	}
	if !halted && stats.Halted {
		halted, haltReason = true, stats.Reason
	}
	return risk.DayState{
		TradesToday: n, OpenRisk: resources.OpenRisk, Equity: account.Equity,
		EquityKnown: account.EquityKnown, BuyingPower: buyingPower,
		RealizedPnL: stats.EffectiveRealizedPnL, LocalRealizedPnL: stats.LocalRealizedPnL,
		ProviderRealizedPnL: stats.ProviderRealizedPnL, DailyLossLimit: stats.DailyLossLimit,
		ConsecutiveLossDays: stats.ConsecutiveLossDays,
		Halted:              halted, HaltReason: haltReason,
	}, nil
}

func (s *server) providerRealizedPnL(ctx context.Context, shadow bool, marketDay time.Time) (*units.Micros, error) {
	if shadow {
		return nil, nil
	}
	provider, ok := s.accountProvider().(broker.RealizedPnLProvider)
	if !ok {
		return nil, nil
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	snapshot, err := provider.RealizedPnL(providerCtx, marketDay, config.Env("TZ_MARKET", "America/New_York"))
	cancel()
	if err != nil || snapshot.AsOf.IsZero() {
		return nil, fmt.Errorf("%w: realized PnL", errBrokerDataUnavailable)
	}
	value := snapshot.Total
	return &value, nil
}

func (s *server) getLimits(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, s.limits) }

func (s *server) accountProvider() broker.AccountProvider {
	if s.account != nil {
		return s.account
	}
	return s.broker
}

func (s *server) executionProvider() broker.ExecutionProvider {
	if s.execution != nil {
		return s.execution
	}
	return s.broker
}

func (s *server) marketProvider() marketdata.Provider {
	if s.market != nil {
		return s.market
	}
	if fake, ok := s.broker.(*broker.Fake); ok {
		return marketdata.NewFakeProvider(fake)
	}
	return nil
}

func (s *server) getState(w http.ResponseWriter, r *http.Request) {
	if err := s.refreshGlobalHalt(); err != nil {
		writeStoreError(w, "refresh global halt", err)
		return
	}
	account := s.accountProvider()
	if account == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "account provider unavailable"})
		return
	}
	acct, err := account.Account(r.Context())
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "account data unavailable"})
		return
	}
	pos, err := account.Positions(r.Context())
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "position data unavailable"})
		return
	}
	window, err := currentMarketWindow()
	if err != nil {
		writeInternalError(w, "derive market day", err)
		return
	}
	orders, err := account.OpenOrders(r.Context())
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "order data unavailable"})
		return
	}
	fills, err := account.RecentFills(r.Context(), window.start)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "fill data unavailable"})
		return
	}
	halted, haltReason := s.haltSnapshot()
	var liveDay, shadowDay risk.DayState
	if err := s.store.WithLedgerLock(false, window.day, func(gate store.OperationGate) error {
		var err error
		liveDay, err = s.dayStateAtAccount(r.Context(), gate, false, acct, window, halted, haltReason)
		return err
	}); err != nil {
		writeStoreError(w, "get live day state", err)
		return
	}
	if err := s.store.WithLedgerLock(true, window.day, func(gate store.OperationGate) error {
		shadowAccount, err := s.shadowAccountSnapshot(r.Context(), gate)
		if err != nil {
			return err
		}
		shadowDay, err = s.dayStateAtAccount(r.Context(), gate, true, shadowAccount, window, halted, haltReason)
		return err
	}); err != nil {
		writeStoreError(w, "get shadow day state", err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"account":      acct,
		"positions":    pos,
		"open_orders":  orders,
		"recent_fills": fills,
		"day":          map[string]risk.DayState{"live": liveDay, "shadow": shadowDay},
		"mode":         s.tradingMode(),
		"source":       "kernel",
		"as_of":        time.Now().UTC(),
	})
}

func (s *server) propose(w http.ResponseWriter, r *http.Request) {
	var identity *store.IdempotencyIdentity
	if s.tradingMode() == config.ModeLive {
		key := r.Header.Get("Idempotency-Key")
		if err := validateIdempotencyKey(key); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		identity = &store.IdempotencyIdentity{Subject: authenticatedSubject(r), Key: key}
	}
	var request proposeRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	op, err := validateAndBuildOperation(request)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if identity != nil {
		identity.RequestHash, err = hashClientIntent(op)
		if err != nil {
			writeInternalError(w, "hash client intent", err)
			return
		}
	}
	if s.tradingMode() == config.ModeShadow {
		op.Shadow = true
	}
	window, err := currentMarketWindow()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	var quote *broker.Quote
	if op.Action == "open" || op.Action == "close" {
		sym := op.Symbol
		if sym == "" {
			sym = op.Underlying
		}
		quoteCtx, cancel := context.WithTimeout(r.Context(), s.brokerCallTimeout())
		q, quoteErr := s.marketProvider().Quote(quoteCtx, sym)
		cancel()
		if quoteErr == nil {
			quote = &q
		}
	}
	if op.Action == "open" {
		op = s.deriveOpenOperation(r.Context(), op, quote)
		if err := s.refreshGlobalHalt(); err != nil {
			writeStoreError(w, "refresh global halt", err)
			return
		}
	}
	var acct broker.AccountState
	opID := store.NewID()
	var halted bool
	var haltReason string
	if op.Action == "open" {
		// An open and a halt transition are linearized here. Once POST /halt
		// acquires the write lock, no later open can classify or reach a broker.
		s.haltMu.RLock()
		defer s.haltMu.RUnlock()
		halted, haltReason = s.halted, s.haltReason
	} else {
		halted, haltReason = s.haltSnapshot()
	}
	var v risk.Verdict
	status := "auto_approved"
	var replay *store.OperationRow
	var executionAttempt *store.ExecutionAttempt
	var committedProposalError error
	var marketDayLock *time.Time
	if op.Action == "open" {
		marketDayLock = &window.day
	}
	if err := s.store.WithProposalLock(identity, op.Shadow, marketDayLock, func(gate store.OperationGate) error {
		if identity != nil {
			existing, err := gate.FindOperationByIdempotency(identity.Subject, identity.Key)
			if err != nil {
				return err
			}
			if existing != nil {
				if !bytes.Equal(existing.RequestHash, identity.RequestHash[:]) {
					return errIdempotencyKeyReused
				}
				replay = existing
				return nil
			}
		}
		if op.Action == "open" {
			// M3A deliberately fetches account state after acquiring the stable
			// per-ledger gate. A prior fill cannot leave this proposal classifying
			// against a stale pre-lock snapshot.
			var err error
			if op.Shadow {
				acct, err = s.shadowAccountSnapshot(r.Context(), gate)
			} else {
				accountCtx, cancel := context.WithTimeout(r.Context(), s.brokerCallTimeout())
				acct, err = s.accountProvider().Account(accountCtx)
				cancel()
			}
			if err != nil {
				return fmt.Errorf("%w: account", errBrokerDataUnavailable)
			}
			// Derive the market day from an advancing database clock after the
			// bounded account read. PostgreSQL now() is transaction-start time and
			// would stamp the old day after a lock wait across midnight.
			databaseNow, err := gate.DatabaseNow()
			if err != nil {
				return err
			}
			window, err = marketDayWindow(databaseNow, config.Env("TZ_MARKET", "America/New_York"))
			if err != nil {
				return err
			}
		}
		if op.Action == "close" {
			ledger, symbol := ledgerName(op.Shadow), operationSymbol(op)
			if err := gate.LockLedgerSymbol(ledger, symbol); err != nil {
				return err
			}
			if op.Shadow {
				positions, err := gate.ShadowPositions()
				if err != nil {
					return err
				}
				var paper *store.ShadowPosition
				for i := range positions {
					if positions[i].Symbol == symbol {
						paper = &positions[i]
						break
					}
				}
				if paper == nil || paper.Qty <= 0 {
					return fmt.Errorf("%w: close requires an existing shadow position for %s", errInvalidClose, symbol)
				}
				if op.Kind != "" && op.Kind != paper.Kind {
					return fmt.Errorf("%w: close kind %q does not match shadow position kind %q", errInvalidClose, op.Kind, paper.Kind)
				}
				if paper.Kind == "option" && op.Qty%units.Qty(units.Scale) != 0 {
					return fmt.Errorf("%w: option qty must be a whole number of contracts", errInvalidClose)
				}
				exposureQty, err := gate.OpenExposureQuantity(ledger, symbol, paper.Kind)
				if err != nil {
					return err
				}
				if paper.Qty != exposureQty {
					if err := gate.InsertEvent("position_exposure_mismatch", map[string]any{
						"operation_id": opID, "ledger": ledger, "symbol": symbol, "paper_qty": paper.Qty,
						"exposure_qty": exposureQty,
					}); err != nil {
						return err
					}
				}
				held, err := gate.HeldCloseQuantity(ledger, symbol)
				if err != nil {
					return err
				}
				closable := minQty(paper.Qty, exposureQty)
				if held > closable || op.Qty > closable-held {
					committedProposalError = errInsufficientClosableQuantity
					return nil
				}
				op.Side, op.Kind, op.Multiplier, op.VerifiedReduction = "sell", paper.Kind, paper.Multiplier, true
			} else {
				brokerCtx, cancel := context.WithTimeout(r.Context(), s.brokerCallTimeout())
				positions, err := s.accountProvider().Positions(brokerCtx)
				cancel()
				if err != nil {
					return fmt.Errorf("%w: positions", errBrokerDataUnavailable)
				}
				positionQty, err := closablePositionQuantity(symbol, positions)
				if err != nil {
					return fmt.Errorf("%w: %v", errInvalidClose, err)
				}
				// Normalize metadata with a quantity that the broker position covers
				// so a position/exposure mismatch is recorded before the final
				// conservative min-quantity check rejects an oversized request.
				probe := op
				probe.Qty = minQty(op.Qty, positionQty)
				normalized, err := normalizeClose(probe, positions)
				if err != nil {
					return fmt.Errorf("%w: %v", errInvalidClose, err)
				}
				exposureQty, err := gate.OpenExposureQuantity(ledger, symbol, normalized.Kind)
				if err != nil {
					return err
				}
				if positionQty != exposureQty {
					if err := gate.InsertEvent("position_exposure_mismatch", map[string]any{
						"operation_id": opID, "ledger": ledger, "symbol": symbol, "broker_qty": positionQty,
						"exposure_qty": exposureQty,
					}); err != nil {
						return err
					}
				}
				held, err := gate.HeldCloseQuantity(ledger, symbol)
				if err != nil {
					return err
				}
				closable := minQty(positionQty, exposureQty)
				if held > closable || op.Qty > closable-held {
					committedProposalError = errInsufficientClosableQuantity
					return nil
				}
				normalized.Qty = op.Qty
				op = normalized
			}
			if op.ClosesOperationID != "" {
				if !validUUID(op.ClosesOperationID) {
					return fmt.Errorf("%w: closes_operation_id is not a UUID", errInvalidClose)
				}
				firstOperationID, err := gate.FirstOpenExposureOperation(ledger, symbol, op.Kind)
				if err != nil {
					return err
				}
				if firstOperationID == "" || firstOperationID != op.ClosesOperationID {
					return fmt.Errorf("%w: closes_operation_id does not match the first FIFO lot", errInvalidClose)
				}
			}
		}
		day := risk.DayState{}
		if op.Action == "open" {
			var err error
			day, err = s.dayStateAtAccount(r.Context(), gate, op.Shadow, acct, window, halted, haltReason)
			if err != nil {
				return err
			}
		}
		v = risk.Classify(op, s.limits, day, quote)
		if op.Action == "close" && !op.Shadow && (quote == nil || !quote.Usable(s.limits.QuoteMaxAgeSec, time.Now().UTC())) {
			v = risk.Verdict{Class: "REJECT", Reasons: []string{"market_data_unavailable"}}
		}
		if err := gate.InsertEvent("operation_proposed", map[string]any{"id": opID, "op": op, "verdict": v}); err != nil {
			return err
		}
		class := v.Class
		switch v.Class {
		case "REJECT":
			class, status = "C", "rejected"
		case "C":
			status = "pending_review"
		}
		if err := gate.InsertOperation(opID, op.Proposer, class, status, op, v, identity); err != nil {
			return err
		}
		if v.Class == "B" && op.Action == "open" {
			if err := gate.InsertTradeGrant(store.TradeGrant{
				OperationID: opID, Ledger: ledgerName(op.Shadow), MarketDay: window.day,
				AuthorizedRisk: op.DerivedMaxRisk, RiskSource: "computed",
			}); err != nil {
				return err
			}
			if err := gate.InsertEvent("trade_grant_created", map[string]any{
				"operation_id": opID, "ledger": ledgerName(op.Shadow),
			}); err != nil {
				return err
			}
		}
		if (v.Class != "A" && v.Class != "B") || op.Action == "tighten_stop" {
			return nil
		}
		attempt := store.ExecutionAttempt{
			ID: store.NewID(), OperationID: opID, Seq: 1, State: "pending",
		}
		switch op.Action {
		case "open", "close":
			limit, err := executionLimit(op, quote, s.limits.QuoteMaxAgeSec)
			if err != nil {
				return err
			}
			attempt.Intent = "place"
			attempt.ClientOrderID = store.NewID()
			if op.Shadow {
				attempt.Intent = "paper_place"
				attempt.ClientOrderID = "shadow:" + attempt.ID
				if quote == nil || !quote.Usable(s.limits.QuoteMaxAgeSec, time.Now().UTC()) {
					return fmt.Errorf("market_data_unavailable")
				}
				if op.Side == "buy" {
					limit = quote.Ask
					if op.Action == "open" && limit > op.ApprovedPriceCap {
						limit = op.ApprovedPriceCap
					}
				} else if op.Limit == nil {
					limit = quote.Bid
				}
			}
			attempt.Qty, attempt.Limit = op.Qty, limit
			if op.Action == "open" {
				reservation := store.OpenReservation{
					ID: store.NewID(), OperationID: opID, Ledger: ledgerName(op.Shadow),
					MarketDay: window.day, Symbol: operationSymbol(op), Kind: op.Kind,
					OriginalQty: op.Qty, RemainingQty: op.Qty,
					OriginalRisk: op.DerivedMaxRisk, RemainingRisk: op.DerivedMaxRisk,
					OriginalCash: op.RequiredCash, RemainingCash: op.RequiredCash,
					ResourceState: "held",
				}
				if err := gate.InsertOpenReservation(reservation); err != nil {
					return err
				}
				attempt.OpenReservationID = reservation.ID
				if err := gate.InsertEvent("open_reservation_created", map[string]any{
					"operation_id": opID, "reservation_id": reservation.ID,
					"ledger": reservation.Ledger, "symbol": reservation.Symbol,
				}); err != nil {
					return err
				}
			} else {
				reservation := store.CloseReservation{
					ID: store.NewID(), OperationID: opID, Ledger: ledgerName(op.Shadow),
					Symbol: operationSymbol(op), OriginalQty: op.Qty,
					RemainingQty: op.Qty, State: "held",
				}
				if err := gate.InsertCloseReservation(reservation); err != nil {
					return err
				}
				attempt.CloseReservationID = reservation.ID
				if err := gate.InsertEvent("close_reservation_created", map[string]any{
					"operation_id": opID, "reservation_id": reservation.ID,
					"symbol": reservation.Symbol,
				}); err != nil {
					return err
				}
			}
		case "cancel":
			attempt.Intent = "cancel"
			attempt.TargetBrokerOrderID = op.BrokerOrderID
		default:
			return nil
		}
		if err := gate.InsertExecutionAttempt(attempt); err != nil {
			return err
		}
		if attempt.Intent == "place" || attempt.Intent == "paper_place" {
			if err := gate.InsertOrder(store.Order{
				ID: store.NewID(), OperationID: opID, ExecutionAttemptID: attempt.ID,
				ClientOrderID: attempt.ClientOrderID, Ledger: ledgerName(op.Shadow),
				Symbol: operationSymbol(op), Side: op.Side, Kind: op.Kind,
				Multiplier: op.Multiplier, Qty: attempt.Qty, Limit: attempt.Limit,
				State: "new",
			}); err != nil {
				return err
			}
		}
		if err := gate.InsertEvent("execution_attempt_created", map[string]any{
			"operation_id": opID, "attempt_id": attempt.ID, "intent": attempt.Intent,
		}); err != nil {
			return err
		}
		executionAttempt = &attempt
		return nil
	}); err != nil {
		if errors.Is(err, errIdempotencyKeyReused) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "idempotency_key_reused"})
			return
		}
		if errors.Is(err, errInsufficientClosableQuantity) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "insufficient closable quantity"})
			return
		}
		if errors.Is(err, errBrokerDataUnavailable) {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "broker account or position data unavailable"})
			return
		}
		if errors.Is(err, errInvalidClose) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": strings.TrimPrefix(err.Error(), errInvalidClose.Error()+": ")})
			return
		}
		writeStoreError(w, "propose transaction", err)
		return
	}
	if errors.Is(committedProposalError, errInsufficientClosableQuantity) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "insufficient closable quantity"})
		return
	}
	if replay != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"operation_id": replay.ID, "status": replay.Status, "class": replay.Class,
			"idempotent_replay": true,
		})
		return
	}

	switch v.Class {
	case "REJECT":
		resp := map[string]any{"operation_id": opID, "status": "rejected", "class": v.Class, "reasons": v.Reasons}
		addRiskFacts(resp, op)
		writeJSON(w, 200, resp)
		return
	case "C":
		resp := map[string]any{"operation_id": opID, "status": "pending_review", "class": v.Class, "checks": v.Checks, "reasons": v.Reasons}
		addRiskFacts(resp, op)
		writeJSON(w, 200, resp)
		return
	}

	// Class A and B execute only through a durable execution attempt. Shadow
	// attempts settle through the atomic paper executor and never reach broker
	// execution capability.
	resp := map[string]any{"operation_id": opID, "status": status, "class": v.Class, "checks": v.Checks, "reasons": v.Reasons, "shadow": op.Shadow}
	addRiskFacts(resp, op)
	if op.Action == "tighten_stop" {
		execution, err := s.executeNonBroker(opID, op)
		if err != nil {
			if errors.Is(err, store.ErrUnavailable) {
				writeStoreError(w, "execute operation", err)
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"operation_id": opID, "status": "failed", "error": err.Error()})
			return
		}
		for key, value := range execution {
			resp[key] = value
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if executionAttempt == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	execution, err := s.executePendingAttempt(r.Context(), executionAttempt.ID)
	if err != nil {
		if errors.Is(err, store.ErrUnavailable) {
			writeStoreError(w, "execute attempt", err)
			return
		}
		if errors.Is(err, errAccountBindingViolation) {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"operation_id": opID, "status": "failed", "error": "account_binding_violation",
			})
			return
		}
		if errors.Is(err, errPaperExecutionFailed) {
			resp["status"] = "failed"
			resp["attempt_id"] = executionAttempt.ID
			resp["attempt_state"] = "failed"
			resp["error"] = "paper_execution_failed"
			writeJSON(w, http.StatusOK, resp)
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"operation_id": opID, "status": "unknown", "attempt_id": executionAttempt.ID,
			"error": "broker result uncertain",
		})
		return
	}
	for k, value := range execution {
		resp[k] = value
	}
	writeJSON(w, 200, resp)
}

func (s *server) shadowAccountSnapshot(ctx context.Context, gate store.OperationGate) (broker.AccountState, error) {
	paper, err := gate.ShadowAccount()
	if err != nil {
		return broker.AccountState{}, err
	}
	positions, err := gate.ShadowPositions()
	if err != nil {
		return broker.AccountState{}, err
	}
	equity := paper.Cash
	for _, position := range positions {
		quoteCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
		quote, err := s.marketProvider().Quote(quoteCtx, position.Symbol)
		cancel()
		if err != nil || !quote.Usable(s.limits.QuoteMaxAgeSec, time.Now().UTC()) {
			return broker.AccountState{}, fmt.Errorf("shadow mark unavailable for %s", position.Symbol)
		}
		// Long paper positions are marked at bid and rounded down, against the
		// account, so shadow risk capacity is never overstated.
		value, err := units.MulQtyPrice(position.Qty, quote.Bid, position.Multiplier, false)
		if err != nil {
			return broker.AccountState{}, err
		}
		equity, err = units.Add(equity, value)
		if err != nil {
			return broker.AccountState{}, err
		}
	}
	return broker.AccountState{
		AccountType: "paper", BuyingPower: paper.BuyingPower,
		Equity: equity, EquityKnown: true, Cash: paper.Cash, CashKnown: true,
		Source: "shadow", AsOf: time.Now().UTC(),
	}, nil
}

func (s *server) executeNonBroker(opID string, op risk.Operation) (map[string]any, error) {
	if op.Action == "tighten_stop" {
		sym := op.Symbol
		if sym == "" {
			sym = op.Underlying
		}
		hypothesis := map[string]any{"action": op.Action, "symbol": sym, "stop": op.Plan["stop"]}
		if err := s.store.InsertJournal(opID, hypothesis, nil, map[string]any{}, op.Shadow); err != nil {
			return nil, err
		}
		s.store.Event("stop_tightened", map[string]any{"operation_id": opID, "symbol": sym, "stop": op.Plan["stop"], "shadow": op.Shadow})
		return map[string]any{"status": "auto_approved", "stop": op.Plan["stop"]}, nil
	}
	return map[string]any{"status": "auto_approved"}, nil
}

var (
	errInsufficientClosableQuantity = errors.New("insufficient closable quantity")
	errBrokerDataUnavailable        = errors.New("broker data unavailable")
	errBrokerResultUnknown          = errors.New("broker result uncertain")
	errInvalidClose                 = errors.New("invalid close")
	errPaperExecutionFailed         = errors.New("paper execution failed")
	errReviewGateRejected           = errors.New("review gate rejected")
)

func ledgerName(shadow bool) string {
	if shadow {
		return "shadow"
	}
	return "live"
}

func operationSymbol(op risk.Operation) string {
	if op.Symbol != "" {
		return op.Symbol
	}
	return op.Underlying
}

func closablePositionQuantity(symbol string, positions []broker.Position) (units.Qty, error) {
	for _, position := range positions {
		if position.Symbol != symbol || position.Qty == 0 {
			continue
		}
		quantity, err := units.AbsQty(position.Qty)
		if err != nil {
			return 0, fmt.Errorf("position quantity is out of range")
		}
		return quantity, nil
	}
	return 0, fmt.Errorf("close requires an existing position for %s", symbol)
}

func minQty(left, right units.Qty) units.Qty {
	if left < right {
		return left
	}
	return right
}

func executionLimit(op risk.Operation, quote *broker.Quote, maxAgeSec int) (units.Micros, error) {
	if op.Action == "open" {
		if op.WorkingPrice <= 0 {
			return 0, fmt.Errorf("no executable price for open")
		}
		return op.WorkingPrice, nil
	}
	if quote == nil || !quote.Usable(maxAgeSec, time.Now().UTC()) {
		return 0, fmt.Errorf("market_data_unavailable")
	}
	if op.Limit != nil {
		return *op.Limit, nil
	}
	if op.Side == "sell" {
		return quote.Bid, nil
	}
	if op.Side == "buy" {
		return quote.Ask, nil
	}
	return 0, fmt.Errorf("no executable price for close")
}

func (s *server) brokerCallTimeout() time.Duration {
	if s.brokerTimeout > 0 {
		return s.brokerTimeout
	}
	return 10 * time.Second
}

func (s *server) workerID() string {
	if s.instanceID != "" {
		return s.instanceID
	}
	return "kernel-test"
}

func (s *server) executePendingAttempt(ctx context.Context, attemptID string) (map[string]any, error) {
	attempt, err := s.store.ClaimPendingAttempt(attemptID, s.workerID())
	if err != nil {
		return nil, err
	}
	if attempt == nil {
		current, err := s.store.GetExecutionAttempt(attemptID)
		if err != nil {
			return nil, err
		}
		operation, err := s.store.GetOperation(current.OperationID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"attempt_id": current.ID, "attempt_state": current.State, "status": operation.Status}, nil
	}
	return s.executeClaimedAttempt(ctx, attempt)
}

func (s *server) executeClaimedAttempt(ctx context.Context, attempt *store.ExecutionAttempt) (map[string]any, error) {
	if attempt.Intent == "paper_place" {
		return s.executeClaimedPaperAttempt(ctx, attempt)
	}
	execution := s.executionProvider()
	if execution == nil {
		_, err := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
			State: "failed", LastError: "execution capability unavailable",
			OperationStatus: "failed", ReleaseReservation: true,
		})
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("execution capability unavailable")
	}
	row, err := s.store.GetOperation(attempt.OperationID)
	if err != nil {
		return nil, err
	}
	var op risk.Operation
	if err := json.Unmarshal(row.Payload, &op); err != nil {
		return nil, fmt.Errorf("decode persisted operation: %w", err)
	}
	if op.Action == "close" && attempt.CloseReservationID == "" {
		return nil, fmt.Errorf("refusing close without reservation")
	}
	bindingCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	bindingErr := s.assertLiveAccountBinding(bindingCtx, attempt.OperationID)
	cancel()
	if bindingErr != nil {
		_, resolveErr := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
			State: "failed", LastError: "account binding failed",
			OperationStatus: "failed", ReleaseReservation: true,
		})
		if resolveErr != nil {
			return nil, resolveErr
		}
		return nil, bindingErr
	}

	brokerCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	var result broker.OrderResult
	if attempt.Intent == "cancel" {
		result, err = execution.CancelOrder(brokerCtx, attempt.TargetBrokerOrderID)
	} else {
		result, err = execution.PlaceLimitOrder(brokerCtx, broker.PlaceRequest{
			ClientOrderID: attempt.ClientOrderID, Symbol: operationSymbol(op), Side: op.Side,
			Qty: attempt.Qty, Limit: attempt.Limit, Kind: op.Kind,
		})
	}
	cancel()
	if err != nil {
		_, resolveErr := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
			State: "unknown", LastError: "broker call failed",
		})
		if resolveErr != nil {
			return nil, resolveErr
		}
		return nil, fmt.Errorf("%w", errBrokerResultUnknown)
	}
	resolution := resolutionForOrder(attempt, result)
	updated, err := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, resolution)
	if err != nil {
		if errors.Is(err, store.ErrIllegalOrderTransition) {
			log.Printf("order transition rejected for attempt %s: %v", attempt.ID, err)
		}
		if errors.Is(err, store.ErrFillIntegrity) {
			_ = s.refreshGlobalHalt()
		}
		return nil, err
	}
	if !updated {
		current, readErr := s.store.GetExecutionAttempt(attempt.ID)
		if readErr != nil {
			return nil, readErr
		}
		operation, readErr := s.store.GetOperation(current.OperationID)
		if readErr != nil {
			return nil, readErr
		}
		return map[string]any{"attempt_id": current.ID, "attempt_state": current.State, "status": operation.Status}, nil
	}
	status := resolution.OperationStatus
	if status == "" {
		status = "auto_approved"
	}
	return map[string]any{
		"status": status, "order": result, "attempt_id": attempt.ID,
		"attempt_state": resolution.State,
	}, nil
}

func (s *server) executeClaimedPaperAttempt(ctx context.Context, attempt *store.ExecutionAttempt) (map[string]any, error) {
	row, err := s.store.GetOperation(attempt.OperationID)
	if err != nil {
		return nil, err
	}
	var op risk.Operation
	if err := json.Unmarshal(row.Payload, &op); err != nil {
		return nil, fmt.Errorf("decode persisted paper operation: %w", err)
	}
	quoteCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	quote, err := s.marketProvider().Quote(quoteCtx, operationSymbol(op))
	cancel()
	if err != nil || !quote.Usable(s.limits.QuoteMaxAgeSec, time.Now().UTC()) {
		_, resolveErr := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
			State: "failed", LastError: "paper market data unavailable",
			OperationStatus: "failed", ReleaseReservation: true,
		})
		if resolveErr != nil {
			return nil, resolveErr
		}
		return nil, fmt.Errorf("%w: market data unavailable", errPaperExecutionFailed)
	}
	price := quote.Ask
	marketable := op.Side == "buy" && quote.Ask <= attempt.Limit
	if op.Side == "sell" {
		price = quote.Bid
		marketable = quote.Bid >= attempt.Limit
	}
	if !marketable {
		_, resolveErr := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
			State: "failed", LastError: "paper limit is not marketable",
			OperationStatus: "failed", ReleaseReservation: true,
		})
		if resolveErr != nil {
			return nil, resolveErr
		}
		return nil, fmt.Errorf("%w: limit is not marketable", errPaperExecutionFailed)
	}
	now := time.Now().UTC()
	result := broker.OrderResult{
		BrokerOrderID: "shadow-order:" + attempt.ID,
		ClientOrderID: attempt.ClientOrderID, State: "filled",
		FilledQty: attempt.Qty, FilledPrice: price,
		Fills: []broker.ReadFill{{
			FillID:        "shadow-fill:" + attempt.ID + ":1",
			BrokerOrderID: "shadow-order:" + attempt.ID,
			Symbol:        operationSymbol(op), Side: op.Side, Qty: attempt.Qty,
			Price: price, Source: "shadow", AsOf: now,
		}},
	}
	resolution := resolutionForOrder(attempt, result)
	updated, err := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, resolution)
	if err != nil {
		if errors.Is(err, store.ErrFillIntegrity) {
			_ = s.refreshGlobalHalt()
		}
		return nil, err
	}
	if !updated {
		current, readErr := s.store.GetExecutionAttempt(attempt.ID)
		if readErr != nil {
			return nil, readErr
		}
		operation, readErr := s.store.GetOperation(current.OperationID)
		if readErr != nil {
			return nil, readErr
		}
		return map[string]any{
			"attempt_id": current.ID, "attempt_state": current.State,
			"status": operation.Status,
		}, nil
	}
	return map[string]any{
		"status": "executed", "order": result, "attempt_id": attempt.ID,
		"attempt_state": "settled",
	}, nil
}

func resolutionForOrder(attempt *store.ExecutionAttempt, result broker.OrderResult) store.AttemptResolution {
	resolution := store.AttemptResolution{State: "unknown", BrokerOrderID: result.BrokerOrderID}
	if attempt.Intent == "place" || attempt.Intent == "paper_place" {
		fills := make([]store.FillInput, 0, len(result.Fills))
		for _, fill := range result.Fills {
			fills = append(fills, store.FillInput{
				BrokerFillID: fill.FillID, Qty: fill.Qty, Price: fill.Price,
				Fees: fill.Fees, TS: fill.AsOf,
			})
		}
		resolution.OrderUpdate = &store.OrderUpdate{
			ExecutionAttemptID: attempt.ID, BrokerOrderID: result.BrokerOrderID,
			State: result.State, FilledQty: result.FilledQty, Fills: fills,
		}
	} else {
		resolution.OrderEvent = map[string]any{
			"operation_id": attempt.OperationID, "order": result,
		}
	}
	switch result.State {
	case "filled":
		resolution.State, resolution.OperationStatus = "settled", "executed"
	case "rejected":
		resolution.State, resolution.OperationStatus = "failed", "failed"
		// The typed order update persists any fills and releases only the
		// unfilled remainder. Keep the zero-fill release flag as an idempotent
		// fallback for conclusively untouched reservations.
		resolution.ReleaseReservation = attempt.CloseReservationID != "" && result.FilledQty == 0
	case "cancelled", "expired":
		resolution.State = "settled"
		if attempt.Intent == "cancel" {
			resolution.OperationStatus = "executed"
		} else {
			resolution.OperationStatus = "failed"
		}
		resolution.ReleaseReservation = attempt.CloseReservationID != "" && result.FilledQty == 0
	case "submitted", "partially_filled":
		if attempt.Intent == "cancel" {
			// Querying a cancel target that is still working does not prove the
			// cancel effect happened. Keep reconciling instead of stranding the
			// attempt in placed, which the order/fill reconciler advances.
			resolution.LastError = "cancel not confirmed by broker"
		} else {
			resolution.State = "placed"
		}
	default:
		resolution.LastError = "unknown broker order state"
	}
	return resolution
}

// proposeRequest is the complete client-writable operation surface. Derived
// risk, execution prices, multiplier, resolved close direction, and verified
// reduction do not exist here, so strict JSON decoding rejects attempts to set
// them before any gate runs.
type proposeRequest struct {
	Proposer          string            `json:"proposer"`
	Action            string            `json:"action"`
	Kind              string            `json:"kind"`
	Underlying        string            `json:"underlying"`
	Symbol            string            `json:"symbol"`
	Side              string            `json:"side"`
	Qty               units.Qty         `json:"qty"`
	Limit             *units.Micros     `json:"limit"`
	MaxRiskUSD        *units.Micros     `json:"max_risk_usd"`
	Short             bool              `json:"short"`
	Plan              map[string]string `json:"plan"`
	Thesis            string            `json:"thesis"`
	Setup             string            `json:"setup"`
	Shadow            bool              `json:"shadow"`
	BrokerOrderID     string            `json:"broker_order_id,omitempty"`
	ClosesOperationID string            `json:"closes_operation_id,omitempty"`
}

var errIdempotencyKeyReused = errors.New("idempotency key reused with a different request")

func validateIdempotencyKey(key string) error {
	if key == "" {
		return fmt.Errorf("idempotency_key_required")
	}
	if len(key) > 200 {
		return fmt.Errorf("invalid_idempotency_key")
	}
	for i := range key {
		if key[i] < 0x21 || key[i] > 0x7e {
			return fmt.Errorf("invalid_idempotency_key")
		}
	}
	return nil
}

// hashClientIntent excludes every value derived from market data, account
// state, risk classification, or close-position normalization.
func hashClientIntent(op risk.Operation) ([sha256.Size]byte, error) {
	intent := proposeRequest{
		Proposer: op.Proposer, Action: op.Action, Kind: op.Kind,
		Underlying: op.Underlying, Symbol: op.Symbol, Side: op.Side,
		Qty: op.Qty, Limit: op.Limit, MaxRiskUSD: op.MaxRiskUSD,
		Short: op.Short, Plan: op.Plan, Thesis: op.Thesis, Setup: op.Setup,
		Shadow: op.Shadow, BrokerOrderID: op.BrokerOrderID,
		ClosesOperationID: op.ClosesOperationID,
	}
	encoded, err := json.Marshal(intent)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func validateAndBuildOperation(request proposeRequest) (risk.Operation, error) {
	op := risk.Operation{
		Proposer: request.Proposer, Action: request.Action, Kind: request.Kind,
		Underlying: request.Underlying, Symbol: request.Symbol, Side: request.Side,
		Qty: request.Qty, Limit: request.Limit, MaxRiskUSD: request.MaxRiskUSD,
		Short: request.Short, Plan: request.Plan, Thesis: request.Thesis,
		Setup: request.Setup, Shadow: request.Shadow,
		BrokerOrderID:     request.BrokerOrderID,
		ClosesOperationID: request.ClosesOperationID,
	}
	symbol := op.Symbol
	if symbol == "" {
		symbol = op.Underlying
	}

	switch op.Action {
	case "open":
		if strings.TrimSpace(op.Underlying) == "" {
			return op, fmt.Errorf("open requires underlying")
		}
		if strings.TrimSpace(symbol) == "" {
			return op, fmt.Errorf("open requires symbol or underlying")
		}
		if op.Kind != "equity" && op.Kind != "option" {
			return op, fmt.Errorf("bad kind %q", op.Kind)
		}
		if op.Side != "buy" && op.Side != "sell" {
			return op, fmt.Errorf("bad side %q", op.Side)
		}
		if op.Qty <= 0 {
			return op, fmt.Errorf("qty must be greater than zero")
		}
		if op.Kind == "option" && op.Qty%units.Qty(units.Scale) != 0 {
			return op, fmt.Errorf("option qty must be a whole number of contracts")
		}
	case "close":
		if strings.TrimSpace(symbol) == "" {
			return op, fmt.Errorf("close requires symbol or underlying")
		}
		if op.Kind != "" && op.Kind != "equity" && op.Kind != "option" {
			return op, fmt.Errorf("bad kind %q", op.Kind)
		}
		// Close side is optional for compatibility and never controls execution.
		if op.Side != "" && op.Side != "buy" && op.Side != "sell" {
			return op, fmt.Errorf("bad side %q", op.Side)
		}
		if op.Qty <= 0 {
			return op, fmt.Errorf("qty must be greater than zero")
		}
		if op.Kind == "option" && op.Qty%units.Qty(units.Scale) != 0 {
			return op, fmt.Errorf("option qty must be a whole number of contracts")
		}
	case "cancel":
		op.BrokerOrderID = strings.TrimSpace(op.BrokerOrderID)
		if op.BrokerOrderID == "" {
			return op, fmt.Errorf("cancel requires broker_order_id")
		}
	case "tighten_stop":
		if strings.TrimSpace(symbol) == "" {
			return op, fmt.Errorf("tighten_stop requires symbol or underlying")
		}
		stop := strings.TrimSpace(op.Plan["stop"])
		if stop == "" {
			return op, fmt.Errorf("tighten_stop requires non-blank plan.stop")
		}
		op.Plan["stop"] = stop
	default:
		return op, fmt.Errorf("bad action %q", op.Action)
	}

	if op.Limit != nil && *op.Limit <= 0 {
		return op, fmt.Errorf("limit must be greater than zero")
	}
	if op.Action != "close" && strings.TrimSpace(op.ClosesOperationID) != "" {
		return op, fmt.Errorf("closes_operation_id is meaningful only on close")
	}
	return op, nil
}

func (s *server) deriveOpenOperation(ctx context.Context, op risk.Operation, quote *broker.Quote) risk.Operation {
	if quote == nil || !quote.Usable(s.limits.QuoteMaxAgeSec, time.Now().UTC()) {
		op.RejectReason = "market_data_unavailable"
		return op
	}

	switch op.Kind {
	case "equity":
		op.Multiplier = 1
	case "option":
		symbol := op.Symbol
		if symbol == "" {
			symbol = op.Underlying
		}
		instrumentCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
		instrument, err := s.marketProvider().Instrument(instrumentCtx, symbol)
		cancel()
		if err != nil || instrument.Kind != "option" || instrument.Multiplier != 100 {
			op.RejectReason = "unsupported_contract"
			return op
		}
		op.Multiplier = instrument.Multiplier
	default:
		op.RejectReason = "unsupported_contract"
		return op
	}

	if op.Limit != nil {
		op.ApprovedPriceCap = *op.Limit
	} else {
		op.ApprovedPriceCap = quote.Ask
	}
	op.WorkingPrice = quote.Ask
	if s.limits.ExecutionPolicy.StartAt == "mid" {
		op.WorkingPrice = quote.Mid()
	}
	if op.WorkingPrice > op.ApprovedPriceCap {
		op.WorkingPrice = op.ApprovedPriceCap
	}

	// Required cash rounds up, against the account, so fractional micro-dollars
	// of premium never become unreserved capacity.
	required, err := units.MulQtyPrice(op.Qty, op.ApprovedPriceCap, op.Multiplier, true)
	if err != nil {
		op.RejectReason = "risk_overflow"
		return op
	}
	feePerUnit := s.limits.ExecutionPolicy.FeePerShare
	if op.Kind == "option" {
		feePerUnit = s.limits.ExecutionPolicy.FeePerContract
	}
	// Fees also round up because they increase the account's required cash.
	fees, err := units.MulQtyPrice(op.Qty, feePerUnit, 1, true)
	if err != nil {
		op.RejectReason = "risk_overflow"
		return op
	}
	required, err = units.Add(required, fees)
	if err != nil || required <= 0 {
		op.RejectReason = "risk_overflow"
		return op
	}
	op.RequiredCash = required
	// In the single-leg long-only model, premium/cash is the maximum loss. A
	// planned stop is not a broker guarantee and never reduces this value.
	op.DerivedMaxRisk = required

	return op
}

func addRiskFacts(response map[string]any, op risk.Operation) {
	if op.Action != "open" {
		return
	}
	response["derived_max_risk"] = op.DerivedMaxRisk
	response["required_cash"] = op.RequiredCash
	response["approved_price_cap"] = op.ApprovedPriceCap
	response["working_price"] = op.WorkingPrice
	response["multiplier"] = op.Multiplier
}

func normalizeClose(op risk.Operation, positions []broker.Position) (risk.Operation, error) {
	symbol := op.Symbol
	if symbol == "" {
		symbol = op.Underlying
	}
	var position *broker.Position
	for i := range positions {
		if positions[i].Symbol == symbol && positions[i].Qty != 0 {
			position = &positions[i]
			break
		}
	}
	if position == nil {
		return op, fmt.Errorf("close requires an existing position for %s", symbol)
	}
	positionQty, err := units.AbsQty(position.Qty)
	if err != nil {
		return op, fmt.Errorf("position quantity is out of range")
	}
	if op.Qty > positionQty {
		return op, fmt.Errorf("close qty %s exceeds position qty %s", op.Qty, positionQty)
	}
	if position.Kind != "equity" && position.Kind != "option" {
		return op, fmt.Errorf("position %s has unsupported kind %q", symbol, position.Kind)
	}
	if op.Kind != "" && op.Kind != position.Kind {
		return op, fmt.Errorf("close kind %q does not match position kind %q", op.Kind, position.Kind)
	}
	if position.Kind == "option" && op.Qty%units.Qty(units.Scale) != 0 {
		return op, fmt.Errorf("option qty must be a whole number of contracts")
	}

	op.Kind = position.Kind
	op.Multiplier = position.Multiplier
	if position.Qty > 0 {
		op.Side = "sell"
	} else {
		op.Side = "buy"
	}
	op.VerifiedReduction = true
	return op, nil
}

func (s *server) getOperation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validUUID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id must be a UUID"})
		return
	}
	row, err := s.store.GetOperation(id)
	if err != nil {
		if errors.Is(err, store.ErrUnavailable) {
			writeStoreError(w, "get operation", err)
			return
		}
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, 200, row)
}

func (s *server) review(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Verdict   string `json:"verdict"` // approved | rejected
		Rationale string `json:"rationale"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if !validUUID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id must be a UUID"})
		return
	}
	if in.Verdict != "approved" && in.Verdict != "rejected" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "verdict must be approved or rejected"})
		return
	}
	row, err := s.store.GetOperation(id)
	if err != nil {
		if errors.Is(err, store.ErrUnavailable) {
			writeStoreError(w, "get operation for review", err)
			return
		}
		writeJSON(w, 409, map[string]string{"error": "not pending review"})
		return
	}
	if row.Status != "pending_review" {
		writeJSON(w, 409, map[string]string{"error": "not pending review"})
		return
	}
	reviewer := authenticatedSubject(r)
	if in.Verdict == "rejected" {
		err := s.store.WithReviewLock(id, func(gate store.OperationGate, _ *store.OperationRow) error {
			verdict := map[string]any{"reviewer": reviewer, "rationale": in.Rationale, "decision": "rejected"}
			if err := gate.SetOperationStatus(id, "rejected", verdict); err != nil {
				return err
			}
			return gate.InsertEvent("operation_reviewed", map[string]any{
				"operation_id": id, "reviewer": reviewer, "rationale": in.Rationale,
				"decision": "rejected",
			})
		})
		if errors.Is(err, store.ErrOperationNotPending) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "not pending review"})
			return
		}
		if err != nil {
			writeStoreError(w, "review operation", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"operation_id": id, "status": "rejected"})
		return
	}

	var persisted risk.Operation
	if err := json.Unmarshal(row.Payload, &persisted); err != nil || persisted.Action != "open" || row.Class != "C" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "operation is not an approvable Class-C open"})
		return
	}
	var quote *broker.Quote
	quoteCtx, cancel := context.WithTimeout(r.Context(), s.brokerCallTimeout())
	freshQuote, quoteErr := s.marketProvider().Quote(quoteCtx, operationSymbol(persisted))
	cancel()
	if quoteErr == nil {
		quote = &freshQuote
	}
	prepared := s.prepareApprovedOpen(r.Context(), persisted, quote)
	if err := s.refreshGlobalHalt(); err != nil {
		writeStoreError(w, "refresh global halt", err)
		return
	}
	// Serialize approval with POST /halt exactly as normal open proposals are
	// serialized. Once the write lock is waiting, no later approval can commit.
	s.haltMu.RLock()
	defer s.haltMu.RUnlock()
	halted, haltReason := s.halted, s.haltReason

	var attempt *store.ExecutionAttempt
	var approvalVerdict risk.Verdict
	var approvedOp risk.Operation
	conflictReason := ""
	err = s.store.WithReviewLock(id, func(gate store.OperationGate, locked *store.OperationRow) error {
		if !bytes.Equal(row.Payload, locked.Payload) {
			conflictReason = "approval_snapshot_mismatch"
			return errReviewGateRejected
		}
		if s.proposalTTL <= 0 {
			return fmt.Errorf("proposal TTL is not configured")
		}
		now, err := gate.DatabaseNow()
		if err != nil {
			return err
		}
		if locked.TS.Add(s.proposalTTL).Before(now) {
			conflictReason = "proposal_expired"
			verdict := map[string]any{
				"reviewer": reviewer, "rationale": in.Rationale, "decision": "expired",
				"proposal_ts": locked.TS, "reviewed_at": now,
			}
			if err := gate.SetOperationStatus(id, "expired", verdict); err != nil {
				return err
			}
			return gate.InsertEvent("operation_reviewed", map[string]any{
				"operation_id": id, "reviewer": reviewer, "decision": "expired",
				"proposal_ts": locked.TS, "reviewed_at": now,
			})
		}
		if err := gate.LockLedger(prepared.Shadow); err != nil {
			return err
		}
		var account broker.AccountState
		if prepared.Shadow {
			account, err = s.shadowAccountSnapshot(r.Context(), gate)
		} else {
			accountCtx, cancel := context.WithTimeout(r.Context(), s.brokerCallTimeout())
			account, err = s.accountProvider().Account(accountCtx)
			cancel()
		}
		if err != nil {
			return fmt.Errorf("%w: account", errBrokerDataUnavailable)
		}
		databaseNow, err := gate.DatabaseNow()
		if err != nil {
			return err
		}
		window, err := marketDayWindow(databaseNow, config.Env("TZ_MARKET", "America/New_York"))
		if err != nil {
			return err
		}
		day, err := s.dayStateAtAccount(r.Context(), gate, prepared.Shadow, account, window, halted, haltReason)
		if err != nil {
			return err
		}
		approvalVerdict = risk.ClassifyAt(prepared, s.limits, day, quote, time.Now().UTC())
		if approvalVerdict.Class == "REJECT" {
			conflictReason = "approval gate rejected"
			if len(approvalVerdict.Reasons) > 0 {
				conflictReason = approvalVerdict.Reasons[0]
			}
			return errReviewGateRejected
		}
		approvedOp = prepared
		staged, err := s.stageApprovedOpen(gate, id, prepared, window.day)
		if err != nil {
			return err
		}
		attempt = staged
		approval := map[string]any{
			"reviewer": reviewer, "rationale": in.Rationale, "decision": "approved",
			"proposal_ts": locked.TS, "approved_at": databaseNow, "market_day": window.day,
			"quote": quote, "verdict": approvalVerdict,
			"approved_price_cap": prepared.ApprovedPriceCap, "working_price": prepared.WorkingPrice,
			"derived_max_risk": prepared.DerivedMaxRisk, "required_cash": prepared.RequiredCash,
			"multiplier": prepared.Multiplier, "attempt_id": staged.ID,
			"open_reservation_id": staged.OpenReservationID,
		}
		if err := gate.SetOperationStatus(id, "approved", approval); err != nil {
			return err
		}
		return gate.InsertEvent("operation_reviewed", map[string]any{
			"operation_id": id, "approval": approval,
		})
	})
	if errors.Is(err, store.ErrOperationNotPending) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "not pending review"})
		return
	}
	if errors.Is(err, errReviewGateRejected) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": conflictReason})
		return
	}
	if errors.Is(err, errBrokerDataUnavailable) {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "broker account data unavailable"})
		return
	}
	if err != nil {
		writeStoreError(w, "approve operation", err)
		return
	}
	if conflictReason == "proposal_expired" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": conflictReason})
		return
	}
	if attempt == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "approval attempt missing"})
		return
	}
	response := map[string]any{
		"operation_id": id, "status": "approved", "class": "C", "shadow": approvedOp.Shadow,
		"checks": approvalVerdict.Checks, "reasons": approvalVerdict.Reasons,
	}
	addRiskFacts(response, approvedOp)
	execution, err := s.executePendingAttempt(r.Context(), attempt.ID)
	if err != nil {
		if errors.Is(err, store.ErrUnavailable) {
			writeStoreError(w, "execute approved attempt", err)
			return
		}
		if errors.Is(err, errAccountBindingViolation) {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"operation_id": id, "status": "failed", "error": "account_binding_violation",
			})
			return
		}
		if errors.Is(err, errPaperExecutionFailed) {
			response["status"] = "failed"
			response["attempt_id"] = attempt.ID
			response["attempt_state"] = "failed"
			response["error"] = "paper_execution_failed"
			writeJSON(w, http.StatusOK, response)
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"operation_id": id, "status": "unknown", "attempt_id": attempt.ID,
			"error": "broker result uncertain",
		})
		return
	}
	for key, value := range execution {
		response[key] = value
	}
	if response["status"] == "auto_approved" {
		response["status"] = "approved"
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) prepareApprovedOpen(ctx context.Context, persisted risk.Operation, quote *broker.Quote) risk.Operation {
	prepared := persisted
	cap := persisted.ApprovedPriceCap
	originalLimit := persisted.Limit
	prepared.Limit = &cap
	prepared.RejectReason = ""
	prepared = s.deriveOpenOperation(ctx, prepared, quote)
	prepared.Limit = originalLimit
	if prepared.RejectReason != "" {
		return prepared
	}
	if persisted.Shadow && quote != nil {
		prepared.WorkingPrice = quote.Ask
		if prepared.WorkingPrice > persisted.ApprovedPriceCap {
			prepared.WorkingPrice = persisted.ApprovedPriceCap
		}
	}
	if persisted.ApprovedPriceCap <= 0 || prepared.ApprovedPriceCap != persisted.ApprovedPriceCap ||
		prepared.Multiplier != persisted.Multiplier || prepared.RequiredCash != persisted.RequiredCash ||
		prepared.DerivedMaxRisk != persisted.DerivedMaxRisk {
		prepared.RejectReason = "approval_snapshot_mismatch"
	}
	return prepared
}

func (s *server) stageApprovedOpen(gate store.OperationGate, operationID string, op risk.Operation, marketDay time.Time) (*store.ExecutionAttempt, error) {
	ledger := ledgerName(op.Shadow)
	if err := gate.InsertTradeGrant(store.TradeGrant{
		OperationID: operationID, Ledger: ledger, MarketDay: marketDay,
		AuthorizedRisk: op.DerivedMaxRisk, RiskSource: "computed",
	}); err != nil {
		return nil, err
	}
	if err := gate.InsertEvent("trade_grant_created", map[string]any{
		"operation_id": operationID, "ledger": ledger,
	}); err != nil {
		return nil, err
	}
	reservation := store.OpenReservation{
		ID: store.NewID(), OperationID: operationID, Ledger: ledger,
		MarketDay: marketDay, Symbol: operationSymbol(op), Kind: op.Kind,
		OriginalQty: op.Qty, RemainingQty: op.Qty,
		OriginalRisk: op.DerivedMaxRisk, RemainingRisk: op.DerivedMaxRisk,
		OriginalCash: op.RequiredCash, RemainingCash: op.RequiredCash,
		ResourceState: "held",
	}
	if err := gate.InsertOpenReservation(reservation); err != nil {
		return nil, err
	}
	if err := gate.InsertEvent("open_reservation_created", map[string]any{
		"operation_id": operationID, "reservation_id": reservation.ID,
		"ledger": ledger, "symbol": reservation.Symbol,
	}); err != nil {
		return nil, err
	}
	attempt := store.ExecutionAttempt{
		ID: store.NewID(), OperationID: operationID, Seq: 1,
		OpenReservationID: reservation.ID, Intent: "place", ClientOrderID: store.NewID(),
		State: "pending", Qty: op.Qty, Limit: op.WorkingPrice,
	}
	if op.Shadow {
		attempt.Intent = "paper_place"
		attempt.ClientOrderID = "shadow:" + attempt.ID
	}
	if attempt.Limit <= 0 || attempt.Limit > op.ApprovedPriceCap {
		return nil, fmt.Errorf("approved working price exceeds its cap")
	}
	if err := gate.InsertExecutionAttempt(attempt); err != nil {
		return nil, err
	}
	if err := gate.InsertOrder(store.Order{
		ID: store.NewID(), OperationID: operationID, ExecutionAttemptID: attempt.ID,
		ClientOrderID: attempt.ClientOrderID, Ledger: ledger,
		Symbol: operationSymbol(op), Side: op.Side, Kind: op.Kind,
		Multiplier: op.Multiplier, Qty: attempt.Qty, Limit: attempt.Limit,
		State: "new",
	}); err != nil {
		return nil, err
	}
	if err := gate.InsertEvent("execution_attempt_created", map[string]any{
		"operation_id": operationID, "attempt_id": attempt.ID, "intent": attempt.Intent,
	}); err != nil {
		return nil, err
	}
	return &attempt, nil
}

func (s *server) postJournal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OperationID    string         `json:"operation_id"`
		Hypothesis     map[string]any `json:"hypothesis"`
		Outcome        map[string]any `json:"outcome"`
		PromptVersions map[string]any `json:"prompt_versions"`
		Shadow         bool           `json:"shadow"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if s.tradingMode() == config.ModeShadow {
		in.Shadow = true
	}
	in.OperationID = strings.TrimSpace(in.OperationID)
	if !validUUID(in.OperationID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "operation_id must be a UUID"})
		return
	}
	var outcome any
	if in.Outcome != nil {
		outcome = in.Outcome
	}
	if err := s.store.InsertJournal(in.OperationID, in.Hypothesis, outcome, in.PromptVersions, in.Shadow); err != nil {
		if errors.Is(err, store.ErrInvalidOperationReference) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "operation_id does not reference an existing operation"})
		} else {
			writeStoreError(w, "insert journal", err)
		}
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) getLessons(w http.ResponseWriter, r *http.Request) {
	limit := 5
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := jsonNumber(v)
		if err != nil || n < 1 || n > maxLessonsLimit {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be an integer between 1 and 100"})
			return
		}
		limit = n
	}
	ls, err := s.store.TopLessons(limit)
	if err != nil {
		writeStoreError(w, "get lessons", err)
		return
	}
	writeJSON(w, 200, ls)
}

func jsonNumber(s string) (int, error) {
	var n int
	err := json.Unmarshal([]byte(s), &n)
	return n, err
}

func (s *server) getBlackboard(w http.ResponseWriter, r *http.Request) {
	day := r.PathValue("day")
	if !validDate(day) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "day must be YYYY-MM-DD"})
		return
	}
	doc, err := s.store.GetBlackboard(day)
	if err != nil {
		writeStoreError(w, "get blackboard", err)
		return
	}
	writeJSON(w, 200, doc)
}

func (s *server) putBlackboard(w http.ResponseWriter, r *http.Request) {
	day := r.PathValue("day")
	if !validDate(day) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "day must be YYYY-MM-DD"})
		return
	}
	var doc json.RawMessage
	if !decodeJSONBody(w, r, &doc) {
		return
	}
	if err := s.store.PutBlackboard(day, doc); err != nil {
		writeStoreError(w, "put blackboard", err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// simQuote is only meaningful with the fake broker: shadow mode's market feed
// and the backtest replay surface.
func (s *server) simQuote(w http.ResponseWriter, r *http.Request) {
	venue := s.simVenue
	if venue == nil {
		venue, _ = s.broker.(*broker.Fake)
	}
	if venue == nil {
		writeJSON(w, 400, map[string]string{"error": "not a sim broker"})
		return
	}
	if halted, reason := s.haltSnapshot(); halted && strings.HasPrefix(reason, store.ErrFillIntegrity.Error()) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "fill reconciliation halted"})
		return
	}
	var q broker.Quote
	if !decodeJSONBody(w, r, &q) {
		return
	}
	if err := venue.SetQuote(q); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.reconcileWorkingOrders(r.Context()); err != nil {
		writeStoreError(w, "persist simulated fills", err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
