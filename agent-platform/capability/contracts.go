// Package capability defines the narrow AP3 cross-plane Tool boundary and its
// non-authoritative catalog. Tool selection and authorization live in Cortex
// Control; connector execution and the evidence record live in the owning
// plane. No connector credential, generic request primitive, or external
// mutation is part of this package.
package capability

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"alpheus/agentplatform/contracts"
)

const SchemaRevisionV1 uint16 = 1

var (
	ErrInvalidCapability = errors.New("invalid capability contract")
	identifierPattern    = regexp.MustCompile(`^[^\s\x00-\x1f\x7f]{1,200}$`)
	digestPattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type ToolID string

const (
	ToolResearchWebFetch   ToolID = "research_web_fetch"
	ToolResearchGEXBOTAsOf ToolID = "research_gexbot_as_of"

	// ToolKernelEarningsResults is intentionally only a catalog candidate until
	// R2 installs its authorization, Kernel bridge, receipt, and role-grant
	// path. Naming a ToolID must never make a capability callable.
	ToolKernelEarningsResults ToolID = "kernel_earnings_results"
)

type ReceiptState string

const ReceiptSucceeded ReceiptState = "succeeded"

// WebFetchRequest is deliberately narrow: only a public HTTP(S) URL and a
// bounded extraction length can cross from Cortex into Research Gateway.
type WebFetchRequest struct {
	URL      string `json:"url"`
	MaxChars int    `json:"max_chars"`
}

// GEXBOTAsOfRequest deliberately names one archived Provider series and one
// point-in-time fence.  Cortex cannot ask the Provider to collect, mutate, or
// reveal its raw payload through this Tool.
type GEXBOTAsOfRequest struct {
	Symbol   string    `json:"symbol"`
	Category string    `json:"category"`
	AsOf     time.Time `json:"as_of"`
}

// KernelEarningsResultsRequest is one explicit equity symbol. It is not a
// generic Kernel/MCP request envelope and cannot carry an account or method.
type KernelEarningsResultsRequest struct {
	Symbol string `json:"symbol"`
}

type ToolCallIntent struct {
	SchemaRevision uint16               `json:"schema_revision"`
	ToolCallID     string               `json:"tool_call_id"`
	ToolID         ToolID               `json:"tool_id"`
	SourceResult   contracts.RecordRef  `json:"source_result"`
	Request        WebFetchRequest      `json:"request"`
	RequestDigest  string               `json:"request_digest"`
	AuthorizedBy   contracts.AuditActor `json:"authorized_by"`
	AuthorizedAt   time.Time            `json:"authorized_at"`
}

// WebFetchEvidence is normalized, untrusted source material.  It is never an
// instruction and remains owned by Research Gateway in durable storage.
type WebFetchEvidence struct {
	SchemaRevision uint16    `json:"schema_revision"`
	EvidenceID     string    `json:"evidence_id"`
	ToolCallID     string    `json:"tool_call_id"`
	Source         string    `json:"source"`
	URL            string    `json:"url"`
	Title          string    `json:"title,omitempty"`
	ContentType    string    `json:"content_type"`
	Text           string    `json:"text"`
	Truncated      bool      `json:"truncated"`
	ContentDigest  string    `json:"content_digest"`
	ObservedAt     time.Time `json:"observed_at"`
	AvailableAt    time.Time `json:"available_at"`
	ArchivedAt     time.Time `json:"archived_at"`
}

type ToolReceipt struct {
	SchemaRevision uint16               `json:"schema_revision"`
	ReceiptID      string               `json:"receipt_id"`
	ToolCallID     string               `json:"tool_call_id"`
	ToolID         ToolID               `json:"tool_id"`
	RequestDigest  string               `json:"request_digest"`
	State          ReceiptState         `json:"state"`
	Evidence       contracts.RecordRef  `json:"evidence"`
	Executor       contracts.AuditActor `json:"executor"`
	CompletedAt    time.Time            `json:"completed_at"`
}

func (value WebFetchRequest) Validate() error {
	parsed, err := url.Parse(value.URL)
	if err != nil || strings.TrimSpace(value.URL) != value.URL || len(value.URL) > 4000 ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || !publicURLHost(parsed.Hostname()) || parsed.User != nil ||
		value.MaxChars < 1 || value.MaxChars > 12000 {
		return ErrInvalidCapability
	}
	return nil
}

func (value GEXBOTAsOfRequest) Validate() error {
	if !gexbotSymbol(value.Symbol) || !gexbotCategory(value.Category) || !validUTC(value.AsOf) {
		return ErrInvalidCapability
	}
	return nil
}

func (value KernelEarningsResultsRequest) Validate() error {
	if !gexbotSymbol(value.Symbol) {
		return ErrInvalidCapability
	}
	return nil
}

func (value ToolCallIntent) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ToolCallID) || value.ToolID != ToolResearchWebFetch ||
		value.SourceResult.Validate() != nil || value.SourceResult.Owner != contracts.OwnerAgentControl ||
		value.SourceResult.RecordType != "model_call_result" || value.Request.Validate() != nil ||
		!validDigest(value.RequestDigest) || value.AuthorizedBy.Validate() != nil ||
		value.AuthorizedBy.Kind != contracts.PrincipalWorkload || value.AuthorizedBy.Audience != contracts.AudienceControlAPI ||
		!validUTC(value.AuthorizedAt) {
		return ErrInvalidCapability
	}
	return nil
}

