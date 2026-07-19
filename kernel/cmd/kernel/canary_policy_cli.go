package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

// canary-policy is the single K0 deployment control path. It intentionally
// does not load broker credentials, limits.yaml, or an HTTP admin surface.
//
// Bootstrap example:
//
//	kernel canary-policy --expected-revision=0 --daily-risk-cap-usd=50 \
//	  --clean-days-before-raise=5 --recorded-by=deploy:jack --reason='initial canary'
//
// Later changes must name the currently active revision. Exact-value retries
// are idempotent. Widening additionally requires --account-id and the exact
// K1C completed-day evidence window.
func dispatchKernelCommand(args []string, output io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "canary-policy":
		return true, runCanaryPolicyCommand(args[1:], output)
	case "canary-attest-day":
		return true, runCanaryDayAttestationCommand(args[1:], output)
	case "kernel-policy":
		return true, runKernelPolicyCommand(args[1:], output)
	default:
		return true, fmt.Errorf("unknown command %q", args[0])
	}
}

func runCanaryDayAttestationCommand(args []string, output io.Writer) error {
	input, err := parseCanaryDayAttestationArgs(args)
	if err != nil {
		return err
	}
	dbTimeout, err := databaseTimeout()
	if err != nil {
		return err
	}
	st, err := store.Open(store.Config{
		URL:           config.Env("DATABASE_URL", "postgresql://alpheus:alpheus@localhost:5432/alpheus?sslmode=disable"),
		MigrationsDir: config.Env("MIGRATIONS_DIR", "../db/migrations"),
		Timeout:       dbTimeout,
		MarketTZ:      config.Env("TZ_MARKET", "America/New_York"),
	})
	if err != nil {
		return err
	}
	defer st.DB.Close()
	attestation, err := st.RecordLiveCanaryDayAttestation(input)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(map[string]any{
		"status": "attested", "attestation": attestation,
	})
}

func runCanaryPolicyCommand(args []string, output io.Writer) error {
	input, err := parseCanaryPolicyArgs(args)
	if err != nil {
		return err
	}
	dbTimeout, err := databaseTimeout()
	if err != nil {
		return err
	}
	st, err := store.Open(store.Config{
		URL:           config.Env("DATABASE_URL", "postgresql://alpheus:alpheus@localhost:5432/alpheus?sslmode=disable"),
		MigrationsDir: config.Env("MIGRATIONS_DIR", "../db/migrations"),
		Timeout:       dbTimeout,
		MarketTZ:      config.Env("TZ_MARKET", "America/New_York"),
	})
	if err != nil {
		return err
	}
	defer st.DB.Close()
	revision, err := st.RecordLiveCanaryRevision(input)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(map[string]any{
		"status": "active", "authority": revision,
	})
}

func parseCanaryPolicyArgs(args []string) (store.RecordLiveCanaryRevisionInput, error) {
	var input store.RecordLiveCanaryRevisionInput
	flags := flag.NewFlagSet("canary-policy", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	capText := flags.String("daily-risk-cap-usd", "", "exact decimal USD cap")
	cleanDays := flags.Int("clean-days-before-raise", 0, "clean days required by policy")
	accountID := flags.String("account-id", "", "bound Provider account; required for widening")
	expectedRevision := flags.Int64("expected-revision", -1, "active revision ID, or 0 for bootstrap")
	recordedBy := flags.String("recorded-by", "", "authenticated deployment subject")
	reason := flags.String("reason", "", "audited policy reason")
	if err := flags.Parse(args); err != nil {
		return input, err
	}
	if flags.NArg() != 0 {
		return input, fmt.Errorf("unexpected positional arguments")
	}
	if *expectedRevision < 0 {
		return input, fmt.Errorf("--expected-revision is required and must be non-negative")
	}
	if *cleanDays <= 0 {
		return input, fmt.Errorf("--clean-days-before-raise must be positive")
	}
	if strings.TrimSpace(*recordedBy) == "" {
		return input, fmt.Errorf("--recorded-by is required")
	}
	if strings.TrimSpace(*reason) == "" {
		return input, fmt.Errorf("--reason is required")
	}
	if *capText == "" {
		return input, fmt.Errorf("--daily-risk-cap-usd is required")
	}
	var cap units.Micros
	if err := json.Unmarshal([]byte(*capText), &cap); err != nil || cap <= 0 {
		return input, fmt.Errorf("--daily-risk-cap-usd must be a positive exact decimal")
	}
	input = store.RecordLiveCanaryRevisionInput{
		DailyAuthorizedRiskCapUSD: cap,
		CleanDaysBeforeRaise:      *cleanDays,
		ExpectedRevisionID:        *expectedRevision,
		AccountID:                 strings.TrimSpace(*accountID),
		RecordedBy:                strings.TrimSpace(*recordedBy),
		Reason:                    strings.TrimSpace(*reason),
	}
	return input, nil
}

func parseCanaryDayAttestationArgs(args []string) (store.RecordLiveCanaryDayAttestationInput, error) {
	var input store.RecordLiveCanaryDayAttestationInput
	flags := flag.NewFlagSet("canary-attest-day", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	accountID := flags.String("account-id", "", "exact bound Provider account")
	marketDay := flags.String("market-day", "", "completed market day in YYYY-MM-DD")
	expectedRevision := flags.Int64("expected-revision", -1, "active canary revision ID")
	attestedBy := flags.String("attested-by", "", "authenticated deployment subject")
	reason := flags.String("reason", "", "audited attestation reason")
	if err := flags.Parse(args); err != nil {
		return input, err
	}
	if flags.NArg() != 0 {
		return input, fmt.Errorf("unexpected positional arguments")
	}
	day, err := time.Parse(time.DateOnly, strings.TrimSpace(*marketDay))
	if err != nil {
		return input, fmt.Errorf("--market-day must be YYYY-MM-DD")
	}
	if strings.TrimSpace(*accountID) == "" {
		return input, fmt.Errorf("--account-id is required")
	}
	if *expectedRevision <= 0 {
		return input, fmt.Errorf("--expected-revision is required and must be positive")
	}
	if strings.TrimSpace(*attestedBy) == "" {
		return input, fmt.Errorf("--attested-by is required")
	}
	if strings.TrimSpace(*reason) == "" {
		return input, fmt.Errorf("--reason is required")
	}
	return store.RecordLiveCanaryDayAttestationInput{
		AccountID: strings.TrimSpace(*accountID), MarketDay: day,
		ExpectedRevisionID: *expectedRevision, AttestedBy: strings.TrimSpace(*attestedBy),
		Reason: strings.TrimSpace(*reason),
	}, nil
}
