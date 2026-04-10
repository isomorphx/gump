//go:build legacy_e2e

package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestG4_RestartFromGateReevaluated(t *testing.T) {
	dir := setupGoRepo(t)
	writeFile(t, dir, ".gump/workflows/test-g4-restart-gate.yaml", `name: test-g4-restart-gate
steps:
  - name: precheck
    gate:
      - bash: "true"
  - name: code
    agent: stub
    output: diff
    prompt: "x"
    gate:
      - compile
    on_failure:
      retry: 2
      restart_from: precheck
`)
	writeFile(t, dir, "spec.md", "x")
	writeFile(t, dir, ".gump-test-scenario.json", `{"by_attempt":{"1":{"files":{"bad.go":"package main\n\nfunc broken() { SYNTAX }\n"}},"2":{"files":{"bad.go":"package main\n\nfunc fixed() {}\n"}}}}`)
	gitCommitAll(t, dir, "setup")
	stdout, stderr, code := runGump(t, []string{"run", "spec.md", "--workflow", "test-g4-restart-gate", "--agent-stub"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout, stderr)
	}
	manifest := readFile(t, filepath.Join(latestRunDir(t, dir), "manifest.ndjson"))
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(manifest), "\n") {
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] == "gate_started" && ev["step"] == "precheck" {
			count++
		}
	}
	if count < 2 {
		t.Fatalf("expected precheck gate to be re-evaluated after restart_from, got %d", count)
	}
}
