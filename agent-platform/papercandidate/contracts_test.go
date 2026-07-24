package papercandidate

import (
	"encoding/json"
	"testing"
)

func TestProposalIsEffectFreeAndStrict(t *testing.T) {
	proposal, err := DecodeProposal([]byte(`{
		"schema_revision":1,
		"strategy_id":"gamma_intraday",
		"symbol":"SPY",
		"kind":"equity",
		"side":"buy",
		"qty":0.125,
		"thesis":"Price accepted above the reviewed trigger level.",
		"invalidation":"Exit the candidate if price closes below the level.",
		"confidence_bps":6200
	}`))
	if err != nil || proposal.Qty.String() != "0.125" {
		t.Fatalf("proposal=%+v err=%v", proposal, err)
	}
	schema, _ := json.Marshal(OutputSchema())
	for _, forbidden := range []string{
		"account", "fill_price", "broker", "live", "approved",
	} {
		if json.Valid(schema) && string(schema) != "" &&
			containsJSONKey(schema, forbidden) {
			t.Fatalf("schema exposes forbidden key %q: %s", forbidden, schema)
		}
	}
}

func TestProposalRejectsAuthorityAndQuantityExpansion(t *testing.T) {
	for _, raw := range []string{
		`{"schema_revision":1,"strategy_id":"manual","symbol":"SPY","kind":"equity","side":"buy","qty":1000.000001,"thesis":"x","invalidation":"y","confidence_bps":1}`,
		`{"schema_revision":1,"strategy_id":"manual","symbol":"SPY","kind":"equity","side":"buy","qty":1,"thesis":"x","invalidation":"y","confidence_bps":1,"fill_price":1}`,
		`{"schema_revision":1,"strategy_id":"manual","symbol":"spy","kind":"equity","side":"buy","qty":1,"thesis":"x","invalidation":"y","confidence_bps":1}`,
	} {
		if _, err := DecodeProposal([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid proposal: %s", raw)
		}
	}
}

func containsJSONKey(raw []byte, key string) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	return hasJSONKey(value, key)
}

func hasJSONKey(value any, key string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for name, child := range typed {
			if name == key || hasJSONKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if hasJSONKey(child, key) {
				return true
			}
		}
	}
	return false
}
