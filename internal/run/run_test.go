package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isomorphx/gump/internal/workflow"
)

func TestNewRun_RequiresGitRepo(t *testing.T) {
	dir := t.TempDir()
	rec := &workflow.Workflow{Name: "tdd"}
	_, err := NewRun(rec, filepath.Join(dir, "spec.md"), dir, []byte("name: tdd"), nil)
	if err == nil {
		t.Fatal("expected error outside git repo")
	}
	if err != nil && !strings.Contains(err.Error(), "git repository") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewRun_RequiresCleanWorkingDir(t *testing.T) {
	repo := initRepo(t)
	specPath := filepath.Join(repo, "spec.md")
	if err := os.WriteFile(specPath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	rec := &workflow.Workflow{Name: "tdd"}
	_, err := NewRun(rec, specPath, repo, []byte("name: tdd"), nil)
	if err == nil {
		t.Fatal("expected error with uncommitted changes")
	}
	if err != nil && !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("unexpected error: %v", err)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGitEnv(t, dir, "commit", "--allow-empty", "-m", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %s", args, err, out)
	}
}

func runGitEnv(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@a.com", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %s", args, err, out)
	}
}