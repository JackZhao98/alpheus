package rhmcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	registrationEndpoint  = "https://agent.robinhood.com/oauth/trading/register"
	authorizationEndpoint = "https://robinhood.com/oauth"
	loopbackRedirect      = "http://127.0.0.1:8399/mcp-callback"
)

// OAuthEndpoints identifies Robinhood's OAuth endpoints. It is configurable
// only to make the boundary testable; production callers use defaults.
type OAuthEndpoints struct {
	RegistrationEndpoint  string
	AuthorizationEndpoint string
	TokenEndpoint         string
	Resource              string
}

func DefaultOAuthEndpoints() OAuthEndpoints {
	return OAuthEndpoints{
		RegistrationEndpoint:  registrationEndpoint,
		AuthorizationEndpoint: authorizationEndpoint,
		TokenEndpoint:         robinhoodTokenEndpoint,
		Resource:              DefaultEndpoint,
	}
}

func (e OAuthEndpoints) normalized() OAuthEndpoints {
	defaults := DefaultOAuthEndpoints()
	if e.RegistrationEndpoint == "" {
		e.RegistrationEndpoint = defaults.RegistrationEndpoint
	}
	if e.AuthorizationEndpoint == "" {
		e.AuthorizationEndpoint = defaults.AuthorizationEndpoint
	}
	if e.TokenEndpoint == "" {
		e.TokenEndpoint = defaults.TokenEndpoint
	}
	if e.Resource == "" {
		e.Resource = defaults.Resource
	}
	return e
}

// AuthorizationStart is the one-time PKCE state required to finish an OAuth
// connection. Its State and Verifier are secrets and must be retained only in
// a short-lived encrypted server-side record.
type AuthorizationStart struct {
	AuthorizationURL string
	State            string
	Verifier         string
	ClientID         string
	RedirectURI      string
}

type registrationResponse struct {
	ClientID string `json:"client_id"`
}

type oauthCallback struct {
	code  string
	state string
}

func oauthCallbackHandler(expectedState string, callbackCh chan<- oauthCallback) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/mcp-callback" {
			http.NotFound(w, r)
			return
		}
		callback := oauthCallback{code: r.URL.Query().Get("code"), state: r.URL.Query().Get("state")}
		if callback.code == "" || callback.state != expectedState {
			http.Error(w, "Invalid OAuth callback.", http.StatusBadRequest)
			return
		}
		select {
		case callbackCh <- callback:
		default:
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "Robinhood authorization received. You may close this tab.")
	})
}

