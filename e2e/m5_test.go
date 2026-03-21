package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// M5-E2E-1: single cook report — basic metrics
func TestM5_E2E1_ReportSingleCookMetrics(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{
  "cost_usd": 0.05,
  "tokens_in": 1000,
  "tokens_out": 500,
  "files": {"main.go": "package main\n\nfunc main() {}"}
}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runPudding(t, []string{"cook", "spec.md", "--recipe", "test", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("cook exit %d: %s %s", code, stdout, stderr)
	}
	reportOut, _, reportCode := runPudding(t, []string{"report"}, nil, dir)
	if reportCode != 0 {
		t.Fatalf("report exit %d: %s", reportCode, reportOut)
	}
	cookID := extractCookID(stdout)
	if cookID != "" {
		short := cookID
		if len(short) > 8 {
			short = short[:8]
		}
		if !strings.Contains(reportOut, short) {
			t.Errorf("report should contain cook id prefix %s: %s", short, reportOut)
		}
	}
	if !strings.Contains(strings.ToLower(reportOut), "pass") {
		t.Error("report should contain pass")
	}
	if !strings.Contains(reportOut, "$0.05") {
		t.Error("report should contain cost $0.05")
	}
	if !strings.Contains(reportOut, "1,000") {
		t.Error("report should format tokens in with thousands separator")
	}
	if !strings.Contains(reportOut, "500") {
		t.Error("report should contain tokens out")
	}
	if !strings.Contains(reportOut, "Duration") {
		t.Error("report should contain Duration")
	}
}

// M5-E2E-2: steps table for TDD recipe
func TestM5_E2E2_ReportStepsTable(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "Add Add(a,b int) int")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"task-1","description":"math","files":["math.go","math_test.go"]}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"math.go":"package main\n\nfunc Add(a,b int) int { return a+b }\n","math_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1,2)!=3 { t.Fatal() } }\n"}}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runPudding(t, []string{"cook", "spec.md", "--recipe", "tdd", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("cook exit %d: %s %s", code, stdout, stderr)
	}
	reportOut, _, reportCode := runPudding(t, []string{"report"}, nil, dir)
	if reportCode != 0 {
		t.Fatalf("report exit %d: %s", reportCode, reportOut)
	}
	if !strings.Contains(reportOut, "Steps") {
		t.Error("report should contain Steps section")
	}
	if !strings.Contains(reportOut, "decompose") {
		t.Error("report should list decompose")
	}
	if !strings.Contains(reportOut, "quality") {
		t.Error("report should list quality")
	}
	lines := strings.Count(reportOut, "\n")
	if lines < 10 {
		t.Errorf("report should have multiple lines: %s", reportOut)
	}
}

