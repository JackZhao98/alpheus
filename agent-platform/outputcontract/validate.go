// Package outputcontract validates AP1 agent output entirely in memory.
//
// The package deliberately implements a closed JSON Schema profile. It never
// loads schemas or references from the filesystem or network, and a successful
// validation is evidence only: it does not persist a receipt or authorize an
// effect.
package outputcontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	// Profile is the only AP1 output-contract profile accepted by Validate.
	Profile = "alpheus-json-schema-2020-12-local-v1"
	// Dialect is the exact JSON Schema dialect URI required at the schema root.
	Dialect = "https://json-schema.org/draft/2020-12/schema"
	// Implementation identifies the validator recorded in Evidence.
	Implementation = "github.com/santhosh-tekuri/jsonschema/v6"
	// ImplementationVersion is pinned by agent-platform/go.mod.
	ImplementationVersion = "v6.0.2"

	maxDocumentBytes = 1 << 20
	maxJSONDepth     = 64
	maxSchemaNodes   = 4096
	maxCombinators   = 32
	rootResourceURL  = "urn:alpheus:output-contract:local"
)

// Evidence identifies the fixed validator and exact inputs that passed it.
// The digests cover the original bytes, not a re-encoded representation.
type Evidence struct {
	Profile               string `json:"profile"`
	Dialect               string `json:"dialect"`
	Implementation        string `json:"implementation"`
	ImplementationVersion string `json:"implementation_version"`
	SchemaSHA256          string `json:"schema_sha256"`
	InstanceSHA256        string `json:"instance_sha256"`
}

// ReasonCode is a stable, non-sensitive validation failure category.
type ReasonCode string

const (
	ReasonSchemaRead               ReasonCode = "schema_read_failed"
	ReasonInstanceRead             ReasonCode = "instance_read_failed"
	ReasonSchemaTooLarge           ReasonCode = "schema_too_large"
	ReasonInstanceTooLarge         ReasonCode = "instance_too_large"
	ReasonSchemaInvalidUTF8        ReasonCode = "schema_invalid_utf8"
	ReasonInstanceInvalidUTF8      ReasonCode = "instance_invalid_utf8"
	ReasonSchemaInvalidJSON        ReasonCode = "schema_invalid_json"
	ReasonInstanceInvalidJSON      ReasonCode = "instance_invalid_json"
	ReasonSchemaDuplicateKey       ReasonCode = "schema_duplicate_key"
	ReasonInstanceDuplicateKey     ReasonCode = "instance_duplicate_key"
	ReasonSchemaDepthExceeded      ReasonCode = "schema_depth_exceeded"
	ReasonInstanceDepthExceeded    ReasonCode = "instance_depth_exceeded"
	ReasonSchemaDialect            ReasonCode = "schema_dialect_rejected"
	ReasonSchemaNodeLimit          ReasonCode = "schema_node_limit_exceeded"
	ReasonSchemaCombinatorLimit    ReasonCode = "schema_combinator_limit_exceeded"
	ReasonSchemaForbiddenKeyword   ReasonCode = "schema_forbidden_keyword"
	ReasonSchemaUnknownKeyword     ReasonCode = "schema_unknown_keyword"
	ReasonSchemaExternalReference  ReasonCode = "schema_external_reference"
	ReasonSchemaCompile            ReasonCode = "schema_compile_failed"
	ReasonInstanceValidationFailed ReasonCode = "instance_validation_failed"
)

// Error reports a typed reason without including schema or instance values.
// It intentionally does not expose an underlying parser or validator error.
type Error struct {
	Code ReasonCode
}

func (e *Error) Error() string {
	return "output contract validation failed: " + string(e.Code)
}

