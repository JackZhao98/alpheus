package rhmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	ErrMutationNotSent        = errors.New("provider mutation was not sent")
	ErrMutationRejected       = errors.New("provider mutation rejected")
	ErrMutationOutcomeUnknown = errors.New("provider mutation outcome unknown")
)

type MutationError struct {
	Kind   error
	Code   string
	Detail string
}

func (e *MutationError) Error() string {
	if e == nil || e.Kind == nil {
		return "provider mutation failed"
	}
	message := e.Kind.Error()
	if e.Code != "" {
		message += " (" + e.Code + ")"
	}
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message
}

func (e *MutationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

func mutationError(kind error, code, detail string) error {
	return &MutationError{Kind: kind, Code: sanitizeProviderCode(code), Detail: sanitizeProviderDetail(detail)}
}

func MutationErrorFacts(err error) (kind, code, detail string, ok bool) {
	var mutationErr *MutationError
	if !errors.As(err, &mutationErr) {
		return "", "", "", false
	}
	switch {
	case errors.Is(mutationErr, ErrMutationNotSent):
		kind = "not_sent"
	case errors.Is(mutationErr, ErrMutationRejected):
		kind = "rejected"
	case errors.Is(mutationErr, ErrMutationOutcomeUnknown):
		kind = "unknown"
	default:
		return "", "", "", false
	}
	return kind, mutationErr.Code, mutationErr.Detail, true
}

type MutationCaller interface {
	Caller
	MutationBoundary()
}

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

// MutationBoundary prevents the retrying read Client from being passed to a
// broker execution adapter merely because both expose Call.
func (c *MutationClient) MutationBoundary() {}

func (c *MutationClient) Call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	if !IsMutationTool(tool) {
		return nil, mutationError(ErrMutationNotSent, "tool_not_allowlisted", "")
	}
	if err := validateMutationArguments(tool, args, c.accountNumber); err != nil {
		return nil, mutationError(ErrMutationNotSent, "invalid_local_arguments", err.Error())
	}
	c.client.mu.Lock()
	defer c.client.mu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, c.client.cfg.CallTimeout)
	defer cancel()
	if err := c.client.limiter.Wait(callCtx); err != nil {
		c.client.setError("mutation rate limit wait timed out")
		return nil, mutationError(ErrMutationNotSent, "local_rate_limit_timeout", "")
	}
	if err := c.client.connectLocked(callCtx); err != nil {
		c.client.setError("provider mutation connect failed")
		return nil, mutationError(ErrMutationNotSent, "provider_connect_failed", "")
	}

	// Exactly one invocation. StreamableClientTransport is also constructed
	// with MaxRetries=-1, disabling SDK reconnect retries beneath this boundary.
	raw, err := callMutationOnce(callCtx, tool, args, c.client.callTool)
	if err != nil {
		_ = c.client.closeSessionLocked()
		kind, code, _, _ := MutationErrorFacts(err)
		c.client.setError("provider mutation " + kind + " (" + code + ")")
		return nil, err
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
		var rejected *toolResultError
		if errors.As(err, &rejected) {
			return nil, mutationError(ErrMutationRejected, "tool_error", rejected.detail)
		}
		return nil, mutationError(ErrMutationOutcomeUnknown, "call_failed", "")
	}
	return raw, nil
}

var (
	providerAuthorizationPattern = regexp.MustCompile(`(?i)(authorization)\s*[:=]?\s*(bearer\s+)?[^\s,;]+`)
	providerSecretPattern        = regexp.MustCompile(`(?i)(bearer|access[_-]?token|refresh[_-]?token)\s*[:=]?\s*[^\s,;]+`)
	providerAccountPattern       = regexp.MustCompile(`\b[0-9]{8,12}\b`)
	providerURLPattern           = regexp.MustCompile(`https?://[^\s]+`)
	providerCodePattern          = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,63}$`)
)

func sanitizeProviderCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !providerCodePattern.MatchString(value) {
		return "provider_error"
	}
	return value
}

func sanitizeProviderDetail(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = providerAuthorizationPattern.ReplaceAllString(value, "$1=[redacted]")
	value = providerSecretPattern.ReplaceAllString(value, "$1=[redacted]")
	value = providerAccountPattern.ReplaceAllString(value, "[account]")
	value = providerURLPattern.ReplaceAllString(value, "[url]")
	const maxDetailBytes = 256
	if len(value) > maxDetailBytes {
		value = value[:maxDetailBytes]
	}
	return strings.TrimSpace(value)
}

var _ MutationCaller = (*MutationClient)(nil)
