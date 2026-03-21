package report

import (
	"testing"
	"time"
)

func TestComputeStallMetrics_CodexShellErrorsAndClaudeCorrection(t *testing.T) {
	t.Parallel()
	var codex []AgentEvent
	codexLine := `{"command":"go test","exit_code":1}`
	for i := 0; i < 3; i++ {
		ev, ok := ParseStdoutLine([]byte(codexLine), ProviderCodex, time.Time{})
		if !ok {
			t.Fatalf("parse codex line %d", i)
		}
		if ev.SemanticLabel != "shell/test" && ev.SemanticLabel != "shell" {
			t.Fatalf("expected shell semantic, got %q", ev.SemanticLabel)
		}
		if string(ev.Raw) != codexLine {
			t.Errorf("Raw should be JSON only without observed_at prefix: %q", ev.Raw)
		}
		codex = append(codex, ev)
	}
	sm := ComputeStallMetrics(codex)
	if sm.ToolErrorCount != 3 {
		t.Errorf("ToolErrorCount: want 3, got %d", sm.ToolErrorCount)
	}
	if sm.FatalLoops != 1 {
		t.Errorf("FatalLoops: want 1 (3 consecutive same-key failures), got %d", sm.FatalLoops)
	}

	assistantErr := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`
	userErr := `{"type":"user","is_error":true,"message":{"content":[]}}`
	assistantOK := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`
	userOK := `{"type":"user","is_error":false,"message":{"content":[]}}`
	var claude []AgentEvent
	for _, line := range []string{assistantErr, userErr, assistantOK, userOK} {
		ev, ok := ParseStdoutLine([]byte(line), ProviderClaudeLike, time.Time{})
		if !ok {
			t.Fatalf("parse claude line: %s", line)
		}
		claude = append(claude, ev)
	}
	sm2 := ComputeStallMetrics(claude)
	if sm2.CorrectionLoops != 1 {
		t.Errorf("CorrectionLoops: want 1, got %+v", sm2)
	}
}

func TestClassifyClaudeLike_SystemIsOther(t *testing.T) {
	t.Parallel()
	line := []byte(`{"type":"system","subtype":"init"}`)
	lab := classifyClaudeLike(line)
	if lab != "other" {
		t.Errorf("system should classify as other, got %q", lab)
	}
}

// LLM2: assistant lines between user errors must not reset the fatal-failure streak.
func TestFatalLoops_ClaudeInterleavedAssistants(t *testing.T) {
	t.Parallel()
	a := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`
	u := `{"type":"user","is_error":true,"message":{"content":[]}}`
	var events []AgentEvent
	for i := 0; i < 3; i++ {
		for _, line := range []string{a, u} {
			ev, ok := ParseStdoutLine([]byte(line), ProviderClaudeLike, time.Time{})
			if !ok {
				t.Fatalf("parse: %s", line)
			}
			events = append(events, ev)
		}
	}
	if n := countFatalFailureRuns(events); n != 1 {
		t.Errorf("FatalLoops want 1, got %d", n)
	}
}

// LLM1: Codex successful shells are single-line; repeated_action must not require assistant→user.
func TestRepeatedLoops_CodexShellSuccesses(t *testing.T) {
	t.Parallel()
	line := `{"command":"go test","exit_code":0}`
	var events []AgentEvent
	for i := 0; i < 3; i++ {
		ev, ok := ParseStdoutLine([]byte(line), ProviderCodex, time.Time{})
		if !ok {
			t.Fatalf("parse codex %d", i)
		}
		events = append(events, ev)
	}
	if n := countRepeatedSuccessRuns(events); n != 1 {
		t.Errorf("RepeatedActionLoops want 1, got %d", n)
	}
}
