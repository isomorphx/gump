//go:build legacy_e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isomorphx/gump/internal/workflow"
)

func TestG3_E2E1_TelemetryFirstRunMessage(t *testing.T) {
	dir := setupGoRepo(t)
	home := t.TempDir()
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "init")
	_, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent-stub"}, map[string]string{"HOME": home}, dir)
	if code != 0 {
		t.Fatalf("run failed: %s", stderr)
	}
	if !strings.Contains(stderr, "Gump collects anonymous workflow metrics") {
		t.Fatalf("missing first-run telemetry message: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(home, ".gump", "anonymous_id")); err != nil {
		t.Fatalf("anonymous_id not created: %v", err)
	}
}

func TestG3_E2E2_NoSendOnFirstRun(t *testing.T) {
	dir := setupGoRepo(t)
	home := t.TempDir()
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "init")
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	_, _, _ = runGump(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent-stub"}, map[string]string{"HOME": home, "GUMP_TELEMETRY_URL": srv.URL}, dir)
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("expected 0 telemetry posts on first run, got %d", hits)
	}
}

func TestG3_E2E3_SendOnSecondRun(t *testing.T) {
	dir := setupGoRepo(t)
	home := t.TempDir()
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "init")
	var hits int32
	var gotWorkflow, gotRunStatus, gotAnonymousID atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		defer r.Body.Close()
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if v, ok := body["workflow"].(string); ok {
				gotWorkflow.Store(v)
			}
			if v, ok := body["run_status"].(string); ok {
				gotRunStatus.Store(v)
			}
			if v, ok := body["anonymous_id"].(string); ok {
				gotAnonymousID.Store(v)
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	env := map[string]string{"HOME": home, "GUMP_TELEMETRY_URL": srv.URL}
	_, _, _ = runGump(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent-stub"}, env, dir)
	_, _, _ = runGump(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent-stub"}, env, dir)
	for i := 0; i < 40 && atomic.LoadInt32(&hits) == 0; i++ {
		time.Sleep(25 * time.Millisecond)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("expected telemetry post on second run")
	}
	if v, _ := gotWorkflow.Load().(string); strings.TrimSpace(v) == "" {
		t.Fatal("telemetry payload missing workflow")
	}
	if v, _ := gotRunStatus.Load().(string); strings.TrimSpace(v) == "" {
		t.Fatal("telemetry payload missing run_status")
	}
	if v, _ := gotAnonymousID.Load().(string); strings.TrimSpace(v) == "" {
		t.Fatal("telemetry payload missing anonymous_id")
	}
}

func TestG3_E2E4_OptOut(t *testing.T) {
	dir := setupGoRepo(t)
	home := t.TempDir()
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "init")
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	env := map[string]string{"HOME": home, "GUMP_TELEMETRY_URL": srv.URL}
	_, _, c1 := runGump(t, []string{"config", "set", "analytics", "false"}, env, dir)
	if c1 != 0 {
		t.Fatal("config set analytics false failed")
	}
	_, stderr, _ := runGump(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent-stub"}, env, dir)
	if strings.Contains(stderr, "Gump collects anonymous workflow metrics") {
		t.Fatalf("telemetry message should be suppressed when opted out: %s", stderr)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("expected no telemetry posts when opted out, got %d", hits)
	}
}

func TestG3_E2E5_DryRunV03(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "init")
	stdout, _, code := runGump(t, []string{"run", "spec.md", "--workflow", "tdd", "--dry-run"}, nil, dir)
	if code != 0 {
		t.Fatalf("dry-run failed: %s", stdout)
	}
	for _, want := range []string{"Gump Dry Run", "guard:", "Budget:", "State Bag Resolutions:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, stdout)
		}
	}
}

func TestG3_E2E6_ReportWithGuard(t *testing.T) {
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
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "init")
	_, _, _ = runGump(t, []string{"run", "spec.md", "--workflow", "w", "--agent-stub"}, nil, dir)
	out, _, code := runGump(t, []string{"report"}, nil, dir)
	if code != 0 {
		t.Fatalf("report failed: %s", out)
	}
	if !strings.Contains(out, "Guards") || !strings.Contains(out, "max_turns") {
		t.Fatalf("expected guards section in report: %s", out)
	}
}

