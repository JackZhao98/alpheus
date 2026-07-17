// Package rhmcp owns the single Robinhood MCP transport boundary. Higher
// layers only see normalized JSON documents and never receive OAuth state,
// response headers, or raw transport errors. Read and mutation clients are
// distinct types because mutation calls must never inherit caching or retries.
package rhmcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	DefaultEndpoint = "https://agent.robinhood.com/mcp/trading"
	maxResultBytes  = 4 << 20
)

type Config struct {
	Endpoint     string
	TokenFile    string
	CallTimeout  time.Duration
	ConnectWait  time.Duration
	IdleTTL      time.Duration
	CacheTTL     time.Duration
	CacheLimit   int
	RatePerSec   float64
	RateBurst    int
	HTTPClient   *http.Client
	AllowedTools []string
}

type ToolSchema struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

type Status struct {
	Connected          bool
	LastSuccessfulRead time.Time
	LastError          string
	SchemaDrift        bool
}

type Caller interface {
	Call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error)
}

type DriftReporter interface {
	MarkSchemaDrift()
}

type DataErrorReporter interface {
	MarkDataError()
}

type cacheEntry struct {
	data      json.RawMessage
	expiresAt time.Time
}

type tokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	last     time.Time
	rate     float64
	capacity float64
}

func newTokenBucket(rate float64, burst int) *tokenBucket {
	if rate <= 0 {
		rate = 4
	}
	if burst < 1 {
		burst = 4
	}
	return &tokenBucket{tokens: float64(burst), last: time.Now(), rate: rate, capacity: float64(burst)}
}

func (b *tokenBucket) Wait(ctx context.Context) error {
	for {
		b.mu.Lock()
		now := time.Now()
		b.tokens += now.Sub(b.last).Seconds() * b.rate
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}
		wait := time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
		b.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Client serializes all MCP calls over one persistent session. There is one
// CallTool expression in this package; reconnect retries return through it.
type Client struct {
	cfg Config

	mu            sync.Mutex
	session       *mcp.ClientSession
	sessionCancel context.CancelFunc
	lastUsed      time.Time

	cacheMu sync.Mutex
	cache   map[string]cacheEntry
	limiter *tokenBucket
	allowed map[string]struct{}

	statusMu sync.Mutex
	status   Status
}

func New(cfg Config) (*Client, error) {
	for _, tool := range cfg.AllowedTools {
		if !IsSafeQueryTool(tool) {
			return nil, fmt.Errorf("provider tool is not read-allowlisted")
		}
	}
	return newClient(cfg, cfg.AllowedTools)
}

func newClient(cfg Config, allowedTools []string) (*Client, error) {
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = 15 * time.Second
	}
	if cfg.ConnectWait <= 0 {
		cfg.ConnectWait = 30 * time.Second
	}
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = 10 * time.Minute
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 15 * time.Second
	}
	if cfg.CacheLimit < 1 {
		cfg.CacheLimit = 256
	}
	if cfg.HTTPClient == nil {
		transport, err := NewFileTokenTransport(cfg.TokenFile, nil)
		if err != nil {
			return nil, err
		}
		cfg.HTTPClient = &http.Client{Transport: transport}
	}
	allowed := make(map[string]struct{}, len(allowedTools))
	for _, tool := range allowedTools {
		if tool != "" {
			allowed[tool] = struct{}{}
		}
	}
	return &Client{
		cfg: cfg, cache: make(map[string]cacheEntry),
		limiter: newTokenBucket(cfg.RatePerSec, cfg.RateBurst), allowed: allowed,
	}, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeSessionLocked()
}

func (c *Client) closeSessionLocked() error {
	var err error
	if c.session != nil {
		err = c.session.Close()
		c.session = nil
	}
	if c.sessionCancel != nil {
		c.sessionCancel()
		c.sessionCancel = nil
	}
	c.setConnected(false)
	return err
}

func (c *Client) connectLocked(ctx context.Context) error {
	if c.session != nil && time.Since(c.lastUsed) <= c.cfg.IdleTTL {
		return nil
	}
	_ = c.closeSessionLocked()

	lifetime, cancel := context.WithCancel(context.Background())
	transport := &mcp.StreamableClientTransport{
		Endpoint: c.cfg.Endpoint, HTTPClient: c.cfg.HTTPClient, MaxRetries: -1,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "alpheus-kernel", Version: "m8a"}, nil)
	type result struct {
		session *mcp.ClientSession
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		session, err := client.Connect(lifetime, transport, nil)
		resultCh <- result{session: session, err: err}
	}()
	timer := time.NewTimer(c.cfg.ConnectWait)
	defer timer.Stop()
	select {
	case result := <-resultCh:
		if result.err != nil {
			cancel()
			return fmt.Errorf("mcp connect failed")
		}
		c.session, c.sessionCancel, c.lastUsed = result.session, cancel, time.Now()
		c.setConnected(true)
		return nil
	case <-timer.C:
		cancel()
		go func() {
			result := <-resultCh
			if result.session != nil {
				_ = result.session.Close()
			}
		}()
		return fmt.Errorf("mcp connect timed out")
	case <-ctx.Done():
		cancel()
		go func() {
			result := <-resultCh
			if result.session != nil {
				_ = result.session.Close()
			}
		}()
		return fmt.Errorf("mcp connect timed out")
	}
}

