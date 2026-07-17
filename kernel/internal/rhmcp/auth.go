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

func registerClient(ctx context.Context, client *http.Client) (registrationResponse, error) {
	body, err := json.Marshal(map[string]any{
		"client_name":                "Alpheus",
		"redirect_uris":              []string{loopbackRedirect},
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"scope":                      "internal",
		"resource":                   DefaultEndpoint,
	})
	if err != nil {
		return registrationResponse{}, fmt.Errorf("encode registration")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationEndpoint, bytes.NewReader(body))
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

func exchangeCode(ctx context.Context, client *http.Client, registered registrationResponse, code, verifier string) (tokenFile, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", registered.ClientID)
	form.Set("redirect_uri", loopbackRedirect)
	form.Set("code_verifier", verifier)
	form.Set("resource", DefaultEndpoint)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, robinhoodTokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenFile{}, fmt.Errorf("build token exchange")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return tokenFile{}, fmt.Errorf("OAuth token exchange failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode != http.StatusOK {
		return tokenFile{}, fmt.Errorf("OAuth token exchange rejected")
	}
	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.AccessToken == "" || result.ExpiresIn <= 0 {
		return tokenFile{}, fmt.Errorf("OAuth token response invalid")
	}
	return tokenFile{
		Version: 1, AccessToken: result.AccessToken, RefreshToken: result.RefreshToken,
		TokenType: result.TokenType, ExpiresAt: time.Now().UTC().Add(time.Duration(result.ExpiresIn) * time.Second),
		ClientID: registered.ClientID,
	}, nil
}

// Authorize performs the interactive loopback OAuth flow and persists only the
// resulting 0600 secret file. The callback page never renders the code/token.
func Authorize(ctx context.Context, tokenPath string, authorizationURL func(string)) error {
	client := &http.Client{Timeout: 30 * time.Second}
	registered, err := registerClient(ctx, client)
	if err != nil {
		return err
	}
	state, err := randomBase64URL(32)
	if err != nil {
		return fmt.Errorf("generate OAuth state")
	}
	verifier, err := randomBase64URL(48)
	if err != nil {
		return fmt.Errorf("generate PKCE verifier")
	}
	challengeRaw := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeRaw[:])
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", registered.ClientID)
	params.Set("redirect_uri", loopbackRedirect)
	params.Set("state", state)
	params.Set("scope", "internal")
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("resource", DefaultEndpoint)

	listener, err := net.Listen("tcp", "127.0.0.1:8399")
	if err != nil {
		return fmt.Errorf("start OAuth callback listener")
	}
	defer listener.Close()
	callbackCh := make(chan oauthCallback, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = oauthCallbackHandler(state, callbackCh)
	go func() { _ = server.Serve(listener) }()
	defer server.Shutdown(context.Background())

	authorizationURL(authorizationEndpoint + "?" + params.Encode())
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	select {
	case <-waitCtx.Done():
		return fmt.Errorf("OAuth authorization timed out")
	case callback := <-callbackCh:
		token, err := exchangeCode(waitCtx, client, registered, callback.code, verifier)
		if err != nil {
			return err
		}
		if err := saveTokenFile(tokenPath, token); err != nil {
			return err
		}
		return nil
	}
}
