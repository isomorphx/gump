//go:build legacy_e2e

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func g5CountManifest(manifest, typ string) int {
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(manifest), "\n") {
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] == typ {
			n++
		}
	}
	return n
}

func g5LastAgentLaunchedCLI(manifest string) string {
	var last string
	for _, line := range strings.Split(strings.TrimSpace(manifest), "\n") {
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] != "agent_launched" {
			continue
		}
		if cli, _ := ev["cli"].(string); cli != "" {
			last = cli
		}
	}
	return last
}

func g5LastAgentLaunchedCLIForStep(manifest, step string) string {
	var last string
	for _, line := range strings.Split(strings.TrimSpace(manifest), "\n") {
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] != "agent_launched" {
			continue
		}
		if ev["step"] != step {
			continue
		}
		if cli, _ := ev["cli"].(string); cli != "" {
			last = cli
		}
	}
	return last
}

func TestG5_E2E_1_VerboseFlag(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "stdout_extra_json_lines": [
    "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"name\":\"Read\",\"input\":{\"path\":\"go.mod\"}}]}}",
    "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}"
  ]
}`)
	writeFile(t, dir, "spec.md", "hello")
	gitCommitAll(t, dir, "setup")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent-stub", "--verbose"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "Read") || !strings.Contains(stderr, "go.mod") {
		t.Fatalf("expected verbose Read + path in stderr, got: %s", stderr)
	}
}

func TestG5_E2E_2_VerboseViaConfig(t *testing.T) {
	dir := setupGoRepo(t)
	home := t.TempDir()
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "stdout_extra_json_lines": [
    "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"name\":\"Read\",\"input\":{\"path\":\"main.go\"}}]}}",
    "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}"
  ]
}`)
	writeFile(t, dir, "spec.md", "hello")
	gitCommitAll(t, dir, "setup")
	env := map[string]string{"HOME": home}
	if err := os.MkdirAll(filepath.Join(home, ".gump"), 0755); err != nil {
		t.Fatal(err)
	}
	_, _, c1 := runPudding(t, []string{"config", "set", "verbose", "true"}, env, dir)
	if c1 != 0 {
		t.Fatalf("config set exit %d", c1)
	}
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent-stub"}, env, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "Read") || !strings.Contains(stderr, "main.go") {
		t.Fatalf("expected verbose Read + path in stderr, got: %s", stderr)
	}
	_, _, _ = runPudding(t, []string{"config", "set", "verbose", "false"}, env, dir)
}

