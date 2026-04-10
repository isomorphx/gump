package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %s", err, out)
	}
	env := append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@a.com", "GIT_CONFIG_GLOBAL=/dev/null")
	commit := exec.Command("git", "commit", "--allow-empty", "-m", "init")
	commit.Dir = dir
	commit.Env = env
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s: %s", err, out)
	}
}

func TestGitRepoRoot_InsideRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	root, err := GitRepoRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Normalize for macOS where /var can equal /private/var
	rootNorm, _ := filepath.EvalSymlinks(root)
	dirNorm, _ := filepath.EvalSymlinks(dir)
	if rootNorm != dirNorm {
		t.Errorf("root %q != dir %q (normalized: %q != %q)", root, dir, rootNorm, dirNorm)
	}
}

func TestGitRepoRoot_OutsideRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := GitRepoRoot(dir)
	if err == nil {
		t.Error("expected error outside repo")
	}
	if err != nil && !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateWorktree_RemoveWorktree(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	wtDir := filepath.Join(repo, ".gump", "worktrees", "run-test")
	branch := "gump/run-test"
	if err := CreateWorktree(repo, wtDir, branch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wtDir); err != nil {
		t.Fatal("worktree dir should exist:", err)
	}
	if err := RemoveWorktree(repo, wtDir, branch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wtDir); err == nil {
		t.Error("worktree dir should be removed")
	}
}

func TestResetTo(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	wtDir := filepath.Join(repo, ".gump", "worktrees", "run-reset")
	branch := "gump/run-reset"
	if err := CreateWorktree(repo, wtDir, branch); err != nil {
		t.Fatal(err)
	}
	defer RemoveWorktree(repo, wtDir, branch)
	initial, _ := HeadCommit(wtDir)
	if err := os.WriteFile(filepath.Join(wtDir, "x.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	add := exec.Command("git", "add", "x.txt")
	add.Dir = wtDir
	add.Run()
	commit := exec.Command("git", "commit", "-m", "add x")
	commit.Dir = wtDir
	commit.Env = append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@a.com", "GIT_CONFIG_GLOBAL=/dev/null")
	commit.Run()
	if err := ResetTo(wtDir, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wtDir, "x.txt")); err == nil {
		t.Error("x.txt should be gone after reset")
	}
}

func TestHasUncommittedChanges_IgnoresGumpRuntimeState(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	if err := os.MkdirAll(filepath.Join(repo, ".gump", "runs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gump", "runs", "state.json"), []byte(`{"ok":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	dirty, err := HasUncommittedChanges(repo)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("expected .gump runtime changes to be ignored")
	}
}
