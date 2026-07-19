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

	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/release"
)

func TestPackInventoryIsComplete(t *testing.T) {
	root := packRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Pack           string   `json:"pack"`
		Owner          string   `json:"owner"`
		SchemaRevision int      `json:"schema_revision"`
		Lifecycle      string   `json:"lifecycle"`
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
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("manifest is not JSON-compatible YAML: %v", err)
	}
	if manifest.Pack != "alpheus.common" || manifest.Owner != "platform_governance" ||
		manifest.SchemaRevision != 1 || manifest.Lifecycle != "frozen" {
		t.Fatalf("invalid pack identity: %#v", manifest)
	}
	declaredTypes := append([]string{}, manifest.Records...)
	declaredTypes = append(declaredTypes, manifest.Commands...)
	declaredTypes = append(declaredTypes, manifest.Events...)
	sort.Strings(declaredTypes)
	if strings.Join(declaredTypes, ",") != strings.Join(CommonTypes(), ",") {
		t.Fatalf("validator/manifest type drift\nvalidator: %v\nmanifest: %v", CommonTypes(), declaredTypes)
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
	assertGoldenInventory(t, root, "digest", manifest.Goldens.Digests)

	jsonYAML := []string{"manifest.yaml", manifest.Assets.API, manifest.Assets.Events,
		manifest.Assets.StateMachines, manifest.Assets.Retention}
	for _, relative := range jsonYAML {
		raw, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil || !json.Valid(raw) {
			t.Fatalf("%s is not strict JSON-compatible YAML: %v", relative, err)
		}
	}
}

func assertGoldenInventory(t *testing.T, root, class string, declared []string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "golden", class))
	if err != nil {
		t.Fatal(err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("unexpected golden directory %s", entry.Name())
		}
		actual = append(actual, filepath.ToSlash(filepath.Join("golden", class, entry.Name())))
	}
	want := append([]string{}, declared...)
	sort.Strings(actual)
	sort.Strings(want)
	if strings.Join(actual, ",") != strings.Join(want, ",") {
		t.Fatalf("%s golden inventory drift\nactual: %v\nmanifest: %v", class, actual, want)
	}
}

func TestGoContractFieldsMatchSchemas(t *testing.T) {
	root := packRoot(t)
	commonSchema := readSchema(t, filepath.Join(root, "schema", "common.schema.json"))
	types := map[string]reflect.Type{
		"AuditActor":            reflect.TypeOf(contracts.AuditActor{}),
		"RecordRef":             reflect.TypeOf(contracts.RecordRef{}),
		"RevisionRef":           reflect.TypeOf(contracts.RevisionRef{}),
		"HeadRef":               reflect.TypeOf(contracts.HeadRef{}),
		"RunOrigin":             reflect.TypeOf(contracts.RunOrigin{}),
		"EffectiveRunAuthority": reflect.TypeOf(contracts.EffectiveRunAuthority{}),
		"CommandEnvelope":       reflect.TypeOf(contracts.CommandEnvelope{}),
		"EventEnvelope":         reflect.TypeOf(contracts.EventEnvelope{}),
		"Freshness":             reflect.TypeOf(contracts.Freshness{}),
		"Failure":               reflect.TypeOf(contracts.Failure{}),
	}
	for name, goType := range types {
		t.Run(name, func(t *testing.T) {
			definition := schemaDefinition(t, commonSchema, name)
			assertFieldParity(t, goType, definition)
		})
	}

	releaseSchema := readSchema(t, filepath.Join(root, "schema", "release.schema.json"))
	assertFieldParity(t, reflect.TypeOf(release.Manifest{}), releaseSchema)
	assertFieldParity(t, reflect.TypeOf(release.CheckEvidence{}), schemaDefinition(t, releaseSchema, "CheckEvidence"))
}

