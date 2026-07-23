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

	"alpheus/agentplatform/taskgraphcontract"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func taskGraphPackRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(filepath.Clean(filepath.Join(packRoot(t), "..", "..")), "taskgraph", "v1")
}

func TestTaskGraphPackInventoryAndBoundary(t *testing.T) {
	root := taskGraphPackRoot(t)
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
		EffectCeiling  string   `json:"effect_ceiling"`
		Authority      string   `json:"authority"`
		Execution      string   `json:"execution"`
		Assets         struct {
			Schemas          []string `json:"schemas"`
			Retention        string   `json:"retention"`
			Canonicalization string   `json:"canonicalization"`
		} `json:"assets"`
		Goldens struct {
			Valid   []string `json:"valid"`
			Invalid []string `json:"invalid"`
			Digests []string `json:"digests"`
		} `json:"goldens"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil ||
		manifest.Pack != "alpheus.taskgraph" || manifest.Owner != "agent_control" ||
		manifest.SchemaRevision != 1 || manifest.Lifecycle != "frozen" ||
		manifest.EffectCeiling != "external_read" || manifest.Authority != "control_only" ||
		manifest.Execution != "disabled_until_atomic_admission" {
		t.Fatalf("invalid TaskGraph manifest: %v %#v", err, manifest)
	}
	declared := append(append([]string{}, manifest.Records...), manifest.Commands...)
	sort.Strings(declared)
	if strings.Join(declared, ",") != strings.Join(TaskGraphTypes(), ",") {
		t.Fatalf("TaskGraph validator/manifest drift: %v %v", TaskGraphTypes(), declared)
	}
	paths := append([]string{}, manifest.Assets.Schemas...)
	paths = append(paths, manifest.Assets.Retention, manifest.Assets.Canonicalization)
	paths = append(paths, manifest.Goldens.Valid...)
	paths = append(paths, manifest.Goldens.Invalid...)
	paths = append(paths, manifest.Goldens.Digests...)
	for _, relative := range paths {
		if relative == "" || filepath.IsAbs(relative) || strings.HasPrefix(filepath.Clean(relative), "..") {
			t.Fatalf("unsafe TaskGraph asset path %q", relative)
		}
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatalf("missing TaskGraph asset %q: %v", relative, err)
		}
	}
	assertGoldenInventory(t, root, "valid", manifest.Goldens.Valid)
	assertGoldenInventory(t, root, "invalid", manifest.Goldens.Invalid)
	assertGoldenInventory(t, root, "digest", manifest.Goldens.Digests)
}

func TestTaskGraphGoFieldsAndEnumsMatchSchema(t *testing.T) {
	schema := readSchema(t, filepath.Join(taskGraphPackRoot(t), "schema", "taskgraph.schema.json"))
	types := map[string]reflect.Type{
		"ToolGrantSnapshot":     reflect.TypeOf(taskgraphcontract.ToolGrantSnapshot{}),
		"TaskGraphNode":         reflect.TypeOf(taskgraphcontract.TaskGraphNode{}),
		"TaskGraphEdge":         reflect.TypeOf(taskgraphcontract.TaskGraphEdge{}),
		"TaskGraphJoin":         reflect.TypeOf(taskgraphcontract.TaskGraphJoin{}),
		"TaskGraphPlan":         reflect.TypeOf(taskgraphcontract.TaskGraphPlan{}),
		"AdmitTaskGraphCommand": reflect.TypeOf(taskgraphcontract.AdmitTaskGraphCommand{}),
	}
	for name, goType := range types {
		t.Run(name, func(t *testing.T) {
			assertFieldParity(t, goType, schemaDefinition(t, schema, name))
		})
	}
	join := schemaDefinition(t, schema, "TaskGraphJoin")["properties"].(map[string]any)
	assertEnum(t, join["policy"].(map[string]any), []string{"all_required", "minimum_succeeded"})
	assertEnum(t, join["failure_policy"].(map[string]any), []string{"continue_if_threshold_met", "fail_graph"})
}

func TestTaskGraphSchemaAndSemanticGoldens(t *testing.T) {
	root := taskGraphPackRoot(t)
	schemaRaw, err := os.ReadFile(filepath.Join(root, "schema", "taskgraph.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaRaw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	const schemaURL = "https://alpheus.local/contracts/taskgraph/v1/taskgraph.schema.json"
	if err := compiler.AddResource(schemaURL, schemaDoc); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}

	validRaw, err := os.ReadFile(filepath.Join(root, "golden", "valid", "task_graph_plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	validDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(validRaw))
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(validDoc); err != nil {
		t.Fatalf("valid TaskGraph schema golden: %v", err)
	}
	_, digest, err := Validate("task_graph_plan", bytes.NewReader(validRaw))
	if err != nil {
		t.Fatalf("valid TaskGraph semantic golden: %v", err)
	}
	expected, err := os.ReadFile(filepath.Join(root, "golden", "digest", "task_graph_plan.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	if digest != strings.TrimSpace(string(expected)) {
		t.Fatalf("TaskGraph digest=%s expected=%s", digest, strings.TrimSpace(string(expected)))
	}

	invalidRaw, err := os.ReadFile(filepath.Join(root, "golden", "invalid", "task_graph_cycle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Validate("task_graph_plan", bytes.NewReader(invalidRaw)); err == nil {
		t.Fatal("cyclic TaskGraph golden passed semantic validation")
	}
}
