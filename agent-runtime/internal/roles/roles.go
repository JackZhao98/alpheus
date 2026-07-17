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
	// PromptSlotOrder preserves the role card's authored order. PromptSlots is a
	// map for convenient lookup, but map iteration must never reorder a prompt.
	PromptSlotOrder []string `yaml:"-"`
}

func (r *Role) UnmarshalYAML(node *yaml.Node) error {
	type plainRole Role
	var decoded plainRole
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*r = Role(decoded)
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != "prompt_slots" {
			continue
		}
		mapping := node.Content[i+1]
		for j := 0; j+1 < len(mapping.Content); j += 2 {
			r.PromptSlotOrder = append(r.PromptSlotOrder, mapping.Content[j].Value)
		}
		break
	}
	return nil
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
