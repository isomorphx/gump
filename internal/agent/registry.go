package agent

import (
	"fmt"
	"strings"
)

// AdapterResolver returns the adapter for a given agent name so the engine stays provider-agnostic per step.
type AdapterResolver interface {
	AdapterFor(agentName string) (AgentAdapter, error)
}

// AgentPrefix returns the provider prefix (claude, codex, gemini, qwen, opencode) so session reuse is per-provider, not per full agent name.
func AgentPrefix(agentName string) string {
	if i := strings.Index(agentName, "-"); i > 0 {
		return agentName[:i]
	}
	return agentName
}

// Registry resolves agent name (prefix before first '-') to an adapter.
// We resolve per step so a single workflow can mix providers (e.g. plan with gemini, code with codex) without engine changes.
type Registry struct {
	Claude   AgentAdapter
	Codex    AgentAdapter
	Gemini   AgentAdapter
	Qwen     AgentAdapter
	OpenCode AgentAdapter
	Cursor   AgentAdapter
}

// AdapterFor returns the adapter for the given agent name, or an error listing available providers.
func (r *Registry) AdapterFor(agentName string) (AgentAdapter, error) {
	prefix := agentName
	if i := strings.Index(agentName, "-"); i > 0 {
		prefix = agentName[:i]
	}
	switch prefix {
	case "claude":
		if r.Claude == nil {
			return nil, fmt.Errorf("claude adapter not registered")
		}
		return r.Claude, nil
	case "codex":
		if r.Codex == nil {
			return nil, fmt.Errorf("codex adapter not registered")
		}
		return r.Codex, nil
	case "gemini":
		if r.Gemini == nil {
			return nil, fmt.Errorf("gemini adapter not registered")
		}
		return r.Gemini, nil
	case "qwen":
		if r.Qwen == nil {
			return nil, fmt.Errorf("qwen adapter not registered")
		}
		return r.Qwen, nil
	case "opencode":
		if r.OpenCode == nil {
			return nil, fmt.Errorf("opencode adapter not registered")
		}
		return r.OpenCode, nil
	case "cursor":
		if r.Cursor == nil {
			return nil, fmt.Errorf("cursor adapter not registered")
		}
		return r.Cursor, nil
	default:
		return nil, fmt.Errorf("unknown agent '%s': no adapter registered for this provider. Available: claude, codex, gemini, qwen, opencode, cursor", agentName)
	}
}

// ContextFileForAgent returns the context filename (CLAUDE.md, AGENTS.md, GEMINI.md, QWEN.md) for the given agent so the right file is written and restored.
func ContextFileForAgent(agentName string) string {
	prefix := agentName
	if i := strings.Index(agentName, "-"); i > 0 {
		prefix = agentName[:i]
	}
	switch prefix {
	case "codex", "opencode":
		return "AGENTS.md"
	case "cursor":
		return ".cursor/rules/gump-agent.mdc"
	case "gemini":
		return "GEMINI.md"
	case "qwen":
		return "QWEN.md"
	default:
		return "CLAUDE.md"
	}
}
