package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

const agentSecretEnvelopeVersion byte = 1

var agentSecretNames = map[string]bool{
	"openai":             true,
	"brave":              true,
	"gexbot":             true,
	"robinhood_research": true,
}

func (s *server) getAgentSecrets(w http.ResponseWriter, _ *http.Request) {
	names, err := s.store.ListAgentSecretNames()
	if err != nil {
		writeStoreError(w, "list agent secrets", err)
		return
	}
	configured := map[string]bool{"openai": false, "brave": false, "gexbot": false, "robinhood_research": false}
	for _, name := range names {
		if agentSecretNames[name] {
			configured[name] = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": configured})
}

func (s *server) putAgentSecret(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if !agentSecretNames[name] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown credential provider"})
		return
	}
	var input struct {
		Value string `json:"value"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	input.Value = strings.TrimSpace(input.Value)
	canonical, ok := canonicalAgentSecretValue(name, input.Value)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid credential"})
		return
	}
	ciphertext, err := sealAgentSecret(s.mode.AgentWebSessionKey, name, canonical)
	if err != nil {
		writeInternalError(w, "encrypt agent secret", err)
		return
	}
	if err := s.store.PutAgentSecret(name, ciphertext); err != nil {
		writeStoreError(w, "store agent secret", err)
		return
	}
	s.store.Event("agent_secret_updated", map[string]string{"provider": name})
	writeJSON(w, http.StatusOK, map[string]any{"provider": name, "configured": true})
}

func validAgentSecretValue(name, value string) bool {
	_, ok := canonicalAgentSecretValue(name, value)
	return ok
}

func canonicalAgentSecretValue(name, value string) (string, bool) {
	if value == "" {
		return "", false
	}
	if name != "robinhood_research" {
		if name == "gexbot" {
			return value, validGEXBotAPIKey(value)
		}
		return value, validAgentAPIKey(value)
	}
	compact, err := compactRobinhoodResearchCredential(json.RawMessage(value))
	return compact, err == nil && len(compact) <= 4000
}

func validGEXBotAPIKey(value string) bool {
	if len(value) < 20 || len(value) > 512 || !strings.HasPrefix(value, "gexbot_") {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsSpace)
}

func (s *server) deleteAgentSecret(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if !agentSecretNames[name] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown credential provider"})
		return
	}
	if err := s.store.DeleteAgentSecret(name); err != nil {
		writeStoreError(w, "delete agent secret", err)
		return
	}
	s.store.Event("agent_secret_deleted", map[string]string{"provider": name})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) loadAgentSecret(name string) (string, error) {
	if s.store == nil || !agentSecretNames[name] {
		return "", fmt.Errorf("agent credential unavailable")
	}
	record, err := s.store.GetAgentSecret(name)
	if err != nil {
		return "", err
	}
	if record == nil {
		return "", fmt.Errorf("agent credential is not configured")
	}
	return openAgentSecret(s.mode.AgentWebSessionKey, name, record.Ciphertext)
}

func sealAgentSecret(root, name, plaintext string) ([]byte, error) {
	gcm, err := agentSecretGCM(root)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate credential nonce: %w", err)
	}
	out := make([]byte, 1, 1+len(nonce)+len(plaintext)+gcm.Overhead())
	out[0] = agentSecretEnvelopeVersion
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, []byte(plaintext), agentSecretAAD(name))
	return out, nil
}

func openAgentSecret(root, name string, envelope []byte) (string, error) {
	gcm, err := agentSecretGCM(root)
	if err != nil {
		return "", err
	}
	if len(envelope) < 1+gcm.NonceSize()+gcm.Overhead() || envelope[0] != agentSecretEnvelopeVersion {
		return "", fmt.Errorf("invalid credential envelope")
	}
	nonce := envelope[1 : 1+gcm.NonceSize()]
	plaintext, err := gcm.Open(nil, nonce, envelope[1+gcm.NonceSize():], agentSecretAAD(name))
	if err != nil {
		return "", fmt.Errorf("decrypt credential")
	}
	return string(plaintext), nil
}

func agentSecretGCM(root string) (cipher.AEAD, error) {
	if len(root) < 32 {
		return nil, fmt.Errorf("credential wrapping key is unavailable")
	}
	mac := hmac.New(sha256.New, []byte(root))
	_, _ = mac.Write([]byte("alpheus-agent-secret-wrapping-key-v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func agentSecretAAD(name string) []byte {
	return []byte("alpheus-agent-secret-v1\n" + name)
}
