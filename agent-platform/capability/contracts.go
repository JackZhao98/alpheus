// Package capability defines the narrow AP3 cross-plane Tool boundary.
//
// It intentionally contains one external-read Tool only: a public web page
// fetch.  Tool selection and authorization live in Cortex Control; connector
// execution and the evidence record live in Research Gateway.  No connector
// credential, generic request primitive, or external mutation is part of this
// package.
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

const ToolResearchWebFetch ToolID = "research_web_fetch"

type ReceiptState string

const ReceiptSucceeded ReceiptState = "succeeded"

// WebFetchRequest is deliberately narrow: only a public HTTP(S) URL and a
// bounded extraction length can cross from Cortex into Research Gateway.
type WebFetchRequest struct {
	URL      string `json:"url"`
	MaxChars int    `json:"max_chars"`
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

func validID(value string) bool     { return identifierPattern.MatchString(value) }
func validDigest(value string) bool { return digestPattern.MatchString(value) }
func validUTC(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }

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
