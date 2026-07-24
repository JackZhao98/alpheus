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
	limits             config.Limits
	mode               config.ModeConfig
	account            broker.AccountProvider
	authorityAccount   broker.AccountProvider
	execution          broker.ExecutionProvider
	market             marketdata.Provider
	authorityMarket    marketdata.Provider
	mcpLab             *mcpReadLab
	simVenue           *broker.Fake
	providerMu         sync.RWMutex
	robinhoodEnabled   bool
	robinhoodSnapshot  rhmcp.CapabilitySnapshot
	robinhoodAccountID string
	// Test seams keep the browser callback path verifiable without contacting
	// Robinhood. Production leaves both nil and uses rhmcp directly.
	robinhoodBegin    func(context.Context, string) (rhmcp.AuthorizationStart, error)
	robinhoodExchange func(context.Context, string, string, string, string) (rhmcp.OAuthToken, error)
	robinhoodDiscover func(context.Context) ([]rhmcp.ToolSchema, error)
	// broker is a simulation/test compatibility seam. Production construction
	// leaves it nil and registers read and execution capabilities separately.
	broker                            broker.Adapter
	store                             storeAPI
	instanceID                        string
	brokerTimeout                     time.Duration
	claimTimeout                      time.Duration
	attemptStale                      time.Duration
	replayCreationGuard               time.Duration
	providerDedupeVerified            bool
	providerReplayWindowBoundVerified bool
	runtimeURL                        string
	runtimeHTTP                       *http.Client
	cortexURL                         string
	cortexTokenFile                   string
	cortexPaperEffectTokenFile        string
	researchURL                       string
	researchHTTP                      *http.Client
	gexHTTP                           *http.Client
	consoleOrigin                     string
	marketTZ                          string
	haltMu                            sync.RWMutex
	halted                            bool
	haltReason                        string
}

// storeAPI keeps the HTTP surface testable without adding a database-mocking
// dependency. The production implementation is *store.Store.
type storeAPI interface {
	CountTradesForDay(shadow bool, start, end time.Time) (int, error)
	CountTradesForDayExcluding(shadow bool, start, end time.Time, operationID string) (int, error)
	InsertEvent(kind string, payload any) error
	InsertEventWithID(kind string, payload any) (int64, error)
	FindOperationByIdempotency(subject, key string) (*store.OperationRow, error)
	WithProposalLock(identity *store.IdempotencyIdentity, shadow, lockLedger bool, fn func(store.OperationGate) error) error
	WithLedgerLock(shadow bool, fn func(store.OperationGate) error) error
	WithReviewLock(id string, fn func(store.OperationGate, *store.OperationRow) error) error
	Event(kind string, payload any)
	SetOperationStatus(id, status string, verdict any) error
	GetOperation(id string) (*store.OperationRow, error)
	ListOperations(status string, limit int, cursor *store.OperationCursor) ([]store.OperationRow, error)
	AgentPaperPortfolio(string) (
		store.AgentPaperAccount, []store.AgentPaperPosition, error,
	)
	ListAgentPaperOrders(string, int) ([]store.AgentPaperOrder, error)
	ExecuteAgentPaperOrder(store.AgentPaperOrderInput) (
		store.AgentPaperOrderResult, error,
	)
	AgentAutonomyProfile(string) (store.AgentAutonomyProfile, error)
	SetAgentAutonomy(string, string, int64, string) (
		store.AgentAutonomyProfile, error,
	)
	CreateAgentIntradaySession(store.AgentIntradaySessionCreate) (
		store.AgentIntradaySession, error,
	)
	RecordAgentIntradaySessionFrame(store.AgentIntradaySessionFrame) (
		store.AgentIntradaySession, error,
	)
	AgentIntradaySessionByReplay(string, string) (
		store.AgentIntradaySession, error,
	)
	ListAgentIntradaySessions(string, int) (
		[]store.AgentIntradaySession, error,
	)
	ListAgentIntradaySessionEvents(string, string, int) (
		[]store.AgentIntradaySessionEvent, error,
	)
	ListControlWarnings(pendingBefore, claimBefore time.Time, limit int) ([]store.ControlWarning, error)
	InsertJournal(operationID string, hypothesis, outcome, promptVersions any, shadow bool) error
	TopLessons(limit int) ([]store.Lesson, error)
	GetBlackboard(day string) (json.RawMessage, error)
	PutBlackboard(day string, doc json.RawMessage) error
	LoadGlobalHalt() (bool, string, error)
	ActivateGlobalHalt(reason, subject, mode string) (store.GlobalHaltTransition, error)
	ResumeGlobalHalt(reason, subject, mode string) (store.GlobalHaltTransition, error)
	GetExecutionAttempt(id string) (*store.ExecutionAttempt, error)
	UpdatePendingAttemptLimit(id string, limit units.Micros) (bool, error)
	ClaimPendingAttempt(id, instance string, leaseDuration time.Duration) (*store.ExecutionAttempt, error)
	ClaimRecoverableAttempt(id, instance, expectedState string, expectedToken int, leaseDuration time.Duration) (*store.ExecutionAttempt, error)
	ClaimPendingAttemptLive(id, instance string, leaseDuration time.Duration) (*store.ExecutionAttempt, error)
	ClaimRecoverableAttemptLive(id, instance, expectedState string, expectedToken int, leaseDuration time.Duration) (*store.ExecutionAttempt, error)
	PrepareAttemptProviderIntent(id string, fencingToken int, accountID string, canonical json.RawMessage, fingerprint []byte) (bool, error)
	RecordPreEffectManifest(input store.PreEffectManifestInput) (*store.PreEffectManifest, error)
	MarkAttemptSentWithManifest(id string, fencingToken int, replay bool, replayGuard time.Duration, replayEvidence *store.ProviderIntentEvidence, manifestID string) (bool, error)
	GetLiveExecutionGate() (store.LiveExecutionGate, error)
	ListRecoverableAttempts(pendingAge time.Duration, limit int) ([]store.ExecutionAttempt, error)
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
	ActivateM3A(snapshot store.M3AActivationSnapshot) error
	LoadLiveCanaryAuthority() (*store.LiveCanaryRevision, error)
	LoadLiveCanaryDayAttestations(accountID string, limit int) ([]store.LiveCanaryDayAttestation, error)
	LoadKernelPolicyAuthority() (*store.KernelPolicyRevision, error)
	LoadBoundKernelPolicy(operation *store.OperationRow) (*store.KernelPolicyRevision, error)
	DatabaseNow() (time.Time, error)
	BrokerLocalStateGeneration() (int64, error)
	RecordBrokerObservation(input store.BrokerObservationInput) (*store.BrokerObservation, error)
	LoadBrokerAccountView(accountID string) (*store.BrokerAccountView, error)
	LoadBrokerObservation(id string) (*store.BrokerAccountView, error)
	ReconcileBrokerObservation(observationID string) (*store.BrokerReconciliationResult, error)
	LoadBrokerCoexistenceView(accountID string, historyLimit int) (*store.BrokerCoexistenceView, error)
	CreateAgentQueryJob(subject, workflow, symbol, query string) (*store.AgentQueryJob, error)
	ClaimAgentQueryJob(id string, leaseDuration time.Duration) (*store.AgentQueryJob, error)
	CompleteClaimedAgentQueryJob(id, claimToken string, result json.RawMessage) (bool, error)
	FailClaimedAgentQueryJob(id, claimToken, errorCode string) (bool, error)
	RecordAgentQueryJobTrace(id, claimToken, stage, errorCode string) (bool, error)
	ListRecoverableAgentQueryJobs(limit int) ([]store.AgentQueryJob, error)
	GetAgentQueryJob(id string) (*store.AgentQueryJob, error)
	PutAgentSecret(name string, ciphertext []byte) error
	GetAgentSecret(name string) (*store.AgentSecretRecord, error)
	DeleteAgentSecret(name string) error
	ListAgentSecretNames() ([]string, error)
}

type dayStateReader interface {
	DatabaseNow() (time.Time, error)
	CountTradesForDay(shadow bool, start, end time.Time) (int, error)
	LedgerResources(ledger, excludeOperationID string) (store.LedgerResources, error)
	InsertDayOpen(marketDay time.Time, ledger string, equity units.Micros) error
	EvaluateDayRisk(input store.DayRiskInput) (store.DayRiskStats, error)
}

