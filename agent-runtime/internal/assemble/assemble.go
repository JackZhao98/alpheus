// Package assemble: context assembly is CODE, not prompt. Whatever the prompt
// says, every run receives the same structured picture: limits, live state,
// blackboard, lessons. This is what makes sessions disposable — state lives
// outside them. This is also where the marketdata facade gets injected once
// the kernel grows /market/* endpoints (ROADMAP Phase 1).
package assemble

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"alpheus/agentruntime/internal/roles"
)

type Client struct {
	Kernel string
	HTTP   *http.Client
}

func New(kernelURL string) *Client {
	return &Client{Kernel: kernelURL, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) getJSON(path string) (json.RawMessage, error) {
	resp, err := c.HTTP.Get(c.Kernel + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return raw, nil
}

// Assemble builds the full context for one session.
func (c *Client) Assemble(role roles.Role) (map[string]json.RawMessage, error) {
	today := time.Now().Format("2006-01-02")
	all := map[string]string{
		"limits":     "/limits",
		"state":      "/state",
		"blackboard": "/blackboard/" + today,
		"lessons":    "/lessons?limit=5",
	}
	wants := map[string]bool{}
	for _, k := range role.InjectedContext {
		wants[k] = true
	}
	ctx := map[string]json.RawMessage{"today": json.RawMessage(`"` + today + `"`)}
	for key, path := range all {
		if len(wants) > 0 && !wants[key] {
			continue
		}
		raw, err := c.getJSON(path)
		if err != nil {
			return nil, err
		}
		ctx[key] = raw
	}
	// TODO: unread inbox items addressed to this role
	// TODO: watchlist memory filtered by applicable_when
	return ctx, nil
}