func (value WebFetchEvidence) Validate() error {
	parsed, err := url.Parse(value.URL)
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.EvidenceID) || !validID(value.ToolCallID) ||
		value.Source != "web-page-untrusted" || err != nil || strings.TrimSpace(value.URL) != value.URL || len(value.URL) > 4000 ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || !publicURLHost(parsed.Hostname()) || parsed.User != nil ||
		len(value.Title) > 1000 || !allowedContentType(value.ContentType) || strings.TrimSpace(value.Text) == "" || len(value.Text) > 12000 ||
		!validDigest(value.ContentDigest) || !validUTC(value.ObservedAt) || !validUTC(value.AvailableAt) || !validUTC(value.ArchivedAt) ||
		value.AvailableAt.Before(value.ObservedAt) || value.ArchivedAt.Before(value.AvailableAt) {
		return ErrInvalidCapability
	}
	return nil
}

func (value ToolReceipt) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ReceiptID) || !validID(value.ToolCallID) ||
		value.ToolID != ToolResearchWebFetch || !validDigest(value.RequestDigest) || value.State != ReceiptSucceeded ||
		value.Evidence.Validate() != nil || value.Evidence.Owner != contracts.OwnerResearchGateway ||
		value.Evidence.RecordType != "web_fetch_evidence" || value.Executor.Validate() != nil ||
		value.Executor.Kind != contracts.PrincipalWorkload || value.Executor.Audience != contracts.AudienceResearchGateway ||
		!validUTC(value.CompletedAt) {
		return ErrInvalidCapability
	}
	return nil
}

// GEXBOTAsOfEvidence is a compact normalized observation.  Raw Provider
// bytes are retained under the Provider's Blob reference and never cross into
// a Worker prompt.
type GEXBOTAsOfEvidence struct {
	SchemaRevision    uint16             `json:"schema_revision"`
	EvidenceID        string             `json:"evidence_id"`
	ToolCallID        string             `json:"tool_call_id"`
	Provider          string             `json:"provider"`
	Available         bool               `json:"available"`
	Symbol            string             `json:"symbol"`
	Category          string             `json:"category"`
	AsOf              time.Time          `json:"as_of"`
	ObservationID     string             `json:"observation_id,omitempty"`
	ObservationDigest string             `json:"observation_digest,omitempty"`
	ObservedAt        *time.Time         `json:"observed_at,omitempty"`
	AvailableAt       *time.Time         `json:"available_at,omitempty"`
	Metrics           GEXBOTMetrics      `json:"metrics,omitempty"`
	Raw               *GEXBOTRawMetadata `json:"raw,omitempty"`
}

type GEXBOTMetrics struct {
	// Decimal strings preserve the Provider's normalized number exactly while
	// allowing the immutable receipt digest to remain canonical across Go and
	// PostgreSQL. They are evidence values, not executable numeric inputs.
	Spot        *string `json:"spot,omitempty"`
	ZeroGamma   *string `json:"zero_gamma,omitempty"`
	MajorPosVol *string `json:"major_pos_vol,omitempty"`
	MajorPosOI  *string `json:"major_pos_oi,omitempty"`
	MajorNegVol *string `json:"major_neg_vol,omitempty"`
	MajorNegOI  *string `json:"major_neg_oi,omitempty"`
}

type GEXBOTRawMetadata struct {
	BlobID        string `json:"blob_id"`
	ContentDigest string `json:"content_digest"`
	SizeBytes     int64  `json:"size_bytes"`
}

type GEXBOTToolReceipt struct {
	SchemaRevision uint16               `json:"schema_revision"`
	ReceiptID      string               `json:"receipt_id"`
	ToolCallID     string               `json:"tool_call_id"`
	ToolID         ToolID               `json:"tool_id"`
	RequestDigest  string               `json:"request_digest"`
	State          ReceiptState         `json:"state"`
	Evidence       contracts.RecordRef  `json:"evidence"`
	Executor       contracts.AuditActor `json:"executor"`
	CompletedAt    time.Time            `json:"completed_at"`
}

