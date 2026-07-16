// Package roles loads role cards. Prompt slots live in the YAML and stay
// blank until filled; the contract binding lives in code.
package roles

import (
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

type Role struct {
	Role            string            `yaml:"role"`
	Version         int               `yaml:"version"` // bump on every prompt edit; journaled per trade
	ModelTier       string            `yaml:"model_tier"`
	OutputSchema    string            `yaml:"output_schema"`
	InjectedContext []string          `yaml:"injected_context"`
	PromptSlots     map[string]string `yaml:"prompt_slots"`
}

func Load(dir string) ([]Role, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out []Role
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var r Role
		if err := yaml.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
