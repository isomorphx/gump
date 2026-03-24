package engine

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/isomorphx/gump/internal/agent"
)

type Turn struct {
	Number      int
	Label       string
	Actions     []Action
	TokensIn    int
	TokensOut   int
	Duration    time.Duration
	IsComplete  bool
	Interrupted bool
}

type Action struct {
	Type   string
	Target string
}

type TurnTracker struct {
	provider        string
	lastEvent       string
	lastModelCallID string
	current         *Turn
	turnCounter     int
	currentStart    time.Time
}

func NewTurnTracker(agentName string) *TurnTracker {
	p := agentName
	if i := strings.Index(agentName, "-"); i > 0 {
		p = agentName[:i]
	}
	return &TurnTracker{provider: p}
}

func (tt *TurnTracker) Consume(ev agent.StreamEvent) (completed *Turn, current *Turn) {
	if tt.shouldStartNewTurn(ev) {
		completed = tt.finalizeCurrent(false)
		tt.turnCounter++
		tt.currentStart = time.Now()
		tt.current = &Turn{
			Number:  tt.turnCounter,
			Label:   "planning",
			Actions: []Action{},
		}
	}
	if tt.current == nil {
		tt.turnCounter++
		tt.currentStart = time.Now()
		tt.current = &Turn{Number: tt.turnCounter, Label: "planning", Actions: []Action{}}
	}
	tt.applyEvent(ev)
	tt.lastEvent = ev.Type
	var base map[string]interface{}
	if json.Unmarshal(ev.Raw, &base) == nil {
		if modelCallID := asString(base["model_call_id"]); modelCallID != "" {
			tt.lastModelCallID = modelCallID
		}
	}
	if tt.shouldCompleteTurn(ev) {
		c := tt.finalizeCurrent(false)
		return c, tt.current
	}
	return completed, tt.current
}

func (tt *TurnTracker) Flush() *Turn {
	return tt.finalizeCurrent(false)
}

func (tt *TurnTracker) Interrupt() *Turn {
	return tt.finalizeCurrent(true)
}

func (tt *TurnTracker) shouldStartNewTurn(ev agent.StreamEvent) bool {
	if tt.current == nil {
		return true
	}
	var base struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(ev.Raw, &base)
	var raw map[string]interface{}
	_ = json.Unmarshal(ev.Raw, &raw)
	switch tt.provider {
	case "codex":
		return base.Type == "turn.started"
	case "opencode":
		return base.Type == "step_start"
	case "cursor":
		if ev.Type != "assistant" {
			return false
		}
		modelCallID := asString(raw["model_call_id"])
		if modelCallID == "" {
			return tt.lastEvent == "user"
		}
		return modelCallID != tt.lastModelCallID
	case "gemini":
		return (base.Type == "tool_use" || (base.Type == "message" && ev.Type == "assistant")) && tt.lastEvent == "user"
	case "qwen", "claude":
		return ev.Type == "assistant" && tt.lastEvent == "user"
	default:
		return ev.Type == "assistant" && tt.lastEvent == "user"
	}
}

func (tt *TurnTracker) shouldCompleteTurn(ev agent.StreamEvent) bool {
	var base struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(ev.Raw, &base)
	switch tt.provider {
	case "codex":
		return base.Type == "turn.completed"
	case "opencode":
		return base.Type == "step_finish"
	default:
		return false
	}
}

func (tt *TurnTracker) applyEvent(ev agent.StreamEvent) {
	actions, inTok, outTok := extractActionsAndTokens(ev.Raw)
	tt.current.Actions = append(tt.current.Actions, actions...)
	tt.current.TokensIn += inTok
	tt.current.TokensOut += outTok
	tt.current.Label = classifyTurn(tt.current.Actions)
}

func (tt *TurnTracker) finalizeCurrent(interrupted bool) *Turn {
	if tt.current == nil {
		return nil
	}
	tt.current.Duration = time.Since(tt.currentStart)
	tt.current.IsComplete = !interrupted
	tt.current.Interrupted = interrupted
	out := *tt.current
	tt.current = nil
	return &out
}