// KernelEarningsObservation is the bounded fact returned by Kernel's narrow
// bridge. It excludes the upstream MCP guide and raw response envelope.
type KernelEarningsObservation struct {
	SchemaRevision uint16               `json:"schema_revision"`
	ToolCallID     string               `json:"tool_call_id"`
	ToolID         ToolID               `json:"tool_id"`
	RequestDigest  string               `json:"request_digest"`
	Provider       string               `json:"provider"`
	Symbol         string               `json:"symbol"`
	Found          bool                 `json:"found"`
	Results        []KernelEarningsItem `json:"results"`
	ObservedAt     time.Time            `json:"observed_at"`
	AvailableAt    time.Time            `json:"available_at"`
}

type KernelEarningsItem struct {
	Symbol  string                    `json:"symbol"`
	Year    int                       `json:"year"`
	Quarter int                       `json:"quarter"`
	EPS     KernelEarningsEPS         `json:"eps"`
	Report  *KernelEarningsReportTime `json:"report"`
}

type KernelEarningsEPS struct {
	Estimate *string `json:"estimate"`
	Actual   *string `json:"actual"`
}

type KernelEarningsReportTime struct {
	Date     *string `json:"date"`
	Timing   *string `json:"timing"`
	Verified bool    `json:"verified"`
}

// KernelEarningsResultsEvidence is durable evidence Cortex may present to a
// Desk. Its source record is Kernel-owned; Control stores only the immutable
// acknowledgement necessary for the Run trace.
type KernelEarningsResultsEvidence struct {
	SchemaRevision uint16               `json:"schema_revision"`
	EvidenceID     string               `json:"evidence_id"`
	ToolCallID     string               `json:"tool_call_id"`
	Provider       string               `json:"provider"`
	Symbol         string               `json:"symbol"`
	Found          bool                 `json:"found"`
	Results        []KernelEarningsItem `json:"results"`
	ObservedAt     time.Time            `json:"observed_at"`
	AvailableAt    time.Time            `json:"available_at"`
}

type KernelEarningsToolReceipt struct {
	SchemaRevision uint16               `json:"schema_revision"`
	ReceiptID      string               `json:"receipt_id"`
	ToolCallID     string               `json:"tool_call_id"`
	ToolID         ToolID               `json:"tool_id"`
	RequestDigest  string               `json:"request_digest"`
	State          ReceiptState         `json:"state"`
	Evidence       contracts.RecordRef  `json:"evidence"`
	Executor       contracts.AuditActor `json:"executor"`
	CompletedAt    time.Time            `json:"completed_at"`
}

func (value GEXBOTAsOfEvidence) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.EvidenceID) || !validID(value.ToolCallID) ||
		value.Provider != "gexbot_classic" || !gexbotSymbol(value.Symbol) || !gexbotCategory(value.Category) || !validUTC(value.AsOf) {
		return ErrInvalidCapability
	}
	if !value.Available {
		if value.ObservationID != "" || value.ObservationDigest != "" || value.ObservedAt != nil || value.AvailableAt != nil ||
			value.Raw != nil || value.Metrics.any() {
			return ErrInvalidCapability
		}
		return nil
	}
	if !validUUID(value.ObservationID) || !validDigest(value.ObservationDigest) || value.ObservedAt == nil || value.AvailableAt == nil ||
		!validUTC(*value.ObservedAt) || !validUTC(*value.AvailableAt) || value.AvailableAt.Before(*value.ObservedAt) || value.AvailableAt.After(value.AsOf) ||
		value.Raw == nil || !validUUID(value.Raw.BlobID) || !validDigest(value.Raw.ContentDigest) || value.Raw.SizeBytes < 1 || value.Raw.SizeBytes > 2<<20 ||
		!value.Metrics.valid() {
		return ErrInvalidCapability
	}
	return nil
}

func (value GEXBOTToolReceipt) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ReceiptID) || !validID(value.ToolCallID) ||
		value.ToolID != ToolResearchGEXBOTAsOf || !validDigest(value.RequestDigest) || value.State != ReceiptSucceeded ||
		value.Evidence.Validate() != nil || value.Evidence.Owner != contracts.OwnerResearchGateway ||
		value.Evidence.RecordType != "gexbot_as_of_evidence" || value.Executor.Validate() != nil ||
		value.Executor.Kind != contracts.PrincipalWorkload || value.Executor.Audience != contracts.AudienceResearchGateway || !validUTC(value.CompletedAt) {
		return ErrInvalidCapability
	}
	return nil
}

