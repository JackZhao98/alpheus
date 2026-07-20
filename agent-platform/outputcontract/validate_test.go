package outputcontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

const permissiveSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema"}`

func TestValidateEvidenceAndExactDigests(t *testing.T) {
	t.Parallel()
	schema := " \n" + permissiveSchema + "\n"
	instance := `{"answer":42}`

	evidence, err := Validate(strings.NewReader(schema), strings.NewReader(instance))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if evidence.Profile != Profile || evidence.Dialect != Dialect {
		t.Fatalf("unexpected profile evidence: %#v", evidence)
	}
	if evidence.Implementation != Implementation || evidence.ImplementationVersion != ImplementationVersion {
		t.Fatalf("unexpected implementation evidence: %#v", evidence)
	}
	if evidence.SchemaSHA256 != testDigest([]byte(schema)) {
		t.Fatalf("schema digest = %q, want exact-input digest", evidence.SchemaSHA256)
	}
	if evidence.InstanceSHA256 != testDigest([]byte(instance)) {
		t.Fatalf("instance digest = %q, want exact-input digest", evidence.InstanceSHA256)
	}
}

func TestValidatePreservesJSONNumberPrecision(t *testing.T) {
	t.Parallel()
	schema := `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "const":9007199254740993
}`

	if _, err := Validate(strings.NewReader(schema), strings.NewReader(`9007199254740993`)); err != nil {
		t.Fatalf("exact integer should pass: %v", err)
	}
	assertReason(t, validateErr(schema, `9007199254740992`), ReasonInstanceValidationFailed)

	fractionSchema := `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "multipleOf":0.0000000000000000001
}`
	if _, err := Validate(strings.NewReader(fractionSchema), strings.NewReader(`0.0000000000000000003`)); err != nil {
		t.Fatalf("exact decimal multiple should pass: %v", err)
	}
}

func TestValidateLocalReferences(t *testing.T) {
	t.Parallel()
	t.Run("json_pointer", func(t *testing.T) {
		schema := `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "$defs":{"amount":{"type":"integer","minimum":1}},
  "type":"object",
  "properties":{"amount":{"$ref":"#/$defs/amount"}},
  "required":["amount"],
  "additionalProperties":false
}`
		if _, err := Validate(strings.NewReader(schema), strings.NewReader(`{"amount":7}`)); err != nil {
			t.Fatalf("local pointer should compile and validate: %v", err)
		}
		assertReason(t, validateErr(schema, `{"amount":0}`), ReasonInstanceValidationFailed)
	})

	t.Run("anchor", func(t *testing.T) {
		schema := `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "$defs":{"name":{"$anchor":"localName","type":"string","minLength":2}},
  "$ref":"#localName"
}`
		if _, err := Validate(strings.NewReader(schema), strings.NewReader(`"ok"`)); err != nil {
			t.Fatalf("local anchor should compile and validate: %v", err)
		}
	})

	t.Run("missing_fragment", func(t *testing.T) {
		schema := `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "$ref":"#/$defs/missing"
}`
		assertReason(t, validateErr(schema, `null`), ReasonSchemaCompile)
	})
}

func TestValidateRejectsDuplicateKeysAndTrailingValues(t *testing.T) {
	t.Parallel()
	assertReason(t, validateErr(
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"integer","type":"string"}`,
		`1`,
	), ReasonSchemaDuplicateKey)
	assertReason(t, validateErr(permissiveSchema, `{"secret":1,"secret":2}`), ReasonInstanceDuplicateKey)
	assertReason(t, validateErr(permissiveSchema+` true`, `null`), ReasonSchemaInvalidJSON)
	assertReason(t, validateErr(permissiveSchema, `null false`), ReasonInstanceInvalidJSON)
}

func TestValidateDepthLimit(t *testing.T) {
	t.Parallel()
	depth64 := strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64)
	if _, err := Validate(strings.NewReader(permissiveSchema), strings.NewReader(depth64)); err != nil {
		t.Fatalf("depth 64 should pass: %v", err)
	}
	depth65 := "[" + depth64 + "]"
	assertReason(t, validateErr(permissiveSchema, depth65), ReasonInstanceDepthExceeded)

	schemaTooDeep := `{"$schema":"https://json-schema.org/draft/2020-12/schema","const":` +
		strings.Repeat("[", 64) + `0` + strings.Repeat("]", 64) + `}`
	assertReason(t, validateErr(schemaTooDeep, `null`), ReasonSchemaDepthExceeded)
}

func TestValidateDocumentLimitsAndUTF8(t *testing.T) {
	t.Parallel()
	maxInstance := `"` + strings.Repeat("x", maxDocumentBytes-2) + `"`
	if _, err := Validate(strings.NewReader(permissiveSchema), strings.NewReader(maxInstance)); err != nil {
		t.Fatalf("exactly 1 MiB instance should pass: %v", err)
	}
	assertReason(t, validateErr(permissiveSchema, maxInstance+" "), ReasonInstanceTooLarge)
	assertReason(t, validateErr(strings.Repeat(" ", maxDocumentBytes+1), `null`), ReasonSchemaTooLarge)

	_, err := Validate(bytes.NewReader(append([]byte(permissiveSchema), 0xff)), strings.NewReader(`null`))
	assertReason(t, err, ReasonSchemaInvalidUTF8)
	_, err = Validate(strings.NewReader(permissiveSchema), bytes.NewReader([]byte{'"', 0xff, '"'}))
	assertReason(t, err, ReasonInstanceInvalidUTF8)
}

func TestValidateRequiresFixedDialect(t *testing.T) {
	t.Parallel()
	assertReason(t, validateErr(`{"type":"integer"}`, `1`), ReasonSchemaDialect)
	assertReason(t, validateErr(`true`, `1`), ReasonSchemaDialect)
	assertReason(t, validateErr(
		`{"$schema":"http://json-schema.org/draft-07/schema","type":"integer"}`,
		`1`,
	), ReasonSchemaDialect)
	assertReason(t, validateErr(
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","properties":{"x":{"$schema":"https://json-schema.org/draft/2020-12/schema"}}}`,
		`{}`,
	), ReasonSchemaDialect)
}

