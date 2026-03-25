package agent

import (
	"strings"
	"testing"
)

func TestCursorModelFlag(t *testing.T) {
	tests := []struct {
		agent string
		want  string
	}{
		{"cursor", ""},
		{"cursor-sonnet", "claude-4.6-sonnet-medium"},
		{"cursor-opus-thinking", "claude-4.6-opus-high-thinking"},
		{"cursor-gpt5", "gpt-5.4-medium"},
		{"cursor-gemini", "gemini-3.1-pro"},
		{"cursor-custom-model", "custom-model"},
	}
	for _, tt := range tests {
		if got := cursorModelFlag(tt.agent); got != tt.want {
			t.Errorf("cursorModelFlag(%q) = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func TestCursorBuildArgs(t *testing.T) {
	args := cursorBuildArgs("hello", "/tmp/wt", "cursor", "")
	full := strings.Join(args, " ")
	for _, must := range []string{"-p", "hello", "--output-format stream-json", "--yolo", "--trust", "--workspace /tmp/wt"} {
		if !strings.Contains(full, must) {
			t.Fatalf("missing %q in args: %v", must, args)
		}
	}
	if strings.Contains(full, "--model") {
		t.Fatalf("cursor default should omit --model: %v", args)
	}

	argsResume := cursorBuildArgs("hello", "/tmp/wt", "cursor-opus", "sess-1")
	fullResume := strings.Join(argsResume, " ")
	if !strings.Contains(fullResume, "--model claude-4.6-opus-high") {
		t.Fatalf("expected mapped model in args: %v", argsResume)
	}
	if !strings.Contains(fullResume, "--resume sess-1") {
		t.Fatalf("expected --resume in args: %v", argsResume)
	}
}

func TestParseCursorResult(t *testing.T) {
	line := []byte(`{"type":"result","session_id":"abc","is_error":false,"duration_ms":1234,"duration_api_ms":1200,"result":"ok","usage":{"inputTokens":10,"outputTokens":20,"cacheWriteTokens":3,"cacheReadTokens":4}}`)
	res := parseCursorResult(line)
	if res.SessionID != "abc" || res.DurationMs != 1234 || res.DurationAPI != 1200 || res.Result != "ok" {
		t.Fatalf("unexpected basic parse: %+v", res)
	}
	if res.InputTokens != 10 || res.OutputTokens != 20 || res.CacheCreationTokens != 3 || res.CacheReadTokens != 4 {
		t.Fatalf("unexpected token parse: %+v", res)
	}
}
