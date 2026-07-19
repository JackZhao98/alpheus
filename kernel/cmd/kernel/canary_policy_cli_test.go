package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

func TestUnknownKernelCommandFailsBeforeNormalStartup(t *testing.T) {
	handled, err := dispatchKernelCommand([]string{"canary-polciy"}, &bytes.Buffer{})
	if !handled || err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	handled, err = dispatchKernelCommand(nil, &bytes.Buffer{})
	if handled || err != nil {
		t.Fatalf("empty args handled=%v err=%v", handled, err)
	}
}

func TestParseCanaryPolicyArgsUsesExactTypedValues(t *testing.T) {
	input, err := parseCanaryPolicyArgs([]string{
		"--expected-revision=12",
		"--daily-risk-cap-usd=50.123456",
		"--clean-days-before-raise=5",
		"--account-id=518428891",
		"--recorded-by=deploy:jack",
		"--reason=reduce initial blast radius",
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.ExpectedRevisionID != 12 || input.DailyAuthorizedRiskCapUSD != units.MustMicros("50.123456") ||
		input.CleanDaysBeforeRaise != 5 || input.AccountID != "518428891" ||
		input.RecordedBy != "deploy:jack" ||
		input.Reason != "reduce initial blast radius" {
		t.Fatalf("input=%+v", input)
	}
}

func TestParseCanaryDayAttestationRequiresExactIdentityAndDate(t *testing.T) {
	input, err := parseCanaryDayAttestationArgs([]string{
		"--account-id=518428891", "--market-day=2026-07-20", "--expected-revision=7",
		"--attested-by=deploy:jack", "--reason=post-close reconciled canary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.AccountID != "518428891" || input.MarketDay.Format(time.DateOnly) != "2026-07-20" ||
		input.ExpectedRevisionID != 7 || input.AttestedBy != "deploy:jack" ||
		input.Reason != "post-close reconciled canary" {
		t.Fatalf("input=%+v", input)
	}
	for index, args := range [][]string{
		{"--market-day=2026-07-20", "--expected-revision=1", "--attested-by=x", "--reason=x"},
		{"--account-id=a", "--market-day=07/20/2026", "--expected-revision=1", "--attested-by=x", "--reason=x"},
		{"--account-id=a", "--market-day=2026-07-20", "--expected-revision=0", "--attested-by=x", "--reason=x"},
		{"--account-id=a", "--market-day=2026-07-20", "--expected-revision=1", "--attested-by=x"},
	} {
		if _, err := parseCanaryDayAttestationArgs(args); err == nil {
			t.Fatalf("case %d accepted: %v", index, args)
		}
	}
}

func TestParseCanaryPolicyArgsRejectsAmbiguousOrIncompleteInput(t *testing.T) {
	base := []string{
		"--expected-revision=0", "--daily-risk-cap-usd=50",
		"--clean-days-before-raise=5", "--recorded-by=deploy:test", "--reason=initial",
	}
	tests := [][]string{
		{"--daily-risk-cap-usd=5e1", "--expected-revision=0", "--clean-days-before-raise=5", "--recorded-by=x", "--reason=x"},
		{"--daily-risk-cap-usd=50", "--clean-days-before-raise=5", "--recorded-by=x", "--reason=x"},
		{"--daily-risk-cap-usd=50", "--expected-revision=0", "--clean-days-before-raise=0", "--recorded-by=x", "--reason=x"},
		append(append([]string{}, base...), "unexpected"),
	}
	for index, args := range tests {
		if _, err := parseCanaryPolicyArgs(args); err == nil {
			t.Fatalf("case %d accepted: %v", index, args)
		}
	}
}

func TestParseKernelPolicyArgsRequiresExplicitCASAndAuditIdentity(t *testing.T) {
	input, err := parseKernelPolicyArgs([]string{
		"--file=/limits.yaml", "--expected-generation=7",
		"--recorded-by=deploy:jack", "--reason=tighten proposal TTL",
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.File != "/limits.yaml" || input.ExpectedGeneration != 7 ||
		input.RecordedBy != "deploy:jack" || input.Reason != "tighten proposal TTL" {
		t.Fatalf("input=%+v", input)
	}
	for index, args := range [][]string{
		{"--expected-generation=0", "--recorded-by=x", "--reason=x"},
		{"--file=x", "--recorded-by=x", "--reason=x"},
		{"--file=x", "--expected-generation=0", "--reason=x"},
		{"--file=x", "--expected-generation=0", "--recorded-by=x"},
		{"--file=x", "--expected-generation=0", "--recorded-by=x", "--reason=x", "extra"},
	} {
		if _, err := parseKernelPolicyArgs(args); err == nil {
			t.Fatalf("case %d accepted: %v", index, args)
		}
	}
}

func TestLiveStartupRequiresDatabaseCanaryAuthorityOnlyInLive(t *testing.T) {
	st := newMemoryStore()
	active, err := requireLiveCanaryAuthority(config.ModeLive, st)
	if err != nil || active.ID != st.liveCanary.ID {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	st.liveCanary = nil
	if _, err := requireLiveCanaryAuthority(config.ModeLive, st); !errors.Is(err, store.ErrLiveCanaryAuthorityMissing) {
		t.Fatalf("missing authority did not close Live: %v", err)
	}
	st.liveCanaryErr = errors.New("must not be read")
	for _, mode := range []string{config.ModeSim, config.ModeShadow, config.ModeReadOnly} {
		if authority, err := requireLiveCanaryAuthority(mode, st); err != nil || authority != nil {
			t.Fatalf("mode=%s authority=%+v err=%v", mode, authority, err)
		}
	}
}

func TestLimitsAndStateExposeDatabasePolicyAuthorities(t *testing.T) {
	s, st, _ := m11Server("37")
	st.liveCanary.ID = 9
	st.liveCanary.Generation = 9

	limitsResponse := httptest.NewRecorder()
	s.getLimits(limitsResponse, httptest.NewRequest("GET", "/limits", nil))
	if limitsResponse.Code != 200 {
		t.Fatalf("limits status=%d body=%s", limitsResponse.Code, limitsResponse.Body.String())
	}
	var limitsBody map[string]any
	if err := json.Unmarshal(limitsResponse.Body.Bytes(), &limitsBody); err != nil {
		t.Fatal(err)
	}
	if _, exists := limitsBody["live_canary"]; exists {
		t.Fatalf("flat/YAML canary leaked: %v", limitsBody)
	}
	if _, exists := limitsBody["build_pinned_kernel_limits"]; exists {
		t.Fatalf("runtime YAML limits leaked: %v", limitsBody)
	}
	dbKernel := limitsBody["db_kernel_policy"].(map[string]any)
	if dbKernel["revision_id"] != float64(1) || dbKernel["generation"] != float64(1) || dbKernel["digest"] == "" {
		t.Fatalf("kernel policy authority=%v", dbKernel)
	}
	dbCanary := limitsBody["db_live_canary"].(map[string]any)
	authority := dbCanary["authority"].(map[string]any)
	if authority["revision_id"] != float64(9) || authority["daily_authorized_risk_cap_usd"] != float64(37) {
		t.Fatalf("limits authority=%v", authority)
	}
	stateResponse := httptest.NewRecorder()
	s.getState(stateResponse, httptest.NewRequest("GET", "/state", nil))
	if stateResponse.Code != 200 {
		t.Fatalf("state status=%d body=%s", stateResponse.Code, stateResponse.Body.String())
	}
	var stateBody map[string]any
	if err := json.Unmarshal(stateResponse.Body.Bytes(), &stateBody); err != nil {
		t.Fatal(err)
	}
	stateCanary := stateBody["db_live_canary"].(map[string]any)
	stateAuthority := stateCanary["authority"].(map[string]any)
	if stateAuthority["generation"] != float64(9) || stateAuthority["daily_authorized_risk_cap_usd"] != float64(37) {
		t.Fatalf("state authority=%v", stateAuthority)
	}
	stateKernel := stateBody["db_kernel_policy"].(map[string]any)
	if stateKernel["revision_id"] != float64(1) || stateKernel["digest"] == "" {
		t.Fatalf("state kernel authority=%v", stateKernel)
	}
}

func TestNonLiveLimitsReportInvalidCanaryWithoutBlockingHealth(t *testing.T) {
	st := newMemoryStore()
	st.liveCanaryErr = store.ErrLiveCanaryAuthorityInvalid
	s := &server{mode: config.ModeConfig{TradingMode: config.ModeSim}, limits: dualLedgerLimits(), store: st}
	response := httptest.NewRecorder()
	s.getLimits(response, httptest.NewRequest("GET", "/limits", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"invalid"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestInvalidKernelPolicyFailsLimitsClosed(t *testing.T) {
	st := newMemoryStore()
	st.kernelPolicyErr = store.ErrKernelPolicyAuthorityInvalid
	s := &server{mode: config.ModeConfig{TradingMode: config.ModeSim}, limits: dualLedgerLimits(), store: st}
	response := httptest.NewRecorder()
	s.getLimits(response, httptest.NewRequest("GET", "/limits", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