// Validate validates instance against schema under the fixed local-only AP1
// profile. Both documents are read at most once into bounded memory.
func Validate(schema io.Reader, instance io.Reader) (Evidence, error) {
	schemaBytes, err := readDocument(schema, ReasonSchemaRead, ReasonSchemaTooLarge)
	if err != nil {
		return Evidence{}, err
	}
	instanceBytes, err := readDocument(instance, ReasonInstanceRead, ReasonInstanceTooLarge)
	if err != nil {
		return Evidence{}, err
	}

	if code := inspectJSON(schemaBytes, documentSchema); code != "" {
		return Evidence{}, reason(code)
	}
	if code := inspectJSON(instanceBytes, documentInstance); code != "" {
		return Evidence{}, reason(code)
	}

	// Use the library decoder for the values passed to compilation and
	// validation. It retains JSON numbers as json.Number and therefore avoids
	// float64 precision loss. inspectJSON above adds duplicate-key and depth
	// enforcement that encoding/json and the library decoder do not provide.
	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		return Evidence{}, reason(ReasonSchemaInvalidJSON)
	}
	instanceDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(instanceBytes))
	if err != nil {
		return Evidence{}, reason(ReasonInstanceInvalidJSON)
	}

	policy := schemaPolicy{}
	if code := policy.check(schemaDoc, true); code != "" {
		return Evidence{}, reason(code)
	}

	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(rejectLoader{})
	if err := compiler.AddResource(rootResourceURL, schemaDoc); err != nil {
		return Evidence{}, reason(ReasonSchemaCompile)
	}
	compiled, err := compiler.Compile(rootResourceURL)
	if err != nil {
		return Evidence{}, reason(ReasonSchemaCompile)
	}
	if err := compiled.Validate(instanceDoc); err != nil {
		return Evidence{}, reason(ReasonInstanceValidationFailed)
	}

	return Evidence{
		Profile:               Profile,
		Dialect:               Dialect,
		Implementation:        Implementation,
		ImplementationVersion: ImplementationVersion,
		SchemaSHA256:          digest(schemaBytes),
		InstanceSHA256:        digest(instanceBytes),
	}, nil
}

func reason(code ReasonCode) error {
	return &Error{Code: code}
}

func readDocument(r io.Reader, readCode, sizeCode ReasonCode) ([]byte, error) {
	if r == nil {
		return nil, reason(readCode)
	}
	b, err := io.ReadAll(io.LimitReader(r, maxDocumentBytes+1))
	if err != nil {
		return nil, reason(readCode)
	}
	if len(b) > maxDocumentBytes {
		return nil, reason(sizeCode)
	}
	return b, nil
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type documentRole uint8

const (
	documentSchema documentRole = iota
	documentInstance
)

func inspectJSON(b []byte, role documentRole) ReasonCode {
	if !utf8.Valid(b) {
		if role == documentSchema {
			return ReasonSchemaInvalidUTF8
		}
		return ReasonInstanceInvalidUTF8
	}

	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if result := scanValue(dec, 0); result != scanOK {
		switch result {
		case scanDuplicate:
			if role == documentSchema {
				return ReasonSchemaDuplicateKey
			}
			return ReasonInstanceDuplicateKey
		case scanDepth:
			if role == documentSchema {
				return ReasonSchemaDepthExceeded
			}
			return ReasonInstanceDepthExceeded
		default:
			if role == documentSchema {
				return ReasonSchemaInvalidJSON
			}
			return ReasonInstanceInvalidJSON
		}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if role == documentSchema {
			return ReasonSchemaInvalidJSON
		}
		return ReasonInstanceInvalidJSON
	}
	return ""
}

type scanResult uint8

const (
	scanOK scanResult = iota
	scanInvalid
	scanDuplicate
	scanDepth
)

func scanValue(dec *json.Decoder, containerDepth int) scanResult {
	tok, err := dec.Token()
	if err != nil {
		return scanInvalid
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return scanOK
	}
	if delim != '{' && delim != '[' {
		return scanInvalid
	}
	containerDepth++
	if containerDepth > maxJSONDepth {
		return scanDepth
	}

	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return scanInvalid
			}
			key, ok := keyToken.(string)
			if !ok {
				return scanInvalid
			}
			if _, exists := seen[key]; exists {
				return scanDuplicate
			}
			seen[key] = struct{}{}
			if result := scanValue(dec, containerDepth); result != scanOK {
				return result
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return scanInvalid
		}
	case '[':
		for dec.More() {
			if result := scanValue(dec, containerDepth); result != scanOK {
				return result
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim(']') {
			return scanInvalid
		}
	}
	return scanOK
}

type schemaPolicy struct {
	nodes int
}

func (p *schemaPolicy) check(node any, root bool) ReasonCode {
	p.nodes++
	if p.nodes > maxSchemaNodes {
		return ReasonSchemaNodeLimit
	}
	if _, ok := node.(bool); ok {
		if root {
			return ReasonSchemaDialect
		}
		return ""
	}
	obj, ok := node.(map[string]any)
	if !ok {
		return ReasonSchemaCompile
	}

	if root {
		dialect, ok := obj["$schema"].(string)
		if !ok || dialect != Dialect {
			return ReasonSchemaDialect
		}
	} else if _, exists := obj["$schema"]; exists {
		return ReasonSchemaDialect
	}

	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if isForbiddenKeyword(key) {
			return ReasonSchemaForbiddenKeyword
		}
		if _, ok := allowedKeywords[key]; !ok {
			return ReasonSchemaUnknownKeyword
		}
		if key == "$ref" {
			if ref, ok := obj[key].(string); ok && !strings.HasPrefix(ref, "#") {
				return ReasonSchemaExternalReference
			}
		}
	}

	for _, key := range singleSchemaKeywords {
		if child, exists := obj[key]; exists {
			if code := p.check(child, false); code != "" {
				return code
			}
		}
	}
	for _, key := range schemaArrayKeywords {
		value, exists := obj[key]
		if !exists {
			continue
		}
		children, ok := value.([]any)
		if !ok {
			continue // the JSON Schema metaschema/compiler will reject its type
		}
		if isCombinator(key) && len(children) > maxCombinators {
			return ReasonSchemaCombinatorLimit
		}
		for _, child := range children {
			if code := p.check(child, false); code != "" {
				return code
			}
		}
	}
	for _, key := range schemaMapKeywords {
		value, exists := obj[key]
		if !exists {
			continue
		}
		children, ok := value.(map[string]any)
		if !ok {
			continue // the JSON Schema metaschema/compiler will reject its type
		}
		names := make([]string, 0, len(children))
		for name := range children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if code := p.check(children[name], false); code != "" {
				return code
			}
		}
	}
	return ""
}

