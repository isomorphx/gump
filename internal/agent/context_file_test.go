package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextFileForAgent(t *testing.T) {
	tests := []struct {
		agent string
		want  string
	}{
		{"claude", "CLAUDE.md"},
		{"claude-opus", "CLAUDE.md"},
		{"codex", "AGENTS.md"},
		{"codex-gpt5", "AGENTS.md"},
		{"gemini", "GEMINI.md"},
		{"gemini-flash", "GEMINI.md"},
		{"qwen", "QWEN.md"},
		{"qwen-coder-plus", "QWEN.md"},
		{"opencode", "AGENTS.md"},
		{"opencode-sonnet", "AGENTS.md"},
	}
	for _, tt := range tests {
		got := ContextFileForAgent(tt.agent)
		if got != tt.want {
			t.Errorf("ContextFileForAgent(%q) = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func TestWritePlanContext_RespectsContextFile(t *testing.T) {
	dir := t.TempDir()
	for _, ctxFile := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", "QWEN.md"} {
		err := WritePlanContext(dir, "spec content here", ctxFile)
		if err != nil {
			t.Fatalf("WritePlanContext(..., %q): %v", ctxFile, err)
		}
		path := filepath.Join(dir, ctxFile)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(data), "spec content here") {
			t.Errorf("%s: content missing spec", ctxFile)
		}
		if !strings.Contains(string(data), "Pudding — Agent Instructions") {
			t.Errorf("%s: content missing header", ctxFile)
		}
		_ = os.Remove(path)
	}
}

func TestWriteWithBackup_BackupFilename(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(existing, []byte("# My Project Rules"), 0644); err != nil {
		t.Fatal(err)
	}
	err := writeWithBackup(dir, "Pudding content", "AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(dir, ".pudding-original-agents.md")
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("backup should exist: %v", err)
	}
	if string(data) != "# My Project Rules" {
		t.Errorf("backup content: got %q", data)
	}
	data2, _ := os.ReadFile(existing)
	if !strings.Contains(string(data2), "Pudding content") {
		t.Errorf("AGENTS.md should have new content: %q", data2)
	}
}

func TestRemoveOtherContextFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range AllProviderContextFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	RemoveOtherContextFiles(dir, "AGENTS.md")
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Error("AGENTS.md should remain")
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "GEMINI.md")); err == nil {
		t.Error("GEMINI.md should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "QWEN.md")); err == nil {
		t.Error("QWEN.md should be removed")
	}
}

func TestRestoreAllContextFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("pudding content"), 0644); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(dir, ".pudding-original-agents.md")
	if err := os.WriteFile(backupPath, []byte("# My Project Rules"), 0644); err != nil {
		t.Fatal(err)
	}
	RestoreAllContextFiles(dir)
	data, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if string(data) != "# My Project Rules" {
		t.Errorf("AGENTS.md should be restored: got %q", data)
	}
	if _, err := os.Stat(backupPath); err == nil {
		t.Error("backup file should be removed after restore")
	}
}
