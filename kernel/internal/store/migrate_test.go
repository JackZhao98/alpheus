package store

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMigrationsOrdersAndChecksums(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"0002_second.sql": "SELECT 2;\n",
		"0001_first.sql":  "SELECT 1;\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	migrations, err := LoadMigrations(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 2 || migrations[0].Version != 1 || migrations[1].Version != 2 {
		t.Fatalf("migrations=%+v", migrations)
	}
	want := sha256.Sum256([]byte(files["0001_first.sql"]))
	if migrations[0].Checksum != want {
		t.Fatalf("checksum=%x want=%x", migrations[0].Checksum, want)
	}
}

func TestLoadMigrationsRejectsUnsafeSets(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{name: "gap", files: map[string]string{"0001_one.sql": "SELECT 1", "0003_three.sql": "SELECT 3"}, want: "contiguous"},
		{name: "bad name", files: map[string]string{"0001-one.sql": "SELECT 1"}, want: "invalid migration filename"},
		{name: "empty", files: map[string]string{"0001_one.sql": " \n"}, want: "read migration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, contents := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := LoadMigrations(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want substring %q", err, tc.want)
			}
		})
	}
}