func main() {
	handled, err := dispatchKernelCommand(os.Args[1:], os.Stdout)
	if err != nil {
		log.Fatalf("command: %v", err)
	}
	if handled {
		return
	}
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
	attemptConfig, err := loadAttemptConfig()
	if err != nil {
		log.Fatalf("execution config: %v", err)
	}
	brokerName := config.Env("BROKER", "fake")
	dbTimeout, err := databaseTimeout()
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	marketTZ := config.Env("TZ_MARKET", "America/New_York")
	st, err := store.Open(store.Config{
		URL:           config.Env("DATABASE_URL", "postgresql://alpheus:alpheus@localhost:5432/alpheus?sslmode=disable"),
		MigrationsDir: config.Env("MIGRATIONS_DIR", "../db/migrations"),
		Timeout:       dbTimeout,
		MarketTZ:      marketTZ,
	})
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	kernelPolicy, err := st.LoadKernelPolicyAuthority()
	if err != nil {
		log.Fatalf("kernel policy authority: %v; run the explicit kernel-policy command before server startup", err)
	}
	limits := kernelPolicy.Policy
	if err := validateProductionQuoteAge(brokerName, limits.QuoteMaxAgeSec); err != nil {
		log.Fatalf("kernel policy authority: %v", err)
	}
	if _, err := requireLiveCanaryAuthority(mode.TradingMode, st); err != nil {
		log.Fatalf("live canary authority: %v", err)
	}
	s := &server{
		limits: limits, mode: mode, store: st,
		instanceID: store.NewID(), brokerTimeout: attemptConfig.brokerTimeout,
		claimTimeout: attemptConfig.claimTimeout, attemptStale: attemptConfig.attemptStale,
		replayCreationGuard: attemptConfig.replayCreationGuard,
		providerDedupeVerified: brokerName == "fake" ||
			(brokerName == "robinhood" && mode.TradingMode == config.ModeLive),
		// FakeBroker completes synchronously inside the bounded call. Robinhood
		// ref-id dedupe is empirically verified; its server-side order-created
		// latency is bounded by REPLAY_CREATION_GUARD_MS (added to the call
		// timeout), so equity auto-replay may be turned on deliberately per
		// deployment. It stays fail-closed unless ROBINHOOD_EQUITY_AUTO_REPLAY=true.
		providerReplayWindowBoundVerified: brokerName == "fake" ||
			(brokerName == "robinhood" && mode.TradingMode == config.ModeLive &&
				config.Env("ROBINHOOD_EQUITY_AUTO_REPLAY", "false") == "true"),
		cortexURL:       config.Env("CORTEX_URL", ""),
		cortexTokenFile: config.Env("CORTEX_INPUT_TOKEN_FILE", ""),
		cortexPaperEffectTokenFile: config.Env(
			"CORTEX_PAPER_EFFECT_TOKEN_FILE", "",
		),
		researchURL:   config.Env("RESEARCH_URL", "http://research-gateway:8300"),
		consoleOrigin: consoleOrigin,
		marketTZ:      marketTZ,
	}
	switch brokerName {
	case "fake":
		s.simVenue = broker.NewFake(units.MustMicros("300"))
		s.account, s.execution = s.simVenue, s.simVenue
		s.authorityAccount = s.simVenue
		s.market = marketdata.NewFakeProvider(s.simVenue)
		s.authorityMarket = s.market
		s.broker = compatibilityBroker(s.simVenue)
	case "robinhood":
		snapshot, loadErr := rhmcp.LoadCapabilitySnapshot(config.Env("RH_MCP_CAPABILITIES_FILE", "../docs/rh_mcp_capabilities.json"))
		if loadErr != nil {
			log.Fatalf("broker: %v", loadErr)
		}
		s.robinhoodEnabled, s.robinhoodSnapshot = true, snapshot
		if activateErr := s.activateRobinhood(context.Background()); activateErr != nil {
			st.Event("robinhood_connect_required", map[string]string{"mode": mode.TradingMode})
			if mode.TradingMode == config.ModeLive {
				log.Fatalf("broker: a persisted Robinhood connection and explicit account binding are required in live mode")
			}
			log.Printf("Robinhood provider inactive: %v", activateErr)
		}
	default:
		log.Fatalf("broker: unknown BROKER %q", brokerName)
	}
	if s.broker == nil {
		s.broker = compatibilityBroker(s.simVenue)
	}
	if s.account != nil {
		if err := s.activateM3AIfNeeded(context.Background()); err != nil {
			log.Fatalf("%v", err)
		}
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
	// GEXBOT collection moved to the credential-isolated Research Plane
	// Provider. Kernel keeps the old dashboard's historical read model only;
	// it no longer schedules external GEXBOT requests or owns fresh snapshots.
	st.Event("kernel_start", map[string]any{
		"broker": os.Getenv("BROKER"), "mode": mode.TradingMode,
		"kernel_policy": map[string]any{
			"revision_id": kernelPolicy.ID, "generation": kernelPolicy.Generation,
			"digest": kernelPolicy.Digest,
		},
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
	brokerTimeout       time.Duration
	claimTimeout        time.Duration
	attemptStale        time.Duration
	replayCreationGuard time.Duration
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
	// The same-ref replay guard must cover the provider send call plus the
	// server's order-creation latency, so the replayed order's created_at stays
	// inside the send window and can never orphan. Added on top of the broker
	// call timeout at the call site.
	replayCreationGuard, err := durationFromMillis("REPLAY_CREATION_GUARD_MS", 15_000)
	if err != nil {
		return attemptTimingConfig{}, err
	}
	if claimTimeout <= brokerTimeout {
		return attemptTimingConfig{}, fmt.Errorf("CLAIM_TIMEOUT_MS must exceed BROKER_TIMEOUT_MS")
	}
	return attemptTimingConfig{
		brokerTimeout: brokerTimeout, claimTimeout: claimTimeout,
		attemptStale: attemptStale, replayCreationGuard: replayCreationGuard,
	}, nil
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

// writeError keeps API failures diagnosable without exposing internal database
// or provider details. Existing callers can continue to display error while
// clients and logs key on the stable error_code.
func writeError(w http.ResponseWriter, status int, errorCode, message string) {
	writeJSON(w, status, map[string]string{"error_code": errorCode, "error": message})
}

const maxJSONBodyBytes int64 = 1 << 20
const maxLessonsLimit = 100

// decodeJSONBody is the single boundary for every JSON write endpoint. It
// enforces media type, a bounded body, a strict schema, and exactly one JSON
// value before endpoint-specific validation or side effects can run.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusBadRequest, "request_content_type_invalid", "content-type must be application/json")
		return false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body exceeds 1 MiB")
		} else {
			writeError(w, http.StatusBadRequest, "request_json_invalid", "invalid JSON body")
		}
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body exceeds 1 MiB")
		} else {
			writeError(w, http.StatusBadRequest, "request_json_multiple_values", "request body must contain exactly one JSON value")
		}
		return false
	}
	return true
}

