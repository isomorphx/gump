//go:build legacy_e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestG2WorkflowStandaloneWithInputs(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/sub.yaml", `name: sub
inputs:
  msg:
    required: true
steps:
  - name: echo
    agent: codex
    output: artifact
    prompt: "{msg}"
`)
	writeFile(t, dir, ".gump/workflows/parent.yaml", `name: parent
steps:
  - name: call-sub
    workflow: sub
    with:
      msg: hello
`)
	writeFile(t, dir, "spec.md", "Spec")
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "parent", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	uuid := extractRunID(stdout)
	sb := readFile(t, filepath.Join(dir, ".gump", "runs", uuid, "state-bag.json"))
	if !strings.Contains(sb, "call-sub.steps.echo") {
		t.Fatalf("state bag should contain grafted child key, got: %s", sb)
	}
}

func TestG2WorkflowInputRequiredMissing(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/sub.yaml", `name: sub
inputs:
  diff:
    required: true
steps:
  - name: echo
    agent: codex
    prompt: "{diff}"
`)
	writeFile(t, dir, ".gump/workflows/parent.yaml", `name: parent
steps:
  - name: call-sub
    workflow: sub
`)
	writeFile(t, dir, "spec.md", "Spec")
	gitCommitAll(t, dir, "setup")
	_, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "parent", "--agent-stub"}, nil, dir)
	if code == 0 {
		t.Fatal("expected failure when required input is missing")
	}
	if !strings.Contains(stderr, "requires input") {
		t.Fatalf("stderr should mention missing input, got: %s", stderr)
	}
}

func TestG2GuardMaxTurns(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/w.yaml", `name: w
steps:
  - name: limited
    agent: codex
    prompt: test
    guard:
      max_turns: 1
`)
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "stdout_extra_json_lines": [
    "{\"type\":\"action\",\"name\":\"write\",\"input\":{\"path\":\".gump/out/plan.json\"}}",
    "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"turn1\"}]}}",
    "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"chunk\"}]}}",
    "{\"type\":\"action\",\"name\":\"write\",\"input\":{\"path\":\".gump/out/plan.json\"}}",
    "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"turn2\"}]}}"
  ]
}`)
	writeFile(t, dir, "spec.md", "Spec")
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "w", "--agent-stub"}, nil, dir)
	if code == 0 {
		t.Fatal("expected guard failure")
	}
	uuid := extractRunID(stdout)
	manifest := readFile(t, filepath.Join(dir, ".gump", "runs", uuid, "manifest.ndjson"))
	if !strings.Contains(manifest, `"type":"guard_triggered"`) || !strings.Contains(manifest, `"guard":"max_turns"`) {
		t.Fatalf("manifest should contain max_turns guard event\nstderr=%s\nmanifest=%s", stderr, manifest)
	}
}

func TestG2StateBagHierarchicalReference(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/sub.yaml", `name: sub
steps:
  - name: echo
    agent: codex
    output: artifact
    prompt: hello
`)
	writeFile(t, dir, ".gump/workflows/parent.yaml", `name: parent
steps:
  - name: call-sub
    workflow: sub
  - name: final
    agent: codex
    output: artifact
    prompt: "{steps.call-sub.steps.echo.output}"
`)
	writeFile(t, dir, "spec.md", "Spec")
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "parent", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

func TestG2ForeachWorkflowWithInputsAndItemInheritance(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/sub.yaml", `name: sub
inputs:
  msg:
    required: true
steps:
  - name: echo
    agent: codex
    output: artifact
    prompt: "{msg}::{item.description}"
`)
	writeFile(t, dir, ".gump/workflows/parent.yaml", `name: parent
steps:
  - name: plan
    agent: codex
    output: plan
    prompt: plan
  - name: impl
    foreach: plan
    parallel: true
    workflow: sub
    with:
      msg: hello
`)
	writeFile(t, dir, ".gump-test-plan.json", `[{"name":"t1","description":"desc-1","files":["a.go"]}]`)
	writeFile(t, dir, "spec.md", "Spec")
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "parent", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

func TestG2WorkflowKeywordAliasDeprecatedWarning(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/sub.yaml", `name: sub
steps:
  - name: echo
    agent: codex
    prompt: ok
`)
	writeFile(t, dir, ".gump/workflows/parent.yaml", `name: parent
steps:
  - name: plan
    agent: codex
    output: plan
    prompt: plan
  - name: impl
    foreach: plan
    `+"rec"+"ipe"+`: sub
`)
	writeFile(t, dir, ".gump-test-plan.json", `[{"name":"t1","description":"d"}]`)
	writeFile(t, dir, "spec.md", "Spec")
	gitCommitAll(t, dir, "setup")
	_, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "parent", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("expected success with deprecated alias, stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "deprecated") {
		t.Fatalf("stderr should contain deprecation warning, got: %s", stderr)
	}
}

func TestG2StandaloneWorkflowStepsIsolation(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/sub.yaml", `name: sub
steps:
  - name: echo
    agent: codex
    output: artifact
    prompt: "{steps.first.output}"
`)
	writeFile(t, dir, ".gump/workflows/parent.yaml", `name: parent
steps:
  - name: first
    agent: codex
    output: artifact
    prompt: hello
  - name: call-sub
    workflow: sub
`)
	writeFile(t, dir, "spec.md", "Spec")
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "parent", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

func TestG2GuardRetryThenPass(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/w.yaml", `name: w
steps:
  - name: guarded
    agent: codex
    prompt: x
    guard:
      max_turns: 1
    on_failure:
      retry: 2
      strategy: [same]
`)
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "stdout_extra_json_lines_by_attempt": {
    "1": [
      "{\"type\":\"action\",\"name\":\"write\",\"input\":{\"path\":\".gump/out/plan.json\"}}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"turn1\"}]}}",
      "{\"type\":\"action\",\"name\":\"write\",\"input\":{\"path\":\".gump/out/plan.json\"}}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"turn2\"}]}}"
    ],
    "2": [
      "{\"type\":\"action\",\"name\":\"write\",\"input\":{\"path\":\".gump/out/plan.json\"}}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"turn1\"}]}}"
    ]
  }
}`)
	writeFile(t, dir, "spec.md", "Spec")
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "w", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("expected success with retry, stdout=%s stderr=%s", stdout, stderr)
	}
	uuid := extractRunID(stdout)
	manifest := readFile(t, filepath.Join(dir, ".gump", "runs", uuid, "manifest.ndjson"))
	for _, needle := range []string{`"type":"guard_triggered"`, `"type":"retry_triggered"`, `"type":"step_completed"`, `"status":"pass"`} {
		if !strings.Contains(manifest, needle) {
			t.Fatalf("manifest missing %s\n%s", needle, manifest)
		}
	}
}

