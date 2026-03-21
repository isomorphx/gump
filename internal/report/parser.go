package report

import (
	"bufio"
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// AgentEvent is one line of agent stdout after parsing (M5 model).
type AgentEvent struct {
	ObservedAt    time.Time
	ProviderAt    *time.Time
	SurfaceLabel  string
	SemanticLabel string
	Raw           []byte
}

// ProviderKind selects semantic classification rules (ledger agent family).
type ProviderKind int

const (
	ProviderClaudeLike ProviderKind = iota
	ProviderCodex
)

// ProviderForAgent maps ledger agent names to classification rules.
func ProviderForAgent(agent string) ProviderKind {
	a := strings.ToLower(agent)
	switch {
	case strings.Contains(a, "codex") && !strings.Contains(a, "claude"):
		return ProviderCodex
	default:
		return ProviderClaudeLike
	}
}

// ParseStdoutFile reads artefact stdout (observed_at prefix or raw JSON) and returns classified events.
func ParseStdoutFile(content []byte, provider ProviderKind, stepStartedAt time.Time) []AgentEvent {
	var out []AgentEvent
	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(nil, 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		ev, ok := ParseStdoutLine(line, provider, stepStartedAt)
		if !ok {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// ParseStdoutLine parses one line; ok is false when the line should be skipped entirely (empty).
func ParseStdoutLine(line []byte, provider ProviderKind, fallbackTime time.Time) (ev AgentEvent, ok bool) {
	observedAt := fallbackTime
	rest := line
	if idx := bytes.IndexByte(line, '{'); idx > 0 {
		prefix := strings.TrimSpace(string(line[:idx]))
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", prefix); err == nil {
			observedAt = t
			rest = bytes.TrimSpace(line[idx:])
		} else if t2, err2 := time.Parse(time.RFC3339Nano, prefix); err2 == nil {
			observedAt = t2
			rest = bytes.TrimSpace(line[idx:])
		}
	}
	if len(rest) == 0 {
		return ev, false
	}
	ev = AgentEvent{ObservedAt: observedAt, Raw: append([]byte(nil), rest...)}
	switch provider {
	case ProviderCodex:
		ev.SemanticLabel = classifyCodexLine(string(rest))
	default:
		ev.SemanticLabel = classifyClaudeLike(rest)
		ev.SurfaceLabel = surfaceTypeFromJSON(rest)
	}
	if ev.SemanticLabel == "" {
		return ev, false
	}
	return ev, true
}

func surfaceTypeFromJSON(line []byte) string {
	var m map[string]interface{}
	if json.Unmarshal(line, &m) != nil {
		return ""
	}
	t, _ := m["type"].(string)
	return t
}

func classifyClaudeLike(line []byte) string {
	var m map[string]interface{}
	if json.Unmarshal(line, &m) != nil {
		return classifyCodexLine(string(line))
	}
	t, _ := m["type"].(string)
	switch t {
	case "result":
		return ""
	case "system":
		return "other"
	}
	if t == "assistant" {
		return classifyClaudeAssistant(m)
	}
	if t == "user" {
		return "user"
	}
	return "other"
}

func classifyClaudeAssistant(m map[string]interface{}) string {
	var content interface{}
	if msg, ok := m["message"].(map[string]interface{}); ok {
		content = msg["content"]
	} else {
		content = m["content"]
	}
	blocks := collectContentBlocks(content)
	if len(blocks) == 0 {
		return "other"
	}
	var toolNames []string
	hasThinking := false
	onlyText := len(blocks) > 0
	for _, b := range blocks {
		bt, _ := b["type"].(string)
		switch bt {
		case "thinking":
			hasThinking = true
			onlyText = false
		case "text":
			continue
		case "tool_use":
			onlyText = false
			if n, ok := b["name"].(string); ok {
				toolNames = append(toolNames, n)
			}
		default:
			onlyText = false
		}
	}
	for _, name := range toolNames {
		if isWriteTool(name) {
			return "write"
		}
	}
	for _, name := range toolNames {
		if isReadTool(name) {
			return "read"
		}
	}
	for _, name := range toolNames {
		if name == "Bash" {
			in := bashInputFromBlock(blocks, name)
			if shellLooksLikeTest(in) {
				return "shell/test"
			}
			return "shell"
		}
	}
	for _, name := range toolNames {
		if name == "WebSearch" || name == "WebFetch" {
			return "web"
		}
	}
	for _, name := range toolNames {
		if strings.HasPrefix(name, "mcp__") {
			return "mcp"
		}
	}
	if len(toolNames) > 0 {
		return "other"
	}
	if hasThinking {
		return "thinking"
	}
	if onlyText {
		return "text"
	}
	return "other"
}

func collectContentBlocks(content interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	switch x := content.(type) {
	case []interface{}:
		for _, e := range x {
			if m, ok := e.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
	case string:
		// Rare: string content counts as text-only assistant surface
		if strings.TrimSpace(x) != "" {
			out = append(out, map[string]interface{}{"type": "text", "text": x})
		}
	}
	return out
}

func isWriteTool(name string) bool {
	switch name {
	case "Write", "Edit", "MultiEdit":
		return true
	default:
		return false
	}
}

func isReadTool(name string) bool {
	switch name {
	case "Read", "Glob", "Grep":
		return true
	default:
		return false
	}
}

func bashInputFromBlock(blocks []map[string]interface{}, toolName string) string {
	for _, b := range blocks {
		if b["type"] != "tool_use" {
			continue
		}
		n, _ := b["name"].(string)
		if n != toolName {
			continue
		}
		if in, ok := b["input"].(map[string]interface{}); ok {
			if s, ok := in["command"].(string); ok {
				return s
			}
			// Some payloads nest differently
			raw, _ := json.Marshal(in)
			return string(raw)
		}
	}
	return ""
}

var (
	testCmdRE   = regexp.MustCompile(`\b(go test|npm test|pytest|cargo test|make test)\b`)
	codexReadRE = regexp.MustCompile(`\b(cat|ls|find|grep|head|tail)\b`)
	codexRmRE   = regexp.MustCompile(`\brm\b`)
)

func shellLooksLikeTest(s string) bool {
	return testCmdRE.MatchString(s)
}

// classifyCodexLine applies inferred shell heuristics (spec: Codex / unstructured fallback).
func classifyCodexLine(line string) string {
	s := line
	if strings.Contains(s, ">") || strings.Contains(s, ">>") || strings.Contains(s, "tee") ||
		strings.Contains(s, "sed -i") || codexRmRE.MatchString(s) ||
		strings.Contains(s, "cp ") || strings.Contains(s, "mv ") || strings.Contains(s, "mkdir") {
		return "write"
	}
	if codexReadRE.MatchString(s) && !strings.Contains(s, ">") && !strings.Contains(s, ">>") {
		return "read"
	}
	if testCmdRE.MatchString(s) {
		return "shell/test"
	}
	if looksLikeShellInvocation(s) {
		return "shell"
	}
	return "other"
}

func looksLikeShellInvocation(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Heuristic: shell comment or known prefixes
	if strings.HasPrefix(s, "$ ") || strings.HasPrefix(s, "% ") {
		return true
	}
	if strings.Contains(s, " | ") && (strings.Contains(s, "grep") || strings.Contains(s, "awk")) {
		return true
	}
	return false
}
