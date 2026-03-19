package agent

import (
	"strings"
	"testing"
)

func TestQwenModelFlag(t *testing.T) {
	tests := []struct {
		agent string
		want  string
	}{
		{"qwen", ""},
		{"qwen-coder", "qwen3-coder"},
		{"qwen-coder-plus", "qwen3-coder-plus"},
		{"qwen-other", "other"},
	}
	for _, tt := range tests {
		got := qwenModelFlag(tt.agent)
		if got != tt.want {
			t.Errorf("qwenModelFlag(%q) = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func TestQwenBuildArgs_Launch(t *testing.T) {
	args := qwenBuildArgs("hello", "qwen", 25, "")
	if len(args) == 0 {
		t.Fatal("args empty")
	}
	// -m must be omitted for agent "qwen"
	for i, a := range args {
		if a == "-m" && i+1 < len(args) {
			t.Errorf("Launch with agent qwen should omit -m, got -m %s", args[i+1])
		}
	}
	if !strings.Contains(strings.Join(args, " "), "--output-format stream-json") {
		t.Error("missing --output-format stream-json")
	}
	if !strings.Contains(strings.Join(args, " "), "--yolo") {
		t.Error("missing --yolo")
	}
	if !strings.Contains(strings.Join(args, " "), "hello") {
		t.Error("prompt not in args")
	}
	if !strings.Contains(strings.Join(args, " "), "--allowed-tools") {
		t.Error("missing --allowed-tools")
	}
}

func TestQwenBuildArgs_Resume(t *testing.T) {
	args := qwenBuildArgs("hi", "qwen-coder", 10, "6548daf5-1bff-4e8e-b1cb-4a0561cac525")
	if args[0] != "--resume" || args[1] != "6548daf5-1bff-4e8e-b1cb-4a0561cac525" {
		t.Errorf("Resume args should start with --resume <session-id>, got %v", args[:2])
	}
	full := strings.Join(args, " ")
	if !strings.Contains(full, "-m qwen3-coder") {
		t.Errorf("expected -m qwen3-coder in %s", full)
	}
}

func TestParseQwenResultJSON(t *testing.T) {
	line := []byte(`{"type":"result","session_id":"uuid-123","is_error":false,"duration_ms":5000,"duration_api_ms":4900,"num_turns":2,"result":"Done.","usage":{"input_tokens":2100,"output_tokens":130,"cache_read_input_tokens":1100}}`)
	res, err := ParseQwenResultJSON(line)
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "uuid-123" {
		t.Errorf("SessionID = %q", res.SessionID)
	}
	if res.IsError {
		t.Error("IsError should be false")
	}
	if res.DurationMs != 5000 || res.DurationAPI != 4900 {
		t.Errorf("DurationMs=%d DurationAPI=%d", res.DurationMs, res.DurationAPI)
	}
	if res.NumTurns != 2 {
		t.Errorf("NumTurns = %d", res.NumTurns)
	}
	if res.Result != "Done." {
		t.Errorf("Result = %q", res.Result)
	}
	if res.InputTokens != 2100 || res.OutputTokens != 130 || res.CacheReadTokens != 1100 {
		t.Errorf("tokens: in=%d out=%d cache=%d", res.InputTokens, res.OutputTokens, res.CacheReadTokens)
	}
	if res.CostUSD != 0 {
		t.Errorf("CostUSD should be 0 for Qwen, got %f", res.CostUSD)
	}
}

func TestQwenSessionIDRegex(t *testing.T) {
	valid := []string{"6548daf5-1bff-4e8e-b1cb-4a0561cac525", "00000000-0000-0000-0000-000000000000"}
	for _, s := range valid {
		if !qwenSessionIDRegex.MatchString(s) {
			t.Errorf("expected valid UUID %q", s)
		}
	}
	invalid := []string{"", "ses_abc", "not-a-uuid"}
	for _, s := range invalid {
		if qwenSessionIDRegex.MatchString(s) {
			t.Errorf("expected invalid for %q", s)
		}
	}
}
