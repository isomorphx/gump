package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/isomorphx/gump/internal/agent"
	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/run"
	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/validate"
	"github.com/isomorphx/gump/internal/workflow"
)

func TestNew(t *testing.T) {
	c := &run.Run{ID: "test", State: state.New()}
	rec := &workflow.Workflow{Name: "r", Steps: []workflow.Step{}}
	cfg := &config.Config{}
	e := New(c, rec, &agent.StubResolver{Stub: &agent.StubAdapter{}}, cfg, "spec content")
	if e.Run != c || e.Workflow != rec || e.SpecContent != "spec content" {
		t.Errorf("New: fields not set %+v", e)
	}
}

func TestStepStatus_Constants(t *testing.T) {
	if StepPending != "pending" || StepRunning != "running" || StepPass != "pass" || StepFatal != "fatal" {
		t.Error("StepStatus constants wrong")
	}
}

func TestStepExecution_ZeroValue(t *testing.T) {
	var se StepExecution
	if se.Attempt != 0 {
		t.Error("Attempt should be 0")
	}
	// StartedAt/FinishedAt are zero value time - no need to assert
	_ = time.Now()
}

// T6: Validation summary with skips shows "N validators passed, M skipped"
func TestFormatValidationPassSummary_WithSkips(t *testing.T) {
	vr := &validate.ValidationResult{
		Pass: true,
		Results: []validate.SingleResult{
			{Validator: "compile", Pass: true},
			{Validator: "test", Pass: true},
			{Validator: "lint (skipped)", Pass: true, Skipped: true},
		},
	}
	passed, skipped := countValidationPassedSkipped(vr)
	if passed != 2 || skipped != 1 {
		t.Errorf("countValidationPassedSkipped: got passed=%d skipped=%d, want 2, 1", passed, skipped)
	}
	summary := formatValidationPassSummary(passed, skipped)
	if !strings.Contains(summary, "2 validators passed") || !strings.Contains(summary, "1 skipped") {
		t.Errorf("summary should contain '2 validators passed' and '1 skipped': got %q", summary)
	}
	if strings.Contains(summary, "failed") {
		t.Error("summary should not contain 'failed'")
	}
}

func TestGoModuleRootBinaryName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/smoketest\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := goModuleRootBinaryName(dir); got != "smoketest" {
		t.Errorf("got %q, want smoketest", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module lone\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := goModuleRootBinaryName(dir); got != "lone" {
		t.Errorf("got %q, want lone", got)
	}
}

func TestFilterRepoFilesOnly_ExcludesGoModuleBinary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/smoketest\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	in := []string{"math.go", "smoketest", "CLAUDE.md"}
	out := filterRepoFilesOnly(dir, in)
	if len(out) != 1 || out[0] != "math.go" {
		t.Errorf("got %v, want [math.go]", out)
	}
}
