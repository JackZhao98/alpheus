package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/rhmcp"
)

func main() {
	action := flag.String("action", "discover", "auth, discover, accounts, bind, or capture")
	tokenFile := flag.String("token-file", os.Getenv("RH_MCP_TOKEN_FILE"), "0600 OAuth state file")
	out := flag.String("out", "", "secret-free capability snapshot output")
	accountLast4 := flag.String("account-last4", "", "last four digits of the explicitly selected account")
	bindingFile := flag.String("binding-file", "", "0600 output file for the exact selected account id")
	privateDir := flag.String("private-dir", "", "private 0700 directory for raw read-only discovery fixtures")
	flag.Parse()
	if *tokenFile == "" {
		log.Fatal("-token-file or RH_MCP_TOKEN_FILE is required")
	}
	ctx := context.Background()
	switch *action {
	case "auth":
		if err := os.MkdirAll(filepath.Dir(*tokenFile), 0o700); err != nil {
			log.Fatal("create token directory")
		}
		if err := rhmcp.Authorize(ctx, *tokenFile, func(url string) {
			fmt.Println(url)
		}); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Robinhood MCP authorization stored successfully")
	case "discover":
		if *out == "" {
			log.Fatal("-out is required for discovery")
		}
		client, err := rhmcp.New(rhmcp.Config{TokenFile: *tokenFile})
		if err != nil {
			log.Fatal(err)
		}
		defer client.Close()
		tools, err := client.Discover(ctx)
		if err != nil {
			log.Fatal(err)
		}
		snapshot := rhmcp.CapabilitySnapshot{
			Version: "rh-mcp-v1", Endpoint: rhmcp.DefaultEndpoint,
			GeneratedAt: time.Now().UTC(), Tools: tools,
		}
		if err := rhmcp.SaveCapabilitySnapshot(*out, snapshot); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote %d tool schemas to %s\n", len(tools), *out)
	case "accounts":
		client, err := rhmcp.New(rhmcp.Config{TokenFile: *tokenFile, AllowedTools: []string{"get_accounts"}})
		if err != nil {
			log.Fatal(err)
		}
		defer client.Close()
		accounts, err := broker.RobinhoodAccountChoices(ctx, client)
		if err != nil {
			log.Fatal(err)
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(accounts); err != nil {
			log.Fatal("encode masked accounts")
		}
	case "bind":
		if len(*accountLast4) != 4 || strings.Trim(*accountLast4, "0123456789") != "" || *bindingFile == "" {
			log.Fatal("bind requires -account-last4 with four digits and -binding-file")
		}
		client, err := rhmcp.New(rhmcp.Config{TokenFile: *tokenFile, AllowedTools: []string{"get_accounts"}})
		if err != nil {
			log.Fatal(err)
		}
		defer client.Close()
		accounts, err := broker.RobinhoodAccountChoices(ctx, client)
		if err != nil {
			log.Fatal(err)
		}
		var selected *broker.RobinhoodAccountChoice
		for i := range accounts {
			if !strings.HasSuffix(accounts[i].AccountNumber, *accountLast4) {
				continue
			}
			if selected != nil {
				log.Fatal("selected account suffix is ambiguous")
			}
			selected = &accounts[i]
		}
		if selected == nil || !selected.AgenticAllowed || selected.State != "active" || selected.Deactivated || selected.PermanentlyDeactivated {
			log.Fatal("selected account is not an active agentic account")
		}
		if err := savePrivateValue(*bindingFile, selected.AccountNumber); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("bound active agentic account %s\n", selected.MaskedAccount)
	case "capture":
		if *bindingFile == "" || *privateDir == "" {
			log.Fatal("capture requires -binding-file and -private-dir")
		}
		if err := captureReadFixtures(ctx, *tokenFile, *bindingFile, *privateDir); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatal("-action must be auth, discover, accounts, bind, or capture")
	}
}

func savePrivateValue(path, value string) error {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(value) == "" {
		return fmt.Errorf("private value path and value are required")
	}
	return savePrivateBytes(path, []byte(strings.TrimSpace(value)+"\n"))
}

func savePrivateBytes(path string, value []byte) error {
	if strings.TrimSpace(path) == "" || len(value) == 0 {
		return fmt.Errorf("private file path and value are required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create private value directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure private value directory")
	}
	temp, err := os.CreateTemp(dir, ".alpheus-private-*")
	if err != nil {
		return fmt.Errorf("create private value")
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("secure private value")
	}
	if _, err := temp.Write(value); err != nil {
		temp.Close()
		return fmt.Errorf("write private value")
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync private value")
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close private value")
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("persist private value")
	}
	return nil
}

func loadPrivateValue(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() < 1 || info.Size() > 256 {
		return "", fmt.Errorf("private binding must be a regular 0600 file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read private binding")
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, "\r\n\t ") {
		return "", fmt.Errorf("private binding is invalid")
	}
	return value, nil
}