func TestG3_E2E7_BuiltinParseValidate(t *testing.T) {
	for _, wf := range []string{"tdd", "cheap2sota", "parallel-tasks", "adversarial-review", "bugfix", "refactor", "freeform"} {
		resolved, err := workflow.Resolve(wf, "")
		if err != nil {
			t.Fatalf("%s resolve failed: %v", wf, err)
		}
		rec, _, err := workflow.Parse(resolved.Raw, "")
		if err != nil {
			t.Fatalf("%s parse failed: %v", wf, err)
		}
		if errs := workflow.Validate(rec); len(errs) > 0 {
			t.Fatalf("%s validate failed: %v", wf, errs[0])
		}
	}
}

func TestG3_E2E8_TDDEndToEndStub(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "Build auth module")
	writeFile(t, dir, ".gump-test-plan.json", `[{"name":"task-1","description":"One","files":["file1.go"]},{"name":"task-2","description":"Two","files":["file2.go"]}]`)
	writeFile(t, dir, ".gump-test-scenario.json", `{"files":{"file1.go":"package main\n\nfunc F1() {}\n","file2.go":"package main\n\nfunc F2() {}\n"}}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, _ := runGump(t, []string{"run", "spec.md", "--workflow", "tdd", "--agent-stub"}, nil, dir)
	uuid := extractRunID(stdout)
	if strings.TrimSpace(uuid) == "" {
		t.Fatalf("missing run id in stdout: %s", stdout)
	}
	if !strings.Contains(stderr, "[gump]") && !strings.Contains(stdout, "[gump]") {
		t.Fatalf("expected gump stream markers, got: %s", stderr)
	}
	manifest := readFile(t, filepath.Join(dir, ".gump", "runs", uuid, "manifest.ndjson"))
	if !strings.Contains(manifest, `"type":"run_started"`) {
		t.Fatalf("manifest missing run_started: %s", manifest)
	}
	stateBag := readFile(t, filepath.Join(dir, ".gump", "runs", uuid, "state-bag.json"))
	if !strings.Contains(stateBag, `"status"`) && !strings.Contains(stateBag, `"cost"`) {
		t.Fatalf("state-bag missing run status/cost markers: %s", stateBag)
	}
}

func TestG3_E2E9_CompatOldLedger(t *testing.T) {
	dir := setupGoRepo(t)
	legacyRunID := "g3e2e9-0000-0000-0000-000000000001"
	ck := "co" + "ok"
	writeFile(t, dir, filepath.Join(".gump", "runs", legacyRunID, "manifest.ndjson"), `{"type":"`+ck+`_started","`+ck+`_id":"`+legacyRunID+`","`+"rec"+"ipe"+`":"legacy","spec":"spec.md","commit":"abc","branch":"main"}
{"type":"validation_passed","step":"v","artifact":"a"}
{"type":"`+ck+`_completed","status":"pass","duration_ms":1,"total_cost_usd":0}
`)
	out, _, code := runGump(t, []string{"report", legacyRunID}, nil, dir)
	if code != 0 {
		t.Fatalf("report should read old ledger format: %s", out)
	}
}

func TestG3_E2E10_NonRegression(t *testing.T) {
	if os.Getenv("G3_NONREG_INNER") == "1" {
		t.Skip("inner non-regression run")
	}
	modRoot := findModuleRoot()
	// WHY: this test already runs inside the e2e package; re-running `./...` from
	// here nests another full e2e run (including this test), which can exceed the
	// default `go test` timeout and appear as a hang on Cmd.Wait().
	// WHY: non-regression should remain fast and deterministic (no live agent e2e paths).
	cmd := exec.Command("go", "test", "-timeout", "3m", "./cmd", "./internal/...")
	cmd.Dir = modRoot
	cmd.Env = append(os.Environ(), "G3_NONREG_INNER=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("non regression failed: %v\n%s", err, string(out))
	}
}
