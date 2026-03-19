package agent

import (
	"strings"
	"testing"
)

func TestModelFlag(t *testing.T) {
	tests := []struct {
		agent string
		want  string
	}{
		{"claude-opus", "opus"},
		{"claude-sonnet", "sonnet"},
		{"claude-haiku", "haiku"},
		{"claude-opus-4-6", "claude-opus-4-6"},
		{"claude-sonnet-4-5", "claude-sonnet-4-5-20250929"},
		{"unknown-agent", "unknown-agent"},
	}
	for _, tt := range tests {
		if got := modelFlag(tt.agent); got != tt.want {
			t.Errorf("modelFlag(%q) = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func TestBuildArgs(t *testing.T) {
	args := buildArgs("hello", "claude-haiku", 10, "")
	wantModel := "--model"
	hasModel := false
	for i, a := range args {
		if a == wantModel && i+1 < len(args) && args[i+1] == "haiku" {
			hasModel = true
			break
		}
	}
	if !hasModel {
		t.Errorf("buildArgs should contain --model haiku, got %v", args)
	}
	hasResume := false
	for i, a := range args {
		if a == "--resume" {
			hasResume = true
			_ = i
			break
		}
	}
	if hasResume {
		t.Error("buildArgs without session should not contain --resume")
	}

	argsResume := buildArgs("hi", "claude-sonnet", 5, "session-123")
	hasResume = false
	for i, a := range argsResume {
		if a == "--resume" && i+1 < len(argsResume) && argsResume[i+1] == "session-123" {
			hasResume = true
			break
		}
	}
	if !hasResume {
		t.Errorf("buildArgs with session should contain --resume session-123, got %v", argsResume)
	}
}

func TestEnvWithout(t *testing.T) {
	env := []string{"A=1", "B=2", "ANTHROPIC_API_KEY=secret", "C=3"}
	out := envWithout(env, "ANTHROPIC_API_KEY")
	if len(out) != 3 {
		t.Errorf("expected 3 entries, got %d: %v", len(out), out)
	}
	for _, e := range out {
		if strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
			t.Errorf("ANTHROPIC_API_KEY should be removed, got %q", e)
		}
	}
}

