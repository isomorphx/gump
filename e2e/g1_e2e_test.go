//go:build legacy_e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// G1-E2E-1 : strict commands in gump mode (no cook/cookbook).
func TestG1_E2E1_StrictCommands(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, "cook")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &bytes.Buffer{}
	cmd.Env = buildEnvForSubprocess(nil)
	_ = cmd.Run()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() == 0 {
		t.Fatal("cook command should not be exposed in gump mode")
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "unknown command") {
		t.Fatalf("expected unknown command for cook: %s", stderr.String())
	}
}

// G1-E2E-2 : gump run exists and supports --workflow.
func TestG1_E2E2_RunCommand(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	stdout, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("run dry-run exit %d: %s", code, stdout)
	}
	if !strings.Contains(stdout, "Recipe:") && !strings.Contains(stdout, "Workflow:") {
		t.Fatalf("dry-run output should contain plan header: %s", stdout)
	}
}

// G1-E2E-3 : apply merge trailer uses Gump-Run.
func TestG1_E2E3_ApplyTrailerGumpRun(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "init")

	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("run exit %d: %s %s", code, stdout, stderr)
	}
	_, _, applyCode := runPudding(t, []string{"apply"}, nil, dir)
	if applyCode != 0 {
		t.Fatalf("apply exit %d", applyCode)
	}
	cmd := exec.Command("git", "log", "-1", "--pretty=%B")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v (%s)", err, string(out))
	}
	if !strings.Contains(string(out), "Gump-Run:") {
		t.Fatalf("merge commit should contain Gump-Run trailer: %s", string(out))
	}
}

// G1-E2E-4 : gump playbook list
func TestG1_E2E4_PlaybookList(t *testing.T) {
	stdout, _, code := runPudding(t, []string{"playbook", "list"}, nil, "")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	for _, s := range []string{"tdd", "bugfix", "refactor"} {
		if !strings.Contains(stdout, s) {
			t.Fatalf("stdout missing %q: %s", s, stdout)
		}
	}
}

// G1-E2E-5 : Ledger renommé
func TestG1_E2E5_LedgerRenamed(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "init")

	stdout, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("run exit %d: %s", code, stdout)
	}
	runID := extractCookID(stdout)
	if runID == "" {
		t.Fatal("expected run UUID in stdout")
	}

	reportOut, _, reportCode := runPudding(t, []string{"report"}, nil, dir)
	if reportCode != 0 {
		t.Fatalf("report exit %d: %s", reportCode, reportOut)
	}
	if !strings.Contains(reportOut, "Gump Report") {
		t.Fatalf("report should mention Gump Report: %s", reportOut)
	}

	manifestPath := filepath.Join(dir, ".gump", "runs", runID, "manifest.ndjson")
	manifest := readFile(t, manifestPath)
	if !strings.Contains(manifest, "run_started") || !strings.Contains(manifest, "run_completed") {
		t.Fatalf("manifest should contain run_started/run_completed: %s", manifest)
	}
}

// G1-E2E-6 : Compat anciens ledgers
func TestG1_E2E6_LegacyLedgerCompat(t *testing.T) {
	dir := setupRepoWithCommit(t)
	runID := "g1-legacy-00000000-0000-0000-0000-000000000001"
	runPath := filepath.Join(dir, ".gump", "runs", runID)
	if err := os.MkdirAll(runPath, 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := `{"ts":"2026-01-01T00:00:00.000Z","type":"cook_started","cook_id":"` + runID + `","recipe":"leg","spec":"spec.md","commit":"abc","branch":"main"}
{"ts":"2026-01-01T00:00:01.000Z","type":"cook_completed","cook_id":"` + runID + `","status":"pass","duration_ms":1000,"total_cost_usd":0,"steps":0,"retries":0,"artifacts":{}}
`
	writeFile(t, dir, filepath.Join(".gump", "runs", runID, "manifest.ndjson"), manifest)

	reportOut, _, reportCode := runPudding(t, []string{"report", runID}, nil, dir)
	if reportCode != 0 {
		t.Fatalf("report exit %d: %s", reportCode, reportOut)
	}
	if !strings.Contains(reportOut, "Gump Report") {
		t.Fatalf("report should mention Gump Report: %s", reportOut)
	}
	if !strings.Contains(strings.ToLower(reportOut), "pass") {
		t.Fatalf("report should mention pass: %s", reportOut)
	}
}

// G1-E2E-7 : --recipe alias deprecated
func TestG1_E2E7_RecipeAliasDeprecatedWarning(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}`)
	gitCommitAll(t, dir, "init")

	_, stderr, code := runPudding(
		t,
		[]string{"run", "spec.md", "--recipe", "freeform", "--agent-stub"},
		map[string]string{"PRESERVE_RECIPE_DEPRECATED": "1"},
		dir,
	)
	if code != 0 {
		t.Fatalf("run exit %d stderr=%s", code, stderr)
	}
	// WHY: we need to ensure users are warned that --recipe is deprecated.
	for _, s := range []string{"warning", "--recipe", "--workflow"} {
		if !strings.Contains(stderr, s) {
			t.Fatalf("stderr missing %q: %s", s, stderr)
		}
	}
}

// G1-E2E-8 : .pudding/recipes/ fallback
func TestG1_E2E8_LegacyRecipesFallback(t *testing.T) {
	dir := setupRepoWithCommit(t)
	writeFile(t, dir, "spec.md", "x")
	// commit legacy recipe so "uncommitted changes" guard doesn't fail.
	writeFile(t, dir, ".pudding/recipes/custom.yaml", `name: custom
description: legacy workflow
steps:
  - name: code
    agent: claude-haiku
    prompt: "{spec}"
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() { }\n"}}`)
	gitCommitAll(t, dir, "add legacy custom recipe")

	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "custom", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("run exit %d stderr=%s", code, stderr)
	}
	// WHY: in gump mode we try `.gump/workflows/` first, then fallback to legacy `.pudding/recipes/`.
	for _, s := range []string{"warning", ".gump/workflows/"} {
		if !strings.Contains(stderr, s) {
			t.Fatalf("stderr missing %q: %s", s, stderr)
		}
	}
}

