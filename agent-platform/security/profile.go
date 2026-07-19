// Package security defines credential-isolated Agent Platform process profiles.
// Configurations carry secret file references only; secret values are never
// valid configuration fields. Profile, audience, and database role bindings
// are compiled security boundaries, not prompt- or deployment-selected power.
package security

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"alpheus/agentplatform/contracts"
)

const (
	SchemaRevisionV1 = 1
	maxSecretBytes   = 64 << 10
)

var ErrInvalidProfile = errors.New("invalid security profile")

type ProfileID string

const (
	ProfileControlAPI       ProfileID = "control-api"
	ProfileWorker           ProfileID = "worker"
	ProfileResearchGateway  ProfileID = "research-gateway"
	ProfileGraceIntake      ProfileID = "grace-intake"
	ProfileGraceEngine      ProfileID = "grace-engine"
	ProfileDelegationEngine ProfileID = "delegation-engine"
	ProfileValidator        ProfileID = "validator"
	ProfileActivator        ProfileID = "activator"
)

type ProfileSpec struct {
	Audience        contracts.Audience
	DatabaseRole    string
	RequiredSecrets []string
	OptionalSecrets []string
}

type ProfileConfig struct {
	SchemaRevision uint16             `json:"schema_revision"`
	Profile        ProfileID          `json:"profile"`
	PrincipalID    string             `json:"principal_id"`
	Audience       contracts.Audience `json:"audience"`
	DatabaseRole   string             `json:"database_role"`
	SecretFiles    map[string]string  `json:"secret_files"`
}

type ProfileSet struct {
	SchemaRevision uint16          `json:"schema_revision"`
	Profiles       []ProfileConfig `json:"profiles"`
}

var profileSpecs = map[ProfileID]ProfileSpec{
	ProfileControlAPI: {
		Audience: contracts.AudienceControlAPI, DatabaseRole: "alpheus_agent_control_api",
		RequiredSecrets: []string{"database_url", "service_token"},
	},
	ProfileWorker: {
		Audience: contracts.AudienceWorker, DatabaseRole: "alpheus_agent_worker",
		RequiredSecrets: []string{"database_url", "service_token"},
		OptionalSecrets: []string{"model_api_key"},
	},
	ProfileResearchGateway: {
		Audience: contracts.AudienceResearchGateway, DatabaseRole: "alpheus_research_gateway",
		RequiredSecrets: []string{"database_url", "service_token"},
		OptionalSecrets: []string{"connector_session"},
	},
	ProfileGraceIntake: {
		Audience: contracts.AudienceGraceIntake, DatabaseRole: "alpheus_grace_intake",
		RequiredSecrets: []string{"database_url", "service_token"},
	},
	ProfileGraceEngine: {
		Audience: contracts.AudienceGraceEngine, DatabaseRole: "alpheus_grace_engine",
		RequiredSecrets: []string{"database_url", "service_token"},
	},
	ProfileDelegationEngine: {
		Audience: contracts.AudienceDelegationEngine, DatabaseRole: "alpheus_delegation_engine",
		RequiredSecrets: []string{"database_url", "service_token"},
	},
	ProfileValidator: {
		Audience: contracts.AudienceValidator, DatabaseRole: "alpheus_agent_validator",
		RequiredSecrets: []string{"database_url", "service_token"},
	},
	ProfileActivator: {
		Audience: contracts.AudienceActivator, DatabaseRole: "alpheus_agent_activator",
		RequiredSecrets: []string{"database_url", "service_token"},
	},
}

func SupportedProfiles() []ProfileID {
	values := make([]ProfileID, 0, len(profileSpecs))
	for profile := range profileSpecs {
		values = append(values, profile)
	}
	sort.Slice(values, func(left, right int) bool { return values[left] < values[right] })
	return values
}

func Spec(profile ProfileID) (ProfileSpec, bool) {
	value, ok := profileSpecs[profile]
	if ok {
		value.RequiredSecrets = append([]string(nil), value.RequiredSecrets...)
		value.OptionalSecrets = append([]string(nil), value.OptionalSecrets...)
	}
	return value, ok
}