func TestG5_E2E_3_ResumeHappyPath(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/g5-resume.yaml", `name: g5-resume
steps:
  - name: prep
    agent: stub
    output: diff
    prompt: "x"
    gate: [compile]
  - name: code
    agent: stub
    output: diff
    prompt: "x"
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "by_attempt": {
    "2": {"files": {"bad.go": "package main\n\nfunc X() { SYNTAXERROR }\n"}},
    "3": {"files": {"bad.go": "package main\n\nfunc X() {}\n"}}
  }
}`)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "setup")
	stdout1, stderr1, code1 := runPudding(t, []string{"run", "spec.md", "--workflow", "g5-resume", "--agent-stub"}, nil, dir)
	if code1 == 0 {
		t.Fatalf("expected first run fatal, exit 0 stdout=%s stderr=%s", stdout1, stderr1)
	}
	stdout2, stderr2, code2 := runPudding(t, []string{"run", "--resume", "--agent-stub"}, nil, dir)
	if code2 != 0 {
		t.Fatalf("resume exit %d stdout=%s stderr=%s", code2, stdout2, stderr2)
	}
	cookDir := latestCookDir(t, dir)
	manifest := readFile(t, filepath.Join(cookDir, "manifest.ndjson"))
	if !strings.Contains(manifest, `"type":"run_resumed"`) {
		t.Fatalf("manifest should contain run_resumed: %s", manifest)
	}
}

func TestG5_E2E_4_ResumeWorktreePreserved(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/g5-wt.yaml", `name: g5-wt
steps:
  - name: code
    agent: stub
    output: diff
    prompt: "x"
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "by_attempt": {
    "1": {"files": {"marker_resume.txt": "v1\n", "x.go": "package main\n\nfunc X() { !!! }\n"}},
    "2": {"files": {"marker_resume.txt": "v2\n", "x.go": "package main\n"}}
  }
}`)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "setup")
	stdout1, _, code1 := runPudding(t, []string{"run", "spec.md", "--workflow", "g5-wt", "--agent-stub"}, nil, dir)
	if code1 == 0 {
		t.Fatal("expected fatal first run")
	}
	wtRe := regexp.MustCompile(`Worktree:\s+(\S+)`)
	m := wtRe.FindStringSubmatch(stdout1)
	if len(m) < 2 {
		t.Fatalf("no worktree in stdout: %s", stdout1)
	}
	wt := m[1]
	b1, err := os.ReadFile(filepath.Join(wt, "marker_resume.txt"))
	if err != nil || string(b1) != "v1\n" {
		t.Fatalf("expected v1 marker in worktree before resume: %v %q", err, string(b1))
	}
	_, _, code2 := runPudding(t, []string{"run", "--resume", "--agent-stub"}, nil, dir)
	if code2 != 0 {
		t.Fatal("resume should pass")
	}
	b2, err := os.ReadFile(filepath.Join(wt, "marker_resume.txt"))
	if err != nil || string(b2) != "v2\n" {
		t.Fatalf("expected resumed run to advance marker via same worktree: %v %q", err, string(b2))
	}
}

func TestG5_E2E_5_ResumeSessionsPreserved(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/g5-sess.yaml", `name: g5-sess
steps:
  - name: tests
    agent: stub
    session: fresh
    output: diff
    prompt: "x"
    gate: [compile]
  - name: impl
    agent: stub
    session: reuse
    output: diff
    prompt: "x"
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "session_id_by_step": {"tests": "sess-from-tests"},
  "by_attempt": {
    "2": {"files": {"bad.go": "package main\n\nfunc B() { XXX }\n"}},
    "3": {"files": {"bad.go": "package main\n\nfunc B() {}\n"}}
  }
}`)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "setup")
	stdout1, _, code1 := runPudding(t, []string{"run", "spec.md", "--workflow", "g5-sess", "--agent-stub"}, nil, dir)
	if code1 == 0 {
		t.Fatal("expected fatal")
	}
	uuid := extractCookID(stdout1)
	man1 := readFile(t, filepath.Join(dir, ".gump", "runs", uuid, "manifest.ndjson"))
	if !strings.Contains(man1, "sess-from-tests") {
		t.Fatalf("first run should record session id: %s", man1)
	}
	_, _, code2 := runPudding(t, []string{"run", "--resume", "--agent-stub"}, nil, dir)
	if code2 != 0 {
		t.Fatal("resume failed")
	}
	man2 := readFile(t, filepath.Join(dir, ".gump", "runs", uuid, "manifest.ndjson"))
	if !strings.Contains(man2, "--resume sess-from-tests") {
		t.Fatalf("resumed impl CLI should contain session resume: %s", man2)
	}
}

func TestG5_E2E_6_ResumePassRefused(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "setup")
	_, _, c1 := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent-stub"}, nil, dir)
	if c1 != 0 {
		t.Skip("first run did not pass; cannot test resume refusal")
	}
	_, stderr, c2 := runPudding(t, []string{"run", "--resume", "--agent-stub"}, nil, dir)
	if c2 == 0 {
		t.Fatal("expected non-zero exit when resuming a passed run")
	}
	if !strings.Contains(stderr, "already completed successfully") {
		t.Fatalf("stderr=%s", stderr)
	}
}

func TestG5_E2E_7_ResumeMissingWorktree(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/g5-mw.yaml", `name: g5-mw
steps:
  - name: code
    agent: stub
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"},"by_attempt":{"1":{"files":{"x.go":"package main\n\nfunc X() { !!! }\n"}},"2":{"files":{"x.go":"package main\n"}}}}`)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "setup")
	stdout1, _, code1 := runPudding(t, []string{"run", "spec.md", "--workflow", "g5-mw", "--agent-stub"}, nil, dir)
	if code1 == 0 {
		t.Fatal("expected fatal")
	}
	wtRe := regexp.MustCompile(`Worktree:\s+(\S+)`)
	m := wtRe.FindStringSubmatch(stdout1)
	if len(m) < 2 {
		t.Fatalf("no worktree: %s", stdout1)
	}
	_ = os.RemoveAll(m[1])
	_, stderr, code2 := runPudding(t, []string{"run", "--resume", "--agent-stub"}, nil, dir)
	if code2 == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stderr, "worktree") || !strings.Contains(stderr, "gump gc") {
		t.Fatalf("stderr=%s", stderr)
	}
}

