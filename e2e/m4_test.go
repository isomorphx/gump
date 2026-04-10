//go:build legacy_e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// M4-E2E-1: gate events (renamed from validation_*)
func TestM4_E2E1_GateEvents(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m4-e1.yaml", `name: test-m4-e1
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "x"
    gate: [compile]
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m4-e1", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	runDir := latestRunDir(t, dir)
	manifest := readFile(t, filepath.Join(runDir, "manifest.ndjson"))
	if !strings.Contains(manifest, "gate_started") {
		t.Error("manifest should contain gate_started")
	}
	if !strings.Contains(manifest, "gate_passed") {
		t.Error("manifest should contain gate_passed")
	}
	if strings.Contains(manifest, "validation_started") || strings.Contains(manifest, "validation_passed") {
		t.Error("manifest must not contain validation_* gate events")
	}
}

// M4-E2E-2: gate_failed
func TestM4_E2E2_GateFailed(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m4-e2.yaml", `name: test-m4-e2
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "x"
    gate: [compile]
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"by_step":{"code":{"files":{"bad.go":"package main\n\nfunc x() { SYNTAXERROR }\n"}}}}`)
	gitCommitAll(t, dir, "setup")
	_, _, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m4-e2", "--agent-stub"}, nil, dir)
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	runDir := latestRunDir(t, dir)
	manifest := readFile(t, filepath.Join(runDir, "manifest.ndjson"))
	if !strings.Contains(manifest, "gate_failed") {
		t.Error("manifest should contain gate_failed")
	}
	if strings.Contains(manifest, "validation_failed") {
		t.Error("manifest must not contain validation_failed")
	}
	var reason string
	for _, line := range strings.Split(manifest, "\n") {
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] == "gate_failed" {
			reason, _ = ev["reason"].(string)
			break
		}
	}
	if strings.TrimSpace(reason) == "" {
		t.Error("gate_failed.reason should be non-empty")
	}
}

// M4-E2E-3: HITL paused / resumed
func TestM4_E2E3_HITLEvents(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m4-e3.yaml", `name: test-m4-e3
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
	cmd := exec.CommandContext(ctx, binaryPath, "run", "spec.md", "--workflow", "test-m4-e3", "--agent-stub")
	cmd.Dir = dir
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	cmd.Stdin = devnull
	cmd.Env = buildEnvForSubprocess(nil)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	_ = cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	runDir := latestRunDir(t, dir)
	manifest := readFile(t, filepath.Join(runDir, "manifest.ndjson"))
	if !strings.Contains(manifest, "hitl_paused") {
		t.Error("manifest should contain hitl_paused")
	}
	if !strings.Contains(manifest, "hitl_resumed") || !strings.Contains(manifest, "continue") {
		t.Error("manifest should contain hitl_resumed continue")
	}
	idxPause := strings.Index(manifest, "hitl_paused")
	idxResume := strings.Index(manifest, "hitl_resumed")
	if idxPause < 0 || idxResume < 0 || idxPause > idxResume {
		t.Error("hitl_paused should appear before hitl_resumed")
	}
}

// M4-E2E-4: budget_exceeded
func TestM4_E2E4_BudgetExceeded(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m4-e4.yaml", `name: test-m4-e4
max_budget: 0.01
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "x"
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"cost_usd": 0.05}`)
	gitCommitAll(t, dir, "setup")
	_, _, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m4-e4", "--agent-stub"}, nil, dir)
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	runDir := latestRunDir(t, dir)
	manifest := readFile(t, filepath.Join(runDir, "manifest.ndjson"))
	if !strings.Contains(manifest, "budget_exceeded") {
		t.Error("manifest should contain budget_exceeded")
	}
	var types []string
	for _, line := range strings.Split(strings.TrimSpace(manifest), "\n") {
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if t, ok := ev["type"].(string); ok {
			types = append(types, t)
		}
	}
	idxB, idxC := -1, -1
	for i, typ := range types {
		if typ == "budget_exceeded" {
			idxB = i
		}
		if typ == "run_completed" {
			idxC = i
		}
	}
	if idxB < 0 || idxC < 0 || idxB > idxC {
		t.Error("budget_exceeded should appear before run_completed")
	}
	var be map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(manifest), "\n") {
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] == "budget_exceeded" {
			be = ev
			break
		}
	}
	if be == nil {
		t.Fatal("no budget_exceeded event")
	}
	if be["scope"] != "run" {
		t.Errorf("scope want run, got %v", be["scope"])
	}
	if mx, ok := be["max_usd"].(float64); !ok || mx != 0.01 {
		t.Errorf("max_usd want 0.01, got %v", be["max_usd"])
	}
	spent, ok := be["spent_usd"].(float64)
	if !ok {
		t.Fatalf("spent_usd missing or not a number: %v", be["spent_usd"])
	}
	if spent < 0.05 {
		t.Errorf("spent_usd want >= 0.05 (stub cost), got %g", spent)
	}
}

// M4-E2E-5: run_started max_budget
func TestM4_E2E5_RunStartedMaxBudget(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m4-e5.yaml", `name: test-m4-e5
max_budget: 5.00
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "x"
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m4-e5", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	runDir := latestRunDir(t, dir)
	manifest := readFile(t, filepath.Join(runDir, "manifest.ndjson"))
	if !strings.Contains(manifest, `"max_budget":5`) && !strings.Contains(manifest, `"max_budget": 5`) {
		t.Error("run_started should include max_budget 5")
	}
}