func extractActionsAndTokens(raw []byte) ([]Action, int, int) {
	var base map[string]interface{}
	if json.Unmarshal(raw, &base) != nil {
		return nil, 0, 0
	}
	var actions []Action
	inTok := 0
	outTok := 0
	if msg, ok := base["message"].(map[string]interface{}); ok {
		if usage, ok := msg["usage"].(map[string]interface{}); ok {
			inTok += int(numFromAny(usage["input_tokens"]))
			outTok += int(numFromAny(usage["output_tokens"]))
		}
		if content, ok := msg["content"].([]interface{}); ok {
			for _, item := range content {
				obj, _ := item.(map[string]interface{})
				if obj == nil || obj["type"] != "tool_use" {
					continue
				}
				tool := asString(obj["name"])
				target := ""
				if in, ok := obj["input"].(map[string]interface{}); ok {
					target = asString(in["file_path"])
					if target == "" {
						target = asString(in["path"])
					}
					if target == "" {
						target = asString(in["command"])
					}
				}
				actions = append(actions, Action{Type: tool, Target: target})
			}
		}
	}
	if item, ok := base["item"].(map[string]interface{}); ok {
		tool := asString(item["type"])
		target := asString(item["command"])
		if usage, ok := base["usage"].(map[string]interface{}); ok {
			inTok += int(numFromAny(usage["input_tokens"]))
			outTok += int(numFromAny(usage["output_tokens"]))
		}
		if tool == "command_execution" || tool == "file_change" {
			actions = append(actions, Action{Type: tool, Target: target})
		}
	}
	if tool := asString(base["tool_name"]); tool != "" {
		target := ""
		if p, ok := base["parameters"].(map[string]interface{}); ok {
			target = asString(p["file_path"])
			if target == "" {
				target = asString(p["command"])
			}
		}
		actions = append(actions, Action{Type: tool, Target: target})
	}
	if asString(base["type"]) == "tool_use" {
		if toolCall, ok := base["tool_call"].(map[string]interface{}); ok {
			tool := asString(toolCall["tool"])
			target := ""
			if state, ok := toolCall["state"].(map[string]interface{}); ok {
				target = strings.TrimSpace(stringifyAny(state["input"]))
				if target == "" {
					if md, ok := state["metadata"].(map[string]interface{}); ok {
						target = strings.TrimSpace(stringifyAny(md["files"]))
					}
				}
			}
			if tool != "" {
				actions = append(actions, Action{Type: tool, Target: target})
			}
		}
	}
	if t := asString(base["type"]); t == "step_finish" {
		if part, ok := base["part"].(map[string]interface{}); ok {
			if tok, ok := part["tokens"].(map[string]interface{}); ok {
				inTok += int(numFromAny(tok["input"]))
				outTok += int(numFromAny(tok["output"]))
			}
		}
	}
	return actions, inTok, outTok
}

func stringifyAny(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case []interface{}:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			s := strings.TrimSpace(stringifyAny(item))
			if s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	case map[string]interface{}:
		if p := asString(x["path"]); p != "" {
			return p
		}
		if c := asString(x["command"]); c != "" {
			return c
		}
		b, _ := json.Marshal(x)
		return string(b)
	default:
		return asString(v)
	}
}

func classifyTurn(actions []Action) string {
	total := len(actions)
	if total == 0 {
		return "planning"
	}
	read, write, edit, patch, bash, search, glob := 0, 0, 0, 0, 0, 0, 0
	for _, a := range actions {
		switch normalizeToolType(a.Type) {
		case "read":
			read++
		case "search":
			search++
		case "glob":
			glob++
		case "write":
			write++
		case "edit":
			edit++
		case "patch":
			patch++
		case "bash":
			bash++
		}
	}
	writeTotal := write + edit + patch
	if (read+search+glob)*100/total > 80 {
		return "exploration"
	}
	if bash*100/total > 80 {
		return "execution"
	}
	if writeTotal > 0 && bash > 0 {
		return "coding"
	}
	if writeTotal*100/total >= 80 && bash == 0 {
		return "writing"
	}
	return "mixed"
}
