package agent

import (
	"context"
	"strings"
	"testing"
)

func TestGeminiModelFlag(t *testing.T) {
	tests := []struct {
		agent    string
		want     string
		wantOmit bool
	}{
		{"gemini", "", true},
		{"gemini-flash", "gemini-3-flash", false},
		{"gemini-pro", "gemini-3.1-pro-preview", false},
		{"gemini-flash-lite", "gemini-3.1-flash-lite", false},
		{"gemini-25-pro", "gemini-2.5-pro-preview", false},
		{"gemini-25-flash", "gemini-2.5-flash", false},
		{"gemini-custom", "custom", false},
	}
	for _, tt := range tests {
		got := geminiModelFlag(tt.agent)
		if tt.wantOmit {
			if got != "" {
				t.Errorf("geminiModelFlag(%q) = %q, want omit (empty)", tt.agent, got)
			}
		} else if got != tt.want {
			t.Errorf("geminiModelFlag(%q) = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func TestGeminiBuildArgs(t *testing.T) {
	args := geminiBuildArgs("hello", "gemini", false)
	if !containsGemini(args, "-p", "hello") || !containsGemini(args, "--output-format", geminiStreamJSON) {
		t.Errorf("launch args: %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), "--yolo") {
		t.Errorf("launch args missing --yolo: %v", args)
	}
	argsR := geminiBuildArgs("follow-up", "gemini", true)
	if argsR[0] != "--resume" {
		t.Errorf("resume args: got %v", argsR)
	}
}

func containsGemini(sl []string, a, b string) bool {
	for i := 0; i < len(sl)-1; i++ {
		if sl[i] == a && sl[i+1] == b {
			return true
		}
	}
	return false
}

func TestGeminiAdapter_LastLaunchCLI(t *testing.T) {
	a := NewGeminiAdapter()
	ctx := context.Background()
	req := LaunchRequest{Worktree: t.TempDir(), Prompt: "test", AgentName: "gemini", Timeout: 0}
	proc, err := a.Launch(ctx, req)
	if err != nil {
		t.Skipf("gemini not in PATH: %v", err)
	}
	_ = proc
	cli := a.LastLaunchCLI()
	if cli == "" {
		t.Error("LastLaunchCLI should be set after Launch")
	}
	if !strings.Contains(cli, "gemini") || !strings.Contains(cli, "-p") {
		t.Errorf("LastLaunchCLI should contain gemini -p: %s", cli)
	}
}
