package roles

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadPreservesPromptSlotOrder(t *testing.T) {
	dir := t.TempDir()
	card := []byte("role: test\nversion: 1\nmodel_tier: monitor\noutput_schema: ExitAction\nprompt_slots:\n  zeta: last alphabetically\n  alpha: first alphabetically\n")
	if err := os.WriteFile(filepath.Join(dir, "test.yaml"), card, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"zeta", "alpha"}
	if len(loaded) != 1 || !reflect.DeepEqual(loaded[0].PromptSlotOrder, want) {
		t.Fatalf("roles=%+v, want order %v", loaded, want)
	}
}
