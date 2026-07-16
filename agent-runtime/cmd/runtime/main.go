// alpheus agent-runtime — session runner. A session = {role}/{date}/{trigger}/{seq}:
// stateless, disposable, restart-safe. Skeleton loop runs every role once per
// tick so the plumbing is observable immediately; the real spine is kernel
// watchdog wakes (TODO: expose POST /wake and let the kernel drive).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"alpheus/agentruntime/internal/assemble"
	"alpheus/agentruntime/internal/cognition"
	"alpheus/agentruntime/internal/contracts"
	"alpheus/agentruntime/internal/roles"
)

var seq int

func main() {
	kernel := env("KERNEL_URL", "http://localhost:8100")
	rolesDir := env("ROLES_DIR", "/roles")
	tick := envInt("TICK_SECONDS", 300)

	rs, err := roles.Load(rolesDir)
	if err != nil {
		log.Fatalf("roles: %v", err)
	}
	cog, err := cognition.New()
	if err != nil {
		log.Fatalf("cognition: %v", err)
	}
	client := assemble.New(kernel)

	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Role
	}
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
	seq++
	sid := fmt.Sprintf("%s/%s/%s/%d", role.Role, time.Now().Format("2006-01-02"), trigger, seq)

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
	body := map[string]any{"proposer": role.Role}
	b, _ := json.Marshal(op)
	_ = json.Unmarshal(b, &body) // merge op fields over proposer

	res, err := postJSON(client, "/operations", body)
	if err != nil {
		return nil, err
	}
	status, _ := res["status"].(string)
	if status == "auto_approved" || status == "executed" {
		plan := map[string]any{}
		if op.Plan != nil {
			pb, _ := json.Marshal(op.Plan)
			_ = json.Unmarshal(pb, &plan)
		}
		_, _ = postJSON(client, "/journal", map[string]any{
			"operation_id":    res["operation_id"],
			"hypothesis":      map[string]any{"thesis": op.Thesis, "setup": op.Setup, "plan": plan},
			"prompt_versions": map[string]any{role.Role: role.Version},
			"shadow":          op.Shadow,
		})
	}
	return res, nil
}

func postJSON(client *assemble.Client, path string, v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	resp, err := client.HTTP.Post(client.Kernel+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("%s: HTTP %d: %v", path, resp.StatusCode, out["error"])
	}
	return out, nil
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
