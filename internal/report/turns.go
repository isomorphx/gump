// Turns approximate cognitive cycles so TTFD and turn-mix metrics stay comparable across providers.
package report

import (
	"strings"
	"time"
)

// Turn is one cognitive cycle in a step (M5).
type Turn struct {
	Number     int
	Label      string
	Events     []AgentEvent
	StartedAt  time.Time
	EndedAt    time.Time
	DurationMs int
}

// BuildTurns groups classified stdout events into turns (spec §5).
func BuildTurns(events []AgentEvent, outputMode string) []Turn {
	if len(events) == 0 {
		return nil
	}
	mode := strings.TrimSpace(outputMode)
	var groups [][]AgentEvent
	var cur []AgentEvent
	for i := range events {
		ev := events[i]
		if i == 0 {
			cur = append(cur, ev)
			continue
		}
		prev := events[i-1]
		if startNewTurn(prev, ev) {
			if len(cur) > 0 {
				groups = append(groups, cur)
			}
			cur = []AgentEvent{ev}
			continue
		}
		cur = append(cur, ev)
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	var turns []Turn
	for i, g := range groups {
		t := Turn{
			Number: i + 1,
			Events: g,
			Label:  labelTurnEvents(g, mode),
		}
		t.StartedAt = g[0].ObservedAt
		t.EndedAt = g[len(g)-1].ObservedAt
		if !t.EndedAt.Before(t.StartedAt) {
			t.DurationMs = int(t.EndedAt.Sub(t.StartedAt) / time.Millisecond)
		}
		turns = append(turns, t)
	}
	return turns
}

func startNewTurn(prev, cur AgentEvent) bool {
	if isAssistantSemantic(cur.SemanticLabel) && prev.SemanticLabel == "user" {
		return true
	}
	if (cur.SemanticLabel == "text" || cur.SemanticLabel == "thinking") &&
		(prev.SemanticLabel == "write" || prev.SemanticLabel == "shell" || prev.SemanticLabel == "shell/test" || prev.SemanticLabel == "read") {
		return true
	}
	return false
}

func isAssistantSemantic(s string) bool {
	switch s {
	case "user", "other":
		return false
	default:
		return true
	}
}

func labelTurnEvents(events []AgentEvent, outputMode string) string {
	switch outputMode {
	case "plan":
		return "planning"
	case "artifact":
		return "writing"
	case "review", "validate":
		return "reviewing"
	}
	has := map[string]bool{}
	for _, e := range events {
		if e.SemanticLabel == "user" {
			continue
		}
		has[e.SemanticLabel] = true
	}
	if has["write"] {
		return "coding"
	}
	if has["shell"] || has["shell/test"] {
		return "execution"
	}
	if has["read"] || has["web"] || has["mcp"] {
		return "exploration"
	}
	nu := nonUserLabels(events)
	if len(nu) == 0 {
		return "other"
	}
	allThinking := true
	for _, s := range nu {
		if s != "thinking" {
			allThinking = false
			break
		}
	}
	if allThinking {
		return "reasoning"
	}
	allText := true
	for _, s := range nu {
		if s != "text" {
			allText = false
			break
		}
	}
	if allText {
		return "communication"
	}
	return "other"
}

func nonUserLabels(events []AgentEvent) []string {
	var out []string
	for _, e := range events {
		if e.SemanticLabel == "user" {
			continue
		}
		out = append(out, e.SemanticLabel)
	}
	return out
}
