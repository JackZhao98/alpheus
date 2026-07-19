package contractvalidate

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"alpheus/agentplatform/blob"
)

func TestBlobPackInventory(t *testing.T) {
	root := filepath.Join(filepath.Clean(filepath.Join(packRoot(t), "..", "..")), "blob", "v1")
	declared := validatePackInventory(t, root)
	sort.Strings(declared)
	if strings.Join(declared, ",") != strings.Join(BlobTypes(), ",") {
		t.Fatalf("blob type drift\nvalidator: %v\nmanifest: %v", BlobTypes(), declared)
	}
}

func TestBlobGoFieldsMatchSchema(t *testing.T) {
	root := filepath.Join(filepath.Clean(filepath.Join(packRoot(t), "..", "..")), "blob", "v1")
	schema := readSchema(t, filepath.Join(root, "schema", "blob.schema.json"))
	types := map[string]reflect.Type{
		"StageGrant":       reflect.TypeOf(blob.StageGrant{}),
		"StagedBlob":       reflect.TypeOf(blob.StagedBlob{}),
		"BlobRef":          reflect.TypeOf(blob.BlobRef{}),
		"ReferenceBinding": reflect.TypeOf(blob.ReferenceBinding{}),
		"LifecycleEvent":   reflect.TypeOf(blob.LifecycleEvent{}),
	}
	for name, goType := range types {
		assertFieldParity(t, goType, schemaDefinition(t, schema, name))
	}
}

func TestBlobEnumsMatchSchema(t *testing.T) {
	root := filepath.Join(filepath.Clean(filepath.Join(packRoot(t), "..", "..")), "blob", "v1")
	schema := readSchema(t, filepath.Join(root, "schema", "blob.schema.json"))
	binding := schemaDefinition(t, schema, "ReferenceBinding")["properties"].(map[string]any)
	assertEnum(t, binding["access_class"].(map[string]any), []string{
		string(blob.AccessExplicit), string(blob.AccessPrivate),
	})
	assertEnum(t, binding["state"].(map[string]any), []string{
		string(blob.BindingActive), string(blob.BindingReleased),
	})
	event := schemaDefinition(t, schema, "LifecycleEvent")["properties"].(map[string]any)
	assertEnum(t, event["subject_kind"].(map[string]any), []string{
		string(blob.SubjectACL), string(blob.SubjectBinding), string(blob.SubjectBlob), string(blob.SubjectStage),
	})
	assertEnum(t, event["transition"].(map[string]any), []string{
		string(blob.TransitionACLGranted), string(blob.TransitionACLRevoked),
		string(blob.TransitionCommitted), string(blob.TransitionDeleted), string(blob.TransitionGCClaimed),
		string(blob.TransitionQuarantined), string(blob.TransitionReferenceBound),
		string(blob.TransitionReferenceReleased), string(blob.TransitionStaged),
	})
}

func TestBlobGoldens(t *testing.T) {
	root := filepath.Join(filepath.Clean(filepath.Join(packRoot(t), "..", "..")), "blob", "v1")
	tests := []struct {
		class, file, digest, contractType string
		valid                             bool
	}{
		{"valid", "stage_grant.json", "stage_grant.sha256", "blob_stage_grant", true},
		{"valid", "staged_blob.json", "staged_blob.sha256", "blob_staged", true},
		{"valid", "blob_ref.json", "blob_ref.sha256", "blob_ref", true},
		{"valid", "reference.json", "reference.sha256", "blob_reference", true},
		{"valid", "lifecycle_event.json", "lifecycle_event.sha256", "blob_lifecycle_event", true},
		{"invalid", "stage_oversized.json", "", "blob_stage_grant", false},
		{"invalid", "active_reference_released.json", "", "blob_reference", false},
	}
	for _, test := range tests {
		t.Run(test.class+"/"+test.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, "golden", test.class, test.file))
			if err != nil {
				t.Fatal(err)
			}
			_, digest, err := Validate(test.contractType, bytes.NewReader(raw))
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t err=%v", test.valid, err)
			}
			if test.valid {
				expected, err := os.ReadFile(filepath.Join(root, "golden", "digest", test.digest))
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
