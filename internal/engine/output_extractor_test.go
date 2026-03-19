package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareOutputDir(t *testing.T) {
	dir := t.TempDir()
	if err := PrepareOutputDir(dir); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, ".pudding", "out")
	if st, err := os.Stat(outPath); err != nil || !st.IsDir() {
		t.Errorf("expected .pudding/out dir: %v", err)
	}
	// Idempotent: empty and recreate
	if err := PrepareOutputDir(dir); err != nil {
		t.Fatal(err)
	}
}

func TestExtractPlanOutput_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, _, err := ExtractPlanOutput(dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if err != nil && !strings.Contains(err.Error(), ".pudding/out/plan.json") {
		t.Errorf("error should mention plan.json: %v", err)
	}
}

func TestExtractPlanOutput_Valid(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, ".pudding", "out")
	_ = os.MkdirAll(outDir, 0755)
	planPath := filepath.Join(outDir, "plan.json")
	content := `[{"name":"t1","description":"D1","files":["a.go"]}]`
	if err := os.WriteFile(planPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	tasks, raw, err := ExtractPlanOutput(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Name != "t1" {
		t.Errorf("tasks: %+v", tasks)
	}
	if raw != content {
		t.Errorf("raw: %q", raw)
	}
}

func TestExtractArtifactOutput_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := ExtractArtifactOutput(dir)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractArtifactOutput_Valid(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, ".pudding", "out")
	_ = os.MkdirAll(outDir, 0755)
	artifactPath := filepath.Join(outDir, "artifact.txt")
	content := "stub artifact output"
	if err := os.WriteFile(artifactPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ExtractArtifactOutput(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != content {
		t.Errorf("got %q", got)
	}
}

