package engine

import (
	"testing"

	"github.com/isomorphx/gump/internal/state"
)

func TestGraftChildStateIntoParent(t *testing.T) {
	parent := state.New()
	child := state.New()
	child.Set("judge.pass", "true")
	child.Set("judge.comments", "x")
	graftChildStateIntoParent(parent, "impl.retry_validator.distance", child)
	if parent.Get("impl.retry_validator.distance.state.judge.pass") != "true" ||
		parent.Get("impl.retry_validator.distance.state.judge.comments") != "x" {
		t.Fatalf("grafted: %q / %q",
			parent.Get("impl.retry_validator.distance.state.judge.pass"),
			parent.Get("impl.retry_validator.distance.state.judge.comments"))
	}
}

func TestMergeChildTelemetry_RunCostOnParentState(t *testing.T) {
	p := &Engine{State: state.New(), agentsUsed: make(map[string]struct{})}
	c := &Engine{
		totalCostUSD: 0.05,
		globalTokens: 100,
		agentsUsed:   map[string]struct{}{"claude-haiku": {}},
	}
	p.mergeChildTelemetry(c)
	if p.totalCostUSD != 0.05 || p.globalTokens != 100 {
		t.Fatalf("engine totals: cost=%f tokens=%d", p.totalCostUSD, p.globalTokens)
	}
	if p.State.Get("run.cost") != "0.05" {
		t.Fatalf("run.cost: %q", p.State.Get("run.cost"))
	}
}
