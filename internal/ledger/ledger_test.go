package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLedger_EmitAndClose(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, "test-cook-id")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	if err := l.Emit(RunStarted{RunID: "test-cook-id", Workflow: "tdd", Spec: "spec.md", Commit: "abc", Branch: "main", AgentsCLI: map[string]string{"claude": "1.0"}}); err != nil {
		t.Fatal(err)
	}
	if err := l.Emit(StepStarted{Step: "code", Agent: "claude", Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	for i, line := range lines {
		if !strings.Contains(line, `"ts"`) {
			t.Errorf("line %d should contain ts", i+1)
		}
		if !strings.Contains(line, `"type":`) {
			t.Errorf("line %d should contain type", i+1)
		}
	}
}

func TestLedger_WriteArtifact(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, "cook-1")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	rel, err := l.WriteArtifact("code-stdout.log", []byte("hello\n"))
	if err != nil {
		t.Fatal(err)
	}
	if rel != "artifacts/code-stdout.log" && rel != filepath.Join("artifacts", "code-stdout.log") {
		t.Errorf("unexpected rel path: %s", rel)
	}
	abs := l.ArtifactPath("code-stdout.log")
	if !strings.HasSuffix(abs, "artifacts"+string(filepath.Separator)+"code-stdout.log") && !strings.Contains(abs, "artifacts/code-stdout.log") {
		t.Errorf("ArtifactPath: %s", abs)
	}
	content, _ := os.ReadFile(filepath.Join(dir, "artifacts", "code-stdout.log"))
	if string(content) != "hello\n" {
		t.Errorf("content: %q", content)
	}
}

func TestSanitizeStepPath(t *testing.T) {
	if got := SanitizeStepPath("implement/task-1/red"); got != "implement-task-1-red" {
		t.Errorf("got %s", got)
	}
}

func TestArtifactName(t *testing.T) {
	if got := ArtifactName("code", 1, "stdout", "log"); got != "code-stdout.log" {
		t.Errorf("got %s", got)
	}
	if got := ArtifactName("implement/task-1/red", 2, "diff", "patch"); got != "implement-task-1-red-attempt2-diff.patch" {
		t.Errorf("got %s", got)
	}
}
