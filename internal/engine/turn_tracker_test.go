package engine

import (
	"testing"

	"github.com/isomorphx/gump/internal/agent"
)

func TestTurnTracker_ClassificationLabels(t *testing.T) {
	cases := []struct {
		name    string
		actions []Action
		want    string
	}{
		{"planning", nil, "planning"},
		{"exploration", []Action{{Type: "read_file"}, {Type: "read_file"}, {Type: "search"}, {Type: "read"}}, "exploration"},
		{"execution", []Action{{Type: "bash"}, {Type: "bash"}, {Type: "bash"}, {Type: "bash"}, {Type: "bash"}, {Type: "read"}}, "execution"},
		{"writing", []Action{{Type: "write_file"}, {Type: "edit_file"}, {Type: "apply_patch"}, {Type: "write"}}, "writing"},
		{"coding", []Action{{Type: "write_file"}, {Type: "bash"}}, "coding"},
	}
	for _, tc := range cases {
		if got := classifyTurn(tc.actions); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestTurnTracker_BoundaryCodex(t *testing.T) {
	tt := NewTurnTracker("codex")
	evs := []agent.StreamEvent{
		{Type: "raw", Raw: []byte(`{"type":"turn.started"}`)},
		{Type: "assistant", Raw: []byte(`{"item":{"type":"command_execution","command":"go test ./..."}}`)},
		{Type: "raw", Raw: []byte(`{"type":"turn.completed"}`)},
		{Type: "raw", Raw: []byte(`{"type":"turn.started"}`)},
	}
	completed := 0
	for _, ev := range evs {
		if c, _ := tt.Consume(ev); c != nil {
			completed++
		}
	}
	if completed == 0 {
		t.Fatal("expected at least one completed turn")
	}
}

func TestNormalizeToolType_DistinctEditPatchWrite(t *testing.T) {
	if got := normalizeToolType("edit_file"); got != "edit" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeToolType("apply_patch"); got != "patch" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeToolType("write_file"); got != "write" {
		t.Fatalf("got %q", got)
	}
}

func TestTurnTracker_BoundaryCursorModelCallID(t *testing.T) {
	tt := NewTurnTracker("cursor")
	_, _ = tt.Consume(agent.StreamEvent{Type: "assistant", Raw: []byte(`{"model_call_id":"m1"}`)})
	c, _ := tt.Consume(agent.StreamEvent{Type: "assistant", Raw: []byte(`{"model_call_id":"m1"}`)})
	if c != nil {
		t.Fatal("same model_call_id should stay in same turn")
	}
	c, _ = tt.Consume(agent.StreamEvent{Type: "assistant", Raw: []byte(`{"model_call_id":"m2"}`)})
	if c == nil {
		t.Fatal("new model_call_id should start a new turn")
	}
}

func TestExtractActionsAndTokens_OpenCodeToolUse(t *testing.T) {
	actions, _, _ := extractActionsAndTokens([]byte(`{
		"type":"tool_use",
		"tool_call":{
			"tool":"apply_patch",
			"state":{
				"input":"patch content",
				"metadata":{"files":["cmd/main.go","internal/x.go"]}
			}
		}
	}`))
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].Type != "apply_patch" {
		t.Fatalf("unexpected action type: %s", actions[0].Type)
	}
	if actions[0].Target == "" {
		t.Fatal("expected non-empty action target")
	}
}
