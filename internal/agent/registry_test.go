package agent

import (
	"strings"
	"testing"
)

func TestRegistry_AdapterFor(t *testing.T) {
	r := &Registry{
		Claude:   NewClaudeAdapter(),
		Codex:    NewCodexAdapter(),
		Gemini:   NewGeminiAdapter(),
		Qwen:     NewQwenAdapter(),
		OpenCode: NewOpenCodeAdapter(),
		Cursor:   NewCursorAdapter(),
	}
	for _, agentName := range []string{"claude", "claude-opus", "codex", "codex-gpt5", "gemini", "gemini-flash", "qwen", "qwen-coder-plus", "opencode", "opencode-sonnet", "cursor", "cursor-opus"} {
		adapter, err := r.AdapterFor(agentName)
		if err != nil {
			t.Errorf("AdapterFor(%q): %v", agentName, err)
		}
		if adapter == nil {
			t.Errorf("AdapterFor(%q): nil adapter", agentName)
		}
	}
	_, err := r.AdapterFor("unknown-provider")
	if err == nil {
		t.Error("AdapterFor(unknown-provider): expected error")
	}
	if err != nil && !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("AdapterFor(unknown): error should mention 'unknown agent': %v", err)
	}
}
