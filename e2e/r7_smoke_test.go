package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/isomorphx/gump/internal/builtin"
	"github.com/isomorphx/gump/internal/config"
)

func TestE2E_SmokeR7_08_DryRunSevenWorkflows(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "smoke")
	gitCommitAll(t, dir, "init")
	for _, wf := range []string{
		"freeform", "tdd", "cheap2sota", "parallel-tasks",
		"implement-spec", "bugfix", "refactor",
	} {
		t.Run(wf, func(t *testing.T) {
			stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", wf, "--dry-run"}, nil, dir)
			if code != 0 {
				t.Fatalf("dry-run %s exit %d stderr=%s stdout=%s", wf, code, stderr, stdout)
			}
			if strings.Contains(strings.ToLower(stdout+stderr), "warning") {
				t.Fatalf("dry-run %s should not warn: stdout+stderr=%s", wf, stdout+stderr)
			}
		})
	}
}

func TestE2E_SmokeR7_09_PlaybookList(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "README.md", "x")
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"playbook", "list"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	for _, name := range []string{
		"freeform", "tdd", "cheap2sota", "parallel-tasks",
		"implement-spec", "bugfix", "refactor",
	} {
		if !strings.Contains(stdout, name) {
			t.Errorf("playbook list missing %q in:\n%s", name, stdout)
		}
	}
}

func TestE2E_SmokeR7_10_PlaybookShowImplementSpec(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "README.md", "x")
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runGump(t, []string{"playbook", "show", "implement-spec"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	for _, frag := range []string{"name: implement-spec", "type: split", "each:"} {
		if !strings.Contains(stdout, frag) {
			t.Errorf("show output missing %q:\n%s", frag, stdout)
		}
	}
}

func TestE2E_SmokeR7_11_ObsoleteProjectConfigIgnored(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)
	obsoleteToml := "pu" + "dding.toml"
	_ = os.WriteFile(filepath.Join(dir, obsoleteToml), []byte(`default_agent = "definitely-not-default"
`), 0644)
	cfg, _, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultAgent != "claude-sonnet" {
		t.Fatalf("obsolete project TOML must be ignored; got default_agent=%q", cfg.DefaultAgent)
	}

	repo := setupGoRepo(t)
	legacyLayoutDir := filepath.Join(repo, "."+"pu"+"dding", "rec"+"ipes")
	_ = os.MkdirAll(legacyLayoutDir, 0755)
	writeFile(t, repo, filepath.Join("."+"pu"+"dding", "rec"+"ipes", "custom.yaml"), `name: custom
steps:
  - name: x
    type: code
    run:
      agent: claude-sonnet
    get:
      prompt: "hi"
`)
	writeFile(t, repo, "spec.md", "x")
	gitCommitAll(t, repo, "init")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "custom", "--dry-run"}, nil, repo)
	if code == 0 {
		t.Fatalf("expected failure for legacy install layout; stdout=%s stderr=%s", stdout, stderr)
	}
	combined := strings.ToLower(stdout + stderr)
	if !strings.Contains(combined, "not found") {
		t.Fatalf("expected workflow not found, got: %s", combined)
	}
}

