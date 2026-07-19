// Package release validates digest-bound stage release manifests. AP0 builds
// this verifier; a manifest never grants trading authority or bypasses a later
// stage's own entry gate.
package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"alpheus/agentplatform/canonical"
	"alpheus/agentplatform/contracts"
)

const (
	SchemaRevisionV1    uint16 = 1
	CanonicalProfile           = canonical.Profile
	digestDomain               = "agent-platform.release-manifest.v1"
	maxManifestBytes           = 1 << 20
	maxEvidenceBytes           = 1 << 20
	maxReleaseFileBytes        = 16 << 20
)

var ErrInvalidManifest = errors.New("invalid release manifest")

var ap0RequiredChecks = []string{
	"blob_store",
	"clean_worktree",
	"compose_config",
	"contract_schema",
	"db_delivery",
	"go_test_race",
	"go_vet",
	"gofmt",
	"governance",
	"migration_compatibility",
	"nonmoney_boundary",
	"secret_leaks",
}

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

// CheckEvidence is the stable, source-controlled definition/result summary for
// one mandatory stage check. Raw logs remain certification artifacts; this
// compact record is what the release manifest digest binds.
type CheckEvidence struct {
	SchemaRevision uint16   `json:"schema_revision"`
	Stage          Stage    `json:"stage"`
	SourceCommit   string   `json:"source_commit"`
	Seed           string   `json:"seed"`
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Command        string   `json:"command"`
	Assertions     []string `json:"assertions"`
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
	if value.Stage == StageAP0 && !hasExactChecks(value.Checks, ap0RequiredChecks) {
		return fmt.Errorf("%w: incomplete AP0 check set", ErrInvalidManifest)
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

func RequiredChecks(stage Stage) []string {
	if stage != StageAP0 {
		return nil
	}
	return append([]string(nil), ap0RequiredChecks...)
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

// VerifyFiles binds every manifest document and mandatory check-evidence file
// to actual regular, non-symlink files beneath one trusted checkout root.
func VerifyFiles(root string, manifest Manifest) error {
	if err := manifest.Validate(); err != nil || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return ErrInvalidManifest
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("%w: resolve release root", ErrInvalidManifest)
	}
	rootInfo, err := os.Stat(resolvedRoot)
	if err != nil || !rootInfo.IsDir() {
		return fmt.Errorf("%w: release root is not a directory", ErrInvalidManifest)
	}
	for _, document := range manifest.Documents {
		digest, _, err := releaseFile(root, resolvedRoot, document.Path)
		if err != nil || digest != document.Digest {
			return fmt.Errorf("%w: document mismatch %s", ErrInvalidManifest, document.Path)
		}
	}
	for _, check := range manifest.Checks {
		relative := path.Join("audit", "agent", strings.ToLower(string(manifest.Stage)), "checks", check.Name+".json")
		digest, raw, err := releaseFile(root, resolvedRoot, relative)
		if err != nil || digest != check.EvidenceDigest {
			return fmt.Errorf("%w: check evidence mismatch %s", ErrInvalidManifest, check.Name)
		}
		evidence, err := decodeCheckEvidence(raw)
		if err != nil || evidence.Validate(manifest, check) != nil {
			return fmt.Errorf("%w: invalid check evidence %s", ErrInvalidManifest, check.Name)
		}
	}
	return nil
}

func (value CheckEvidence) Validate(manifest Manifest, check Check) error {
	if value.SchemaRevision != SchemaRevisionV1 || value.Stage != manifest.Stage || value.SourceCommit != manifest.SourceCommit ||
		!validID(value.Seed) || value.Name != check.Name || value.Status != check.Status ||
		(value.Status != "pass" && value.Status != "fail") || strings.TrimSpace(value.Command) == "" || len(value.Command) > 1000 ||
		len(value.Assertions) == 0 || len(value.Assertions) > 64 ||
		!sort.StringsAreSorted(value.Assertions) || !uniqueStrings(len(value.Assertions), func(index int) string { return value.Assertions[index] }) {
		return ErrInvalidManifest
	}
	for _, assertion := range value.Assertions {
		if !validName(assertion) {
			return ErrInvalidManifest
		}
	}
	return nil
}

func decodeCheckEvidence(raw []byte) (*CheckEvidence, error) {
	if len(raw) == 0 || len(raw) > maxEvidenceBytes {
		return nil, ErrInvalidManifest
	}
	strict, err := canonical.JSON(raw)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(strict))
	decoder.DisallowUnknownFields()
	var evidence CheckEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidManifest
	}
	return &evidence, nil
}

func releaseFile(root, resolvedRoot, relative string) (string, []byte, error) {
	if !validRelativePath(relative) {
		return "", nil, ErrInvalidManifest
	}
	current := root
	for _, component := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", nil, ErrInvalidManifest
		}
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return "", nil, err
	}
	contained, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || contained == ".." || strings.HasPrefix(contained, ".."+string(filepath.Separator)) {
		return "", nil, ErrInvalidManifest
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxReleaseFileBytes {
		return "", nil, ErrInvalidManifest
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxReleaseFileBytes+1))
	if err != nil || len(raw) > maxReleaseFileBytes {
		return "", nil, ErrInvalidManifest
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), raw, nil
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

func hasExactChecks(values []Check, required []string) bool {
	if len(values) != len(required) {
		return false
	}
	for index := range values {
		if values[index].Name != required[index] {
			return false
		}
	}
	return true
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