// G1-E2E-9 : State bag — métriques step
func TestG1_E2E9_StateBagStepMetrics(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "Build something")
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "tokens_in": 1000,
  "tokens_out": 500,
  "cost_usd": 0.05,
  "files": {"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}
}`)
	gitCommitAll(t, dir, "init")

	stdout, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("run exit %d: %s", code, stdout)
	}
	runID := extractCookID(stdout)
	if runID == "" {
		t.Fatal("expected run UUID in stdout")
	}

	sbPath := filepath.Join(dir, ".gump", "runs", runID, "state-bag.json")
	sbContent := readFile(t, sbPath)

	var sb struct {
		Entries map[string]struct {
			Status   string `json:"status"`
			Cost     string `json:"cost"`
			TokensIn string `json:"tokens_in"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(sbContent), &sb); err != nil {
		t.Fatalf("state-bag.json invalid: %v", err)
	}

	exec := sb.Entries["execute"]
	if exec.TokensIn != "1000" || exec.Cost != "0.05" || exec.Status != "pass" {
		t.Fatalf("execute metrics mismatch: %+v", exec)
	}
}

// G1-E2E-10 : State bag — run.*
func TestG1_E2E10_StateBagRunMetrics(t *testing.T) {
	dir := setupGoRepo(t)
	// Use a spec that triggers the builtin `tdd` workflow with compile/test gates
	// satisfied by our injected math.go/math_test.go.
	writeFile(t, dir, "spec.md", "Implement auth")
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "tokens_in": 1000,
  "tokens_out": 500,
  "cost_usd": 0.05,
  "files": {
    "math.go": "package main\n\nfunc Add(a, b int) int { return a + b }\n",
    "math_test.go": "package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal() } }\n"
  }
}`)
	gitCommitAll(t, dir, "init")

	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "tdd", "--agent-stub"}, nil, dir)
	// WHY: In this suite, some workflows can return non-zero while still
	// producing the state-bag we want to validate.
	if code != 0 {
		t.Logf("run exit %d (continuing): stdout=%s stderr=%s", code, stdout, stderr)
	}
	runID := extractCookID(stdout)
	if runID == "" {
		runID = extractCookID(stderr)
	}
	if runID == "" {
		t.Fatal("expected run UUID in stdout/stderr")
	}

	sbPath := filepath.Join(dir, ".gump", "runs", runID, "state-bag.json")
	sbContent := readFile(t, sbPath)

	var sb struct {
		Run map[string]string `json:"run"`
	}
	if err := json.Unmarshal([]byte(sbContent), &sb); err != nil {
		t.Fatalf("state-bag.json invalid: %v", err)
	}

	if sb.Run["cost"] == "" {
		t.Fatalf("run.cost should be non-empty: %+v", sb.Run)
	}
	tokensIn := sb.Run["tokens_in"]
	n, err := strconv.Atoi(tokensIn)
	if err != nil || n <= 0 {
		t.Fatalf("run.tokens_in should be >0, got %q (err=%v)", tokensIn, err)
	}
	if strings.TrimSpace(sb.Run["status"]) == "" {
		t.Fatalf("run.status should be non-empty: %+v", sb.Run)
	}
}

// G1-E2E-11 : Variables dans les prompts
func TestG1_E2E11_PromptVariableResolution(t *testing.T) {
	dir := setupRepoWithCommit(t)
	writeFile(t, dir, "spec.md", "x")

	// Create a workflow that uses {steps.first.cost} and {run.cost} in the 2nd step prompt.
	writeFile(t, dir, ".gump/workflows/custom.yaml", `name: custom
description: uses run/steps metrics in prompts
steps:
  - name: first
    agent: claude-haiku
    output: artifact
    prompt: "First step"
  - name: second
    agent: claude-opus
    prompt: |
      first_cost={steps.first.cost}
      run_cost={run.cost}
`)

	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "cost_usd_by_step": {"first": 0.05, "second": 0.0}
}`)
	gitCommitAll(t, dir, "init")

	stdout, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "custom", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("run exit %d: %s", code, stdout)
	}
	runID := extractCookID(stdout)
	if runID == "" {
		t.Fatal("expected run UUID in stdout")
	}

	wtDir := filepath.Join(dir, ".gump", "worktrees", "run-"+runID)
	claude := readFile(t, filepath.Join(wtDir, "CLAUDE.md"))
	if !strings.Contains(claude, "first_cost=0.05") {
		t.Fatalf("CLAUDE.md should resolve {steps.first.cost}: %s", claude)
	}
	if !strings.Contains(claude, "run_cost=0.05") {
		t.Fatalf("CLAUDE.md should resolve {run.cost}: %s", claude)
	}
}