func TestValidateRejectsExternalReferences(t *testing.T) {
	t.Parallel()
	for _, ref := range []string{
		"https://example.invalid/schema.json",
		"other.json#/$defs/x",
		"urn:example:schema",
	} {
		t.Run(ref, func(t *testing.T) {
			schema := fmt.Sprintf(`{"$schema":%q,"$ref":%q}`, Dialect, ref)
			assertReason(t, validateErr(schema, `null`), ReasonSchemaExternalReference)
		})
	}
}

func TestValidateRejectsForbiddenAndUnknownKeywords(t *testing.T) {
	t.Parallel()
	for _, keyword := range []string{
		"$id", "$dynamicRef", "$dynamicAnchor", "$vocabulary",
		"format", "contentEncoding", "contentMediaType", "contentSchema",
	} {
		t.Run(keyword, func(t *testing.T) {
			value := `true`
			if keyword == "$id" || keyword == "$dynamicRef" || keyword == "$dynamicAnchor" || keyword == "format" || strings.HasPrefix(keyword, "content") {
				value = `"forbidden"`
			}
			schema := fmt.Sprintf(`{"$schema":%q,%q:%s}`, Dialect, keyword, value)
			assertReason(t, validateErr(schema, `null`), ReasonSchemaForbiddenKeyword)
		})
	}
	assertReason(t, validateErr(
		fmt.Sprintf(`{"$schema":%q,"x-alpheus-extension":true}`, Dialect),
		`null`,
	), ReasonSchemaUnknownKeyword)
	assertReason(t, validateErr(
		fmt.Sprintf(`{"$schema":%q,"properties":{"x":{"typoType":"string"}}}`, Dialect),
		`{}`,
	), ReasonSchemaUnknownKeyword)
}

func TestValidateSchemaComplexityLimits(t *testing.T) {
	t.Parallel()
	combinators := strings.TrimSuffix(strings.Repeat("true,", maxCombinators+1), ",")
	assertReason(t, validateErr(
		fmt.Sprintf(`{"$schema":%q,"allOf":[%s]}`, Dialect, combinators),
		`null`,
	), ReasonSchemaCombinatorLimit)

	var properties strings.Builder
	for i := 0; i < maxSchemaNodes; i++ {
		if i > 0 {
			properties.WriteByte(',')
		}
		fmt.Fprintf(&properties, "%q:true", fmt.Sprintf("p%04d", i))
	}
	tooManyNodes := fmt.Sprintf(`{"$schema":%q,"properties":{%s}}`, Dialect, properties.String())
	assertReason(t, validateErr(tooManyNodes, `{}`), ReasonSchemaNodeLimit)
}

func TestValidateRejectsMalformedSchema(t *testing.T) {
	t.Parallel()
	assertReason(t, validateErr(
		fmt.Sprintf(`{"$schema":%q,"type":7}`, Dialect),
		`null`,
	), ReasonSchemaCompile)
	assertReason(t, validateErr(
		fmt.Sprintf(`{"$schema":%q,"$ref":7}`, Dialect),
		`null`,
	), ReasonSchemaCompile)
}

func TestValidationErrorDoesNotEchoInstanceValues(t *testing.T) {
	t.Parallel()
	secret := "DO_NOT_ECHO_THIS_INSTANCE_VALUE"
	schema := fmt.Sprintf(`{"$schema":%q,"type":"integer"}`, Dialect)
	err := validateErr(schema, fmt.Sprintf("%q", secret))
	assertReason(t, err, ReasonInstanceValidationFailed)
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked instance value: %v", err)
	}
}

func TestValidateReadFailures(t *testing.T) {
	t.Parallel()
	_, err := Validate(errorReader{}, strings.NewReader(`null`))
	assertReason(t, err, ReasonSchemaRead)
	_, err = Validate(strings.NewReader(permissiveSchema), errorReader{})
	assertReason(t, err, ReasonInstanceRead)
	_, err = Validate(nil, strings.NewReader(`null`))
	assertReason(t, err, ReasonSchemaRead)
}

func TestValidateIsSafeForConcurrentUse(t *testing.T) {
	schema := `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "properties":{"n":{"type":"integer"}},
  "required":["n"]
}`
	const workers = 24
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := Validate(strings.NewReader(schema), strings.NewReader(fmt.Sprintf(`{"n":%d}`, i)))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Validate() error = %v", err)
		}
	}
}

func validateErr(schema, instance string) error {
	_, err := Validate(strings.NewReader(schema), strings.NewReader(instance))
	return err
}

func assertReason(t *testing.T, err error, want ReasonCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected reason %q, got nil", want)
	}
	var validationErr *Error
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *outputcontract.Error", err)
	}
	if validationErr.Code != want {
		t.Fatalf("reason = %q, want %q (error: %v)", validationErr.Code, want, err)
	}
}

func testDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
