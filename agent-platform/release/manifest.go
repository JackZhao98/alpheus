// Package release validates digest-bound stage release manifests. AP0 builds
// this verifier; a manifest never grants trading authority or bypasses a later
// stage's own entry gate.
package release

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"

	"alpheus/agentplatform/canonical"
	"alpheus/agentplatform/contracts"
)

const (
	SchemaRevisionV1 uint16 = 1
	CanonicalProfile        = canonical.Profile
	digestDomain            = "agent-platform.release-manifest.v1"
	maxManifestBytes        = 1 << 20
)

var ErrInvalidManifest = errors.New("invalid release manifest")

type Stage string

const (
	StageAP0  Stage = "AP0"
	StageAP1  Stage = "AP1"
	StageAP2  Stage = "AP2"
	StageAP3  Stage = "AP3"
	StageAP4  Stage = "AP4"
	StageAP5  Stage = "AP5"
	StageAP6  Stage = "AP6"
	StageAP7  Stage = "AP7"
	StageAP8  Stage = "AP8"
	StageAP9  Stage = "AP9"
	StageAP10 Stage = "AP10"
	StageAP11 Stage = "AP11"
	StageAP12 Stage = "AP12"
	StageAP13 Stage = "AP13"
	StageAP14 Stage = "AP14"
	StageAP15 Stage = "AP15"
)

type Decision string

const (
	DecisionAuthorized Decision = "authorized"
	DecisionRejected   Decision = "rejected"
)

type Document struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type Check struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	EvidenceDigest string `json:"evidence_digest"`
}

type Manifest struct {
	SchemaRevision   uint16                `json:"schema_revision"`
	CanonicalProfile string                `json:"canonical_profile"`
	ReleaseID        string                `json:"release_id"`
	Stage            Stage                 `json:"stage"`
	Decision         Decision              `json:"decision"`
	SourceCommit     string                `json:"source_commit"`
	CreatedAt        time.Time             `json:"created_at"`
	AuthorizedBy     contracts.AuditActor  `json:"authorized_by"`
	EffectCeiling    contracts.EffectClass `json:"effect_ceiling"`
	Documents        []Document            `json:"documents"`
	Checks           []Check               `json:"checks"`
}

func DecodeStrict(reader io.Reader) (*Manifest, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read release manifest: %w", err)
	}
	if len(raw) > maxManifestBytes {
		return nil, fmt.Errorf("%w: manifest exceeds %d bytes", ErrInvalidManifest, maxManifestBytes)
	}
	strict, err := canonical.JSON(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(strict))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode release manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decode release manifest: trailing JSON value")
		}
		return nil, fmt.Errorf("decode release manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (value Manifest) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || value.CanonicalProfile != CanonicalProfile ||
		!validID(value.ReleaseID) || !knownStage(value.Stage) || !knownDecision(value.Decision) ||
		!validCommit(value.SourceCommit) || value.CreatedAt.IsZero() || value.CreatedAt.Location() != time.UTC ||
		value.AuthorizedBy.Validate() != nil || value.AuthorizedBy.Kind != contracts.PrincipalUser ||
		value.AuthorizedBy.Audience != contracts.AudienceActivator ||
		len(value.Documents) == 0 || len(value.Documents) > 256 || len(value.Checks) == 0 || len(value.Checks) > 256 {
		return ErrInvalidManifest
	}
	if value.Stage == StageAP0 && value.EffectCeiling != contracts.EffectNone {
		return fmt.Errorf("%w: AP0 effect ceiling must be none", ErrInvalidManifest)
	}
	if contracts.ValidateEffectClass(value.EffectCeiling) != nil {
		return fmt.Errorf("%w: invalid effect ceiling", ErrInvalidManifest)
	}
	if !strictlySortedDocuments(value.Documents) || !strictlySortedChecks(value.Checks) {
		return fmt.Errorf("%w: evidence lists must be sorted and unique", ErrInvalidManifest)
	}
	for _, document := range value.Documents {
		if !validRelativePath(document.Path) || !validSHA256(document.Digest) {
			return ErrInvalidManifest
		}
	}
	for _, check := range value.Checks {
		if !validName(check.Name) || (check.Status != "pass" && check.Status != "fail") ||
			!validSHA256(check.EvidenceDigest) || value.Decision == DecisionAuthorized && check.Status != "pass" {
			return ErrInvalidManifest
		}
	}
	return nil
}

func (value Manifest) Digest() (string, error) {
	if err := value.Validate(); err != nil {
		return "", err
	}
	return canonical.Digest(digestDomain, value)
}

func Verify(raw []byte, expectedStage Stage, expectedDigest string) (*Manifest, string, error) {
	if expectedStage != "" && !knownStage(expectedStage) {
		return nil, "", fmt.Errorf("%w: invalid expected stage", ErrInvalidManifest)
	}
	if expectedDigest != "" && !validSHA256(expectedDigest) {
		return nil, "", fmt.Errorf("%w: invalid expected digest", ErrInvalidManifest)
	}
	manifest, err := DecodeStrict(bytes.NewReader(raw))
	if err != nil {
		return nil, "", err
	}
	if expectedStage != "" && manifest.Stage != expectedStage {
		return nil, "", fmt.Errorf("%w: stage=%s want=%s", ErrInvalidManifest, manifest.Stage, expectedStage)
	}
	digest, err := manifest.Digest()
	if err != nil {
		return nil, "", err
	}
	if expectedDigest != "" && digest != expectedDigest {
		return nil, "", fmt.Errorf("%w: digest mismatch", ErrInvalidManifest)
	}
	return manifest, digest, nil
}

func strictlySortedDocuments(values []Document) bool {
	return sort.SliceIsSorted(values, func(left, right int) bool {
		return values[left].Path < values[right].Path
	}) && uniqueStrings(len(values), func(index int) string { return values[index].Path })
}

func strictlySortedChecks(values []Check) bool {
	return sort.SliceIsSorted(values, func(left, right int) bool {
		return values[left].Name < values[right].Name
	}) && uniqueStrings(len(values), func(index int) string { return values[index].Name })
}

func uniqueStrings(length int, at func(int) string) bool {
	for index := 1; index < length; index++ {
		if at(index-1) == at(index) {
			return false
		}
	}
	return true
}

func validRelativePath(value string) bool {
	if value == "" || len(value) > 300 || strings.Contains(value, "\\") || path.IsAbs(value) {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && !strings.HasPrefix(clean, "../")
}

func validCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validID(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 200 {
		return false
	}
	for _, char := range value {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validName(value string) bool {
	if value == "" || len(value) > 80 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value {
		if char < 'a' || char > 'z' {
			if char < '0' || char > '9' {
				if char != '_' && char != '-' && char != '.' {
					return false
				}
			}
		}
	}
	return true
}

func knownStage(value Stage) bool {
	switch value {
	case StageAP0, StageAP1, StageAP2, StageAP3, StageAP4, StageAP5, StageAP6, StageAP7,
		StageAP8, StageAP9, StageAP10, StageAP11, StageAP12, StageAP13, StageAP14, StageAP15:
		return true
	default:
		return false
	}
}

func knownDecision(value Decision) bool {
	return value == DecisionAuthorized || value == DecisionRejected
}
