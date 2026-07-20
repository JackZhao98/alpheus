// alpheus agent-runtime — session runner. A session is identified by role,
// date, trigger, occurrence, and a local sequence: stateless, disposable, and
// restart-safe. The authenticated POST /wake path is the schedule spine; the
// configurable tick loop is only a fallback.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"alpheus/agentruntime/internal/assemble"
	"alpheus/agentruntime/internal/cognition"
	"alpheus/agentruntime/internal/contracts"
	"alpheus/agentruntime/internal/roles"
)

var seq atomic.Uint64

func main() {
	kernel := env("KERNEL_URL", "http://localhost:8100")
	rolesDir := env("ROLES_DIR", "/roles")
	tick, err := envNonNegativeInt("TICK_SECONDS", 300)
	if err != nil {
		log.Fatalf("runtime config: %v", err)
	}
	runtimeToken := env("RUNTIME_TOKEN", "")
	kernelToken := env("KERNEL_TOKEN", "")
	tradingMode := env("TRADING_MODE", "sim")
	if tradingMode != "sim" && (runtimeToken == "" || kernelToken == "") {
		log.Fatal("RUNTIME_TOKEN and KERNEL_TOKEN are required outside sim mode")
	}

	rs, err := roles.Load(rolesDir)
	if err != nil {
		log.Fatalf("roles: %v", err)
	}
	client := assemble.New(kernel, runtimeToken)
	telemetrySink := func(event cognition.Telemetry) error {
		_, err := postJSON(client, "/telemetry", event)
		return err
	}
	cog, err := cognition.New(cognition.WithTelemetry(telemetrySink))
	if err != nil {
		log.Fatalf("cognition: %v", err)
	}

	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Role
	}
	roleByName := make(map[string]roles.Role, len(rs))
	for _, role := range rs {
		roleByName[role.Role] = role
	}
	wakeHandler := newRuntimeHandler(kernelToken, roleByName, func(role roles.Role, trigger, occurrenceID string) {
		go runSession(client, cog, role, trigger, occurrenceID)
	}, func(workflow, symbol, question, openAIAPIKey string) (queryResult, error) {
		return runManualQuery(client, cog, roleByName, workflow, symbol, question, openAIAPIKey, telemetrySink)
	})
	log.Printf("agent-runtime up: roles=%v cognition=%s kernel=%s", names, env("COGNITION", "stub"), kernel)
	if tick > 0 {
		go runTickFallback(client, cog, rs, time.Duration(tick)*time.Second)
	} else {
		log.Printf("agent-runtime tick fallback disabled; watchdog spine is the only driver")
	}
	addr := env("RUNTIME_ADDR", ":8200")
	log.Printf("agent-runtime wake endpoint listening on %s", addr)
	if err := http.ListenAndServe(addr, wakeHandler); err != nil {
		log.Fatalf("wake server: %v", err)
	}
}

