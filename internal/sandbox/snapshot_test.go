package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshot_NoChanges_ReturnsEmpty(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	wtDir := filepath.Join(repo, ".pudding", "worktrees", "cook-snap")
	if err := CreateWorktree(repo, wtDir, "pudding/cook-snap"); err != nil {
		t.Fatal(err)
	}
	defer RemoveWorktree(repo, wtDir, "pudding/cook-snap")
	dc, err := Snapshot(wtDir, "plan", "-", 1)
	if err != nil {
		t.Fatal(err)
	}
	if dc.Patch != "" || len(dc.FilesChanged) != 0 {
		t.Errorf("expected empty contract: patch=%q files=%v", dc.Patch, dc.FilesChanged)
	}
	if dc.BaseCommit != dc.HeadCommit {
		t.Error("base and head should match when no changes")
	}
}

func TestSnapshot_WithChanges_ReturnsDiff(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	wtDir := filepath.Join(repo, ".pudding", "worktrees", "cook-snap2")
	if err := CreateWorktree(repo, wtDir, "pudding/cook-snap2"); err != nil {
		t.Fatal(err)
	}
	defer RemoveWorktree(repo, wtDir, "pudding/cook-snap2")
	base, _ := HeadCommit(wtDir)
	if err := os.WriteFile(filepath.Join(wtDir, "new.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	dc, err := Snapshot(wtDir, "red", "task-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	if dc.BaseCommit != base {
		t.Errorf("base %q != %q", dc.BaseCommit, base)
	}
	if dc.HeadCommit == base {
		t.Error("head should differ from base after commit")
	}
	if dc.Patch == "" || !strings.Contains(dc.Patch, "new.txt") {
		t.Errorf("patch should mention new.txt: %s", dc.Patch)
	}
	if len(dc.FilesChanged) != 1 || dc.FilesChanged[0] != "new.txt" {
		t.Errorf("files changed: %v", dc.FilesChanged)
	}
}
