package rhmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrMutationOutcomeUnknown = errors.New("provider mutation outcome unknown")

// MutationClient is the only MCP client type permitted to call Robinhood
// mutation tools. It has no cache and issues exactly one CallTool request. A
// failed response is conservatively ambiguous and must not be retried here.
type MutationClient struct {
	client        *Client
	accountNumber string
}

func NewMutation(cfg Config, accountNumber string) (*MutationClient, error) {
	if len(cfg.AllowedTools) != 0 {
		return nil, fmt.Errorf("mutation allowlist is fixed")
	}
	if strings.TrimSpace(accountNumber) == "" || accountNumber != strings.TrimSpace(accountNumber) {
		return nil, fmt.Errorf("mutation account binding is required")
	}
	client, err := newClient(cfg, MutationTools)
	if err != nil {
		return nil, err
	}
	return &MutationClient{client: client, accountNumber: accountNumber}, nil
}

func (c *MutationClient) Close() error {
	return c.client.Close()
}

func (c *MutationClient) Call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	if !IsMutationTool(tool) {
		return nil, fmt.Errorf("provider tool is not mutation-allowlisted")
	}
	if err := validateMutationArguments(tool, args, c.accountNumber); err != nil {
		return nil, err
	}
	c.client.mu.Lock()
	defer c.client.mu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, c.client.cfg.CallTimeout)
	defer cancel()
	if err := c.client.limiter.Wait(callCtx); err != nil {
		c.client.setError("mutation rate limit wait timed out")
		return nil, ErrMutationOutcomeUnknown
	}
	if err := c.client.connectLocked(callCtx); err != nil {
		c.client.setError("provider mutation connect failed")
		return nil, ErrMutationOutcomeUnknown
	}

	// Exactly one invocation. StreamableClientTransport is also constructed
	// with MaxRetries=-1, disabling SDK reconnect retries beneath this boundary.
	raw, err := callMutationOnce(callCtx, tool, args, c.client.callTool)
	if err != nil {
		_ = c.client.closeSessionLocked()
		c.client.setError("provider mutation outcome unknown")
		return nil, ErrMutationOutcomeUnknown
	}
	c.client.lastUsed = time.Now()
	c.client.setConnected(true)
	return raw, nil
}

func validateMutationArguments(tool string, args map[string]any, accountNumber string) error {
	boundAccount, ok := args["account_number"].(string)
	if !ok || boundAccount != accountNumber || accountNumber == "" {
		return fmt.Errorf("mutation account binding mismatch")
	}
	switch tool {
	case "place_equity_order", "place_option_order":
		refID, ok := args["ref_id"].(string)
		if !ok || !validUUID(refID) {
			return fmt.Errorf("mutation ref_id must be a UUID")
		}
	case "cancel_equity_order", "cancel_option_order":
		orderID, ok := args["order_id"].(string)
		if !ok || !validUUID(orderID) {
			return fmt.Errorf("mutation order_id must be a UUID")
		}
	}
	return nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func callMutationOnce(
	ctx context.Context,
	tool string,
	args map[string]any,
	invoke func(context.Context, string, map[string]any) (json.RawMessage, error),
) (json.RawMessage, error) {
	raw, err := invoke(ctx, tool, args)
	if err != nil {
		return nil, ErrMutationOutcomeUnknown
	}
	return raw, nil
}
