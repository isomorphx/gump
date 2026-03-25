package agent

import (
	"context"
	"strings"
	"testing"
)

func TestCodexModelFlag(t *testing.T) {
	tests := []struct {
		agent    string
		want     string
		wantOmit bool
	}{
		{"codex", "", true},
		{"codex-gpt54", "gpt-5.4", false},
		{"codex-gpt54-mini", "gpt-5.4-mini", false},
		{"codex-gpt53", "gpt-5.3-codex", false},
		{"codex-o3", "o3-codex", false},
		{"codex-custom", "custom", false},
	}
	for _, tt := range tests {
		got := codexModelFlag(tt.agent)
		if tt.wantOmit {
			if got != "" {
				t.Errorf("codexModelFlag(%q) = %q, want omit (empty)", tt.agent, got)
			}
		} else if got != tt.want {
			t.Errorf("codexModelFlag(%q) = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func TestCodexBuildArgs(t *testing.T) {
	// Launch: no resume, prompt only
	args := codexBuildArgs("hello", "codex", false, "")
	if len(args) < 5 {
		t.Fatalf("expected at least 5 args, got %v", args)
	}
	if args[0] != "exec" || args[1] != "hello" {
		t.Errorf("launch args: got %v", args)
	}
	if strings.Join(args, " ") != strings.Join([]string{"exec", "hello", "--json", "--full-auto", "-C", "", "--skip-git-repo-check"}, " ") {
		t.Errorf("launch without -m: got %v", args)
	}
	// With model
	argsM := codexBuildArgs("hi", "codex-gpt54", false, "")
	if !contains(argsM, "-m") || !contains(argsM, "gpt-5.4") {
		t.Errorf("launch with model: got %v", argsM)
	}
	// Resume
	argsR := codexBuildArgs("follow-up", "codex", true, "thread-abc")
	if argsR[1] != "resume" || argsR[2] != "thread-abc" || argsR[3] != "follow-up" {
		t.Errorf("resume args: got %v", argsR)
	}
}

func contains(sl []string, s string) bool {
	for _, v := range sl {
		if v == s {
			return true
		}
	}
	return false
}

func TestCodexAdapter_LastLaunchCLI(t *testing.T) {
	a := NewCodexAdapter()
	ctx := context.Background()
	req := LaunchRequest{Worktree: t.TempDir(), Prompt: "test", AgentName: "codex", Timeout: 0}
	proc, err := a.Launch(ctx, req)
	if err != nil {
		t.Skipf("codex not in PATH: %v", err)
	}
	_ = proc
	cli := a.LastLaunchCLI()
	if cli == "" {
		t.Error("LastLaunchCLI should be set after Launch")
	}
	if !strings.Contains(cli, "codex") || !strings.Contains(cli, "exec") {
		t.Errorf("LastLaunchCLI should contain codex exec: %s", cli)
	}
}
