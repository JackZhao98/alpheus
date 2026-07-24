package inputgateway

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodePendingTaskGraphNodeSessionFailsClosed(t *testing.T) {
	raw := []byte(`{
	  "graph_id":"graph-1",
	  "node":{
	    "task_id":"task-1",
	    "role_id":"market_scout",
	    "role_revision":1,
	    "depth":1,
	    "objective":{
	      "schema_revision":1,
	      "blob_id":"11111111-1111-4111-8111-111111111111",
	      "content_digest":"` + strings.Repeat("a", 64) + `",
	      "media_type":"application/json",
	      "size_bytes":10,
	      "origin":{"owner":"agent_control","record_type":"task_objective","record_id":"task-1","schema_revision":1,"record_digest":"` + strings.Repeat("b", 64) + `"},
	      "committed_at":"2026-07-24T08:00:00Z"
	    },
	    "input_refs":[],
	    "output_contract_name":"specialist_memo_v1",
	    "output_contract":{"owner":"agent_control","record_type":"output_contract_revision","record_id":"specialist-memo","schema_revision":1,"record_digest":"` + strings.Repeat("c", 64) + `","generation":1},
	    "tool_grants":[],
	    "limit":{"max_model_calls":1,"max_input_tokens":1000,"max_output_tokens":1000,"max_tool_calls":0,"max_external_cost_micro_usd":0,"max_wall_time_ms":30000,"max_idle_time_ms":5000,"max_tasks":1,"max_depth":0,"max_fanout":0,"max_parallelism":1,"max_invalid_output_retries":0,"max_infrastructure_retries":0},
	    "deadline_at":"2026-07-24T08:10:00Z"
	  },
	  "raw_input":{
	    "schema_revision":1,
	    "blob_id":"22222222-2222-4222-8222-222222222222",
	    "content_digest":"` + strings.Repeat("d", 64) + `",
	    "media_type":"text/plain; charset=utf-8",
	    "size_bytes":10,
	    "origin":{"owner":"agent_control","record_type":"input_raw","record_id":"wake-1","schema_revision":1,"record_digest":"` + strings.Repeat("e", 64) + `"},
	    "committed_at":"2026-07-24T08:00:00Z"
	  }
	}`)
	value, err := decodePendingTaskGraphNodeSession(raw)
	if err != nil || value.GraphID != "graph-1" ||
		value.Node.TaskID != "task-1" ||
		value.RawInput.Origin.RecordID != "wake-1" {
		t.Fatalf("value=%+v err=%v", value, err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["graph_id"] = ""
	invalid, _ := json.Marshal(decoded)
	if _, err := decodePendingTaskGraphNodeSession(invalid); err == nil {
		t.Fatal("missing graph identity was accepted")
	}
}
