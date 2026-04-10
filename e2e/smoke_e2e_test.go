package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	stdout, _, code := runGump(t, []string{"--version"}, nil, "")
	if code != 0 {
		t.Errorf("exit code %d", code)
	}
	// WHY: Without ldflags, we expect the "dev" build format defined by internal/version.
	if !strings.Contains(stdout, "dev") || !strings.Contains(stdout, "gump") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestRunRequiresArgs(t *testing.T) {
	_, stderr, code := runGump(t, []string{"run"}, nil, "")
	if code == 0 {
		t.Error("expected non-zero exit")
	}
	if !strings.Contains(stderr, "requires") && !strings.Contains(stderr, "Usage") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestRunSpecNotFound(t *testing.T) {
	dir := setupRepo(t)
	_, stderr, code := runGump(t, []string{"run", "nonexistent.md", "--workflow", "tdd"}, nil, dir)
	if code == 0 {
		t.Error("expected non-zero exit")
	}
	if !strings.Contains(stderr, "nonexistent") {
		t.Errorf("stderr %q", stderr)
	}
	if !strings.Contains(stderr, "not found") && !strings.Contains(stderr, "no such file") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestRunWorkflowNotFound(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, "spec.md", "hello")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "doesnotexist"}, nil, dir)
	_ = stdout
	if code == 0 {
		t.Error("expected non-zero exit")
	}
	if !strings.Contains(stderr, "doesnotexist") && !strings.Contains(stdout, "doesnotexist") {
		t.Errorf("stderr %q stdout %q", stderr, stdout)
	}
	if !strings.Contains(stderr, "not found") && !strings.Contains(stdout, "not found") {
		t.Errorf("stderr %q stdout %q", stderr, stdout)
	}
}

func TestRunDryRunTDD(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, "spec.md", "Implement a hello world function")
	stdout, _, code := runGump(t, []string{"run", "spec.md", "--workflow", "tdd", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	for _, s := range []string{"Budget:", "$8.00", "gate=[schema]", "type=split", "each:"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("stdout missing %q: %s", s, stdout)
		}
	}
}

func TestRunDryRunFreeform(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, "spec.md", "x")
	stdout, _, code := runGump(t, []string{"run", "spec.md", "--workflow", "freeform", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	for _, s := range []string{"freeform", "execute", "claude-opus"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("stdout missing %q: %s", s, stdout)
		}
	}
}

func TestPlaybookList(t *testing.T) {
	stdout, _, code := runGump(t, []string{"playbook", "list"}, nil, "")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	for _, s := range []string{"tdd", "bugfix", "refactor", "cheap2sota", "parallel-tasks", "implement-spec", "freeform"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("stdout missing %q: %s", s, stdout)
		}
	}
}

func TestPlaybookShowTDD(t *testing.T) {
	stdout, _, code := runGump(t, []string{"playbook", "show", "tdd"}, nil, "")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	if !strings.Contains(stdout, "name: tdd") {
		t.Errorf("stdout %q", stdout)
	}
	if !strings.Contains(stdout, "type: split") {
		t.Errorf("stdout should contain type: split: %q", stdout)
	}
	if !strings.Contains(stdout, "quality") {
		t.Errorf("stdout should contain quality: %q", stdout)
	}
	if !strings.Contains(stdout, "type: code") {
		t.Errorf("stdout should contain type: code: %q", stdout)
	}
	if !strings.Contains(stdout, "claude-opus") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestPlaybookProjectOverridesBuiltin(t *testing.T) {
	dir := setupRepo(t)
	os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/tdd.yaml", `name: tdd
steps:
  - name: custom-step
    type: code
    agent: custom-agent
    prompt: "custom"
    gate: [compile]
`)
	writeFile(t, dir, "spec.md", "x")
	stdout, _, code := runGump(t, []string{"run", "spec.md", "--workflow", "tdd", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	if !strings.Contains(stdout, "custom-step") || !strings.Contains(stdout, "custom-agent") {
		t.Errorf("stdout %q", stdout)
	}
	if strings.Contains(stdout, "claude-opus") {
		t.Errorf("built-in should be overridden, stdout %q", stdout)
	}
}

func TestConfigProject(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, "gump.toml", `default_agent = "my-agent"
[validation]
compile_cmd = "make build"
`)
	stdout, _, code := runGump(t, []string{"config"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	if !strings.Contains(stdout, "my-agent") || !strings.Contains(stdout, "make build") || !strings.Contains(stdout, "gump.toml") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestConfigEnvOverridesFile(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, "gump.toml", `default_agent = "file-agent"`)
	stdout, _, code := runGump(t, []string{"config"}, map[string]string{"GUMP_DEFAULT_AGENT": "env-agent"}, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	if !strings.Contains(stdout, "env-agent") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestRunInvalidWorkflowYAML(t *testing.T) {
	dir := setupRepo(t)
	_ = os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/bad.yaml", `name: bad
steps:
  - name: invalid
    type: code
    agent: claude
    prompt: "x"
    steps:
      - name: sub
        type: code
        agent: claude
        prompt: "x"
`)
	writeFile(t, dir, "spec.md", "x")
	_, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "bad"}, nil, dir)
	if code == 0 {
		t.Error("expected non-zero exit")
	}
	if !strings.Contains(stderr, "has both 'agent' and 'steps'") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestPlaybookListIncludesProjectWorkflows(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, ".gump/workflows/custom.yaml", `name: custom
description: My custom workflow
steps:
  - name: s
    type: code
    agent: a
    prompt: p
review: []
`)
	stdout, _, code := runGump(t, []string{"playbook", "list"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	if !strings.Contains(stdout, "custom") || !strings.Contains(stdout, "My custom workflow") {
		t.Errorf("stdout %q", stdout)
	}
	if !strings.Contains(stdout, "tdd") || !strings.Contains(stdout, "bugfix") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestRunStrategyShorthandParsed(t *testing.T) {
	dir := setupRepo(t)
	writeFile(t, dir, ".gump/workflows/shorthand.yaml", `name: shorthand
steps:
  - name: do
    type: code
    agent: claude
    prompt: test
    retry:
      - exit: 5
`)
	writeFile(t, dir, "spec.md", "x")
	stdout, _, code := runGump(t, []string{"run", "spec.md", "--workflow", "shorthand", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stdout)
	}
	if !strings.Contains(stdout, "retry: exit:5") {
		t.Errorf("stdout %q", stdout)
	}
}
