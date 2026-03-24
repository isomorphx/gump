package engine

import (
	"strings"
	"testing"
	"time"
)

func TestCompactTokens(t *testing.T) {
	if got := compactTokens(15000); got != "15k tok" {
		t.Fatalf("got %q", got)
	}
	if got := compactTokens(1200); got != "1.2k tok" {
		t.Fatalf("got %q", got)
	}
	if got := compactTokens(124000); got != "124k tok" {
		t.Fatalf("got %q", got)
	}
}

func TestSpinnerStates(t *testing.T) {
	if spinnerForTurn(Turn{IsComplete: false}) != "⠿" {
		t.Fatal("running spinner should be ⠿")
	}
	if spinnerForTurn(Turn{IsComplete: false, Interrupted: true}) != "✗" {
		t.Fatal("interrupted spinner should be ✗")
	}
	if spinnerForTurn(Turn{IsComplete: true}) != "✓" {
		t.Fatal("complete spinner should be ✓")
	}
}

func TestFormatTurnLine(t *testing.T) {
	line := formatTurnLine(Turn{
		Number:     1,
		Label:      "exploration",
		Actions:    []Action{{Type: "read_file"}, {Type: "read_file"}},
		TokensIn:   1000,
		TokensOut:  200,
		Duration:   12 * time.Second,
		IsComplete: false,
	})
	for _, part := range []string{"T1", "⠿", "exploration", "read", "1.2k tok", "12s"} {
		if !strings.Contains(line, part) {
			t.Fatalf("line missing %q: %s", part, line)
		}
	}
}

func TestFormatActionLine_TruncatesTargets(t *testing.T) {
	path := strings.Repeat("a", 100)
	line := formatActionLine(Action{Type: "read_file", Target: path})
	if !strings.HasSuffix(line, "...") {
		t.Fatalf("path target should be truncated: %s", line)
	}
	cmd := strings.Repeat("x", 120)
	line = formatActionLine(Action{Type: "bash", Target: cmd})
	if !strings.HasSuffix(line, "...") {
		t.Fatalf("bash target should be truncated: %s", line)
	}
}