func TestGoEnumsMatchSchema(t *testing.T) {
	root := packRoot(t)
	commonSchema := readSchema(t, filepath.Join(root, "schema", "common.schema.json"))
	tests := map[string][]string{
		"Owner": {
			string(contracts.OwnerAgentControl), string(contracts.OwnerBlob), string(contracts.OwnerDelegation),
			string(contracts.OwnerGrace), string(contracts.OwnerKernel), string(contracts.OwnerPlatformGovernance),
			string(contracts.OwnerResearchGateway), string(contracts.OwnerWorker),
		},
		"PrincipalKind": {
			string(contracts.PrincipalKernel), string(contracts.PrincipalUser), string(contracts.PrincipalWorkload),
		},
		"Audience": {
			string(contracts.AudienceActivator), string(contracts.AudienceControlAPI),
			string(contracts.AudienceDelegationEngine), string(contracts.AudienceGraceEngine),
			string(contracts.AudienceGraceIntake), string(contracts.AudienceKernel),
			string(contracts.AudienceKernelAdmin), string(contracts.AudienceResearchGateway),
			string(contracts.AudienceValidator), string(contracts.AudienceWorker),
		},
		"EffectClass": {
			string(contracts.EffectBrokerMutation), string(contracts.EffectExactConfirmation),
			string(contracts.EffectExternalRead), string(contracts.EffectNone),
			string(contracts.EffectOperationIntent),
		},
		"PlatformMode": {
			string(contracts.ModeDisabled), string(contracts.ModeLiveAutonomous),
			string(contracts.ModeLiveConfirmed), string(contracts.ModeReadOnly), string(contracts.ModeShadow),
		},
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			definition := schemaDefinition(t, commonSchema, name)
			assertEnum(t, definition, want)
		})
	}

	runOrigin := schemaDefinition(t, commonSchema, "RunOrigin")
	properties := runOrigin["properties"].(map[string]any)
	assertEnum(t, properties["kind"].(map[string]any), []string{
		string(contracts.OriginExternalEvent), string(contracts.OriginKernelEvent),
		string(contracts.OriginSchedule), string(contracts.OriginSystemMaintenance),
		string(contracts.OriginSystemRecovery), string(contracts.OriginUserRequest),
	})
}

func readSchema(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var schema map[string]any
	if err := decoder.Decode(&schema); err != nil {
		t.Fatalf("decode schema %s: %v", path, err)
	}
	return schema
}

func schemaDefinition(t *testing.T, schema map[string]any, name string) map[string]any {
	t.Helper()
	definitions, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatal("schema has no $defs")
	}
	definition, ok := definitions[name].(map[string]any)
	if !ok {
		t.Fatalf("schema has no definition %s", name)
	}
	return definition
}

func assertFieldParity(t *testing.T, goType reflect.Type, schema map[string]any) {
	t.Helper()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties")
	}
	schemaFields := make([]string, 0, len(properties))
	for name := range properties {
		schemaFields = append(schemaFields, name)
	}
	sort.Strings(schemaFields)

	goFields, goRequired := jsonFieldSets(goType)
	if strings.Join(goFields, ",") != strings.Join(schemaFields, ",") {
		t.Fatalf("field drift\n go: %v\nschema: %v", goFields, schemaFields)
	}

	requiredValues, ok := schema["required"].([]any)
	if !ok {
		t.Fatal("schema has no required list")
	}
	schemaRequired := make([]string, 0, len(requiredValues))
	for _, value := range requiredValues {
		name, ok := value.(string)
		if !ok {
			t.Fatal("required field is not a string")
		}
		schemaRequired = append(schemaRequired, name)
	}
	sort.Strings(schemaRequired)
	if strings.Join(goRequired, ",") != strings.Join(schemaRequired, ",") {
		t.Fatalf("required-field drift\n go: %v\nschema: %v", goRequired, schemaRequired)
	}
}

func assertEnum(t *testing.T, schema map[string]any, want []string) {
	t.Helper()
	raw, ok := schema["enum"].([]any)
	if !ok {
		t.Fatal("schema has no enum")
	}
	got := make([]string, 0, len(raw))
	for _, value := range raw {
		text, ok := value.(string)
		if !ok {
			t.Fatal("enum value is not a string")
		}
		got = append(got, text)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("enum drift\n go: %v\nschema: %v", want, got)
	}
}

func jsonFieldSets(value reflect.Type) ([]string, []string) {
	var fields []string
	var required []string
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		tag := field.Tag.Get("json")
		if field.Anonymous && tag == "" {
			embeddedFields, embeddedRequired := jsonFieldSets(field.Type)
			fields = append(fields, embeddedFields...)
			required = append(required, embeddedRequired...)
			continue
		}
		name, options, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		fields = append(fields, name)
		if !strings.Contains(options, "omitempty") {
			required = append(required, name)
		}
	}
	sort.Strings(fields)
	sort.Strings(required)
	return fields, required
}