func writeInternalError(w http.ResponseWriter, context string, err error) {
	log.Printf("%s: %v", context, err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeStoreError(w http.ResponseWriter, context string, err error) {
	if errors.Is(err, errMarketDayAdvanced) {
		log.Printf("%s: %v", context, err)
		writeError(w, http.StatusServiceUnavailable, "market_day_advanced", "market day advanced; retry")
		return
	}
	if errors.Is(err, store.ErrUnavailable) {
		log.Printf("%s: %v", context, err)
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "database unavailable")
		return
	}
	if errors.Is(err, store.ErrLiveCanaryAuthorityMissing) ||
		errors.Is(err, store.ErrLiveCanaryAuthorityInvalid) {
		log.Printf("%s: %v", context, err)
		writeError(w, http.StatusServiceUnavailable, "live_canary_authority_unavailable", "live canary authority unavailable")
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
	asOf       time.Time
}

type databaseClock interface {
	DatabaseNow() (time.Time, error)
}

func (s *server) marketTimezone() string {
	if s.marketTZ != "" {
		return s.marketTZ
	}
	return config.Env("TZ_MARKET", "America/New_York")
}

func (s *server) databaseMarketWindow(clock databaseClock) (marketWindow, error) {
	now, err := clock.DatabaseNow()
	if err != nil {
		return marketWindow{}, err
	}
	return marketDayWindow(now, s.marketTimezone())
}

func (s *server) ensureMarketDay(clock databaseClock, expected marketWindow) error {
	current, err := s.databaseMarketWindow(clock)
	if err != nil {
		return err
	}
	if !current.day.Equal(expected.day) {
		return errMarketDayAdvanced
	}
	return nil
}

func marketDayWindow(now time.Time, tzName string) (marketWindow, error) {
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return marketWindow{}, fmt.Errorf("market timezone %q: %w", tzName, err)
	}
	localNow := now.In(loc)
	day := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	return marketWindow{
		day: day, start: day.UTC(), end: day.AddDate(0, 0, 1).UTC(), asOf: now.UTC(),
	}, nil
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
	return s.dayStateAtAccountWithLimits(ctx, gate, shadow, account, window, halted, haltReason, s.limits)
}

func (s *server) dayStateAtAccountWithLimits(ctx context.Context, gate dayStateReader, shadow bool, account broker.AccountState, window marketWindow, halted bool, haltReason string, limits config.Limits) (risk.DayState, error) {
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
	if err := s.ensureMarketDay(gate, window); err != nil {
		return risk.DayState{}, err
	}
	stats, err := gate.EvaluateDayRisk(store.DayRiskInput{
		Ledger: ledger, MarketDay: window.day, Start: window.start, End: window.end,
		ObservedAt:              window.asOf,
		ProviderRealizedPnL:     providerPnL,
		MaxDailyLossPct:         limits.HardLimits.MaxDailyLossPct,
		ConsecutiveLossDaysHalt: limits.HardLimits.ConsecutiveLossDaysHalt,
		PnLReconciliationLimit:  limits.PnLReconciliationTolerance,
	})
	if err != nil {
		return risk.DayState{}, err
	}
	if err := s.ensureMarketDay(gate, window); err != nil {
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
	provider, ok := s.authorityAccountProvider().(broker.RealizedPnLProvider)
	if !ok {
		return nil, nil
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	snapshot, err := provider.RealizedPnL(providerCtx, marketDay, s.marketTimezone())
	cancel()
	if err != nil || snapshot.AsOf.IsZero() {
		return nil, fmt.Errorf("%w: realized PnL", errBrokerDataUnavailable)
	}
	value := snapshot.Total
	return &value, nil
}

func (s *server) getLimits(w http.ResponseWriter, _ *http.Request) {
	kernelPolicy, err := s.store.LoadKernelPolicyAuthority()
	if err != nil {
		writeStoreError(w, "get kernel policy authority", err)
		return
	}
	canary, err := s.liveCanaryAuthorityView()
	if err != nil {
		writeStoreError(w, "get live canary authority", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"db_kernel_policy": kernelPolicy,
		"db_live_canary":   canary,
	})
}

func (s *server) accountProvider() broker.AccountProvider {
	s.providerMu.RLock()
	account, compatibility := s.account, s.broker
	s.providerMu.RUnlock()
	if account != nil {
		return account
	}
	return compatibility
}

// authorityAccountProvider is the only account capability eligible to support
// a Live decision or broker-effect reconciliation. Production wiring supplies
// a non-cacheable Provider; fake/test wiring naturally falls back to account.
func (s *server) authorityAccountProvider() broker.AccountProvider {
	s.providerMu.RLock()
	authority := s.authorityAccount
	s.providerMu.RUnlock()
	if authority != nil {
		return authority
	}
	return s.accountProvider()
}

func (s *server) executionProvider() broker.ExecutionProvider {
	s.providerMu.RLock()
	execution, compatibility := s.execution, s.broker
	s.providerMu.RUnlock()
	if execution != nil {
		return execution
	}
	return compatibility
}

func compatibilityBroker(fake *broker.Fake) broker.Adapter {
	// Converting a nil *Fake directly to broker.Adapter creates a non-nil
	// interface whose method call panics. Production Robinhood construction has
	// no compatibility broker, especially in read-only mode.
	if fake == nil {
		return nil
	}
	return fake
}

func (s *server) marketProvider() marketdata.Provider {
	s.providerMu.RLock()
	market, compatibility := s.market, s.broker
	s.providerMu.RUnlock()
	if market != nil {
		return market
	}
	if fake, ok := compatibility.(*broker.Fake); ok {
		return marketdata.NewFakeProvider(fake)
	}
	return nil
}

// authorityMarketProvider is the non-cacheable market capability for Live
// decisions. Cached market data remains available only through marketProvider.
func (s *server) authorityMarketProvider() marketdata.Provider {
	s.providerMu.RLock()
	authority := s.authorityMarket
	s.providerMu.RUnlock()
	if authority != nil {
		return authority
	}
	return s.marketProvider()
}

func (s *server) getState(w http.ResponseWriter, r *http.Request) {
	if err := s.refreshGlobalHalt(); err != nil {
		writeStoreError(w, "refresh global halt", err)
		return
	}
	kernelPolicy, err := s.store.LoadKernelPolicyAuthority()
	if err != nil {
		writeStoreError(w, "get kernel policy authority", err)
		return
	}
	canary, err := s.liveCanaryAuthorityView()
	if err != nil {
		writeStoreError(w, "get live canary authority", err)
		return
	}
	account := s.accountProvider()
	if account == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "account provider unavailable"})
		return
	}
	halted, haltReason := s.haltSnapshot()
	var window marketWindow
	if err := s.store.WithLedgerLock(false, func(gate store.OperationGate) error {
		var err error
		window, err = s.databaseMarketWindow(gate)
		return err
	}); err != nil {
		writeStoreError(w, "get state market window", err)
		return
	}
	snapshot, err := s.captureProviderSnapshot(r.Context(), "read_model")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "provider snapshot unavailable"})
		return
	}
	acct, pos, orders := snapshot.Account, snapshot.Positions, snapshot.Orders
	var liveDay, shadowDay risk.DayState
	if err := s.store.WithLedgerLock(false, func(gate store.OperationGate) error {
		liveWindow, err := s.databaseMarketWindow(gate)
		if err != nil {
			return err
		}
		if !liveWindow.day.Equal(window.day) {
			return errMarketDayAdvanced
		}
		window = liveWindow
		liveDay, err = s.dayStateAtAccountWithLimits(r.Context(), gate, false, acct, window, halted, haltReason, kernelPolicy.Policy)
		return err
	}); err != nil {
		writeStoreError(w, "get live day state", err)
		return
	}
	if err := s.store.WithLedgerLock(true, func(gate store.OperationGate) error {
		shadowAccount, err := s.shadowAccountSnapshotWithLimits(r.Context(), gate, kernelPolicy.Policy)
		if err != nil {
			return err
		}
		shadowWindow, err := s.databaseMarketWindow(gate)
		if err != nil {
			return err
		}
		if !shadowWindow.day.Equal(window.day) {
			return errMarketDayAdvanced
		}
		shadowDay, err = s.dayStateAtAccountWithLimits(r.Context(), gate, true, shadowAccount, shadowWindow, halted, haltReason, kernelPolicy.Policy)
		return err
	}); err != nil {
		writeStoreError(w, "get shadow day state", err)
		return
	}
	fills, fillObservation, fillObjects, err := s.captureProviderFills(
		r.Context(), "read_model", snapshot.Observation.AccountID, snapshot.Observation.Source, window.start,
	)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "fill data unavailable"})
		return
	}
	brokerObjects := append([]store.BrokerObservedObject(nil), snapshot.View.Objects...)
	brokerObjects = append(brokerObjects, fillObjects...)
	// RecentFills is fetched after both ledger snapshots. Re-check the database
	// clock before publishing so a request that crosses market midnight never
	// combines a prior-day risk snapshot with next-day fills.
	if err := s.store.WithLedgerLock(false, func(gate store.OperationGate) error {
		return s.ensureMarketDay(gate, window)
	}); err != nil {
		writeStoreError(w, "verify state market day", err)
		return
	}
	liveExecutionGate, err := s.store.GetLiveExecutionGate()
	if err != nil {
		writeStoreError(w, "get live execution gate", err)
		return
	}
	coexistence, err := s.store.LoadBrokerCoexistenceView(snapshot.Observation.AccountID, 50)
	if err != nil {
		writeStoreError(w, "get broker coexistence view", err)
		return
	}
	canaryAttestations, err := s.store.LoadLiveCanaryDayAttestations(snapshot.Observation.AccountID, 10)
	if err != nil {
		writeStoreError(w, "get live canary day attestations", err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"account":                  acct,
		"positions":                pos,
		"open_orders":              orders,
		"recent_fills":             fills,
		"day":                      map[string]risk.DayState{"live": liveDay, "shadow": shadowDay},
		"live_execution_gate":      liveExecutionGate,
		"db_live_canary":           canary,
		"db_kernel_policy":         kernelPolicy,
		"broker_observation":       snapshot.Observation,
		"broker_fill_observation":  fillObservation,
		"broker_objects":           brokerObjects,
		"broker_coexistence":       coexistence,
		"live_canary_attestations": canaryAttestations,
		"mode":                     s.tradingMode(),
		"source":                   "kernel",
		"as_of":                    window.asOf,
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
	var window marketWindow
	var quote *broker.Quote
	// marketDataDiag preserves the specific reason a quote was missing or
	// unusable. Without it, a fetch failure, a stale quote, and an insane quote
	// all collapse into the generic "market_data_unavailable" reject with no
	// trace of the underlying provider error.
	var marketDataDiag string
	var closeInstrument *broker.Instrument
	var decisionSnapshot *providerSnapshot
	if !op.Shadow && (op.Action == "close" || op.Action == "cancel") {
		decisionSnapshot, err = s.captureReconciledProviderDecision(r.Context())
		if err != nil {
			if errors.Is(err, errAccountBindingViolation) {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "account_binding_violation"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "broker decision snapshot unavailable"})
			return
		}
		bindDecisionObservation(&op, decisionSnapshot)
		if op.Action == "cancel" {
			classifyCancelDecision(decisionSnapshot, &op)
		}
	}
	if op.Action == "open" || op.Action == "close" {
		sym := op.Symbol
		if sym == "" {
			sym = op.Underlying
		}
		quoteProvider := s.marketProvider()
		quoteProviderLabel := "base"
		if s.tradingMode() == config.ModeLive && !op.Shadow {
			quoteProvider = s.authorityMarketProvider()
			quoteProviderLabel = "authority"
		}
		quoteCtx, cancel := context.WithTimeout(r.Context(), s.brokerCallTimeout())
		q, quoteErr := quoteProvider.Quote(quoteCtx, sym)
		cancel()
		if quoteErr == nil {
			quote = &q
		} else {
			marketDataDiag = fmt.Sprintf("quote_fetch_failed(provider=%s): %v", quoteProviderLabel, quoteErr)
			log.Printf("propose %s: %s symbol=%s", op.Action, marketDataDiag, sym)
		}
		if op.Action == "close" && s.tradingMode() == config.ModeLive && !op.Shadow {
			instrumentCtx, cancel := context.WithTimeout(r.Context(), s.brokerCallTimeout())
			instrument, instrumentErr := s.authorityMarketProvider().Instrument(instrumentCtx, sym)
			cancel()
			if instrumentErr == nil {
				closeInstrument = &instrument
			}
		}
	}
	if op.Action == "open" {
		if err := s.refreshGlobalHalt(); err != nil {
			writeStoreError(w, "refresh global halt", err)
			return
		}
	}
	var acct broker.AccountState
	opID := store.NewID()
	var halted bool
	var haltReason string
	// This snapshot improves local classification messages only. Admission and
	// Provider-send authority are serialized in PostgreSQL, so no process lock
	// is held across account or Provider network calls.
	halted, haltReason = s.haltSnapshot()
	var v risk.Verdict
	status := "auto_approved"
	var replay *store.OperationRow
	var executionAttempt *store.ExecutionAttempt
	var controlEpisode *store.ExternalControlEpisode
	var committedProposalError error
	if err := s.store.WithProposalLock(identity, op.Shadow, op.Action == "open", func(gate store.OperationGate) error {
		verifyOpenMarketDay := func() error {
			if op.Action != "open" {
				return nil
			}
			return s.ensureMarketDay(gate, window)
		}
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
		kernelAuthority, err := gate.KernelPolicyAuthority()
		if err != nil {
			return err
		}
		effectiveLimits := kernelAuthority.Policy
		if op.Action == "open" {
			op = s.deriveOpenOperationWithLimits(r.Context(), op, quote, effectiveLimits)
		}
		if op.Action == "open" {
			// M3A deliberately fetches account state after acquiring the stable
			// per-ledger gate. A prior fill cannot leave this proposal classifying
			// against a stale pre-lock snapshot.
			var err error
			if op.Shadow {
				acct, err = s.shadowAccountSnapshotWithLimits(r.Context(), gate, effectiveLimits)
			} else {
				accountCtx, cancel := context.WithTimeout(r.Context(), s.brokerCallTimeout())
				acct, err = s.authorityAccountProvider().Account(accountCtx)
				cancel()
			}
			if err != nil {
				return fmt.Errorf("%w: account", errBrokerDataUnavailable)
			}
			// Derive the market day from an advancing database clock after the
			// bounded account read. PostgreSQL now() is transaction-start time and
			// would stamp the old day after a lock wait across midnight.
			window, err = s.databaseMarketWindow(gate)
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
				if decisionSnapshot == nil {
					return fmt.Errorf("%w: decision snapshot", errBrokerDataUnavailable)
				}
				positions := decisionSnapshot.Positions
				if s.tradingMode() == config.ModeSim {
					// Simulation has no live execution latch/pre-effect barrier. Its
					// in-memory Provider read is non-networked, so refresh under the symbol
					// gate to preserve the same no-reversal invariant as Live.
					positionsCtx, cancel := context.WithTimeout(r.Context(), s.brokerCallTimeout())
					positions, err = s.authorityAccountProvider().Positions(positionsCtx)
					cancel()
					if err != nil {
						return fmt.Errorf("%w: positions", errBrokerDataUnavailable)
					}
					if !samePositionDecision(op, decisionSnapshot.Positions, positions) {
						return fmt.Errorf("%w: decision position changed", errInvalidClose)
					}
				}
				positionQty, err := closablePositionQuantity(op, positions)
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
				if normalized.Kind == "equity" && normalized.InstrumentID == "" {
					if closeInstrument == nil || closeInstrument.InstrumentID == "" ||
						closeInstrument.Symbol != symbol || closeInstrument.Kind != "equity" ||
						closeInstrument.Multiplier != normalized.Multiplier || !closeInstrument.PrecisionSane() {
						return fmt.Errorf("%w: equity position instrument identity is unavailable", errInvalidClose)
					}
					normalized.InstrumentID = closeInstrument.InstrumentID
					normalized.QtyIncrement = closeInstrument.QtyIncrement
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
				externalWorking, err := externalWorkingCloseQuantity(decisionSnapshot, normalized)
				if err != nil {
					return fmt.Errorf("%w: %v", errInvalidClose, err)
				}
				reserved, err := units.AddQty(held, externalWorking)
				if err != nil || reserved > positionQty || op.Qty > positionQty-reserved {
					committedProposalError = errInsufficientClosableQuantity
					return nil
				}
				normalized.Qty = op.Qty
				trackedQty := minQty(op.Qty, exposureQty)
				externalQty := op.Qty - trackedQty
				normalized.TrackedCloseQty = trackedQty
				normalized.ExternalCloseQty = externalQty
				normalized.DecisionObservationID = op.DecisionObservationID
				normalized.DecisionObservationGeneration = op.DecisionObservationGeneration
				normalized.DecisionObservationDigest = op.DecisionObservationDigest
				normalized.BrokerPositionID = matchingPositionID(op, positions)
				for i := range positions {
					if positions[i].PositionID == normalized.BrokerPositionID {
						normalized.DecisionPositionQty = positions[i].Qty
						break
					}
				}
				normalized.BrokerObjectOrigin = observedObjectOrigin(
					decisionSnapshot, store.BrokerFamilyPositions, normalized.BrokerPositionID,
				)
				op = normalized
				if positionQty != exposureQty {
					origin := "mixed"
					if exposureQty == 0 {
						origin = "external"
					}
					controlEpisode = &store.ExternalControlEpisode{
						ID: store.NewID(), OperationID: opID, ControlAction: "close_position",
						Origin: origin, BrokerObservationID: op.DecisionObservationID,
						ObservationGeneration: op.DecisionObservationGeneration,
						ObjectKey:             op.BrokerPositionID, RequestedQty: op.Qty,
						TrackedQty: trackedQty, ExternalQty: externalQty,
					}
				}
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
			day, err = s.dayStateAtAccountWithLimits(r.Context(), gate, op.Shadow, acct, window, halted, haltReason, effectiveLimits)
			if err != nil {
				return err
			}
		}
		v = risk.Classify(op, effectiveLimits, day, quote)
		if op.Action == "close" && !op.Shadow && (quote == nil || !quote.Usable(effectiveLimits.QuoteMaxAgeSec, time.Now().UTC())) {
			v = risk.Verdict{Class: "REJECT", Reasons: []string{"market_data_unavailable"}}
		}
		var canaryAuthority *store.LiveCanaryRevision
		if v.Class == "B" && op.Action == "open" && !op.Shadow {
			reason, usage, authority, err := s.liveCanaryRefusal(gate, opID, window.day, op.DerivedMaxRisk, op.Qty, op.QtyIncrement)
			if err != nil {
				return err
			}
			canaryAuthority = authority
			if reason != "" {
				v = risk.Verdict{Class: "REJECT", Reasons: []string{reason}}
				if err := s.insertCanaryRefusalEvent(gate, opID, reason, window.day, op.DerivedMaxRisk, op.Qty, op.QtyIncrement, usage, authority); err != nil {
					return err
				}
			}
		}
		liveEffect := s.tradingMode() == config.ModeLive && !op.Shadow &&
			(op.Action == "open" || op.Action == "close" || op.Action == "cancel")
		if liveEffect && (v.Class == "A" || v.Class == "B") {
			// The idempotency lookup above must stay first. This gate is the last
			// read before any operation entitlement is persisted, so a busy or
			// unknown account cannot accumulate stale Live work behind its latch.
			if err := gate.RequireLiveExecutionIdle(op.Action == "open"); err != nil {
				return err
			}
		}
		class := v.Class
		switch v.Class {
		case "REJECT":
			class, status = "C", "rejected"
		case "C":
			status = "pending_review"
		}
		policyBinding, err := gate.InsertOperationBound(opID, op.Proposer, class, status, op, v, identity, kernelAuthority)
		if err != nil {
			return err
		}
		if err := gate.InsertEvent("operation_proposed", map[string]any{
			"id": opID, "op": op, "verdict": v, "kernel_policy": policyBinding,
		}); err != nil {
			return err
		}
		if op.Action == "cancel" && op.VerifiedReduction &&
			(op.BrokerObjectOrigin == "external" || op.BrokerObjectOrigin == "ambiguous") {
			controlEpisode = &store.ExternalControlEpisode{
				ID: store.NewID(), OperationID: opID, ControlAction: "cancel_order",
				Origin: op.BrokerObjectOrigin, BrokerObservationID: op.DecisionObservationID,
				ObservationGeneration: op.DecisionObservationGeneration, ObjectKey: op.BrokerOrderID,
			}
		}
		if controlEpisode != nil && (v.Class == "A" || v.Class == "B") {
			if err := gate.InsertExternalControlEpisode(*controlEpisode); err != nil {
				return err
			}
			if err := gate.InsertEvent("external_control_episode_created", map[string]any{
				"operation_id": opID, "episode_id": controlEpisode.ID,
				"control_action": controlEpisode.ControlAction, "origin": controlEpisode.Origin,
				"broker_observation_id": controlEpisode.BrokerObservationID,
				"object_key":            controlEpisode.ObjectKey, "requested_qty": controlEpisode.RequestedQty,
				"tracked_qty": controlEpisode.TrackedQty, "external_qty": controlEpisode.ExternalQty,
			}); err != nil {
				return err
			}
		}
		if v.Class == "B" && op.Action == "open" {
			var canaryRevisionID int64
			if canaryAuthority != nil {
				canaryRevisionID = canaryAuthority.ID
			}
			if err := gate.InsertTradeGrant(store.TradeGrant{
				OperationID: opID, Ledger: ledgerName(op.Shadow), MarketDay: window.day,
				AuthorizedRisk: op.DerivedMaxRisk, RiskSource: "computed",
				LiveCanaryRevisionID: canaryRevisionID,
			}); err != nil {
				return err
			}
			grantEvent := map[string]any{
				"operation_id": opID, "ledger": ledgerName(op.Shadow), "kernel_policy": policyBinding,
			}
			if binding := liveCanaryEventBinding(canaryAuthority); binding != nil {
				grantEvent["live_canary"] = binding
			}
			if err := gate.InsertEvent("trade_grant_created", grantEvent); err != nil {
				return err
			}
		}
		if (v.Class != "A" && v.Class != "B") || op.Action == "tighten_stop" {
			return verifyOpenMarketDay()
		}
		attempt := store.ExecutionAttempt{
			ID: store.NewID(), OperationID: opID, Seq: 1, State: "pending",
		}
		switch op.Action {
		case "open", "close":
			limit, err := executionLimit(op, quote, effectiveLimits.QuoteMaxAgeSec)
			if err != nil {
				return err
			}
			attempt.Intent = "place"
			attempt.ClientOrderID = store.NewID()
			if op.Shadow {
				attempt.Intent = "paper_place"
				attempt.ClientOrderID = "shadow:" + attempt.ID
				if quote == nil || !quote.Usable(effectiveLimits.QuoteMaxAgeSec, time.Now().UTC()) {
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
			return verifyOpenMarketDay()
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
				ApprovedPriceBound: executionPriceBound(op, attempt.Limit), State: "new",
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
		// Keep this as the final statement in the open transaction. If staging
		// crossed market midnight, every entitlement row rolls back together.
		return verifyOpenMarketDay()
	}); err != nil {
		if errors.Is(err, errIdempotencyKeyReused) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "idempotency_key_reused"})
			return
		}
		if errors.Is(err, store.ErrLiveExecutionBusy) {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusConflict, map[string]string{"error": "live_execution_busy"})
			return
		}
		if errors.Is(err, store.ErrLiveExecutionSuspended) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "live_execution_suspended"})
			return
		}
		if errors.Is(err, store.ErrLiveSendHalted) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "global_halt_active"})
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
		if detail := marketDataRejectionDetail(marketDataDiag, v.Reasons, quote, s.limits.QuoteMaxAgeSec); detail != "" {
			resp["market_data_detail"] = detail
			log.Printf("propose %s rejected: operation_id=%s market_data_detail=%s", op.Action, opID, detail)
		}
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
		if errors.Is(err, errPreEffectRejected) {
			resp["status"] = "failed"
			resp["attempt_id"] = executionAttempt.ID
			resp["attempt_state"] = "failed"
			resp["error"] = "proposal_stale"
			writeJSON(w, http.StatusConflict, resp)
			return
		}
		if errors.Is(err, errPreEffectUnavailable) {
			resp["status"] = "failed"
			resp["attempt_id"] = executionAttempt.ID
			resp["attempt_state"] = "failed"
			resp["error"] = "pre_effect_unavailable"
			writeJSON(w, http.StatusBadGateway, resp)
			return
		}
		if errors.Is(err, errBrokerMutationFailed) {
			resp["status"] = "failed"
			resp["attempt_id"] = executionAttempt.ID
			resp["attempt_state"] = "failed"
			resp["error"] = "broker_mutation_failed"
			writeJSON(w, http.StatusBadGateway, resp)
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
	return s.shadowAccountSnapshotWithLimits(ctx, gate, s.limits)
}

