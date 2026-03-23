package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// M2-E2E-1: diff mode context file + conventions.
func TestM2_E2E1_ContextFileDiffMode(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/conventions.md", "Use Go 1.22")
	writeFile(t, dir, ".pudding/recipes/test-m2-diff.yaml", `name: test-m2-diff
description: M2 diff context
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "Do the task: {spec}"
    gate: [compile]
`)
	writeFile(t, dir, "spec.md", "Add a function")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files": {"add.go": "package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	env := append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@a.com", "GIT_CONFIG_GLOBAL=/dev/null")
	add := exec.Command("git", "add", "spec.md", ".pudding/recipes/test-m2-diff.yaml", ".pudding-test-scenario.json", "-f", ".gump/conventions.md")
	add.Dir = dir
	add.Env = env
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s: %s", err, out)
	}
	commit := exec.Command("git", "commit", "-m", "setup")
	commit.Dir = dir
	commit.Env = env
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s: %s", err, out)
	}
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "test-m2-diff", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	uuid := extractCookID(stdout)
	if uuid == "" {
		t.Fatal("no cook id")
	}
	claude := filepath.Join(dir, ".gump", "worktrees", "run-"+uuid, "CLAUDE.md")
	body := readFile(t, claude)
	if !strings.Contains(body, "You are executing a code step") {
		t.Error("expected code step wording")
	}
	if !strings.Contains(body, "Use Go 1.22") {
		t.Error("expected conventions injected")
	}
	if !strings.Contains(body, "git diff") {
		t.Error("expected output expectations (git diff)")
	}
	if strings.Contains(body, ".gump/out/") {
		t.Error("diff mode should not spell the reserved output path (avoids implying artifact/plan outputs)")
	}
}

// M2-E2E-2: review mode context file.
func TestM2_E2E2_ContextFileReviewMode(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".pudding/recipes/test-m2-review.yaml", `name: test-m2-review
description: M2 review context
steps:
  - name: rev
    agent: claude-haiku
    output: review
    prompt: "Review the code"
    gate: [compile]
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files": {"add.go": "package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "test-m2-review", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	uuid := extractCookID(stdout)
	claude := filepath.Join(dir, ".gump", "worktrees", "run-"+uuid, "CLAUDE.md")
	body := readFile(t, claude)
	if !strings.Contains(body, "review step") {
		t.Error("expected review step wording")
	}
	if !strings.Contains(body, ".gump/out/review.json") {
		t.Error("expected review.json path")
	}
	if !strings.Contains(body, `"pass": true`) {
		t.Error("expected schema snippet")
	}
	if !strings.Contains(body, "Do NOT modify any source code") {
		t.Error("expected reviewer rule")
	}
}

// M2-E2E-3: truncation markers on retry with configured limits.
func TestM2_E2E3_TruncationOnRetry(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "gump.toml", `[error_context]
max_error_chars = 2000
max_diff_chars = 3000
`)
	hugeMsg := strings.Repeat("Z", 6000)
	bigGo := "package main\n\nfunc Add(a, b int) int { return 0 }\n// " + strings.Repeat("Y", 9000) + "\n"
	addTest := fmt.Sprintf(`package main

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatalf(%q)
	}
}
`, hugeMsg)
	scenario := fmt.Sprintf(`{"by_attempt":{"1":{"files":{"add.go":%q,"add_test.go":%q}},"2":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}},"files":{}}`,
		bigGo, addTest)
	writeFile(t, dir, ".pudding-test-scenario.json", scenario)
	writeFile(t, dir, ".pudding/recipes/test-m2-trunc.yaml", `name: test-m2-trunc
steps:
  - name: code
    agent: claude-haiku
    prompt: "Implement Add"
    gate: [compile, test]
    on_failure:
      retry: 2
      strategy: [same]
`)
	writeFile(t, dir, "spec.md", "Add")
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "test-m2-trunc", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	uuid := extractCookID(stdout)
	claude := filepath.Join(dir, ".gump", "worktrees", "run-"+uuid, "CLAUDE.md")
	body := readFile(t, claude)
	if !strings.Contains(body, "[... truncated") {
		t.Error("expected truncation marker in retry context")
	}
	if !strings.Contains(body, "retry attempt 2 of 2") {
		t.Error("expected retry attempt 2 of 2")
	}
}

// M2-E2E-4: context source file injection.
func TestM2_E2E4_ContextSourceFileInjection(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "docs/arch.md", "MARKER_CONTEXT_TEST_12345")
	writeFile(t, dir, ".pudding/recipes/test-m2-ctx.yaml", `name: test-m2-ctx
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "Read arch"
    context:
      - file: "docs/arch.md"
    gate: [compile]
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files": {"add.go": "package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "test-m2-ctx", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	uuid := extractCookID(stdout)
	claude := filepath.Join(dir, ".gump", "worktrees", "run-"+uuid, "CLAUDE.md")
	body := readFile(t, claude)
	if !strings.Contains(body, "MARKER_CONTEXT_TEST_12345") {
		t.Error("expected context file body")
	}
	if !strings.Contains(body, "### docs/arch.md") {
		t.Error("expected context heading with file path")
	}
}

// M2-E2E-5: codex uses AGENTS.md.
func TestM2_E2E5_AgentFileCodex(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".pudding/recipes/test-m2-codex.yaml", `name: test-m2-codex
steps:
  - name: code
    agent: codex
    output: diff
    prompt: "x"
    gate: [compile]
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files": {"add.go": "package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "test-m2-codex", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	uuid := extractCookID(stdout)
	wt := filepath.Join(dir, ".gump", "worktrees", "run-"+uuid)
	agents := filepath.Join(wt, "AGENTS.md")
	if _, err := os.Stat(agents); err != nil {
		t.Fatalf("AGENTS.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "CLAUDE.md")); err == nil {
		t.Error("should not write CLAUDE.md for codex")
	}
	body := readFile(t, agents)
	if !strings.Contains(body, "Gump") {
		t.Error("expected Gump in AGENTS.md")
	}
}
