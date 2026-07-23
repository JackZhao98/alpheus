package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "credential migration failed:", err)
		os.Exit(1)
	}
}
func run() error {
	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	root := os.Getenv("AGENT_WEB_SESSION_KEY")
	name := strings.TrimSpace(os.Getenv("AGENT_SECRET_NAME"))
	target := strings.TrimSpace(os.Getenv("AGENT_SECRET_TARGET_FILE"))
	if name == "" {
		name = "openai"
	}
	if target == "" {
		target = strings.TrimSpace(os.Getenv("CORTEX_OPENAI_API_KEY_FILE"))
	}
	if dbURL == "" || len(root) < 32 || target == "" || (name != "openai" && name != "gexbot") {
		if os.Getenv("AGENT_SECRET_OPTIONAL") == "true" {
			return nil
		}
		return fmt.Errorf("required migration configuration is unavailable")
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var envelope []byte
	if err := db.QueryRowContext(ctx, "SELECT ciphertext FROM agent_secret WHERE name=$1", name).Scan(&envelope); err != nil {
		if err == sql.ErrNoRows && os.Getenv("AGENT_SECRET_OPTIONAL") == "true" {
			return nil
		}
		return err
	}
	plaintext, err := open(root, name, envelope)
	if err != nil {
		return err
	}
	defer func() {
		for i := range plaintext {
			plaintext[i] = 0
		}
	}()
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, plaintext, 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
func open(root, name string, envelope []byte) ([]byte, error) {
	gcm, err := gcm(root)
	if err != nil {
		return nil, err
	}
	if len(envelope) < 1+gcm.NonceSize()+gcm.Overhead() || envelope[0] != 1 {
		return nil, fmt.Errorf("invalid credential envelope")
	}
	nonce := envelope[1 : 1+gcm.NonceSize()]
	value, err := gcm.Open(nil, nonce, envelope[1+gcm.NonceSize():], []byte("alpheus-agent-secret-v1\n"+name))
	if err != nil {
		return nil, fmt.Errorf("decrypt credential")
	}
	return value, nil
}
func gcm(root string) (cipher.AEAD, error) {
	mac := hmac.New(sha256.New, []byte(root))
	_, _ = mac.Write([]byte("alpheus-agent-secret-wrapping-key-v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