func runManualQuery(client *assemble.Client, fallback cognition.Cognition, roleByName map[string]roles.Role,
	workflow, symbol, question, openAIAPIKey string, telemetry func(cognition.Telemetry) error) (queryResult, error) {
	requestedWorkflow := workflow
	result := queryResult{Workflow: workflow, Cognition: env("COGNITION", "stub")}
	monitorModel := ""
	if openAIAPIKey != "" {
		monitorModel = env("MONITOR_MODEL", "gpt-5.6-sol")
		result.Cognition, result.Provider, result.Model = "llm", "openai", monitorModel
	}
	if workflow == "auto" {
		intentRole, ok := roleByName["intent_interpreter"]
		if !ok {
			return queryResult{}, fmt.Errorf("intent interpreter unavailable")
		}
		intentContext, err := client.Assemble(intentRole)
		if err != nil {
			return queryResult{}, err
		}
		intentContext["user_query"], _ = json.Marshal(question)
		intentContext["symbol"], _ = json.Marshal(symbol)
		intentContext["capability_manifest"] = json.RawMessage(queryCapabilityManifestJSON)
		intentCog := fallback
		if openAIAPIKey != "" {
			intentCog, err = cognition.NewOpenAIQueryForTier(openAIAPIKey, "monitor", monitorModel, cognition.WithTelemetry(telemetry))
			if err != nil {
				return queryResult{}, err
			}
		}
		intentOutput, err := intentCog.Run(intentRole, intentContext)
		if err != nil {
			return queryResult{}, err
		}
		if err := intentOutput.Validate(); err != nil {
			return queryResult{}, err
		}
		intent, ok := intentOutput.(contracts.QueryIntent)
		if !ok {
			return queryResult{}, fmt.Errorf("intent interpreter returned the wrong contract")
		}
		workflow, err = resolveQueryIntent(intent)
		if err != nil {
			return queryResult{}, err
		}
		result.RequestedWorkflow = requestedWorkflow
		result.IntentOutput = intent
		result.Workflow = workflow
		if workflow == "refuse" {
			result.Role, result.Output = intentRole.Role, intent
			return result, nil
		}
	}

	scout, ok := roleByName["scout"]
	if !ok {
		return queryResult{}, fmt.Errorf("scout unavailable")
	}
	scoutContext, err := client.AssembleQuery(scout, symbol, question)
	if err != nil {
		return queryResult{}, err
	}
	scoutCog := fallback
	result.Role = scout.Role
	if openAIAPIKey != "" {
		scoutCog, err = cognition.NewOpenAIQueryForTier(openAIAPIKey, "monitor", monitorModel, cognition.WithTelemetry(telemetry))
		if err != nil {
			return queryResult{}, err
		}
	}
	scoutOutput, err := scoutCog.Run(scout, scoutContext)
	if err != nil {
		return queryResult{}, err
	}
	if err := scoutOutput.Validate(); err != nil {
		return queryResult{}, err
	}
	if workflow == "scout" {
		result.Output = scoutOutput
		return result, nil
	}

	desk, ok := roleByName["desk_master"]
	if !ok {
		return queryResult{}, fmt.Errorf("decision desk unavailable")
	}
	deskContext, err := client.Assemble(desk)
	if err != nil {
		return queryResult{}, err
	}
	deskContext["user_query"] = scoutContext["user_query"]
	deskContext["symbol"] = scoutContext["symbol"]
	scoutBrief, err := json.Marshal(scoutOutput)
	if err != nil {
		return queryResult{}, err
	}
	deskContext["scout_brief"] = scoutBrief
	deskCog := fallback
	if openAIAPIKey != "" {
		deciderModel := env("DECIDER_MODEL", "gpt-5.6-sol")
		deskCog, err = cognition.NewOpenAIQueryForTier(openAIAPIKey, "decider", deciderModel, cognition.WithTelemetry(telemetry))
		if err != nil {
			return queryResult{}, err
		}
		result.Model = deciderModel
	}
	deskOutput, err := deskCog.Run(desk, deskContext)
	if err != nil {
		return queryResult{}, err
	}
	if err := deskOutput.Validate(); err != nil {
		return queryResult{}, err
	}
	decision, ok := deskOutput.(contracts.DeskDecision)
	if !ok || decision.Action == "PROPOSE" || len(decision.Proposals) != 0 || len(decision.BlackboardPatch) != 0 {
		return queryResult{}, fmt.Errorf("read-only decision desk attempted a mutation or proposal")
	}
	result.Role = desk.Role
	result.Output = decision
	result.ScoutOutput = scoutOutput
	return result, nil
}

const queryCapabilityManifestJSON = `{"capabilities":[{"id":"market_quote","kind":"data"},{"id":"market_bars","kind":"data"},{"id":"technical_indicators","kind":"data"},{"id":"equity_fundamentals","kind":"data"},{"id":"company_financials","kind":"data"},{"id":"earnings_results","kind":"data"},{"id":"portfolio_state","kind":"context"},{"id":"scout","kind":"role"},{"id":"decision_desk","kind":"role"}],"routes":{"SCOUT":["market_quote","market_bars","scout"],"TEAM":["market_quote","market_bars","portfolio_state","scout","decision_desk"]}}`

var queryRouteCapabilities = map[string][]string{
	"SCOUT": {"market_quote", "market_bars", "scout"},
	"TEAM":  {"market_quote", "market_bars", "portfolio_state", "scout", "decision_desk"},
}

var queryKnownCapabilities = map[string]bool{
	"market_quote": true, "market_bars": true, "technical_indicators": true, "equity_fundamentals": true,
	"company_financials": true, "earnings_results": true, "portfolio_state": true,
	"scout": true, "decision_desk": true,
}

func resolveQueryIntent(intent contracts.QueryIntent) (string, error) {
	selected := map[string]bool{}
	for _, capability := range intent.RequiredCapabilities {
		if !queryKnownCapabilities[capability] || selected[capability] {
			return "", fmt.Errorf("intent selected an unknown or duplicate capability")
		}
		selected[capability] = true
	}
	if intent.Route == "REFUSE" {
		return "refuse", nil
	}
	if len(intent.MissingInputs) != 0 {
		return "", fmt.Errorf("intent selected a route with missing inputs")
	}
	required, ok := queryRouteCapabilities[intent.Route]
	if !ok {
		return "", fmt.Errorf("intent selected an unknown route")
	}
	for _, capability := range required {
		if !selected[capability] {
			return "", fmt.Errorf("intent omitted a required capability")
		}
	}
	return strings.ToLower(intent.Route), nil
}

