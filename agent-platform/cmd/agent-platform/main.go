package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"alpheus/agentplatform/release"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agent-platform verify-release [flags]")
	}
	switch args[0] {
	case "verify-release":
		return verifyRelease(args[1:], output)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func verifyRelease(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("verify-release", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	file := flags.String("file", "", "release manifest JSON file")
	expectedStage := flags.String("expect-stage", "", "exact AP stage")
	expectedDigest := flags.String("expect-digest", "", "exact manifest SHA-256")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*file) == "" || strings.TrimSpace(*expectedStage) == "" ||
		strings.TrimSpace(*expectedDigest) == "" {
		return fmt.Errorf("--file, --expect-stage, and --expect-digest are required; positional arguments are forbidden")
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read release manifest: %w", err)
	}
	manifest, digest, err := release.Verify(raw, release.Stage(strings.TrimSpace(*expectedStage)), strings.TrimSpace(*expectedDigest))
	if err != nil {
		return err
	}
	if manifest.Decision != release.DecisionAuthorized {
		return errors.New("release manifest decision is not authorized")
	}
	return json.NewEncoder(output).Encode(map[string]any{
		"status": "verified", "stage": manifest.Stage, "manifest_digest": digest,
		"source_commit": manifest.SourceCommit,
	})
}