// M5-E2E-3: --last N aggregate
func TestM5_E2E3_ReportLastNAggregate(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"files":{"main.go":"package main\n\nfunc main() {}"}}`)
	gitCommitAll(t, dir, "init")
	for i := 0; i < 3; i++ {
		_, _, code := runPudding(t, []string{"cook", "spec.md", "--recipe", "test", "--agent-stub"}, nil, dir)
		if code != 0 {
			t.Fatalf("cook %d failed", i)
		}
	}
	reportOut, _, reportCode := runPudding(t, []string{"report", "--last", "3"}, nil, dir)
	if reportCode != 0 {
		t.Fatalf("report exit %d: %s", reportCode, reportOut)
	}
	if !strings.Contains(reportOut, "Last 3 cooks") {
		t.Errorf("expected Last 3 cooks: %s", reportOut)
	}
	if !strings.Contains(reportOut, "Success rate") {
		t.Error("expected Success rate")
	}
	if !strings.Contains(reportOut, "3/3") && !strings.Contains(reportOut, "100%") {
		t.Error("expected full success summary")
	}
	if !strings.Contains(reportOut, "Avg duration") {
		t.Error("expected Avg duration")
	}
	if !strings.Contains(reportOut, "Avg cost") {
		t.Error("expected Avg cost")
	}
}

// M5-E2E-4: turn distribution
func TestM5_E2E4_TurnDistribution(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "x")
	extraLines := []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`,
	}
	scen, err := json.Marshal(map[string]interface{}{
		"files": map[string]string{
			"main.go": "package main\n\nfunc main() {}",
		},
		"stdout_extra_json_lines": extraLines,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, ".pudding-test-scenario.json", string(scen))
	gitCommitAll(t, dir, "init")
	_, _, code := runPudding(t, []string{"cook", "spec.md", "--recipe", "test", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatal("cook failed")
	}
	reportOut, _, reportCode := runPudding(t, []string{"report"}, nil, dir)
	if reportCode != 0 {
		t.Fatalf("report exit %d: %s", reportCode, reportOut)
	}
	if !strings.Contains(reportOut, "Turn Distribution") {
		t.Error("expected Turn Distribution")
	}
	if !strings.Contains(reportOut, "coding") && !strings.Contains(reportOut, "execution") && !strings.Contains(reportOut, "exploration") {
		t.Errorf("expected a turn label: %s", reportOut)
	}
}

// M5-E2E-5: context usage (claude-sonnet 50k / 200k)
func TestM5_E2E5_ContextUsage(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".pudding/recipes/test-sonnet.yaml", `name: test-sonnet
max_budget: 5.00
steps:
  - name: impl
    agent: claude-sonnet
    output: diff
    prompt: x
    gate: [compile]
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".pudding-test-scenario.json", `{"tokens_in":50000,"tokens_out":10,"files":{"main.go":"package main\n\nfunc main() {}"}}`)
	gitCommitAll(t, dir, "init")
	_, _, code := runPudding(t, []string{"cook", "spec.md", "--recipe", "test-sonnet", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatal("cook failed")
	}
	reportOut, _, reportCode := runPudding(t, []string{"report"}, nil, dir)
	if reportCode != 0 {
		t.Fatalf("report exit %d: %s", reportCode, reportOut)
	}
	if !strings.Contains(reportOut, "Context Usage") {
		t.Error("expected Context Usage")
	}
	if !strings.Contains(reportOut, "25%") {
		t.Errorf("expected 25%% context usage: %s", reportOut)
	}
}

// M5-E2E-6: manifest v3 aliases
func TestM5_E2E6_ManifestV3Compat(t *testing.T) {
	dir := setupGoRepo(t)
	cooksDir := filepath.Join(dir, ".pudding", "cooks")
	if err := os.MkdirAll(cooksDir, 0755); err != nil {
		t.Fatal(err)
	}
	cookID := "m5e2e6-0000-0000-0000-000000000001"
	cookPath := filepath.Join(cooksDir, cookID)
	if err := os.MkdirAll(cookPath, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"ts":"2026-01-01T00:00:00.000Z","type":"cook_started","cook_id":"` + cookID + `","recipe":"leg","spec":"spec.md","commit":"abc","branch":"main"}
{"ts":"2026-01-01T00:00:01.000Z","type":"step_started","step":"gate-step","agent":"","output_mode":"","item":"","attempt":1,"session_mode":""}
{"ts":"2026-01-01T00:00:02.000Z","type":"validation_passed","step":"gate-step","artifact":"x"}
{"ts":"2026-01-01T00:00:03.000Z","type":"step_completed","step":"gate-step","status":"pass","duration_ms":100,"artifacts":{}}
{"ts":"2026-01-01T00:00:04.000Z","type":"cook_completed","status":"pass","duration_ms":1000,"total_cost_usd":0,"steps":1,"retries":0,"artifacts":{}}
`
	writeFile(t, dir, filepath.Join(".pudding/cooks", cookID, "manifest.ndjson"), manifest)
	reportOut, _, reportCode := runPudding(t, []string{"report", cookID}, nil, dir)
	if reportCode != 0 {
		t.Fatalf("report exit %d: %s", reportCode, reportOut)
	}
	if !strings.Contains(strings.ToLower(reportOut), "pass") {
		t.Errorf("expected pass: %s", reportOut)
	}
}

// M5-E2E-7: full TDD integration
func TestM5_E2E7_ReportFullTDD(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, "spec.md", "Add(a,b int) int")
	writeFile(t, dir, ".pudding-test-plan.json", `[{"name":"task-1","description":"math","files":["add.go","add_test.go"]}]`)
	writeFile(t, dir, ".pudding-test-scenario.json", `{"by_attempt":{"1":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return 0 }\n"}},"2":{"files":{"add.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"}}},"files":{"add_test.go":"package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal() } }\n"}}`)
	gitCommitAll(t, dir, "init")
	stdout, stderr, code := runPudding(t, []string{"cook", "spec.md", "--recipe", "tdd", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("cook exit %d: %s %s", code, stdout, stderr)
	}
	reportOut, _, reportCode := runPudding(t, []string{"report"}, nil, dir)
	if reportCode != 0 {
		t.Fatalf("report exit %d: %s", reportCode, reportOut)
	}
	if !strings.Contains(strings.ToLower(reportOut), "pass") {
		t.Error("expected PASS in report")
	}
	if !strings.Contains(reportOut, "Budget") || !strings.Contains(reportOut, "$5.00") {
		t.Error("expected Budget line with $5.00")
	}
	if !strings.Contains(reportOut, "Retries") {
		t.Error("expected Retries")
	}
	if !regexp.MustCompile(`Retries\s+[1-9]`).MatchString(reportOut) {
		t.Errorf("expected at least one retry in report: %s", reportOut)
	}
	if !strings.Contains(reportOut, "decompose") || !strings.Contains(reportOut, "quality") {
		t.Error("expected step names")
	}
	if !strings.Contains(reportOut, "build/") || !strings.Contains(reportOut, "tests") || !strings.Contains(reportOut, "impl") {
		t.Errorf("expected foreach step paths in report: %s", reportOut)
	}
	if !strings.Contains(reportOut, "Turn Distribution") {
		t.Error("expected Turn Distribution")
	}
	cookDir := latestCookDir(t, dir)
	manifest := readFile(t, filepath.Join(cookDir, "manifest.ndjson"))
	if strings.Contains(manifest, "validation_passed") || strings.Contains(manifest, "validation_failed") {
		t.Error("ledger should use gate_* not validation_*")
	}
	if !strings.Contains(manifest, "gate_passed") && !strings.Contains(manifest, "gate_started") {
		t.Log("manifest (info):", manifest)
	}
}