func (value ProfileConfig) Validate() error {
	spec, ok := Spec(value.Profile)
	if value.SchemaRevision != SchemaRevisionV1 || !ok || !validIdentity(value.PrincipalID) ||
		value.Audience != spec.Audience || value.DatabaseRole != spec.DatabaseRole || value.SecretFiles == nil {
		return ErrInvalidProfile
	}
	allowed := make(map[string]bool, len(spec.RequiredSecrets)+len(spec.OptionalSecrets))
	for _, name := range spec.RequiredSecrets {
		allowed[name] = true
		if _, exists := value.SecretFiles[name]; !exists {
			return fmt.Errorf("%w: required secret file %s is missing", ErrInvalidProfile, name)
		}
	}
	for _, name := range spec.OptionalSecrets {
		allowed[name] = true
	}
	seenPaths := make(map[string]struct{}, len(value.SecretFiles))
	for name, path := range value.SecretFiles {
		if !allowed[name] || !validSecretName(name) || !validSecretPath(path) {
			return ErrInvalidProfile
		}
		if _, exists := seenPaths[path]; exists {
			return fmt.Errorf("%w: secret files alias within profile", ErrInvalidProfile)
		}
		seenPaths[path] = struct{}{}
	}
	return nil
}

func (value ProfileConfig) Actor() (contracts.AuditActor, error) {
	if err := value.Validate(); err != nil {
		return contracts.AuditActor{}, err
	}
	return contracts.AuditActor{
		PrincipalID: value.PrincipalID, Kind: contracts.PrincipalWorkload, Audience: value.Audience,
	}, nil
}

// ValidateProfileSet rejects identity, database-role, or secret-path sharing.
// Even database URL files must differ because they hold distinct credentials.
func ValidateProfileSet(values []ProfileConfig) error {
	if len(values) == 0 {
		return ErrInvalidProfile
	}
	profiles := make(map[ProfileID]struct{}, len(values))
	principals := make(map[string]struct{}, len(values))
	roles := make(map[string]struct{}, len(values))
	secretPaths := make(map[string]struct{})
	for _, value := range values {
		if err := value.Validate(); err != nil {
			return err
		}
		if _, exists := profiles[value.Profile]; exists {
			return fmt.Errorf("%w: duplicate profile", ErrInvalidProfile)
		}
		if _, exists := principals[value.PrincipalID]; exists {
			return fmt.Errorf("%w: shared principal", ErrInvalidProfile)
		}
		if _, exists := roles[value.DatabaseRole]; exists {
			return fmt.Errorf("%w: shared database role", ErrInvalidProfile)
		}
		profiles[value.Profile] = struct{}{}
		principals[value.PrincipalID] = struct{}{}
		roles[value.DatabaseRole] = struct{}{}
		for _, path := range value.SecretFiles {
			if _, exists := secretPaths[path]; exists {
				return fmt.Errorf("%w: shared secret file", ErrInvalidProfile)
			}
			secretPaths[path] = struct{}{}
		}
	}
	return nil
}

func (value ProfileSet) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || len(value.Profiles) == 0 || len(value.Profiles) > len(profileSpecs) {
		return ErrInvalidProfile
	}
	for index := 1; index < len(value.Profiles); index++ {
		if value.Profiles[index-1].Profile >= value.Profiles[index].Profile {
			return fmt.Errorf("%w: profiles must be strictly sorted", ErrInvalidProfile)
		}
	}
	return ValidateProfileSet(value.Profiles)
}

// LoadSecret loads one bounded, owner-only regular file. It removes a single
// conventional trailing line ending and rejects embedded line endings or NUL.
func LoadSecret(path string) ([]byte, error) {
	if !validSecretPath(path) {
		return nil, ErrInvalidProfile
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("stat secret file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidProfile
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open secret file: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() ||
		openedInfo.Mode().Perm()&0o077 != 0 || openedInfo.Size() <= 0 || openedInfo.Size() > maxSecretBytes {
		return nil, ErrInvalidProfile
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxSecretBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxSecretBytes {
		return nil, ErrInvalidProfile
	}
	raw = bytes.TrimSuffix(raw, []byte{'\n'})
	raw = bytes.TrimSuffix(raw, []byte{'\r'})
	if len(raw) == 0 || bytes.ContainsAny(raw, "\x00\r\n") {
		return nil, ErrInvalidProfile
	}
	return raw, nil
}

func validSecretPath(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && len(value) <= 1024
}

func validSecretName(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value {
		if char != '_' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func validIdentity(value string) bool {
	if value == "" || len(value) > 200 || value != strings.TrimSpace(value) {
		return false
	}
	for _, char := range value {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return false
		}
	}
	return true
}
