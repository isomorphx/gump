package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildStepDetailAndRender(t *testing.T) {
	dir := t.TempDir()
	diffRel := ".gump/runs/run-1/artefacts/build_impl.diff"
	diffPath := filepath.Join(dir, filepath.FromSlash(diffRel))
	if err := os.MkdirAll(filepath.Dir(diffPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(diffPath, []byte("diff --git a/a.go b/a.go\n"), 0644); err != nil {
		t.Fatal(err)
	}

	manifest := strings.Join([]string{
		`{"type":"step_started","step":"build/impl","agent":"claude-sonnet","output_mode":"diff","attempt":1}`,
		`{"type":"agent_completed","step":"build/impl","duration_ms":1200,"tokens_in":1000,"tokens_out":200,"cost_usd":0.12}`,
		`{"type":"gate_failed","step":"build/impl","reason":"tests failed"}`,
		`{"type":"retry_triggered","step":"build/impl","attempt":1}`,
		`{"type":"step_started","step":"build/impl","agent":"claude-sonnet","output_mode":"diff","attempt":2}`,
		`{"type":"agent_completed","step":"build/impl","duration_ms":900,"tokens_in":700,"tokens_out":150,"cost_usd":0.08}`,
		`{"type":"step_completed","step":"build/impl","status":"pass","artifacts":{"diff":"` + diffRel + `"}}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "manifest.ndjson"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}

	stateBag := `{"entries":{"build/impl":{"output":"diff --git a/a.go b/a.go","status":"pass","tokens_in":"1700","tokens_out":"350","turns":"5","retries":"1"}}}`
	if err := os.WriteFile(filepath.Join(dir, "state-bag.json"), []byte(stateBag), 0644); err != nil {
		t.Fatal(err)
	}

	detail, err := BuildStepDetail(dir, "impl")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Step != "build/impl" {
		t.Fatalf("unexpected step: %s", detail.Step)
	}
	if len(detail.Attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(detail.Attempts))
	}
	if detail.Attempts[0].Status != "gate_fail" {
		t.Fatalf("expected first attempt gate_fail, got %s", detail.Attempts[0].Status)
	}
	if detail.Attempts[1].Status != "pass" {
		t.Fatalf("expected second attempt pass, got %s", detail.Attempts[1].Status)
	}
	out := RenderStepDetail(detail)
	for _, needle := range []string{"Attempts:", "State Bag:", "Files Changed:", "gate_fail"} {
		if !strings.Contains(out, needle) {
			t.Fatalf("render missing %q\n%s", needle, out)
		}
	}
}
