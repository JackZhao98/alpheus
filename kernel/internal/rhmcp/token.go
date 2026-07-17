package rhmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const robinhoodTokenEndpoint = "https://api.robinhood.com/oauth2/token/"

type tokenFile struct {
	Version      int       `json:"version"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at"`
	ClientID     string    `json:"client_id"`
}

type FileTokenTransport struct {
	mu      sync.Mutex
	path    string
	base    http.RoundTripper
	refresh *http.Client
}

func NewFileTokenTransport(path string, base http.RoundTripper) (*FileTokenTransport, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("RH_MCP_TOKEN_FILE is required")
	}
	if base == nil {
		base = http.DefaultTransport
	}
	transport := &FileTokenTransport{
		path: path, base: base,
		refresh: &http.Client{Timeout: 30 * time.Second},
	}
	if _, err := transport.load(); err != nil {
		return nil, err
	}
	return transport, nil
}

func (t *FileTokenTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.mu.Lock()
	token, err := t.load()
	if err == nil {
		err = t.ensureFresh(request.Context(), &token)
	}
	t.mu.Unlock()
	if err != nil {
		return nil, err
	}
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+token.AccessToken)
	return t.base.RoundTrip(clone)
}

func (t *FileTokenTransport) load() (tokenFile, error) {
	info, err := os.Stat(t.path)
	if err != nil {
		return tokenFile{}, fmt.Errorf("OAuth state unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return tokenFile{}, fmt.Errorf("OAuth state file must be a regular file with mode 0600")
	}
	raw, err := os.ReadFile(t.path)
	if err != nil {
		return tokenFile{}, fmt.Errorf("read OAuth state")
	}
	var token tokenFile
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&token); err != nil {
		return tokenFile{}, fmt.Errorf("decode OAuth state")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return tokenFile{}, fmt.Errorf("decode OAuth state")
	}
	if token.Version != 1 || token.AccessToken == "" || token.ClientID == "" {
		return tokenFile{}, fmt.Errorf("OAuth state is incomplete")
	}
	if token.TokenType != "" && !strings.EqualFold(token.TokenType, "Bearer") {
		return tokenFile{}, fmt.Errorf("unsupported OAuth token type")
	}
	return token, nil
}

func (t *FileTokenTransport) ensureFresh(ctx context.Context, token *tokenFile) error {
	if token.ExpiresAt.IsZero() || time.Until(token.ExpiresAt) > 5*time.Minute {
		return nil
	}
	if token.RefreshToken == "" {
		return fmt.Errorf("OAuth authorization expired; reconnect required")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", token.RefreshToken)
	form.Set("client_id", token.ClientID)
	form.Set("resource", DefaultEndpoint)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, robinhoodTokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build OAuth refresh")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := t.refresh.Do(request)
	if err != nil {
		return fmt.Errorf("OAuth refresh failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OAuth refresh rejected")
	}
	var refreshed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &refreshed); err != nil || refreshed.AccessToken == "" || refreshed.ExpiresIn <= 0 {
		return fmt.Errorf("OAuth refresh response invalid")
	}
	token.AccessToken = refreshed.AccessToken
	if refreshed.RefreshToken != "" {
		token.RefreshToken = refreshed.RefreshToken
	}
	token.TokenType = refreshed.TokenType
	token.ExpiresAt = time.Now().UTC().Add(time.Duration(refreshed.ExpiresIn) * time.Second)
	return t.save(*token)
}

func (t *FileTokenTransport) save(token tokenFile) error {
	return saveTokenFile(t.path, token)
}

func saveTokenFile(path string, token tokenFile) error {
	raw, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("encode OAuth state")
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".rh-oauth-*")
	if err != nil {
		return fmt.Errorf("persist OAuth state")
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("persist OAuth state")
	}
	if _, err := temp.Write(append(raw, '\n')); err != nil {
		temp.Close()
		return fmt.Errorf("persist OAuth state")
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("persist OAuth state")
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("persist OAuth state")
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("persist OAuth state")
	}
	return nil
}