// M4-E2E-6: step_started item (foreach)
func TestM4_E2E6_StepStartedItem(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m4-e6.yaml", `name: test-m4-e6
steps:
  - name: plan
    agent: claude-haiku
    output: plan
    prompt: "x"
    gate: [compile]
  - name: impl
    foreach: plan
    steps:
      - name: build
        agent: claude-haiku
        output: diff
        prompt: "y"
        gate: [compile]
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-plan.json", `[{"name":"add-auth","description":"auth","files":["main.go"]}]`)
	writeFile(t, dir, ".gump-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m4-e6", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	runDir := latestRunDir(t, dir)
	manifest := readFile(t, filepath.Join(runDir, "manifest.ndjson"))
	if !strings.Contains(manifest, `"item":"add-auth"`) && !strings.Contains(manifest, `"item": "add-auth"`) {
		t.Error("build step_started should have item add-auth")
	}
	for _, line := range strings.Split(strings.TrimSpace(manifest), "\n") {
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] != "step_started" {
			continue
		}
		if _, has := ev["task"]; has {
			t.Error("step_started must not use legacy task field")
		}
	}
}

// M4-E2E-7: observed_at on stdout artifact
func TestM4_E2E7_StdoutObservedAt(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m4-e7.yaml", `name: test-m4-e7
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "x"
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m4-e7", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	runDir := latestRunDir(t, dir)
	artDir := filepath.Join(runDir, "artifacts")
	entries, err := os.ReadDir(artDir)
	if err != nil {
		t.Fatal(err)
	}
	var logPath string
	reName := regexp.MustCompile(`.*-stdout\.log$`)
	for _, e := range entries {
		if !e.IsDir() && reName.MatchString(e.Name()) {
			logPath = filepath.Join(artDir, e.Name())
			break
		}
	}
	if logPath == "" {
		t.Fatal("no *-stdout.log in artifacts")
	}
	data := readFile(t, logPath)
	if strings.TrimSpace(data) == "" {
		t.Fatal("stdout log empty")
	}
	lineRe := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z `)
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if line == "" {
			continue
		}
		if !lineRe.MatchString(line) {
			t.Errorf("line missing observed_at prefix: %s", line)
			continue
		}
		rest := strings.TrimSpace(lineRe.ReplaceAllString(line, ""))
		if !strings.HasPrefix(rest, "{") {
			t.Errorf("after timestamp, expected JSON: %s", rest)
			continue
		}
		var v interface{}
		if json.Unmarshal([]byte(rest), &v) != nil {
			t.Errorf("rest not valid JSON: %s", rest)
		}
	}
}

// M4-E2E-8: session_mode on step_started
func TestM4_E2E8_SessionMode(t *testing.T) {
	t.Run("reuse", func(t *testing.T) {
		dir := setupGoRepo(t)
		writeFile(t, dir, ".gump/workflows/test-m4-e8a.yaml", `name: test-m4-e8a
steps:
  - name: code
    agent: claude-haiku
    session: reuse
    output: diff
    prompt: "x"
`)
		writeFile(t, dir, "spec.md", "x")
		writeFile(t, dir, ".gump-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
		gitCommitAll(t, dir, "setup")
		stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m4-e8a", "--agent-stub"}, nil, dir)
		if code != 0 {
			t.Fatalf("exit %d: %s %s", code, stdout, stderr)
		}
		runDir := latestRunDir(t, dir)
		manifest := readFile(t, filepath.Join(runDir, "manifest.ndjson"))
		if !strings.Contains(manifest, `"session_mode":"reuse"`) && !strings.Contains(manifest, `"session_mode": "reuse"`) {
			t.Error("step_started should have session_mode reuse")
		}
	})
	t.Run("default_fresh", func(t *testing.T) {
		dir := setupGoRepo(t)
		writeFile(t, dir, ".gump/workflows/test-m4-e8b.yaml", `name: test-m4-e8b
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "x"
`)
		writeFile(t, dir, "spec.md", "x")
		writeFile(t, dir, ".gump-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
		gitCommitAll(t, dir, "setup")
		stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m4-e8b", "--agent-stub"}, nil, dir)
		if code != 0 {
			t.Fatalf("exit %d: %s %s", code, stdout, stderr)
		}
		runDir := latestRunDir(t, dir)
		manifest := readFile(t, filepath.Join(runDir, "manifest.ndjson"))
		if !strings.Contains(manifest, `"session_mode":"fresh"`) && !strings.Contains(manifest, `"session_mode": "fresh"`) {
			t.Error("step_started should have session_mode fresh")
		}
	})
}

// M4-E2E-9: session_id in state bag + ledger
func TestM4_E2E9_SessionIDKey(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-m4-e9.yaml", `name: test-m4-e9
steps:
  - name: code
    agent: claude-haiku
    output: diff
    prompt: "x"
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"session_id_by_step":{"code":"sid-m4-test"},"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-m4-e9", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	runDir := latestRunDir(t, dir)
	sb := readFile(t, filepath.Join(runDir, "state-bag.json"))
	if !strings.Contains(sb, "session_id") {
		t.Error("state-bag should persist session_id for the step")
	}
	if !strings.Contains(sb, "sid-m4-test") {
		t.Error("state-bag should contain session id value")
	}
	manifest := readFile(t, filepath.Join(runDir, "manifest.ndjson"))
	found := false
	for _, line := range strings.Split(strings.TrimSpace(manifest), "\n") {
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] != "state_bag_updated" {
			continue
		}
		k, _ := ev["key"].(string)
		if strings.HasSuffix(k, ".session_id") {
			found = true
			break
		}
	}
	if !found {
		t.Error("manifest should contain state_bag_updated for .session_id")
	}
}