func (s *server) shadowAccountSnapshotWithLimits(ctx context.Context, gate store.OperationGate, limits config.Limits) (broker.AccountState, error) {
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
		if err != nil || !quote.Usable(limits.QuoteMaxAgeSec, time.Now().UTC()) {
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
	errBrokerMutationFailed         = errors.New("broker mutation failed")
	errBrokerResultUnknown          = errors.New("broker result uncertain")
	errPreEffectRejected            = errors.New("pre-effect proposal rejected")
	errPreEffectUnavailable         = errors.New("pre-effect facts unavailable")
	errInvalidClose                 = errors.New("invalid close")
	errPaperExecutionFailed         = errors.New("paper execution failed")
	errReviewGateRejected           = errors.New("review gate rejected")
	errMarketDayAdvanced            = errors.New("market day advanced during locked decision")
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

func closablePositionQuantity(op risk.Operation, positions []broker.Position) (units.Qty, error) {
	symbol := operationSymbol(op)
	var matched *broker.Position
	for i := range positions {
		position := &positions[i]
		if position.Symbol != symbol || position.Qty == 0 ||
			(op.PositionID != "" && position.PositionID != op.PositionID) {
			continue
		}
		if matched != nil {
			return 0, fmt.Errorf("close position identity is ambiguous for %s", symbol)
		}
		matched = position
	}
	if matched != nil {
		quantity, err := units.AbsQty(matched.Qty)
		if err != nil {
			return 0, fmt.Errorf("position quantity is out of range")
		}
		return quantity, nil
	}
	return 0, fmt.Errorf("close requires an existing position for %s", symbol)
}

func matchingPositionID(op risk.Operation, positions []broker.Position) string {
	symbol := operationSymbol(op)
	for _, position := range positions {
		if position.Symbol == symbol && position.Qty != 0 &&
			(op.PositionID == "" || position.PositionID == op.PositionID) {
			return position.PositionID
		}
	}
	return ""
}

func samePositionDecision(op risk.Operation, decision, current []broker.Position) bool {
	symbol := operationSymbol(op)
	find := func(positions []broker.Position) *broker.Position {
		for i := range positions {
			if positions[i].Symbol == symbol && positions[i].Qty != 0 &&
				(op.PositionID == "" || positions[i].PositionID == op.PositionID) {
				return &positions[i]
			}
		}
		return nil
	}
	left, right := find(decision), find(current)
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.PositionID == right.PositionID && left.InstrumentID == right.InstrumentID &&
		left.Qty == right.Qty && left.Kind == right.Kind && left.Multiplier == right.Multiplier
}

func minQty(left, right units.Qty) units.Qty {
	if left < right {
		return left
	}
	return right
}

// priceBoundedOpen reports whether an open order's fill is capped by its own
// declared limit (limit or stop-limit with an explicit limit). Such an order
// can be priced and risk-bounded without a live quote, so the quote-freshness
// gates at propose and pre-effect do not apply to it. Market and stop-market
// orders (unbounded fill) and shadow paper fills are never price-bounded here.
func priceBoundedOpen(op risk.Operation) bool {
	return (op.OrderType == "limit" || op.OrderType == "stop_limit") && !op.Shadow && op.Limit != nil
}

func executionLimit(op risk.Operation, quote *broker.Quote, maxAgeSec int) (units.Micros, error) {
	if op.Action == "open" {
		if op.OrderType == "market" || op.OrderType == "stop_market" {
			if op.ApprovedPriceCap <= 0 {
				return 0, fmt.Errorf("no authorized risk bound for unpriced open")
			}
			return op.ApprovedPriceCap, nil
		}
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

func executionPriceBound(op risk.Operation, fallback units.Micros) units.Micros {
	if op.Action == "open" && op.ApprovedPriceCap > 0 {
		return op.ApprovedPriceCap
	}
	if op.Action == "close" && op.Limit != nil && *op.Limit > 0 {
		return *op.Limit
	}
	return fallback
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
	attempt, err := s.claimPendingAttempt(attemptID)
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

func (s *server) claimPendingAttempt(attemptID string) (*store.ExecutionAttempt, error) {
	if s.tradingMode() == config.ModeLive {
		return s.store.ClaimPendingAttemptLive(attemptID, s.workerID(), s.attemptClaimTimeout())
	}
	return s.store.ClaimPendingAttempt(attemptID, s.workerID(), s.attemptClaimTimeout())
}

func (s *server) claimRecoverableAttempt(seen *store.ExecutionAttempt) (*store.ExecutionAttempt, error) {
	if s.tradingMode() == config.ModeLive {
		return s.store.ClaimRecoverableAttemptLive(seen.ID, s.workerID(), seen.State, seen.Attempt, s.attemptClaimTimeout())
	}
	return s.store.ClaimRecoverableAttempt(seen.ID, s.workerID(), seen.State, seen.Attempt, s.attemptClaimTimeout())
}

func (s *server) executeClaimedAttempt(ctx context.Context, attempt *store.ExecutionAttempt) (map[string]any, error) {
	return s.executeClaimedAttemptWithReplay(ctx, attempt, false)
}

func (s *server) executeClaimedAttemptWithReplay(ctx context.Context, attempt *store.ExecutionAttempt, replay bool) (map[string]any, error) {
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
	if s.tradingMode() == config.ModeLive && attempt.Intent == "place" && !supportsOrderKind(execution, op.Kind) {
		_, resolveErr := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
			State: "failed", LastError: "provider order kind is not certified",
			ProviderErrorCode: "unsupported_order_kind", OperationStatus: "failed", ReleaseReservation: true,
		})
		return nil, errors.Join(fmt.Errorf("provider order kind is not certified"), resolveErr)
	}
	if op.Action == "close" && attempt.CloseReservationID == "" {
		return nil, fmt.Errorf("refusing close without reservation")
	}
	var placeRequest broker.PlaceRequest
	var replayEvidence *store.ProviderIntentEvidence
	if attempt.Intent == "place" {
		placeRequest = persistedPlaceRequest(op, attempt)
		if s.tradingMode() == config.ModeLive {
			canonical, fingerprint, evidenceErr := canonicalProviderIntent(placeRequest, op.InstrumentID)
			if evidenceErr != nil {
				_, resolveErr := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
					State: "failed", LastError: "provider intent evidence invalid",
					ProviderErrorCode: "intent_invalid", OperationStatus: "failed", ReleaseReservation: true,
				})
				return nil, errors.Join(evidenceErr, resolveErr)
			}
			boundAccountID := s.boundRobinhoodAccountID()
			if boundAccountID == "" {
				return nil, fmt.Errorf("live provider account binding is unavailable")
			}
			if replay {
				if attempt.ProviderAccountID != boundAccountID || !providerIntentEvidenceMatches(attempt, canonical, fingerprint) {
					_, resolveErr := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
						State: "unknown", LastError: "same-ref replay evidence mismatch",
						ProviderErrorCode: "replay_intent_mismatch",
					})
					return nil, errors.Join(fmt.Errorf("same-ref replay evidence mismatch"), resolveErr)
				}
				replayEvidence = &store.ProviderIntentEvidence{
					AccountID: boundAccountID, Canonical: canonical, Fingerprint: fingerprint,
				}
			} else {
				prepared, evidenceErr := s.store.PrepareAttemptProviderIntent(
					attempt.ID, attempt.Attempt, boundAccountID, canonical, fingerprint,
				)
				if evidenceErr != nil || !prepared {
					return nil, errors.Join(evidenceErr, fmt.Errorf("provider intent evidence was not durably prepared"))
				}
			}
		}
	}
	var preEffect *store.PreEffectManifest
	if s.tradingMode() == config.ModeLive {
		preEffect, err = s.captureLivePreEffect(ctx, attempt, op, replay)
	}
	if err != nil {
		if replay {
			code := "pre_effect_unavailable"
			message := "pre-effect refresh failed before same-reference replay"
			if errors.Is(err, errPreEffectRejected) {
				code = "pre_effect_rejected"
				message = "pre-effect barrier rejected same-reference replay"
			}
			unknownErr := s.keepAttemptUnknown(attempt, message, code, "")
			return nil, errors.Join(err, unknownErr)
		}
		providerCode := "pre_effect_unavailable"
		lastError := "pre-effect refresh failed"
		if errors.Is(err, errPreEffectRejected) {
			providerCode = "proposal_stale"
			lastError = "proposal rejected at pre-effect barrier"
		}
		_, resolveErr := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
			State: "failed", LastError: lastError,
			ProviderErrorCode: providerCode,
			OperationStatus:   "failed", ReleaseReservation: true,
		})
		if resolveErr != nil {
			return nil, resolveErr
		}
		return nil, err
	}
	if s.tradingMode() == config.ModeLive {
		marked, markErr := s.store.MarkAttemptSentWithManifest(
			attempt.ID, attempt.Attempt, replay, s.brokerCallTimeout()+s.replayCreationGuard, replayEvidence, preEffect.ID,
		)
		if errors.Is(markErr, store.ErrReplayWindowExpired) {
			unknownErr := s.keepAttemptUnknown(attempt, "same-ref replay window expired", "replay_window_expired", "")
			return nil, errors.Join(markErr, unknownErr)
		}
		if errors.Is(markErr, store.ErrLiveSendHalted) {
			if replay {
				unknownErr := s.keepAttemptUnknown(attempt, "same-ref replay suppressed by global halt", "replay_suppressed_halt", "")
				return nil, errors.Join(markErr, unknownErr)
			}
			resolved, resolveErr := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
				State: "failed", LastError: "global halt committed before provider send",
				ProviderErrorCode: "send_suppressed_halt", OperationStatus: "failed", ReleaseReservation: true,
				OrderUpdate: &store.OrderUpdate{ExecutionAttemptID: attempt.ID, State: "rejected"},
			})
			if resolveErr != nil {
				return nil, errors.Join(markErr, resolveErr)
			}
			if !resolved {
				current, readErr := s.store.GetExecutionAttempt(attempt.ID)
				if readErr != nil {
					return nil, errors.Join(readErr, fmt.Errorf("halt suppressed provider send but cleanup lost fencing"))
				}
				return nil, fmt.Errorf("halt suppressed provider send but cleanup lost fencing: state=%s fencing_token=%d", current.State, current.Attempt)
			}
			return nil, errors.Join(markErr, errBrokerMutationFailed)
		}
		if replay && (markErr != nil || !marked) {
			code := "replay_send_fence_lost"
			message := "same-reference replay send fence was not available"
			if errors.Is(markErr, store.ErrPreEffectStale) {
				code = "pre_effect_rejected"
				message = "pre-effect authority changed before same-reference replay"
			}
			unknownErr := s.keepAttemptUnknown(attempt, message, code, "")
			return nil, errors.Join(markErr, unknownErr, fmt.Errorf("provider replay send was not durably marked"))
		}
		if errors.Is(markErr, store.ErrPreEffectStale) {
			_, resolveErr := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
				State: "failed", LastError: "proposal changed before provider send",
				ProviderErrorCode: "proposal_stale", OperationStatus: "failed", ReleaseReservation: true,
			})
			return nil, errors.Join(errPreEffectRejected, markErr, resolveErr)
		}
		if markErr != nil || !marked {
			return nil, errors.Join(markErr, fmt.Errorf("provider send was not durably marked"))
		}
	}

	brokerCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	var result broker.OrderResult
	if attempt.Intent == "cancel" {
		result, err = execution.CancelOrder(brokerCtx, attempt.TargetBrokerOrderID)
	} else {
		result, err = execution.PlaceOrder(brokerCtx, placeRequest)
	}
	cancel()
	if err != nil {
		kind, code, detail, classified := rhmcp.MutationErrorFacts(err)
		if !replay && classified && (kind == "not_sent" || kind == "rejected") {
			lastError := "provider mutation " + kind
			if code != "" {
				lastError += " (" + code + ")"
			}
			if detail != "" {
				lastError += ": " + detail
			}
			_, resolveErr := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
				State: "failed", LastError: lastError,
				ProviderErrorCode: code, OperationStatus: "failed", ReleaseReservation: true,
			})
			if resolveErr != nil {
				return nil, resolveErr
			}
			return nil, fmt.Errorf("%w", errBrokerMutationFailed)
		}
		_, resolveErr := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
			State: "unknown", LastError: "provider mutation outcome unknown", ProviderErrorCode: code,
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

