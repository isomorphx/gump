package engine

import (
	"testing"

	"github.com/isomorphx/gump/internal/workflow"
)

func TestGuardRuntime_MaxTurns(t *testing.T) {
	s := &workflow.Step{Guard: workflow.Guard{MaxTurns: 1}}
	g := NewGuardRuntime(s)
	_, _, _ = g.CheckEvent([]byte(`{"type":"action","name":"write","input":{"path":".gump/out/plan.json"}}`))
	if _, _, ok := g.CheckEvent([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"a"}]}}`)); ok {
		t.Fatal("first turn should pass")
	}
	_, _, _ = g.CheckEvent([]byte(`{"type":"action","name":"write","input":{"path":".gump/out/plan.json"}}`))
	if got, _, ok := g.CheckEvent([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"b"}]}}`)); !ok || got != "max_turns" {
		t.Fatalf("expected max_turns, got ok=%v guard=%q", ok, got)
	}
}

func TestGuardRuntime_NoWrite(t *testing.T) {
	v := true
	s := &workflow.Step{Guard: workflow.Guard{NoWrite: &v}}
	g := NewGuardRuntime(s)
	if got, _, ok := g.CheckEvent([]byte(`{"type":"action","name":"write","input":{"path":"main.go"}}`)); !ok || got != "no_write" {
		t.Fatalf("expected no_write, got ok=%v guard=%q", ok, got)
	}
}
