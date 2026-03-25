package agent

import (
	"strings"
	"testing"
)

func TestOpenCodeModelFlag(t *testing.T) {
	tests := []struct {
		agent string
		want  string
	}{
		{"opencode", ""},
		{"opencode-sonnet", "anthropic/claude-sonnet-4-6"},
		{"opencode-opus", "anthropic/claude-opus-4-6"},
		{"opencode-gpt54", "openai/gpt-5.4"},
		{"opencode-gpt53", "openai/gpt-5.3"},
		{"opencode-gemini", "google/gemini-3.1-pro"},
	}
	for _, tt := range tests {
		got := opencodeModelFlag(tt.agent)
		if got != tt.want {
			t.Errorf("opencodeModelFlag(%q) = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func TestOpenCodeBuildArgs_Launch(t *testing.T) {
	args := opencodeBuildArgs("hello", "opencode", "/tmp/wt", "")
	if len(args) < 4 {
		t.Fatalf("args too short: %v", args)
	}
	if args[0] != "run" {
		t.Errorf("first arg should be run, got %q", args[0])
	}
	if args[1] != "hello" {
		t.Errorf("prompt should be second, got %q", args[1])
	}
	full := strings.Join(args, " ")
	if !strings.Contains(full, "--format json") {
		t.Error("missing --format json")
	}
	if !strings.Contains(full, "--dir /tmp/wt") {
		t.Error("missing --dir")
	}
	// No --session for Launch
	if strings.Contains(full, "--session") {
		t.Error("Launch should not have --session")
	}
}

func TestOpenCodeBuildArgs_Resume(t *testing.T) {
	args := opencodeBuildArgs("hi", "opencode-sonnet", "/work", "ses_34fdce821ffesYYSMB00M8ifRb")
	full := strings.Join(args, " ")
	if !strings.Contains(full, "--session") {
		t.Error("Resume should have --session")
	}
	if !strings.Contains(full, "ses_34fdce821ffesYYSMB00M8ifRb") {
		t.Error("session ID should be in args")
	}
	if !strings.Contains(full, "--model anthropic/claude-sonnet-4-6") {
		t.Error("expected --model for opencode-sonnet")
	}
}

func TestOpenCodeSessionIDRegex(t *testing.T) {
	if !opencodeSessionIDRegex.MatchString("ses_abc") {
		t.Error("ses_ prefix should match")
	}
	if !opencodeSessionIDRegex.MatchString("ses_34fdce821ffesYYSMB00M8ifRb") {
		t.Error("ses_ id should match")
	}
	if opencodeSessionIDRegex.MatchString("6548daf5-1bff-4e8e-b1cb-4a0561cac525") {
		t.Error("UUID should not match opencode format")
	}
	if opencodeSessionIDRegex.MatchString("") {
		t.Error("empty should not match")
	}
}

func TestOpenCodeAggregateFromReader(t *testing.T) {
	ndjson := `{"type":"step_start","timestamp":1000,"sessionID":"ses_ok","part":{"type":"step-start"}}
{"type":"step_finish","timestamp":2000,"sessionID":"ses_ok","part":{"type":"step-finish","reason":"stop","tokens":{"input":100,"output":50,"cache":{"read":10}}}}
`
	agg := opencodeAggregateFromReader(strings.NewReader(ndjson))
	if agg.SessionID != "ses_ok" {
		t.Errorf("SessionID = %q", agg.SessionID)
	}
	if agg.NumTurns != 1 {
		t.Errorf("NumTurns = %d", agg.NumTurns)
	}
	if agg.DurationMs != 1000 {
		t.Errorf("DurationMs = %d", agg.DurationMs)
	}
	if agg.InputTokens != 100 || agg.OutputTokens != 50 || agg.CacheReadTokens != 10 {
		t.Errorf("tokens: in=%d out=%d cache=%d", agg.InputTokens, agg.OutputTokens, agg.CacheReadTokens)
	}
	if agg.IsError {
		t.Error("IsError should be false when reason=stop")
	}
}