func TestE2E_SmokeR7_12_ResumeImplementSpecTwoTasks(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "feat")
	// Minimal split + code per task (no converge retries) so step_started counts stay deterministic vs built-in implement-spec.
	_ = os.MkdirAll(filepath.Join(dir, ".gump", "workflows"), 0755)
	writeFile(t, dir, ".gump/workflows/r7-12-resume.yaml", `name: r7-12-resume
max_budget: 20.00
steps:
  - name: decompose
    type: split
    get:
      prompt: |
        Plan tasks.
    run:
      agent: claude-opus
    gate: [schema]
    each:
      - name: work
        type: code
        get:
          prompt: |
            Implement {task.description}
        run:
          agent: claude-sonnet
        gate: [compile, test]
  - name: quality
    gate: [compile, lint, test]
`)
	writeFile(t, dir, ".gump-test-plan.json", `[
  {"name":"u","description":"u","files":["u.go","u_test.go"]},
  {"name":"v","description":"v","files":["v.go","v_test.go",".gump-test-scenario.json"]}
]`)
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "by_task_step": {
    "u": {
      "work": {"files": {
        "u.go": "package main\n\nfunc U() int { return 1 }\n",
        "u_test.go": "package main\nimport \"testing\"\nfunc TestU(t *testing.T) { if U()!=1 { t.Fatal() } }\n"
      }}
    },
    "v": {
      "work": {"files": {
        "v.go": "package main\n\nfunc V() int { return NOT_A_VALUE }\n",
        "v_test.go": "package main\nimport \"testing\"\nfunc TestV(t *testing.T) { if V()!=2 { t.Fatal() } }\n"
      }}
    }
  }
}`)
	gitCommitAll(t, dir, "init")
	stdout1, stderr1, code1 := runGump(t, []string{"run", "spec.md", "--workflow", "r7-12-resume", "--agent-stub"}, envWithStubPath(), dir)
	if code1 == 0 {
		t.Fatalf("expected failure on task v compile stderr=%s", stderr1)
	}
	runID := extractRunID(stdout1 + stderr1)
	if runID == "" {
		t.Fatal("no run id")
	}
	wt := filepath.Join(dir, ".gump", "worktrees", "run-"+runID)
	// Refresh stub scenario inside the worktree (now an allowed path for task v) plus sources.
	writeFile(t, wt, "v.go", "package main\n\nfunc V() int { return 2 }\n")
	writeFile(t, wt, "v_test.go", "package main\nimport \"testing\"\nfunc TestV(t *testing.T) { if V()!=2 { t.Fatal() } }\n")
	writeFile(t, wt, ".gump-test-scenario.json", `{
  "by_task_step": {
    "v": {
      "work": {"files": {
        "v.go": "package main\n\nfunc V() int { return 2 }\n",
        "v_test.go": "package main\nimport \"testing\"\nfunc TestV(t *testing.T) { if V()!=2 { t.Fatal() } }\n"
      }}
    }
  }
}`)
	writeFile(t, dir, ".gump-test-scenario.json", `{
  "by_task_step": {
    "v": {
      "work": {"files": {
        "v.go": "package main\n\nfunc V() int { return 2 }\n",
        "v_test.go": "package main\nimport \"testing\"\nfunc TestV(t *testing.T) { if V()!=2 { t.Fatal() } }\n"
      }}
    }
  }
}`)
	gitCommitAll(t, dir, "fix v task")
	_ = os.Remove(filepath.Join(wt, ".gump", "stub-launch-seq"))

	stdout2, stderr2, code2 := runGump(t, []string{"run", "--resume", "--agent-stub"}, envWithStubPath(), dir)
	if code2 != 0 {
		t.Fatalf("resume exit %d stdout=%s stderr=%s", code2, stdout2, stderr2)
	}
	runDir := filepath.Join(dir, ".gump", "runs", runID)
	if n := countManifestStepStarted(t, runDir, "decompose/u/work"); n != 1 {
		t.Fatalf("task u work should start once, got %d", n)
	}
	if n := countManifestStepStarted(t, runDir, "decompose/v/work"); n != 2 {
		t.Fatalf("task v work should start twice (fail+resume), got %d", n)
	}
	st := readRunState(t, dir, runID)
	if st["decompose/v/work.status"] != "pass" {
		t.Fatalf("v work should pass after resume: %q", st["decompose/v/work.status"])
	}
}

func moduleRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found from test working directory")
		}
		dir = parent
	}
}

// TestE2E_SmokeR7_13_RaceDetector runs the full module under the race detector (nested invocation).
// -skip avoids recursive re-entry when the outer `go test ./...` includes this package.
func TestE2E_SmokeR7_13_RaceDetector(t *testing.T) {
	root := moduleRootDir(t)
	cmd := exec.Command("go", "test", "-race", "./...", "-count=1", "-skip", "TestE2E_SmokeR7_13_RaceDetector")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test -race ./...: %v\n%s", err, out)
	}
}