func TestG5_E2E_8_OnFailureConditionalGateFail(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/g5-c8.yaml", `name: g5-c8
steps:
  - name: code
    agent: stub
    output: diff
    prompt: "x"
    gate: [compile]
    on_failure:
      gate_fail:
        retry: 2
        strategy: [same]
      guard_fail:
        retry: 1
        strategy: [same]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "by_attempt": {
    "1": {"files": {"z.go": "package main\n\nfunc Z() { BAD }\n"}},
    "2": {"files": {"z.go": "package main\n\nfunc Z() {}\n"}}
  }
}`)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "setup")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "g5-c8", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	manifest := readFile(t, filepath.Join(latestCookDir(t, dir), "manifest.ndjson"))
	if g5CountManifest(manifest, "retry_triggered") < 1 {
		t.Fatalf("expected retry_triggered in manifest: %s", manifest)
	}
}

func TestG5_E2E_9_OnFailureGuardFailRouting(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/g5-c9.yaml", `name: g5-c9
steps:
  - name: code
    agent: stub
    output: diff
    prompt: "x"
    guard:
      max_turns: 1
    gate: [compile]
    on_failure:
      gate_fail:
        retry: 5
        strategy: [same]
      guard_fail:
        retry: 2
        strategy: ["escalate: claude-opus"]
`)
	// Attempt 1: exceed max_turns (action then two assistant texts). Attempt 2: single assistant, valid compile.
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "stdout_extra_json_lines_by_attempt": {
    "1": [
      "{\"type\":\"action\",\"name\":\"write\",\"input\":{\"path\":\".gump/out/x\"}}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"t1\"}]}}",
      "{\"type\":\"action\",\"name\":\"write\",\"input\":{\"path\":\".gump/out/x\"}}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"t2\"}]}}"
    ],
    "2": [
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}"
    ]
  }
}`)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "setup")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "g5-c9", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	manifest := readFile(t, filepath.Join(latestCookDir(t, dir), "manifest.ndjson"))
	if !strings.Contains(manifest, `"strategy":"escalate: claude-opus"`) && !strings.Contains(manifest, `escalate: claude-opus`) {
		t.Fatalf("expected guard_fail escalate strategy in manifest: %s", manifest)
	}
}