func runTickFallback(client *assemble.Client, cog cognition.Cognition, rs []roles.Role, interval time.Duration) {
	for {
		for _, role := range rs {
			runSession(client, cog, role, "tick", "")
		}
		time.Sleep(interval)
	}
}

// runSession never lets one dead session kill the runtime.
func runSession(client *assemble.Client, cog cognition.Cognition, role roles.Role, trigger, occurrenceID string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[%s] session panic: %v", role.Role, r)
		}
	}()
	sequence := seq.Add(1)
	if occurrenceID == "" {
		occurrenceID = fmt.Sprintf("local-%d", sequence)
	}
	sid := fmt.Sprintf("%s/%s/%s/%s/%d", role.Role, time.Now().Format("2006-01-02"), trigger, occurrenceID, sequence)

	ctx, err := client.Assemble(role)
	if err != nil {
		log.Printf("[%s] assemble: %v", sid, err)
		return
	}
	out, err := cog.Run(role, ctx)
	if err != nil {
		log.Printf("[%s] cognition: %v", sid, err)
		return
	}
	if err := out.Validate(); err != nil { // contract enforced regardless of prompt
		log.Printf("[%s] contract violation: %v", sid, err)
		return
	}
	log.Printf("[%s] -> %T", sid, out)

	for _, op := range extractOps(out) {
		res, err := submit(client, op, role)
		if err != nil {
			log.Printf("[%s] submit: %v", sid, err)
			continue
		}
		log.Printf("[%s] kernel: %s %v", sid, res["status"], res["reasons"])
	}
	// TODO: apply DeskDecision.BlackboardPatch via PUT /blackboard
	// TODO: persist coach lessons through the kernel
}

func extractOps(out contracts.Output) []contracts.ProposedOperation {
	switch v := out.(type) {
	case contracts.DeskDecision:
		return v.Proposals
	case contracts.ExitAction:
		return v.Operations
	}
	return nil
}

func submit(client *assemble.Client, op contracts.ProposedOperation, role roles.Role) (map[string]any, error) {
	// Marshal the typed decimal fields directly. Routing through map[string]any
	// would decode JSON numbers as float64 before re-encoding them.
	body := struct {
		Proposer string `json:"proposer"`
		contracts.ProposedOperation
	}{Proposer: role.Role, ProposedOperation: op}
	res, err := postOperationJSON(client, body)
	if err != nil {
		return nil, err
	}
	status, _ := res["status"].(string)
	// tighten_stop is journaled atomically by the kernel so direct API callers
	// and runtime callers get the same audit trail without duplicate rows.
	if (status == "auto_approved" || status == "executed") && op.Action != "tighten_stop" {
		plan := map[string]any{}
		if op.Plan != nil {
			pb, _ := json.Marshal(op.Plan)
			_ = json.Unmarshal(pb, &plan)
		}
		journalShadow := op.Shadow
		if forcedShadow, ok := res["shadow"].(bool); ok {
			journalShadow = forcedShadow
		}
		_, _ = postJSON(client, "/journal", map[string]any{
			"operation_id":    res["operation_id"],
			"hypothesis":      map[string]any{"thesis": op.Thesis, "setup": op.Setup, "plan": plan},
			"prompt_versions": map[string]any{role.Role: role.Version},
			"shadow":          journalShadow,
		})
	}
	return res, nil
}

func postJSON(client *assemble.Client, path string, v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out, status, err := doJSONRequest(client, path, b, "")
	if err != nil {
		return nil, err
	}
	if status >= http.StatusBadRequest {
		return out, fmt.Errorf("%s: HTTP %d: %v", path, status, out["error"])
	}
	return out, nil
}

func postOperationJSON(client *assemble.Client, v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	key, err := newIdempotencyKey()
	if err != nil {
		return nil, err
	}
	var out map[string]any
	var status int
	for attempt := 0; attempt < 2; attempt++ {
		out, status, err = doJSONRequest(client, "/operations", b, key)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	if status >= http.StatusBadRequest {
		return out, fmt.Errorf("/operations: HTTP %d: %v", status, out["error"])
	}
	return out, nil
}

func newIdempotencyKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate idempotency key: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func doJSONRequest(client *assemble.Client, path string, body []byte, idempotencyKey string) (map[string]any, int, error) {
	req, err := http.NewRequest(http.MethodPost, client.Kernel+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	client.Authorize(req)
	resp, err := client.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, resp.StatusCode, err
	}
	return out, resp.StatusCode, nil
}

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

func envNonNegativeInt(k string, fallback int) (int, error) {
	value := os.Getenv(k)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", k)
	}
	return n, nil
}