func persistedPlaceRequest(op risk.Operation, attempt *store.ExecutionAttempt) broker.PlaceRequest {
	positionEffect := "open"
	if op.Action == "close" {
		positionEffect = "close"
	}
	orderType := op.OrderType
	if orderType == "" {
		orderType = "limit"
	}
	return broker.PlaceRequest{
		ClientOrderID: attempt.ClientOrderID, Symbol: operationSymbol(op), Side: op.Side,
		PositionEffect: positionEffect, OrderType: orderType,
		Qty: attempt.Qty, Limit: attempt.Limit, StopPrice: valueOrZero(op.StopPrice), Kind: op.Kind,
	}
}

func valueOrZero(value *units.Micros) units.Micros {
	if value == nil {
		return 0
	}
	return *value
}

func canonicalProviderIntent(req broker.PlaceRequest, instrumentID string) (json.RawMessage, []byte, error) {
	intent := broker.ProviderPlaceIntent{
		Kind: req.Kind, InstrumentID: strings.TrimSpace(instrumentID), Symbol: req.Symbol,
		Side: req.Side, PositionEffect: req.PositionEffect, Qty: req.Qty,
		TimeInForce: "gfd", MarketHours: "regular_hours",
	}
	switch req.OrderType {
	case "limit":
		intent.OrderType, intent.Trigger = "limit", "immediate"
		intent.Limit = req.Limit
	case "market":
		intent.OrderType, intent.Trigger = "market", "immediate"
	case "stop_limit":
		intent.OrderType, intent.Trigger = "limit", "stop"
		intent.Limit, intent.StopPrice = req.Limit, req.StopPrice
	case "stop_market":
		intent.OrderType, intent.Trigger = "market", "stop"
		intent.StopPrice = req.StopPrice
	default:
		return nil, nil, fmt.Errorf("provider place intent is incomplete")
	}
	if intent.InstrumentID == "" || strings.TrimSpace(intent.Symbol) == "" ||
		(intent.Kind != "equity" && intent.Kind != "option") ||
		(intent.Side != "buy" && intent.Side != "sell") ||
		(intent.PositionEffect != "open" && intent.PositionEffect != "close") ||
		intent.Qty <= 0 ||
		(intent.OrderType == "limit" && intent.Limit <= 0) ||
		(intent.OrderType == "market" && (intent.Kind != "equity" || intent.Side != "buy" || intent.Limit != 0)) ||
		(intent.Trigger == "immediate" && intent.StopPrice != 0) ||
		(intent.Trigger == "stop" && intent.StopPrice <= 0) ||
		(intent.Trigger != "immediate" && intent.Trigger != "stop") ||
		(intent.OrderType != "limit" && intent.OrderType != "market") {
		return nil, nil, fmt.Errorf("provider place intent is incomplete")
	}
	canonical, err := json.Marshal(intent)
	if err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(canonical)
	return canonical, digest[:], nil
}

