package contractvalidate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"alpheus/agentplatform/delivery"
	"alpheus/agentplatform/security"
)

func TestSecurityAndDeliveryPackInventory(t *testing.T) {
	contractsRoot := filepath.Clean(filepath.Join(packRoot(t), "..", ".."))
	securityRoot := filepath.Join(contractsRoot, "security", "v1")
	deliveryRoot := filepath.Join(contractsRoot, "delivery", "v1")
	securityTypes := validatePackInventory(t, securityRoot)
	deliveryTypes := validatePackInventory(t, deliveryRoot)
	declared := append(securityTypes, deliveryTypes...)
	sort.Strings(declared)
	if strings.Join(declared, ",") != strings.Join(SecurityDeliveryTypes(), ",") {
		t.Fatalf("security/delivery type drift\nvalidator: %v\nmanifest: %v", SecurityDeliveryTypes(), declared)
	}
}

func TestSecurityRoleSQLCoversEveryProfile(t *testing.T) {
	contractsRoot := filepath.Clean(filepath.Join(packRoot(t), "..", ".."))
	raw, err := os.ReadFile(filepath.Join(contractsRoot, "security", "v1", "permissions", "roles.sql"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, profile := range security.SupportedProfiles() {
		spec, ok := security.Spec(profile)
		if !ok || !strings.Contains(text, "'"+spec.DatabaseRole+"'") {
			t.Fatalf("profile %s database role %s missing from roles.sql", profile, spec.DatabaseRole)
		}
	}
	for _, role := range []string{
		"alpheus_agent_migrator", "alpheus_agent_delivery_dispatcher",
		"alpheus_agent_delivery_repair", "alpheus_agent_diagnostics",
		"alpheus_blob_gc", "alpheus_blob_diagnostics",
	} {
		if !strings.Contains(text, "'"+role+"'") {
			t.Fatalf("required operational role %s missing", role)
		}
	}
}

func validatePackInventory(t *testing.T, root string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SchemaRevision int      `json:"schema_revision"`
		Records        []string `json:"records"`
		Commands       []string `json:"commands"`
		Events         []string `json:"events"`
		Assets         struct {
			Schemas          []string `json:"schemas"`
			API              string   `json:"api"`
			Events           string   `json:"events"`
			StateMachines    string   `json:"state_machines"`
			Permissions      string   `json:"permissions"`
			Retention        string   `json:"retention"`
			Canonicalization string   `json:"canonicalization"`
		} `json:"assets"`
		Goldens struct {
			Valid   []string `json:"valid"`
			Invalid []string `json:"invalid"`
			Digests []string `json:"digests"`
		} `json:"goldens"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.SchemaRevision != 1 {
		t.Fatalf("invalid pack manifest: %v", err)
	}
	paths := append([]string{}, manifest.Assets.Schemas...)
	paths = append(paths, manifest.Assets.API, manifest.Assets.Events, manifest.Assets.StateMachines,
		manifest.Assets.Permissions, manifest.Assets.Retention, manifest.Assets.Canonicalization)
	paths = append(paths, manifest.Goldens.Valid...)
	paths = append(paths, manifest.Goldens.Invalid...)
	paths = append(paths, manifest.Goldens.Digests...)
	for _, relative := range paths {
		if relative == "" || filepath.IsAbs(relative) || strings.HasPrefix(filepath.Clean(relative), "..") {
			t.Fatalf("unsafe asset path %q", relative)
		}
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatalf("asset %q: %v", relative, err)
		}
	}
	assertGoldenInventory(t, root, "valid", manifest.Goldens.Valid)
	assertGoldenInventory(t, root, "invalid", manifest.Goldens.Invalid)
	if len(manifest.Goldens.Digests) > 0 {
		assertGoldenInventory(t, root, "digest", manifest.Goldens.Digests)
	}
	for _, relative := range []string{
		"manifest.yaml", manifest.Assets.API, manifest.Assets.Events,
		manifest.Assets.StateMachines, manifest.Assets.Retention,
	} {
		content, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil || !json.Valid(content) {
			t.Fatalf("%s is not JSON-compatible YAML: %v", relative, err)
		}
	}
	types := append([]string{}, manifest.Records...)
	types = append(types, manifest.Commands...)
	types = append(types, manifest.Events...)
	return types
}

func TestSecurityAndDeliveryGoFieldsMatchSchemas(t *testing.T) {
	contractsRoot := filepath.Clean(filepath.Join(packRoot(t), "..", ".."))
	securitySchema := readSchema(t, filepath.Join(contractsRoot, "security", "v1", "schema", "security.schema.json"))
	assertFieldParity(t, reflect.TypeOf(security.ProfileConfig{}), schemaDefinition(t, securitySchema, "ProfileConfig"))
	assertFieldParity(t, reflect.TypeOf(security.ProfileSet{}), schemaDefinition(t, securitySchema, "ProfileSet"))

	deliverySchema := readSchema(t, filepath.Join(contractsRoot, "delivery", "v1", "schema", "delivery.schema.json"))
	types := map[string]reflect.Type{
		"Lease":            reflect.TypeOf(delivery.Lease{}),
		"OutboxRecord":     reflect.TypeOf(delivery.OutboxRecord{}),
		"InboxReceipt":     reflect.TypeOf(delivery.InboxReceipt{}),
		"QuarantineRecord": reflect.TypeOf(delivery.QuarantineRecord{}),
	}
	for name, goType := range types {
		assertFieldParity(t, goType, schemaDefinition(t, deliverySchema, name))
	}
}

func TestSecurityAndDeliveryEnumsMatchSchemas(t *testing.T) {
	contractsRoot := filepath.Clean(filepath.Join(packRoot(t), "..", ".."))
	securitySchema := readSchema(t, filepath.Join(contractsRoot, "security", "v1", "schema", "security.schema.json"))
	profileConfig := schemaDefinition(t, securitySchema, "ProfileConfig")
	profileProperties := profileConfig["properties"].(map[string]any)
	profiles := security.SupportedProfiles()
	profileNames := make([]string, len(profiles))
	for index := range profiles {
		profileNames[index] = string(profiles[index])
	}
	assertEnum(t, profileProperties["profile"].(map[string]any), profileNames)

	deliverySchema := readSchema(t, filepath.Join(contractsRoot, "delivery", "v1", "schema", "delivery.schema.json"))
	outbox := schemaDefinition(t, deliverySchema, "OutboxRecord")["properties"].(map[string]any)
	assertEnum(t, outbox["state"].(map[string]any), []string{
		string(delivery.OutboxAvailable), string(delivery.OutboxDelivered),
		string(delivery.OutboxLeased), string(delivery.OutboxQuarantined),
	})
	quarantine := schemaDefinition(t, deliverySchema, "QuarantineRecord")["properties"].(map[string]any)
	assertEnum(t, quarantine["state"].(map[string]any), []string{
		string(delivery.QuarantineActive), string(delivery.QuarantineReplayRequested),
		string(delivery.QuarantineResolved),
	})
}

func TestSecurityAndDeliveryGoldens(t *testing.T) {
	contractsRoot := filepath.Clean(filepath.Join(packRoot(t), "..", ".."))
	tests := []struct {
		pack         string
		class        string
		filename     string
		digestFile   string
		contractType string
		valid        bool
	}{
		{"security", "valid", "profile_set.json", "profile_set.sha256", "profile_set", true},
		{"security", "invalid", "profile_audience_escalation.json", "", "profile_config", false},
		{"security", "invalid", "profile_set_shared_secret.json", "", "profile_set", false},
		{"delivery", "valid", "inbox_receipt.json", "inbox_receipt.sha256", "inbox_receipt", true},
		{"delivery", "valid", "outbox_available.json", "outbox_available.sha256", "outbox_record", true},
		{"delivery", "valid", "quarantine_active.json", "quarantine_active.sha256", "quarantine_record", true},
		{"delivery", "invalid", "outbox_delivered_with_lease.json", "", "outbox_record", false},
		{"delivery", "invalid", "quarantine_replay_without_generation.json", "", "quarantine_record", false},
	}
	for _, test := range tests {
		name := strings.Join([]string{test.pack, test.class, test.filename}, "/")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(contractsRoot, test.pack, "v1", "golden", test.class, test.filename))
			if err != nil {
				t.Fatal(err)
			}
			_, digest, err := Validate(test.contractType, bytes.NewReader(raw))
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t err=%v", test.valid, err)
			}
			if test.valid {
				expected, err := os.ReadFile(filepath.Join(contractsRoot, test.pack, "v1", "golden", "digest", test.digestFile))
				if err != nil {
					t.Fatal(err)
				}
				if digest != strings.TrimSpace(string(expected)) {
					t.Fatalf("digest=%s expected=%s", digest, strings.TrimSpace(string(expected)))
				}
			}
		})
	}
}
