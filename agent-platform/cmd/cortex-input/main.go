package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/inputgateway"
	"alpheus/agentplatform/security"
	_ "github.com/lib/pq"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	principal := env("CORTEX_PRINCIPAL_ID", "cortex-control-1")
	subjectID := env("CORTEX_SUBJECT_PRINCIPAL_ID", "owner-1")
	databaseURL, err := loadSecretEnv("CORTEX_DATABASE_URL_FILE")
	if err != nil {
		return err
	}
	serviceToken, err := loadSecretEnv("CORTEX_INPUT_TOKEN_FILE")
	if err != nil {
		return err
	}
	store, err := blob.NewLocalStore(env("CORTEX_BLOB_ROOT", "/var/lib/alpheus/cortex-blobs"))
	if err != nil {
		return fmt.Errorf("open Cortex BlobStore: %w", err)
	}
	database, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return fmt.Errorf("open Cortex database: %w", err)
	}
	defer database.Close()
	database.SetMaxOpenConns(8)
	database.SetMaxIdleConns(4)
	database.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		return fmt.Errorf("ping Cortex database: %w", err)
	}
	adapter, err := inputgateway.NewPostgresAdapter(database, store, principal)
	if err != nil {
		return err
	}
	gateway, err := inputgateway.New(adapter, adapter)
	if err != nil {
		return err
	}
	actor := contracts.AuditActor{PrincipalID: principal, Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceControlAPI}
	subject := contracts.AuditActor{PrincipalID: subjectID, Kind: contracts.PrincipalUser, Audience: contracts.AudienceControlAPI}
	if actor.Validate() != nil || subject.Validate() != nil {
		return fmt.Errorf("invalid Cortex actor configuration")
	}
	handler := inputgateway.NewHandler(gateway, actor, bearerSubject(serviceToken, subject))
	server := &http.Server{
		Addr:              env("CORTEX_INPUT_ADDR", ":8400"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	log.Printf("Cortex Input Gateway listening on %s as %s", server.Addr, principal)
	return server.ListenAndServe()
}

func bearerSubject(token string, subject contracts.AuditActor) func(*http.Request) (contracts.AuditActor, error) {
	expected := []byte("Bearer " + token)
	return func(request *http.Request) (contracts.AuditActor, error) {
		values := request.Header.Values("Authorization")
		if len(values) != 1 || len(values[0]) != len(expected) || subtle.ConstantTimeCompare([]byte(values[0]), expected) != 1 {
			return contracts.AuditActor{}, fmt.Errorf("invalid Cortex input credential")
		}
		return subject, nil
	}
}

func loadSecretEnv(name string) (string, error) {
	path := strings.TrimSpace(os.Getenv(name))
	if path == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	raw, err := security.LoadSecret(path)
	if err != nil {
		return "", fmt.Errorf("load %s: %w", name, err)
	}
	return string(raw), nil
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
