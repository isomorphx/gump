package agent

import (
	"testing"
)

func TestParseResultJSON(t *testing.T) {
	t.Run("valid_result", func(t *testing.T) {
		line := []byte(`{"type":"result","session_id":"s1","is_error":false,"duration_ms":100,"duration_api_ms":90,"num_turns":2,"result":"Done","total_cost_usd":0.01,"usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"modelUsage":{"claude-haiku":{"inputTokens":10,"outputTokens":20,"cacheCreationInputTokens":0,"cacheReadInputTokens":0,"costUSD":0.01}}}`)
		res, err := ParseResultJSON(line)
		if err != nil {
			t.Fatal(err)
		}
		if res.SessionID != "s1" || res.IsError != false || res.DurationMs != 100 || res.NumTurns != 2 || res.Result != "Done" {
			t.Errorf("unexpected result: %+v", res)
		}
		if res.InputTokens != 10 || res.OutputTokens != 20 || res.CostUSD != 0.01 {
			t.Errorf("usage/cost: input=%d output=%d cost=%f", res.InputTokens, res.OutputTokens, res.CostUSD)
		}
		if len(res.ModelUsage) != 1 || res.ModelUsage["claude-haiku"].CostUSD != 0.01 {
			t.Errorf("modelUsage: %+v", res.ModelUsage)
		}
	})

	t.Run("not_result_type", func(t *testing.T) {
		line := []byte(`{"type":"assistant","message":{}}`)
		_, err := ParseResultJSON(line)
		if err == nil {
			t.Error("expected error for type=assistant")
		}
	})

	t.Run("invalid_json", func(t *testing.T) {
		line := []byte(`{invalid`)
		_, err := ParseResultJSON(line)
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("fallbacks", func(t *testing.T) {
		line := []byte(`{"type":"result"}`)
		res, err := ParseResultJSON(line)
		if err != nil {
			t.Fatal(err)
		}
		if res.SessionID != "" || res.Result != "" || res.CostUSD != 0 || res.NumTurns != 0 {
			t.Errorf("fallbacks: %+v", res)
		}
		if res.InputTokens != 0 || res.OutputTokens != 0 {
			t.Error("usage should default to 0 when absent")
		}
	})
}

func TestParseResultJSON_UsageNil(t *testing.T) {
	line := []byte(`{"type":"result","session_id":"x","is_error":false}`)
	res, err := ParseResultJSON(line)
	if err != nil {
		t.Fatal(err)
	}
	if res.InputTokens != 0 || res.OutputTokens != 0 {
		t.Errorf("usage should default to 0: %+v", res)
	}
}
