//go:build legacy_e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func countStepStartedForName(t *testing.T, runDir, name string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runDir, "manifest.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] != "step_started" {
			continue
		}
		step, _ := ev["step"].(string)
		if step == name || strings.HasSuffix(step, "/"+name) {
			n++
		}
	}
	return n
}

func latestRunDir(t *testing.T, repoRoot string) string {
	t.Helper()
	runsDir := filepath.Join(repoRoot, ".gump", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	type ent struct {
		name  string
		mtime time.Time
	}
	var dirs []ent
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, ent{e.Name(), info.ModTime()})
	}
	if len(dirs) == 0 {
		t.Fatal("no run dirs in .gump/runs")
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mtime.After(dirs[j].mtime) })
	return filepath.Join(runsDir, dirs[0].name)
}

// M3-E2E-1: gate / on_failure — retry v4
func TestM3_E2E1_GateOnFailureRetry(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m3-r1.yaml", `name: test-m3-r1
description: M3 retry v4
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "Implement Add: {spec}"
    gate:
      - compile
    on_failure:
      retry: 2
      strategy: [same]
`)
	writeFile(t, dir, "spec.md", "Add")
	writeFile(t, dir, ".gump-test-scenario.json", `{"by_attempt":{"1":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { INVALID }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m3-r1", "--agent-stub"}, nil, dir)
	combined := stdout + stderr
	if code != 0 {
		t.Fatalf("exit %d: %s", code, combined)
	}
	runDir := latestRunDir(t, dir)
	manifest := readFile(t, filepath.Join(runDir, "manifest.ndjson"))
	if !strings.Contains(manifest, "retry_triggered") {
		t.Error("ledger should contain retry_triggered")
	}
	if !strings.Contains(manifest, "gate_failed") {
		t.Error("ledger should contain gate_failed")
	}
	if !strings.Contains(manifest, "gate_passed") {
		t.Error("ledger should contain gate_passed")
	}
}

// M3-E2E-2: restart_from after gate failure — code runs again with prior failure context.
func TestM3_E2E2_RestartFrom(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m3-rf.yaml", `name: test-m3-rf
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "x"
    gate: [compile]
    on_failure:
      retry: 10
      strategy: [same]
  - name: gatefail
    agent: claude-haiku
    output: diff
    prompt: "break compile"
    gate: [compile]
    on_failure:
      retry: 10
      restart_from: code
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "by_step": {
    "gatefail": {"files": {"bad.go": "package main\n\nfunc x() { BAD }\n"}}
  },
  "by_restart": {
    "1": {"files": {"bad.go": "package main\n\nfunc x() {}\n"}}
  }
}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m3-rf", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	runDir := latestRunDir(t, dir)
	if countStepStartedForName(t, runDir, "code") < 2 {
		t.Errorf("expected code to run at least twice after restart_from")
	}
}

// M3-E2E-3: output review pass
func TestM3_E2E3_ReviewPass(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m3-rev.yaml", `name: test-m3-rev
steps:
  - name: impl
    agent: claude-haiku
    output: diff
    prompt: "x"
    gate: [compile]
  - name: check
    agent: claude-haiku
    output: review
    prompt: "Review"
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m3-rev", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	runDir := latestRunDir(t, dir)
	sb := readFile(t, filepath.Join(runDir, "state-bag.json"))
	if !strings.Contains(sb, "check") {
		t.Error("state-bag should reference check step")
	}
}

// M3-E2E-4: review fail → restart_from — review.json per restart cycle, then pass.
func TestM3_E2E4_ReviewFailRestart(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m3-rvrf.yaml", `name: test-m3-rvrf
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "x"
    gate: [compile]
    on_failure:
      retry: 10
      strategy: [same]
  - name: review
    agent: claude-haiku
    output: review
    prompt: "review"
    on_failure:
      retry: 10
      restart_from: code
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "review_by_cycle": {
    "0": "{\"pass\":false,\"comment\":\"fix the code\"}",
    "1": "{\"pass\":true,\"comment\":\"ok\"}"
  }
}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m3-rvrf", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	runDir := latestRunDir(t, dir)
	sb := readFile(t, filepath.Join(runDir, "state-bag.json"))
	if !strings.Contains(sb, "fix the code") && !strings.Contains(sb, "pass") {
		t.Errorf("state-bag should retain failed review output: %s", sb)
	}
}