func cacheKey(tool string, args map[string]any) (string, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("encode tool arguments")
	}
	digest := sha256.Sum256(append(append([]byte(tool), 0), raw...))
	return hex.EncodeToString(digest[:]), nil
}

func (c *Client) cacheGet(key string) (json.RawMessage, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	entry, ok := c.cache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(c.cache, key)
		return nil, false
	}
	return append(json.RawMessage(nil), entry.data...), true
}

func (c *Client) cachePut(key string, data json.RawMessage) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	now := time.Now()
	for cacheKey, entry := range c.cache {
		if now.After(entry.expiresAt) {
			delete(c.cache, cacheKey)
		}
	}
	if len(c.cache) >= c.cfg.CacheLimit {
		oldestKey := ""
		var oldest time.Time
		for cacheKey, entry := range c.cache {
			if oldestKey == "" || entry.expiresAt.Before(oldest) {
				oldestKey, oldest = cacheKey, entry.expiresAt
			}
		}
		delete(c.cache, oldestKey)
	}
	c.cache[key] = cacheEntry{data: append(json.RawMessage(nil), data...), expiresAt: now.Add(c.cfg.CacheTTL)}
}

func (c *Client) Call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	if _, ok := c.allowed[tool]; !ok {
		return nil, fmt.Errorf("provider tool is not read-allowlisted")
	}
	key, err := cacheKey(tool, args)
	if err != nil {
		return nil, err
	}
	if data, ok := c.cacheGet(key); ok {
		return data, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if data, ok := c.cacheGet(key); ok {
		return data, nil
	}
	callCtx, cancel := context.WithTimeout(ctx, c.cfg.CallTimeout)
	defer cancel()
	if err := c.limiter.Wait(callCtx); err != nil {
		c.setError("rate limit wait timed out")
		return nil, fmt.Errorf("provider request timed out")
	}

	var raw json.RawMessage
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.connectLocked(callCtx); err != nil {
			c.setError(err.Error())
			return nil, err
		}
		raw, err = c.callTool(callCtx, tool, args)
		if err == nil {
			c.lastUsed = time.Now()
			c.cachePut(key, raw)
			c.setSuccess()
			return raw, nil
		}
		_ = c.closeSessionLocked()
		if attempt == 0 && callCtx.Err() == nil {
			continue
		}
	}
	c.setError("provider call failed")
	return nil, fmt.Errorf("provider call failed")
}

// callTool is the sole MCP CallTool boundary in Alpheus.
func (c *Client) callTool(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	result, err := c.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, fmt.Errorf("tool returned an error")
	}
	if result.StructuredContent != nil {
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil || len(raw) > maxResultBytes {
			return nil, fmt.Errorf("invalid structured tool result")
		}
		return raw, nil
	}
	var textResult bytes.Buffer
	foundText := false
	for _, content := range result.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok {
			continue
		}
		foundText = true
		if textResult.Len()+len(text.Text) > maxResultBytes {
			return nil, fmt.Errorf("invalid text tool result")
		}
		textResult.WriteString(text.Text)
	}
	if foundText {
		raw := textResult.Bytes()
		if !json.Valid(raw) {
			return nil, fmt.Errorf("invalid text tool result")
		}
		return append(json.RawMessage(nil), raw...), nil
	}
	return nil, fmt.Errorf("tool result has no JSON content")
}

func (c *Client) Discover(ctx context.Context) ([]ToolSchema, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, c.cfg.CallTimeout)
	defer cancel()
	if err := c.connectLocked(callCtx); err != nil {
		return nil, err
	}
	var out []ToolSchema
	cursor := ""
	for {
		result, err := c.session.ListTools(callCtx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			_ = c.closeSessionLocked()
			return nil, fmt.Errorf("list tools failed")
		}
		for _, tool := range result.Tools {
			input, err := json.Marshal(tool.InputSchema)
			if err != nil {
				return nil, fmt.Errorf("invalid input schema for %s", tool.Name)
			}
			var output json.RawMessage
			if tool.OutputSchema != nil {
				output, err = json.Marshal(tool.OutputSchema)
				if err != nil {
					return nil, fmt.Errorf("invalid output schema for %s", tool.Name)
				}
			}
			out = append(out, ToolSchema{Name: tool.Name, Description: tool.Description, InputSchema: input, OutputSchema: output})
		}
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *Client) Status() Status {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	return c.status
}

func (c *Client) setConnected(connected bool) {
	c.statusMu.Lock()
	c.status.Connected = connected
	c.statusMu.Unlock()
}

func (c *Client) setSuccess() {
	c.statusMu.Lock()
	c.status.Connected = true
	c.status.LastSuccessfulRead = time.Now().UTC()
	c.status.LastError = ""
	c.statusMu.Unlock()
}

func (c *Client) MarkSchemaDrift() {
	c.statusMu.Lock()
	c.status.SchemaDrift = true
	c.status.LastError = "provider schema drift"
	c.statusMu.Unlock()
}

func (c *Client) MarkDataError() {
	c.setError("provider data validation failed")
}

func (c *Client) setError(message string) {
	c.statusMu.Lock()
	c.status.LastError = message
	c.statusMu.Unlock()
}