func isForbiddenKeyword(key string) bool {
	switch key {
	case "$id", "$dynamicRef", "$dynamicAnchor", "$vocabulary", "format":
		return true
	default:
		return strings.HasPrefix(key, "content")
	}
}

func isCombinator(key string) bool {
	return key == "allOf" || key == "anyOf" || key == "oneOf"
}

var allowedKeywords = map[string]struct{}{
	"$anchor": {}, "$comment": {}, "$defs": {}, "$ref": {}, "$schema": {},
	"additionalProperties": {}, "allOf": {}, "anyOf": {}, "const": {},
	"contains": {}, "default": {}, "dependentRequired": {}, "dependentSchemas": {},
	"deprecated": {}, "description": {}, "else": {}, "enum": {},
	"examples": {}, "exclusiveMaximum": {}, "exclusiveMinimum": {}, "if": {},
	"items": {}, "maxContains": {}, "maxItems": {}, "maxLength": {},
	"maxProperties": {}, "maximum": {}, "minContains": {}, "minItems": {},
	"minLength": {}, "minProperties": {}, "minimum": {}, "multipleOf": {},
	"not": {}, "oneOf": {}, "pattern": {}, "patternProperties": {},
	"prefixItems": {}, "properties": {}, "propertyNames": {}, "readOnly": {},
	"required": {}, "then": {}, "title": {}, "type": {},
	"unevaluatedItems": {}, "unevaluatedProperties": {}, "uniqueItems": {},
	"writeOnly": {},
}

var singleSchemaKeywords = []string{
	"additionalProperties", "contains", "else", "if", "items", "not",
	"propertyNames", "then", "unevaluatedItems", "unevaluatedProperties",
}

var schemaArrayKeywords = []string{"allOf", "anyOf", "oneOf", "prefixItems"}

var schemaMapKeywords = []string{"$defs", "dependentSchemas", "patternProperties", "properties"}

type rejectLoader struct{}

func (rejectLoader) Load(string) (any, error) {
	return nil, errors.New("output contract resource loading is disabled")
}