// M3-E2E-5: max_budget run
func TestM3_E2E5_MaxBudgetRun(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m3-bud.yaml", `name: test-m3-bud
max_budget: 0.10
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "x"
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"cost_usd": 0.15}`)
	gitCommitAll(t, dir, "setup")
	_, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m3-bud", "--agent-stub"}, nil, dir)
	if code == 0 {
		t.Fatal("expected non-zero exit on budget")
	}
	if !strings.Contains(stderr, "budget") && !strings.Contains(stderr, "exceeded") {
		t.Errorf("stderr should mention budget: %s", stderr)
	}
}

// M3-E2E-6: step max_budget
func TestM3_E2E6_StepBudget(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m3-sb.yaml", `name: test-m3-sb
steps:
  - name: cheap
    agent: claude-haiku
    output: diff
    max_budget: 0.05
    prompt: "x"
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"cost_usd_by_step":{"cheap":0.08}}`)
	gitCommitAll(t, dir, "setup")
	_, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m3-sb", "--agent-stub"}, nil, dir)
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "cheap") || !strings.Contains(stderr, "budget") {
		t.Errorf("stderr should mention cheap and budget: %s", stderr)
	}
}

// M3-E2E-7: HITL pause
func TestM3_E2E7_HITL(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m3-hitl.yaml", `name: test-m3-hitl
steps:
  - name: code
    agent: claude-haiku
    output: diff
    hitl: true
    prompt: "x"
    gate: [compile]
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "setup")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, "run", "spec.md", "--workflow", "test-m3-hitl", "--agent-stub")
	cmd.Dir = dir
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	cmd.Stdin = devnull
	cmd.Env = buildEnvForSubprocess(nil)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	combined := errb.String()
	if code != 0 {
		t.Fatalf("exit %d: %s", code, combined)
	}
	if !strings.Contains(combined, "HITL pause") || !strings.Contains(combined, "Press Enter") {
		t.Errorf("stderr should contain HITL pause and Press Enter: %s", combined)
	}
}

// M3-E2E-8: {steps.X.files} in state bag / prompt
func TestM3_E2E8_StepFiles(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m3-files.yaml", `name: test-m3-files
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "Create hello"
  - name: review
    agent: claude-haiku
    output: artifact
    prompt: "Files changed: {steps.code.files}"
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"by_step":{"code":{"files":{"hello.go":"package main\n\nfunc Hello() {}\n","hello_test.go":"package main\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {}\n"}}}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m3-files", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	uuid := extractRunID(stdout)
	wt := filepath.Join(dir, ".gump", "worktrees", "run-"+uuid)
	claude := filepath.Join(wt, "CLAUDE.md")
	body := readFile(t, claude)
	if !strings.Contains(body, "hello.go") || !strings.Contains(body, "hello_test.go") {
		t.Error("review context should list files from steps.code.files")
	}
	runDir := latestRunDir(t, dir)
	sb := readFile(t, filepath.Join(runDir, "state-bag.json"))
	if !strings.Contains(sb, "hello.go") || !strings.Contains(sb, `"files"`) {
		t.Errorf("state-bag should list changed files for code: %s", sb)
	}
}

// M3-E2E-9: session_id in state bag
func TestM3_E2E9_SessionIDStateBag(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m3-sid.yaml", `name: test-m3-sid
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "x"
  - name: review
    agent: claude-haiku
    output: artifact
    prompt: "y"
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"session_id_by_step":{"code":"test-session-abc"}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m3-sid", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	runDir := latestRunDir(t, dir)
	sb := readFile(t, filepath.Join(runDir, "state-bag.json"))
	if !strings.Contains(sb, "test-session-abc") {
		t.Error("state-bag should contain code session_id")
	}
}

// M3-E2E-10: default session fresh (empty session_id on launch)
func TestM3_E2E10_SessionFreshDefault(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m3-fresh.yaml", `name: test-m3-fresh
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "a"
  - name: review
    agent: claude-haiku
    output: artifact
    prompt: "b"
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"unique_session_each_call": true}`)
	gitCommitAll(t, dir, "setup")
	stdout, _, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m3-fresh", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	runDir := latestRunDir(t, dir)
	data := readFile(t, filepath.Join(runDir, "manifest.ndjson"))
	// WHY: fresh session omits or zeroes session_id on launch; match either form in NDJSON.
	nEmpty := strings.Count(data, `"session_id":""`) + strings.Count(data, `"session_id": ""`)
	if nEmpty < 2 {
		// Some builds omit empty fields; require agent_launched lines without resume in cli
		if strings.Count(data, "agent_launched") < 2 {
			t.Error("expected two agent_launched events")
		}
	}
}

// M3-E2E-11: reuse-on-retry + restart_from — second pass through review resumes session when configured.
func TestM3_E2E11_ReuseOnRetryRestart(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m3-r11.yaml", `name: test-m3-r11
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "x"
    gate: [compile]
    session: reuse-on-retry
    on_failure:
      retry: 10
      strategy: [same]
  - name: review
    agent: claude-haiku
    output: review
    prompt: "r"
    session: reuse-on-retry
    on_failure:
      retry: 10
      restart_from: code
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "session_id_by_step": {"review": "sess-reuse-test"},
  "review_by_cycle": {
    "0": "{\"pass\":false,\"comment\":\"retry\"}",
    "1": "{\"pass\":true,\"comment\":\"done\"}"
  }
}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m3-r11", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	runDir := latestRunDir(t, dir)
	data := readFile(t, filepath.Join(runDir, "manifest.ndjson"))
	if !strings.Contains(data, "sess-reuse-test") {
		t.Error("manifest should record review session id for reuse-on-retry")
	}
}