func (value KernelEarningsObservation) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ToolCallID) || value.ToolID != ToolKernelEarningsResults ||
		!validDigest(value.RequestDigest) || value.Provider != "kernel_robinhood_mcp" || !gexbotSymbol(value.Symbol) ||
		len(value.Results) > 8 || !validUTC(value.ObservedAt) || !validUTC(value.AvailableAt) || value.AvailableAt.Before(value.ObservedAt) {
		return ErrInvalidCapability
	}
	if !value.Found && len(value.Results) != 0 {
		return ErrInvalidCapability
	}
	for _, result := range value.Results {
		if !validKernelEarningsItem(result, value.Symbol) {
			return ErrInvalidCapability
		}
	}
	return nil
}

func (value KernelEarningsResultsEvidence) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.EvidenceID) || !validID(value.ToolCallID) ||
		value.Provider != "kernel_robinhood_mcp" || !gexbotSymbol(value.Symbol) || len(value.Results) > 8 ||
		!validUTC(value.ObservedAt) || !validUTC(value.AvailableAt) || value.AvailableAt.Before(value.ObservedAt) {
		return ErrInvalidCapability
	}
	if !value.Found && len(value.Results) != 0 {
		return ErrInvalidCapability
	}
	for _, result := range value.Results {
		if !validKernelEarningsItem(result, value.Symbol) {
			return ErrInvalidCapability
		}
	}
	return nil
}

func (value KernelEarningsToolReceipt) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ReceiptID) || !validID(value.ToolCallID) ||
		value.ToolID != ToolKernelEarningsResults || !validDigest(value.RequestDigest) || value.State != ReceiptSucceeded ||
		value.Evidence.Validate() != nil || value.Evidence.Owner != contracts.OwnerKernel || value.Evidence.RecordType != "kernel_earnings_results_evidence" ||
		value.Executor.Validate() != nil || value.Executor.Kind != contracts.PrincipalKernel || value.Executor.Audience != contracts.AudienceKernel ||
		!validUTC(value.CompletedAt) {
		return ErrInvalidCapability
	}
	return nil
}

func validID(value string) bool     { return identifierPattern.MatchString(value) }
func validDigest(value string) bool { return digestPattern.MatchString(value) }
func validUTC(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }

func gexbotSymbol(value string) bool {
	return regexp.MustCompile(`^[A-Z0-9._-]{1,16}$`).MatchString(value)
}

func gexbotCategory(value string) bool {
	return value == "gex_full" || value == "gex_zero" || value == "gex_one"
}

func validUUID(value string) bool {
	return regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(value)
}

func (value GEXBOTMetrics) any() bool {
	return value.Spot != nil || value.ZeroGamma != nil || value.MajorPosVol != nil || value.MajorPosOI != nil || value.MajorNegVol != nil || value.MajorNegOI != nil
}

func (value GEXBOTMetrics) valid() bool {
	for _, metric := range []*string{value.Spot, value.ZeroGamma, value.MajorPosVol, value.MajorPosOI, value.MajorNegVol, value.MajorNegOI} {
		if metric != nil && (len(*metric) < 1 || len(*metric) > 64 || !regexp.MustCompile(`^-?[0-9]+(?:[.][0-9]+)?$`).MatchString(*metric)) {
			return false
		}
	}
	return true
}

func validKernelEarningsItem(item KernelEarningsItem, expectedSymbol string) bool {
	if item.Symbol != expectedSymbol || item.Year < 1900 || item.Year > 2200 || item.Quarter < 1 || item.Quarter > 4 ||
		!validNullableEarningsString(item.EPS.Estimate) || !validNullableEarningsString(item.EPS.Actual) {
		return false
	}
	if item.Report == nil {
		return true
	}
	if item.Report.Date != nil {
		if len(*item.Report.Date) != len("2006-01-02") || strings.TrimSpace(*item.Report.Date) != *item.Report.Date {
			return false
		}
		if _, err := time.Parse("2006-01-02", *item.Report.Date); err != nil {
			return false
		}
	}
	return item.Report.Timing == nil || *item.Report.Timing == "am" || *item.Report.Timing == "pm"
}

func validNullableEarningsString(value *string) bool {
	return value == nil || (len(*value) > 0 && len(*value) <= 64 && strings.TrimSpace(*value) == *value)
}

func allowedContentType(value string) bool {
	switch value {
	case "text/html", "application/xhtml+xml", "text/plain", "application/json":
		return true
	default:
		return false
	}
}

func publicURLHost(host string) bool {
	if host == "" || strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true // DNS resolution and pinning are enforced by Research Gateway.
	}
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsUnspecified() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
}