func TestG2GuardNoWriteOverrideFalse(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/w.yaml", `name: w
steps:
  - name: plan
    agent: codex
    output: plan
    prompt: test
    guard:
      no_write: false
`)
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "stdout_extra_json_lines": [
    "{\"type\":\"action\",\"name\":\"write\",\"input\":{\"path\":\"main.go\"}}"
  ]
}`)
	writeFile(t, dir, "spec.md", "Spec")
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "w", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("expected success with no_write override, stdout=%s stderr=%s", stdout, stderr)
	}
	uuid := extractRunID(stdout)
	manifest := readFile(t, filepath.Join(dir, ".gump", "runs", uuid, "manifest.ndjson"))
	if strings.Contains(manifest, `"type":"guard_triggered"`) {
		t.Fatalf("guard should not trigger with override false: %s", manifest)
	}
}

func TestG2E2E13NonRegression(t *testing.T) {
	if os.Getenv("G2_NONREG_INNER") == "1" {
		t.Skip("inner non-regression run")
	}
	modRoot := findModuleRoot()
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = modRoot
	cmd.Env = append(os.Environ(), "G2_NONREG_INNER=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("non regression failed: %v\n%s", err, string(out))
	}
}

func TestG2DryRunShowsWorkflowAndGuard(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/w.yaml", `name: w
steps:
  - name: call-sub
    workflow: sub
  - name: do
    agent: codex
    prompt: ok
    guard:
      max_turns: 2
`)
	writeFile(t, dir, "spec.md", "Spec")
	gitCommitAll(t, dir, "setup")
	stdout, _, code := runGump(t, []string{"run", "spec.md", "--workflow", "w", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("dry-run failed: %s", stdout)
	}
	if !strings.Contains(stdout, "workflow=sub") || !strings.Contains(stdout, "guard:") {
		t.Fatalf("dry-run should print workflow and guard, got: %s", stdout)
	}
}

func TestG2GuardNoWriteImplicitForPlan(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/w.yaml", `name: w
steps:
  - name: plan
    agent: codex
    output: plan
    prompt: test
`)
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "stdout_extra_json_lines": [
    "{\"type\":\"action\",\"name\":\"write\",\"input\":{\"path\":\"main.go\"}}"
  ]
}`)
	writeFile(t, dir, "spec.md", "Spec")
	gitCommitAll(t, dir, "setup")
	stdout, _, code := runGump(t, []string{"run", "spec.md", "--workflow", "w", "--agent-stub"}, nil, dir)
	if code == 0 {
		t.Fatal("expected implicit no_write guard failure")
	}
	uuid := extractRunID(stdout)
	manifestPath := filepath.Join(dir, ".gump", "runs", uuid, "manifest.ndjson")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"guard":"no_write"`) {
		t.Fatalf("expected no_write guard event, got: %s", string(data))
	}
}