func providerIntentEvidenceMatches(attempt *store.ExecutionAttempt, canonical json.RawMessage, fingerprint []byte) bool {
	var persisted broker.ProviderPlaceIntent
	if json.Unmarshal(attempt.ProviderIntent, &persisted) != nil {
		return false
	}
	persistedCanonical, err := json.Marshal(persisted)
	if err != nil || !bytes.Equal(persistedCanonical, canonical) {
		return false
	}
	digest := sha256.Sum256(persistedCanonical)
	return bytes.Equal(digest[:], fingerprint) && bytes.Equal(digest[:], attempt.IntentFingerprint)
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
	if err := validateAttemptPolicyBinding(row, attempt); err != nil {
		return nil, err
	}
	_, currentLimits, err := s.operationPolicyPair(row)
	if err != nil {
		return nil, err
	}
	quoteMaxAge := currentLimits.QuoteMaxAgeSec
	if attempt.QuoteMaxAgeSec > 0 {
		quoteMaxAge = minInt(attempt.QuoteMaxAgeSec, currentLimits.QuoteMaxAgeSec)
	}
	quoteCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	quote, err := s.marketProvider().Quote(quoteCtx, operationSymbol(op))
	cancel()
	if err != nil || !quote.Usable(quoteMaxAge, time.Now().UTC()) {
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
	OrderType         string            `json:"order_type"`
	ExecutionStyle    string            `json:"execution_style"`
	Qty               units.Qty         `json:"qty"`
	Limit             *units.Micros     `json:"limit"`
	StopPrice         *units.Micros     `json:"stop_price"`
	MaxRiskUSD        *units.Micros     `json:"max_risk_usd"`
	Short             bool              `json:"short"`
	Plan              map[string]string `json:"plan"`
	Thesis            string            `json:"thesis"`
	Setup             string            `json:"setup"`
	Shadow            bool              `json:"shadow"`
	BrokerOrderID     string            `json:"broker_order_id,omitempty"`
	PositionID        string            `json:"position_id,omitempty"`
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
		OrderType: op.OrderType, ExecutionStyle: op.ExecutionStyle,
		Qty: op.Qty, Limit: op.Limit, StopPrice: op.StopPrice, MaxRiskUSD: op.MaxRiskUSD,
		Short: op.Short, Plan: op.Plan, Thesis: op.Thesis, Setup: op.Setup,
		Shadow: op.Shadow, BrokerOrderID: op.BrokerOrderID, PositionID: op.PositionID,
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
		OrderType: request.OrderType, ExecutionStyle: strings.TrimSpace(request.ExecutionStyle),
		Qty: request.Qty, Limit: request.Limit, StopPrice: request.StopPrice, MaxRiskUSD: request.MaxRiskUSD,
		Short: request.Short, Plan: request.Plan, Thesis: request.Thesis,
		Setup: request.Setup, Shadow: request.Shadow,
		BrokerOrderID:     request.BrokerOrderID,
		PositionID:        request.PositionID,
		ClosesOperationID: request.ClosesOperationID,
	}
	symbol := op.Symbol
	if symbol == "" {
		symbol = op.Underlying
	}

	switch op.Action {
	case "open":
		if op.OrderType == "" {
			op.OrderType = "limit"
		}
		if op.ExecutionStyle == "" {
			op.ExecutionStyle = "static"
		}
		if op.ExecutionStyle != "static" && op.ExecutionStyle != "managed" {
			return op, fmt.Errorf("bad execution_style %q", op.ExecutionStyle)
		}
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
		switch op.OrderType {
		case "limit":
			if op.StopPrice != nil {
				return op, fmt.Errorf("limit orders must not include stop_price")
			}
		case "market":
			if op.ExecutionStyle != "static" {
				return op, fmt.Errorf("market orders require static execution_style")
			}
			if op.Kind != "equity" || op.Side != "buy" {
				return op, fmt.Errorf("market orders currently support equity buys only")
			}
			if op.Qty%units.Qty(units.Scale) != 0 {
				return op, fmt.Errorf("market orders require a whole number of shares")
			}
			if op.Limit != nil {
				return op, fmt.Errorf("market orders must not include limit")
			}
			if op.MaxRiskUSD == nil || *op.MaxRiskUSD <= 0 {
				return op, fmt.Errorf("market orders require positive max_risk_usd")
			}
			if op.StopPrice != nil {
				return op, fmt.Errorf("market orders must not include stop_price")
			}
		case "stop_market":
			if op.ExecutionStyle != "static" {
				return op, fmt.Errorf("stop-market orders require static execution_style")
			}
			if op.Kind != "equity" || op.Side != "buy" {
				return op, fmt.Errorf("stop-market orders currently support equity buys only")
			}
			if op.Qty%units.Qty(units.Scale) != 0 {
				return op, fmt.Errorf("stop-market orders require a whole number of shares")
			}
			if op.Limit != nil {
				return op, fmt.Errorf("stop-market orders must not include limit")
			}
			if op.StopPrice == nil || *op.StopPrice <= 0 {
				return op, fmt.Errorf("stop-market orders require positive stop_price")
			}
			if op.MaxRiskUSD == nil || *op.MaxRiskUSD <= 0 {
				return op, fmt.Errorf("stop-market orders require positive max_risk_usd")
			}
		case "stop_limit":
			if op.ExecutionStyle != "static" {
				return op, fmt.Errorf("stop-limit orders require static execution_style")
			}
			if op.Kind != "equity" || op.Side != "buy" {
				return op, fmt.Errorf("stop-limit orders currently support equity buys only")
			}
			if op.Qty%units.Qty(units.Scale) != 0 {
				return op, fmt.Errorf("stop-limit orders require a whole number of shares")
			}
			if op.Limit == nil || *op.Limit <= 0 {
				return op, fmt.Errorf("stop-limit orders require positive limit")
			}
			if op.StopPrice == nil || *op.StopPrice <= 0 {
				return op, fmt.Errorf("stop-limit orders require positive stop_price")
			}
			if *op.Limit < *op.StopPrice {
				return op, fmt.Errorf("buy stop-limit limit must be at or above stop_price")
			}
		default:
			return op, fmt.Errorf("bad order_type %q", op.OrderType)
		}
	case "close":
		if op.OrderType != "" {
			return op, fmt.Errorf("order_type is currently supported only for open")
		}
		if op.ExecutionStyle == "" {
			op.ExecutionStyle = "static"
		}
		if op.ExecutionStyle != "static" && op.ExecutionStyle != "managed" {
			return op, fmt.Errorf("bad execution_style %q", op.ExecutionStyle)
		}
		if op.ExecutionStyle == "managed" && op.Limit == nil {
			return op, fmt.Errorf("managed close requires limit")
		}
		op.PositionID = strings.TrimSpace(op.PositionID)
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
		if op.ExecutionStyle != "" {
			return op, fmt.Errorf("execution_style is supported only for open or close")
		}
		if op.OrderType != "" {
			return op, fmt.Errorf("order_type is currently supported only for open")
		}
		op.BrokerOrderID = strings.TrimSpace(op.BrokerOrderID)
		if op.BrokerOrderID == "" {
			return op, fmt.Errorf("cancel requires broker_order_id")
		}
	case "tighten_stop":
		if op.ExecutionStyle != "" {
			return op, fmt.Errorf("execution_style is supported only for open or close")
		}
		if op.OrderType != "" {
			return op, fmt.Errorf("order_type is currently supported only for open")
		}
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
	if op.StopPrice != nil && *op.StopPrice <= 0 {
		return op, fmt.Errorf("stop_price must be greater than zero")
	}
	if op.Action != "open" && op.StopPrice != nil {
		return op, fmt.Errorf("stop_price is currently supported only for open")
	}
	if op.Action != "close" && strings.TrimSpace(op.ClosesOperationID) != "" {
		return op, fmt.Errorf("closes_operation_id is meaningful only on close")
	}
	if op.Action != "close" && strings.TrimSpace(op.PositionID) != "" {
		return op, fmt.Errorf("position_id is meaningful only on close")
	}
	return op, nil
}

func (s *server) deriveOpenOperation(ctx context.Context, op risk.Operation, quote *broker.Quote) risk.Operation {
	return s.deriveOpenOperationWithLimits(ctx, op, quote, s.limits)
}

func (s *server) deriveOpenOperationWithLimits(ctx context.Context, op risk.Operation, quote *broker.Quote, limits config.Limits) risk.Operation {
	quoteUsable := quote != nil && quote.Usable(limits.QuoteMaxAgeSec, time.Now().UTC())
	// A price-bounded order (limit or stop-limit) has its fill capped by its own
	// limit price, so it can be risk-capped and priced without a live quote.
	// Market and stop-market orders (unbounded fill, need marketability) and
	// shadow paper fills still require a usable quote and reject without one.
	if !quoteUsable && !priceBoundedOpen(op) {
		op.RejectReason = "market_data_unavailable"
		return op
	}
	symbol := op.Symbol
	if symbol == "" {
		symbol = op.Underlying
	}
	var precision *broker.Instrument
	if s.tradingMode() == config.ModeLive && !op.Shadow && !supportsOrderKind(s.executionProvider(), op.Kind) {
		op.RejectReason = "unsupported_contract"
		return op
	}

	switch op.Kind {
	case "equity":
		op.Multiplier = 1
		if s.tradingMode() == config.ModeLive && !op.Shadow {
			instrumentCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
			instrument, err := s.authorityMarketProvider().Instrument(instrumentCtx, symbol)
			cancel()
			if err != nil || instrument.Kind != "equity" || instrument.Multiplier != 1 ||
				!instrument.PrecisionSane() {
				op.RejectReason = "unsupported_contract"
				return op
			}
			op.InstrumentID = instrument.InstrumentID
			op.QtyIncrement = instrument.QtyIncrement
			precision = &instrument
		}
	case "option":
		instrumentCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
		instrument, err := s.authorityMarketProvider().Instrument(instrumentCtx, symbol)
		cancel()
		if err != nil || instrument.Kind != "option" || instrument.Multiplier != 100 ||
			!instrument.PrecisionSane() {
			op.RejectReason = "unsupported_contract"
			return op
		}
		op.Multiplier = instrument.Multiplier
		op.InstrumentID = instrument.InstrumentID
		op.QtyIncrement = instrument.QtyIncrement
		precision = &instrument
	default:
		op.RejectReason = "unsupported_contract"
		return op
	}

	if quoteUsable {
		op.WorkingPrice = quote.Ask
	}
	if op.StopPrice != nil {
		if op.Shadow {
			op.RejectReason = "shadow_stop_orders_not_supported"
			return op
		}
		// A newly proposed buy stop must rest above the market. Once approved,
		// crossing the trigger during the short pre-effect refresh is still the
		// user's authorized intent; the risk cap below remains authoritative.
		// Without a usable quote this intent check cannot run; a stop-limit's
		// fill stays bounded by its own limit, so it proceeds unchecked here.
		if quoteUsable && *op.StopPrice <= quote.Ask && op.ApprovedPriceCap == 0 {
			op.RejectReason = "buy_stop_must_be_above_market"
			return op
		}
		if precision != nil {
			stop := floorPriceForInstrument(*op.StopPrice, *precision)
			if stop != *op.StopPrice || !precision.SupportsPrice(stop) {
				op.RejectReason = "unsupported_contract"
				return op
			}
			op.StopPrice = &stop
		}
	}
	if op.OrderType == "market" || op.OrderType == "stop_market" {
		// A true market order has no provider-visible price. The explicit client
		// declaration remains the durable cash/risk authorization; this bound is
		// never sent to Robinhood as a limit price.
		wholeShares := int64(op.Qty) / units.Scale
		if wholeShares <= 0 || op.MaxRiskUSD == nil {
			op.RejectReason = "risk_not_computed"
			return op
		}
		fees, err := units.MulQtyPrice(op.Qty, limits.ExecutionPolicy.FeePerShare, 1, true)
		if err != nil || *op.MaxRiskUSD <= fees {
			op.RejectReason = "risk_overflow"
			return op
		}
		priceBudget := *op.MaxRiskUSD - fees
		op.ApprovedPriceCap = units.Micros(int64(priceBudget) / wholeShares)
		if op.OrderType == "stop_market" && op.StopPrice != nil && op.ApprovedPriceCap < *op.StopPrice {
			op.RejectReason = "stop_risk_cap_below_trigger"
			return op
		}
		estimated, err := units.MulQtyPrice(op.Qty, quote.Ask, 1, true)
		if err == nil {
			estimated, err = units.Add(estimated, fees)
		}
		if err != nil || estimated > *op.MaxRiskUSD || op.ApprovedPriceCap <= 0 {
			op.RejectReason = "market_risk_cap_below_quote"
			if op.OrderType == "stop_market" {
				op.RejectReason = "stop_market_risk_cap_below_quote"
			}
			return op
		}
		op.RequiredCash = *op.MaxRiskUSD
		op.DerivedMaxRisk = *op.MaxRiskUSD
		return op
	}
	if op.Limit != nil {
		op.ApprovedPriceCap = *op.Limit
	} else {
		op.ApprovedPriceCap = quote.Ask
	}
	if op.OrderType == "stop_limit" {
		op.WorkingPrice = op.ApprovedPriceCap
	} else if quoteUsable && limits.ExecutionPolicy.StartAt == "mid" {
		op.WorkingPrice = quote.Mid()
	}
	if !quoteUsable {
		// No live quote (resting limit): start working at the authorized cap.
		op.WorkingPrice = op.ApprovedPriceCap
	}
	if op.WorkingPrice > op.ApprovedPriceCap {
		op.WorkingPrice = op.ApprovedPriceCap
	}
	if precision != nil {
		cap := floorPriceForInstrument(op.ApprovedPriceCap, *precision)
		working := floorPriceForInstrument(op.WorkingPrice, *precision)
		if cap <= 0 || working <= 0 {
			op.RejectReason = "unsupported_contract"
			return op
		}
		if working > cap {
			working = cap
		}
		if !precision.SupportsPrice(cap) || !precision.SupportsPrice(working) {
			op.RejectReason = "unsupported_contract"
			return op
		}
		op.ApprovedPriceCap, op.WorkingPrice = cap, working
	}

	// Required cash rounds up, against the account, so fractional micro-dollars
	// of premium never become unreserved capacity.
	required, err := units.MulQtyPrice(op.Qty, op.ApprovedPriceCap, op.Multiplier, true)
	if err != nil {
		op.RejectReason = "risk_overflow"
		return op
	}
	feePerUnit := limits.ExecutionPolicy.FeePerShare
	if op.Kind == "option" {
		feePerUnit = limits.ExecutionPolicy.FeePerContract
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

func supportsOrderKind(execution broker.ExecutionProvider, kind string) bool {
	if support, ok := execution.(broker.OrderKindSupport); ok {
		return support.SupportsOrderKind(kind)
	}
	return true
}

// marketDataRejectionDetail turns a generic "market_data_unavailable" reject
// into a specific, loggable cause: the raw provider fetch error (with which
// provider path failed), an insane bid/ask, or a stale quote with its age. It
// returns "" when the reject was not about market data, so the field is only
// attached when it explains something.
func marketDataRejectionDetail(fetchDiag string, reasons []string, quote *broker.Quote, maxAgeSec int) string {
	marketData := false
	for _, reason := range reasons {
		if reason == "market_data_unavailable" {
			marketData = true
			break
		}
	}
	if !marketData {
		return ""
	}
	if fetchDiag != "" {
		return fetchDiag
	}
	if quote == nil {
		return "quote_missing"
	}
	if !quote.Sane() {
		return fmt.Sprintf("quote_not_sane(bid=%d,ask=%d)", quote.Bid, quote.Ask)
	}
	if !quote.Fresh(maxAgeSec, time.Now().UTC()) {
		return fmt.Sprintf("quote_stale(age=%s,max=%ds,as_of=%s)",
			time.Since(quote.AsOf).Round(time.Second), maxAgeSec, quote.AsOf.UTC().Format(time.RFC3339))
	}
	return "quote_unusable"
}

func addRiskFacts(response map[string]any, op risk.Operation) {
	if op.Action == "open" || op.Action == "close" {
		response["execution_style"] = op.ExecutionStyle
	}
	if op.Action != "open" {
		return
	}
	response["order_type"] = op.OrderType
	if op.StopPrice != nil {
		response["stop_price"] = *op.StopPrice
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
		if positions[i].Symbol == symbol && positions[i].Qty != 0 &&
			(op.PositionID == "" || positions[i].PositionID == op.PositionID) {
			if position != nil {
				return op, fmt.Errorf("close position identity is ambiguous for %s", symbol)
			}
			position = &positions[i]
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
	op.InstrumentID = position.InstrumentID
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
	freshQuote, quoteErr := s.authorityMarketProvider().Quote(quoteCtx, operationSymbol(persisted))
	cancel()
	if quoteErr == nil {
		quote = &freshQuote
	}
	if err := s.refreshGlobalHalt(); err != nil {
		writeStoreError(w, "refresh global halt", err)
		return
	}
	halted, haltReason := s.haltSnapshot()

	var attempt *store.ExecutionAttempt
	var approvalVerdict risk.Verdict
	var boundVerdict risk.Verdict
	var approvedOp risk.Operation
	conflictReason := ""
	err = s.store.WithReviewLock(id, func(gate store.OperationGate, locked *store.OperationRow) error {
		if !bytes.Equal(row.Payload, locked.Payload) {
			conflictReason = "approval_snapshot_mismatch"
			return errReviewGateRejected
		}
		currentAuthority, err := gate.KernelPolicyAuthority()
		if err != nil {
			return err
		}
		boundAuthority, err := gate.BoundKernelPolicy(locked)
		if err != nil {
			return err
		}
		now, err := gate.DatabaseNow()
		if err != nil {
			return err
		}
		if locked.ExpiresAt.IsZero() || !now.Before(locked.ExpiresAt) {
			conflictReason = "proposal_expired"
			verdict := map[string]any{
				"reviewer": reviewer, "rationale": in.Rationale, "decision": "expired",
				"proposal_ts": locked.TS, "expires_at": locked.ExpiresAt, "reviewed_at": now,
			}
			if err := gate.SetOperationStatus(id, "expired", verdict); err != nil {
				return err
			}
			return gate.InsertEvent("operation_reviewed", map[string]any{
				"operation_id": id, "reviewer": reviewer, "decision": "expired",
				"proposal_ts": locked.TS, "expires_at": locked.ExpiresAt, "reviewed_at": now,
			})
		}
		prepared := s.prepareApprovedOpenWithLimits(r.Context(), persisted, quote, boundAuthority.Policy)
		if err := gate.LockLedger(prepared.Shadow); err != nil {
			return err
		}
		var account broker.AccountState
		if prepared.Shadow {
			markLimits := currentAuthority.Policy
			markLimits.QuoteMaxAgeSec = minInt(boundAuthority.Policy.QuoteMaxAgeSec, currentAuthority.Policy.QuoteMaxAgeSec)
			account, err = s.shadowAccountSnapshotWithLimits(r.Context(), gate, markLimits)
		} else {
			accountCtx, cancel := context.WithTimeout(r.Context(), s.brokerCallTimeout())
			account, err = s.authorityAccountProvider().Account(accountCtx)
			cancel()
		}
		if err != nil {
			return fmt.Errorf("%w: account", errBrokerDataUnavailable)
		}
		window, err := s.databaseMarketWindow(gate)
		if err != nil {
			return err
		}
		databaseNow := window.asOf
		day, err := s.dayStateAtAccountWithLimits(r.Context(), gate, prepared.Shadow, account, window, halted, haltReason, currentAuthority.Policy)
		if err != nil {
			return err
		}
		boundVerdict = risk.ClassifyAt(prepared, boundAuthority.Policy, day, quote, time.Now().UTC())
		approvalVerdict = risk.ClassifyAt(prepared, currentAuthority.Policy, day, quote, time.Now().UTC())
		if boundVerdict.Class == "REJECT" || approvalVerdict.Class == "REJECT" {
			conflictReason = "approval gate rejected"
			if len(boundVerdict.Reasons) > 0 && boundVerdict.Class == "REJECT" {
				conflictReason = boundVerdict.Reasons[0]
			} else if len(approvalVerdict.Reasons) > 0 {
				conflictReason = approvalVerdict.Reasons[0]
			}
			return errReviewGateRejected
		}
		var canaryAuthority *store.LiveCanaryRevision
		if !prepared.Shadow {
			canaryReason, usage, authority, err := s.liveCanaryRefusal(gate, id, window.day, prepared.DerivedMaxRisk, prepared.Qty, prepared.QtyIncrement)
			if err != nil {
				return err
			}
			canaryAuthority = authority
			if canaryReason != "" {
				conflictReason = canaryReason
				return s.insertCanaryRefusalEvent(gate, id, canaryReason, window.day, prepared.DerivedMaxRisk, prepared.Qty, prepared.QtyIncrement, usage, authority)
			}
			if err := gate.RequireLiveExecutionIdle(true); err != nil {
				return err
			}
		}
		approvedOp = prepared
		staged, err := s.stageApprovedOpen(gate, id, prepared, window.day, canaryAuthority)
		if err != nil {
			return err
		}
		attempt = staged
		approval := map[string]any{
			"reviewer": reviewer, "rationale": in.Rationale, "decision": "approved",
			"proposal_ts": locked.TS, "approved_at": databaseNow, "market_day": window.day,
			"quote": quote, "bound_verdict": boundVerdict, "current_verdict": approvalVerdict,
			"bound_kernel_policy": operationPolicyView(locked),
			"current_kernel_policy": map[string]any{
				"revision_id": currentAuthority.ID, "generation": currentAuthority.Generation,
				"digest": currentAuthority.Digest,
			},
			"approved_price_cap": prepared.ApprovedPriceCap, "working_price": prepared.WorkingPrice,
			"derived_max_risk": prepared.DerivedMaxRisk, "required_cash": prepared.RequiredCash,
			"multiplier": prepared.Multiplier, "attempt_id": staged.ID,
			"open_reservation_id": staged.OpenReservationID,
		}
		if err := gate.SetOperationStatus(id, "approved", approval); err != nil {
			return err
		}
		if err := gate.InsertEvent("operation_reviewed", map[string]any{
			"operation_id": id, "approval": approval,
		}); err != nil {
			return err
		}
		// Approval and all of its entitlement rows share one database-derived
		// market day or the review transaction is rolled back.
		return s.ensureMarketDay(gate, window)
	})
	if errors.Is(err, store.ErrOperationNotPending) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "not pending review"})
		return
	}
	if errors.Is(err, errReviewGateRejected) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": conflictReason})
		return
	}
	if errors.Is(err, store.ErrLiveExecutionBusy) {
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusConflict, map[string]string{"error": "live_execution_busy"})
		return
	}
	if errors.Is(err, store.ErrLiveExecutionSuspended) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "live_execution_suspended"})
		return
	}
	if errors.Is(err, store.ErrLiveSendHalted) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "global_halt_active"})
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
	if conflictReason != "" {
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
		if errors.Is(err, errBrokerMutationFailed) {
			response["status"] = "failed"
			response["attempt_id"] = attempt.ID
			response["attempt_state"] = "failed"
			response["error"] = "broker_mutation_failed"
			writeJSON(w, http.StatusBadGateway, response)
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
	return s.prepareApprovedOpenWithLimits(ctx, persisted, quote, s.limits)
}

func (s *server) prepareApprovedOpenWithLimits(ctx context.Context, persisted risk.Operation, quote *broker.Quote, limits config.Limits) risk.Operation {
	prepared := persisted
	cap := persisted.ApprovedPriceCap
	originalLimit := persisted.Limit
	prepared.Limit = &cap
	prepared.RejectReason = ""
	prepared = s.deriveOpenOperationWithLimits(ctx, prepared, quote, limits)
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
		prepared.DerivedMaxRisk != persisted.DerivedMaxRisk || prepared.QtyIncrement != persisted.QtyIncrement {
		prepared.RejectReason = "approval_snapshot_mismatch"
	}
	return prepared
}

func operationPolicyView(operation *store.OperationRow) map[string]any {
	if operation == nil {
		return nil
	}
	return map[string]any{
		"revision_id": operation.KernelPolicyRevisionID,
		"generation":  operation.KernelPolicyGeneration,
		"digest":      operation.KernelPolicyDigest,
		"expires_at":  operation.ExpiresAt,
	}
}

func (s *server) stageApprovedOpen(gate store.OperationGate, operationID string, op risk.Operation, marketDay time.Time, canaryAuthority *store.LiveCanaryRevision) (*store.ExecutionAttempt, error) {
	ledger := ledgerName(op.Shadow)
	var canaryRevisionID int64
	if canaryAuthority != nil {
		canaryRevisionID = canaryAuthority.ID
	}
	if s.tradingMode() == config.ModeLive && !op.Shadow && canaryRevisionID <= 0 {
		return nil, store.ErrLiveCanaryAuthorityInvalid
	}
	if err := gate.InsertTradeGrant(store.TradeGrant{
		OperationID: operationID, Ledger: ledger, MarketDay: marketDay,
		AuthorizedRisk: op.DerivedMaxRisk, RiskSource: "computed",
		LiveCanaryRevisionID: canaryRevisionID,
	}); err != nil {
		return nil, err
	}
	grantEvent := map[string]any{
		"operation_id": operationID, "ledger": ledger,
	}
	if binding := liveCanaryEventBinding(canaryAuthority); binding != nil {
		grantEvent["live_canary"] = binding
	}
	if err := gate.InsertEvent("trade_grant_created", grantEvent); err != nil {
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
		ApprovedPriceBound: executionPriceBound(op, attempt.Limit), State: "new",
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