func TestG5_E2E_10_OnFailureFlatBackwardCompat(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/g5-c10.yaml", `name: g5-c10
steps:
  - name: code
    agent: stub
    gate: [compile]
    on_failure:
      retry: 3
      strategy: [same]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "by_attempt": {
    "1": {"files": {"a.go": "package main\n\nfunc A() { !!! }\n"}},
    "2": {"files": {"a.go": "package main\n\nfunc A() { ### }\n"}},
    "3": {"files": {"a.go": "package main\n\nfunc A() {}\n"}}
  }
}`)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "setup")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "g5-c10", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	manifest := readFile(t, filepath.Join(latestCookDir(t, dir), "manifest.ndjson"))
	if g5CountManifest(manifest, "retry_triggered") < 2 {
		t.Fatalf("expected at least 2 retry_triggered for flat retry:3: %s", manifest)
	}
}

func TestG5_E2E_11_OnFailureIndependentCounters(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/g5-c11.yaml", `name: g5-c11
steps:
  - name: code
    agent: stub
    output: diff
    prompt: "x"
    guard:
      max_turns: 1
    gate: [compile]
    on_failure:
      gate_fail:
        retry: 3
        strategy: [same]
      guard_fail:
        retry: 2
        strategy: [same]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "stdout_extra_json_lines_by_attempt": {
    "1": [
      "{\"type\":\"action\",\"name\":\"write\",\"input\":{\"path\":\".gump/out/x\"}}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"t1\"}]}}",
      "{\"type\":\"action\",\"name\":\"write\",\"input\":{\"path\":\".gump/out/x\"}}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"t2\"}]}}"
    ]
  },
  "by_attempt": {
    "2": {"files": {"b.go": "package main\n\nfunc B() { XXX }\n"}},
    "3": {"files": {"b.go": "package main\n\nfunc B() { YYY }\n"}},
    "4": {"files": {"b.go": "package main\n\nfunc B() {}\n"}}
  }
}`)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "setup")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "g5-c11", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	manifest := readFile(t, filepath.Join(latestCookDir(t, dir), "manifest.ndjson"))
	if !strings.Contains(manifest, `"type":"guard_triggered"`) {
		t.Fatalf("expected guard_triggered: %s", manifest)
	}
	if g5CountManifest(manifest, "gate_failed") < 2 {
		t.Fatalf("expected multiple gate_failed: %s", manifest)
	}
}

func TestG5_E2E_12_ReviewFailFallbackToGateFail(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/g5-c12.yaml", `name: g5-c12
steps:
  - name: rev
    agent: stub
    output: review
    prompt: "x"
    gate: [compile]
    on_failure:
      gate_fail:
        retry: 2
        strategy: [same]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "review_by_attempt": {
    "1": "{\"pass\":false,\"comment\":\"no\"}",
    "2": "{\"pass\":true,\"comment\":\"ok\"}"
  }
}`)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "setup")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "g5-c12", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	manifest := readFile(t, filepath.Join(latestCookDir(t, dir), "manifest.ndjson"))
	if g5CountManifest(manifest, "retry_triggered") < 1 {
		t.Fatalf("expected retries routed via gate_fail fallback: %s", manifest)
	}
}

func TestG5_E2E_13_ReportDetail(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/g5-rep.yaml", `name: g5-rep
steps:
  - name: impl
    agent: stub
    gate: [compile]
    on_failure:
      retry: 2
      strategy: [same]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "by_attempt": {
    "1": {"files": {"q.go": "package main\n\nfunc Q() { !!! }\n"}},
    "2": {"files": {"q.go": "package main\n\nfunc Q() {}\n"}}
  }
}`)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "setup")
	_, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "g5-rep", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("run failed %d", code)
	}
	stdout, stderr, rc := runPudding(t, []string{"report", "--detail", "impl"}, nil, dir)
	if rc != 0 {
		t.Fatalf("report %d stderr=%s", rc, stderr)
	}
	for _, s := range []string{"Attempts:", "gate_fail", "State Bag:", "Files Changed:"} {
		if !strings.Contains(stdout, s) {
			t.Fatalf("detail output missing %q:\n%s", s, stdout)
		}
	}
}

func TestG5_E2E_14_CursorStubCLI(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "setup")
	_, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent", "cursor", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	manifest := readFile(t, filepath.Join(latestCookDir(t, dir), "manifest.ndjson"))
	cli := g5LastAgentLaunchedCLI(manifest)
	for _, frag := range []string{"cursor-agent", "-p", "--yolo", "--trust"} {
		if !strings.Contains(cli, frag) {
			t.Fatalf("CLI %q should contain %q (full: %s)", cli, frag, cli)
		}
	}
}

func TestG5_E2E_15_CursorContextMDC(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".cursor/rules/existing.mdc", "---\ndescription: test\n---\nbody\n")
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent", "cursor", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	wtRe := regexp.MustCompile(`Worktree:\s+(\S+)`)
	m := wtRe.FindStringSubmatch(stdout)
	if len(m) < 2 {
		t.Fatalf("worktree: %s", stdout)
	}
	wt := m[1]
	// Run defers RestoreAllContextFiles: generated gump-agent.mdc is removed at end.
	gumpMDC := filepath.Join(wt, ".cursor/rules/gump-agent.mdc")
	if _, err := os.Stat(gumpMDC); err == nil {
		t.Fatalf("gump-agent.mdc should be removed after run, still present")
	}
	exist := filepath.Join(wt, ".cursor/rules/existing.mdc")
	b, err := os.ReadFile(exist)
	if err != nil || !strings.Contains(string(b), "body") {
		t.Fatalf("existing.mdc: %v %q", err, string(b))
	}
}

func TestG5_E2E_16_DoctorCursorLine(t *testing.T) {
	stdout, stderr, code := runPudding(t, []string{"doctor"}, map[string]string{"GUMP_E2E_SKIP_CURSOR_DOCTOR": "1"}, "")
	if code != 0 {
		t.Fatalf("doctor %d stderr=%s", code, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "cursor:") {
		t.Fatalf("expected cursor: in doctor output: %s", combined)
	}
}

func TestG5_E2E_17_CursorResumeSession(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/g5-cur.yaml", `name: g5-cur
steps:
  - name: tests
    agent: stub
    session: fresh
    output: diff
    prompt: "x"
    gate: [compile]
  - name: impl
    agent: stub
    session:
      reuse: tests
    output: diff
    prompt: "x"
    gate: [compile]