func randomBase64URL(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func registerClient(ctx context.Context, client *http.Client, redirectURI string, endpoints OAuthEndpoints) (registrationResponse, error) {
	endpoints = endpoints.normalized()
	body, err := json.Marshal(map[string]any{
		"client_name":                "Alpheus",
		"redirect_uris":              []string{redirectURI},
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"scope":                      "internal",
		"resource":                   endpoints.Resource,
	})
	if err != nil {
		return registrationResponse{}, fmt.Errorf("encode registration")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoints.RegistrationEndpoint, bytes.NewReader(body))
	if err != nil {
		return registrationResponse{}, fmt.Errorf("build registration")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return registrationResponse{}, fmt.Errorf("Robinhood client registration failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return registrationResponse{}, fmt.Errorf("Robinhood client registration rejected")
	}
	var registered registrationResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&registered); err != nil || registered.ClientID == "" {
		return registrationResponse{}, fmt.Errorf("Robinhood client registration response invalid")
	}
	return registered, nil
}

func exchangeCode(ctx context.Context, client *http.Client, registered registrationResponse, code, verifier, redirectURI string, endpoints OAuthEndpoints) (OAuthToken, error) {
	endpoints = endpoints.normalized()
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", registered.ClientID)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	form.Set("resource", endpoints.Resource)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoints.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthToken{}, fmt.Errorf("build token exchange")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return OAuthToken{}, fmt.Errorf("OAuth token exchange failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode != http.StatusOK {
		return OAuthToken{}, fmt.Errorf("OAuth token exchange rejected")
	}
	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.AccessToken == "" || result.ExpiresIn <= 0 {
		return OAuthToken{}, fmt.Errorf("OAuth token response invalid")
	}
	return OAuthToken{
		Version: 1, AccessToken: result.AccessToken, RefreshToken: result.RefreshToken,
		TokenType: result.TokenType, ExpiresAt: time.Now().UTC().Add(time.Duration(result.ExpiresIn) * time.Second),
		ClientID: registered.ClientID,
	}, nil
}

func validOAuthRedirectURI(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" {
		return false
	}
	host := parsed.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// BeginAuthorization dynamically registers an OAuth client and constructs a
// PKCE S256 authorization URL. It performs no persistence and never opens a
// browser, so an HTTP application can own its redirect and callback safely.
func BeginAuthorization(ctx context.Context, redirectURI string, client *http.Client, endpoints OAuthEndpoints) (AuthorizationStart, error) {
	if !validOAuthRedirectURI(redirectURI) {
		return AuthorizationStart{}, fmt.Errorf("OAuth callback URL must be HTTPS or local loopback")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	endpoints = endpoints.normalized()
	registered, err := registerClient(ctx, client, redirectURI, endpoints)
	if err != nil {
		return AuthorizationStart{}, err
	}
	state, err := randomBase64URL(32)
	if err != nil {
		return AuthorizationStart{}, fmt.Errorf("generate OAuth state")
	}
	verifier, err := randomBase64URL(48)
	if err != nil {
		return AuthorizationStart{}, fmt.Errorf("generate PKCE verifier")
	}
	challengeRaw := sha256.Sum256([]byte(verifier))
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", registered.ClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)
	params.Set("scope", "internal")
	params.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challengeRaw[:]))
	params.Set("code_challenge_method", "S256")
	params.Set("resource", endpoints.Resource)
	return AuthorizationStart{
		AuthorizationURL: endpoints.AuthorizationEndpoint + "?" + params.Encode(),
		State:            state, Verifier: verifier, ClientID: registered.ClientID, RedirectURI: redirectURI,
	}, nil
}

// ExchangeAuthorizationCode completes an already-started PKCE flow. It keeps
// the raw code and resulting token inside the caller's server process.
func ExchangeAuthorizationCode(ctx context.Context, clientID, code, verifier, redirectURI string, client *http.Client, endpoints OAuthEndpoints) (OAuthToken, error) {
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(code) == "" || strings.TrimSpace(verifier) == "" || !validOAuthRedirectURI(redirectURI) {
		return OAuthToken{}, fmt.Errorf("OAuth callback is invalid")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return exchangeCode(ctx, client, registrationResponse{ClientID: clientID}, code, verifier, redirectURI, endpoints)
}

// Authorize performs the interactive loopback OAuth flow and persists only the
// resulting 0600 secret file. The callback page never renders the code/token.
func Authorize(ctx context.Context, tokenPath string, authorizationURL func(string)) error {
	client := &http.Client{Timeout: 30 * time.Second}
	start, err := BeginAuthorization(ctx, loopbackRedirect, client, DefaultOAuthEndpoints())
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:8399")
	if err != nil {
		return fmt.Errorf("start OAuth callback listener")
	}
	defer listener.Close()
	callbackCh := make(chan oauthCallback, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = oauthCallbackHandler(start.State, callbackCh)
	go func() { _ = server.Serve(listener) }()
	defer server.Shutdown(context.Background())

	authorizationURL(start.AuthorizationURL)
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	select {
	case <-waitCtx.Done():
		return fmt.Errorf("OAuth authorization timed out")
	case callback := <-callbackCh:
		token, err := ExchangeAuthorizationCode(waitCtx, start.ClientID, callback.code, start.Verifier, start.RedirectURI, client, DefaultOAuthEndpoints())
		if err != nil {
			return err
		}
		if err := saveTokenFile(tokenPath, token); err != nil {
			return err
		}
		return nil
	}
}
