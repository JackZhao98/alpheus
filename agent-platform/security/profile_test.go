package security

import (
	"os"
	"path/filepath"
	"testing"

	"alpheus/agentplatform/contracts"
)

func config(profile ProfileID, principal, root string) ProfileConfig {
	spec, ok := Spec(profile)
	if !ok {
		panic("unknown test profile")
	}
	secrets := make(map[string]string, len(spec.RequiredSecrets))
	for _, name := range spec.RequiredSecrets {
		secrets[name] = filepath.Join(root, string(profile)+"-"+name)
	}
	return ProfileConfig{
		SchemaRevision: SchemaRevisionV1, Profile: profile, PrincipalID: principal,
		Audience: spec.Audience, DatabaseRole: spec.DatabaseRole, SecretFiles: secrets,
	}
}

func TestProfileBindingsFailClosed(t *testing.T) {
	value := config(ProfileWorker, "worker-1", t.TempDir())
	if err := value.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	actor, err := value.Actor()
	if err != nil || actor.Kind != contracts.PrincipalWorkload || actor.Audience != contracts.AudienceWorker {
		t.Fatalf("actor=%#v err=%v", actor, err)
	}

	tests := map[string]func(*ProfileConfig){
		"audience escalation": func(value *ProfileConfig) { value.Audience = contracts.AudienceActivator },
		"database escalation": func(value *ProfileConfig) { value.DatabaseRole = "alpheus_agent_activator" },
		"inline secret":       func(value *ProfileConfig) { value.SecretFiles["model_api_key"] = "sk-inline" },
		"unknown secret":      func(value *ProfileConfig) { value.SecretFiles["broker_token"] = "/secret/broker" },
		"missing secret":      func(value *ProfileConfig) { delete(value.SecretFiles, "service_token") },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := config(ProfileWorker, "worker-1", t.TempDir())
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestProfileSetRejectsSharedAuthority(t *testing.T) {
	root := t.TempDir()
	control := config(ProfileControlAPI, "control-1", filepath.Join(root, "control"))
	worker := config(ProfileWorker, "worker-1", filepath.Join(root, "worker"))
	if err := ValidateProfileSet([]ProfileConfig{control, worker}); err != nil {
		t.Fatalf("valid set: %v", err)
	}

	worker.SecretFiles["service_token"] = control.SecretFiles["service_token"]
	if err := ValidateProfileSet([]ProfileConfig{control, worker}); err == nil {
		t.Fatal("shared secret file accepted")
	}
	worker = config(ProfileWorker, "control-1", filepath.Join(root, "worker-2"))
	if err := ValidateProfileSet([]ProfileConfig{control, worker}); err == nil {
		t.Fatal("shared principal accepted")
	}
}

func TestProfileSetRequiresCanonicalOrder(t *testing.T) {
	root := t.TempDir()
	value := ProfileSet{
		SchemaRevision: SchemaRevisionV1,
		Profiles: []ProfileConfig{
			config(ProfileWorker, "worker-1", filepath.Join(root, "worker")),
			config(ProfileControlAPI, "control-1", filepath.Join(root, "control")),
		},
	}
	if err := value.Validate(); err == nil {
		t.Fatal("unsorted profiles accepted")
	}
}

func TestLoadSecret(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "token")
	if err := os.WriteFile(valid, []byte("secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSecret(valid)
	if err != nil || string(got) != "secret-value" {
		t.Fatalf("secret=%q err=%v", got, err)
	}

	loose := filepath.Join(root, "loose")
	if err := os.WriteFile(loose, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecret(loose); err == nil {
		t.Fatal("world-readable secret accepted")
	}

	link := filepath.Join(root, "link")
	if err := os.Symlink(valid, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecret(link); err == nil {
		t.Fatal("secret symlink accepted")
	}

	multiline := filepath.Join(root, "multiline")
	if err := os.WriteFile(multiline, []byte("secret\nsecond"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecret(multiline); err == nil {
		t.Fatal("multiline secret accepted")
	}
}

func TestSupportedProfilesAreStrictlySorted(t *testing.T) {
	values := SupportedProfiles()
	for index := 1; index < len(values); index++ {
		if values[index-1] >= values[index] {
			t.Fatalf("profiles not sorted: %v", values)
		}
	}
}

func TestReturnedSpecCannotMutateRegistry(t *testing.T) {
	spec, ok := Spec(ProfileWorker)
	if !ok {
		t.Fatal("worker spec missing")
	}
	spec.RequiredSecrets[0] = "broker_token"
	value := config(ProfileWorker, "worker-1", t.TempDir())
	if err := value.Validate(); err != nil {
		t.Fatalf("external spec mutation changed registry: %v", err)
	}
}