`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "files": {"main.go": "package main\n\nfunc main() {}\n"},
  "session_id_by_step": {"tests": "cursor-sess-1"},
  "by_attempt": {
    "2": {"files": {"bad.go": "package main\n\nfunc B() { XXX }\n"}},
    "3": {"files": {"bad.go": "package main\n\nfunc B() {}\n"}}
  }
}`)
	writeFile(t, dir, "spec.md", "x")
	gitCommitAll(t, dir, "setup")
	stdout1, _, code1 := runPudding(t, []string{"run", "spec.md", "--workflow", "g5-cur", "--agent", "cursor", "--agent-stub"}, nil, dir)
	if code1 == 0 {
		t.Fatal("expected fatal")
	}
	uuid := extractCookID(stdout1)
	_, _, code2 := runPudding(t, []string{"run", "--resume", "--agent", "cursor", "--agent-stub"}, nil, dir)
	if code2 != 0 {
		t.Fatal("resume failed")
	}
	man := readFile(t, filepath.Join(dir, ".gump", "runs", uuid, "manifest.ndjson"))
	implCLI := g5LastAgentLaunchedCLIForStep(man, "impl")
	if !strings.Contains(implCLI, "--resume") || !strings.Contains(implCLI, "cursor-sess-1") {
		t.Fatalf("last impl launch should resume tests session; cli=%q manifest tail excerpt logged above", implCLI)
	}
}

func TestG5_E2E_18_ModelAliasClaudeOpusplan(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "setup")
	_, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent", "claude-opusplan", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatal(code)
	}
	cli := g5LastAgentLaunchedCLI(readFile(t, filepath.Join(latestCookDir(t, dir), "manifest.ndjson")))
	if !strings.Contains(cli, "--model opusplan") {
		t.Fatalf("cli=%s", cli)
	}
}

func TestG5_E2E_19_ModelAliasCodexGpt54(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "setup")
	_, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent", "codex-gpt54", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatal(code)
	}
	cli := g5LastAgentLaunchedCLI(readFile(t, filepath.Join(latestCookDir(t, dir), "manifest.ndjson")))
	if !strings.Contains(cli, "-m gpt-5.4") && !strings.Contains(cli, "gpt-5.4") {
		t.Fatalf("cli=%s", cli)
	}
}

func TestG5_E2E_20_ModelAliasGeminiPro(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "setup")
	_, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent", "gemini-pro", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatal(code)
	}
	cli := g5LastAgentLaunchedCLI(readFile(t, filepath.Join(latestCookDir(t, dir), "manifest.ndjson")))
	if !strings.Contains(cli, "gemini-3.1-pro-preview") {
		t.Fatalf("cli=%s", cli)
	}
	if strings.Contains(cli, "gemini-2.5-pro-preview") {
		t.Fatalf("should not use old model id: %s", cli)
	}
}

func TestG5_E2E_21_ModelAliasPassthrough(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}\n"}}`)
	gitCommitAll(t, dir, "setup")
	_, _, code := runPudding(t, []string{"run", "spec.md", "--workflow", "freeform", "--agent", "claude-some-future-model", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatal(code)
	}
	cli := g5LastAgentLaunchedCLI(readFile(t, filepath.Join(latestCookDir(t, dir), "manifest.ndjson")))
	if !strings.Contains(cli, "--model some-future-model") {
		t.Fatalf("cli=%s", cli)
	}
}

func TestG5_E2E_22_NonRegression(t *testing.T) {
	if os.Getenv("G5_NONREG_INNER") == "1" {
		t.Skip("inner non-regression run")
	}
	modRoot := findModuleRoot()
	listCmd := exec.Command("go", "list", "./...")
	listCmd.Dir = modRoot
	listOut, err := listCmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	var pkgs []string
	for _, line := range strings.Split(strings.TrimSpace(string(listOut)), "\n") {
		if line == "" || strings.HasSuffix(line, "/e2e") {
			continue
		}
		pkgs = append(pkgs, line)
	}
	if len(pkgs) == 0 {
		t.Fatal("no packages to test")
	}
	args := append([]string{"test", "-count=1"}, pkgs...)
	testCmd := exec.Command("go", args...)
	testCmd.Dir = modRoot
	testCmd.Env = append(os.Environ(), "G5_NONREG_INNER=1")
	out, err := testCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test (all except e2e) failed: %v\n%s", err, string(out))
	}
}
