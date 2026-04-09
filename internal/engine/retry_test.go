package engine

import (
	"testing"

	"github.com/isomorphx/gump/internal/workflow"
)

func TestExpandStrategyUsedInRetry(t *testing.T) {
	// same: 2, escalate: claude-sonnet → [same, same, escalate: claude-sonnet]
	entries := []workflow.StrategyEntryCompat{
		{Type: "same", Count: 2},
		{Type: "escalate", Agent: "claude-sonnet", Count: 1},
	}
	expanded := workflow.ExpandStrategy(entries)
	if len(expanded) != 3 {
		t.Fatalf("len(expanded) = %d, want 3", len(expanded))
	}
	if expanded[0].Type != "same" || expanded[1].Type != "same" {
		t.Errorf("first two should be same: %+v %+v", expanded[0], expanded[1])
	}
	if expanded[2].Type != "escalate" || expanded[2].Agent != "claude-sonnet" {
		t.Errorf("third should be escalate:claude-sonnet: %+v", expanded[2])
	}
}
