package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/policy"
	"alpheus/kernel/internal/store"
)

type kernelPolicyCommandInput struct {
	File               string
	ExpectedGeneration int64
	RecordedBy         string
	Reason             string
}

// kernel-policy is the explicit K1 bootstrap/activation path. Normal server
// startup never imports a file and this command never loads broker credentials.
//
// Bootstrap example:
//
//	kernel kernel-policy --file=/limits.yaml --expected-generation=0 \
//	  --recorded-by=deploy:jack --reason='initial typed Kernel policy'
func runKernelPolicyCommand(args []string, output io.Writer) error {
	input, err := parseKernelPolicyArgs(args)
	if err != nil {
		return err
	}
	typedPolicy, err := policy.LoadBootstrapFile(input.File)
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
	revision, err := st.RecordKernelPolicyRevision(store.RecordKernelPolicyRevisionInput{
		Policy: typedPolicy, ExpectedGeneration: input.ExpectedGeneration,
		RecordedBy: input.RecordedBy, Reason: input.Reason,
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(map[string]any{
		"status": "active", "authority": revision,
	})
}

func parseKernelPolicyArgs(args []string) (kernelPolicyCommandInput, error) {
	var input kernelPolicyCommandInput
	flags := flag.NewFlagSet("kernel-policy", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	file := flags.String("file", "", "strict policy bootstrap YAML file")
	expected := flags.Int64("expected-generation", -1, "active generation, or 0 for bootstrap")
	recordedBy := flags.String("recorded-by", "", "authenticated deployment subject")
	reason := flags.String("reason", "", "audited policy reason")
	if err := flags.Parse(args); err != nil {
		return input, err
	}
	if flags.NArg() != 0 {
		return input, fmt.Errorf("unexpected positional arguments")
	}
	if strings.TrimSpace(*file) == "" {
		return input, fmt.Errorf("--file is required")
	}
	if *expected < 0 {
		return input, fmt.Errorf("--expected-generation is required and must be non-negative")
	}
	if strings.TrimSpace(*recordedBy) == "" {
		return input, fmt.Errorf("--recorded-by is required")
	}
	if strings.TrimSpace(*reason) == "" {
		return input, fmt.Errorf("--reason is required")
	}
	return kernelPolicyCommandInput{
		File: strings.TrimSpace(*file), ExpectedGeneration: *expected,
		RecordedBy: strings.TrimSpace(*recordedBy), Reason: strings.TrimSpace(*reason),
	}, nil
}
