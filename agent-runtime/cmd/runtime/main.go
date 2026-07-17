// alpheus agent-runtime — session runner. A session = {role}/{date}/{trigger}/{seq}:
// stateless, disposable, restart-safe. Skeleton loop runs every role once per
// tick so the plumbing is observable immediately. POST /wake is authenticated
// and ready; M6 wires the kernel watchdog to drive it.
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
	tick := envInt("TICK_SECONDS", 300)
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
	cog, err := cognition.New()
	if err != nil {
		log.Fatalf("cognition: %v", err)
	}
	client := assemble.New(kernel, runtimeToken)

	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Role
	}
	roleByName := make(map[string]roles.Role, len(rs))
	for _, role := range rs {
		roleByName[role.Role] = role
	}
	wakeHandler := newWakeHandler(kernelToken, roleByName, func(role roles.Role, trigger string) {
		go runSession(client, cog, role, trigger)
	})
	go func() {
		addr := env("RUNTIME_ADDR", ":8200")
		log.Printf("agent-runtime wake endpoint listening on %s", addr)
		if err := http.ListenAndServe(addr, wakeHandler); err != nil {
			log.Fatalf("wake server: %v", err)
		}
	}()
	log.Printf("agent-runtime up: roles=%v cognition=%s kernel=%s", names, env("COGNITION", "stub"), kernel)

	for {
		for _, role := range rs {
			runSession(client, cog, role, "tick")
		}
		time.Sleep(time.Duration(tick) * time.Second)
	}
}

// runSession never lets one dead session kill the runtime.
func runSession(client *assemble.Client, cog cognition.Cognition, role roles.Role, trigger string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[%s] session panic: %v", role.Role, r)
		}
	}()
	sequence := seq.Add(1)
	sid := fmt.Sprintf("%s/%s/%s/%d", role.Role, time.Now().Format("2006-01-02"), trigger, sequence)

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

func envInt(k string, fallback int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
