package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/diff"
	"github.com/isomorphx/gump/internal/workflow"
	"github.com/isomorphx/gump/internal/state"
)

func TestRunValidators_CompileAndTest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	validators := []workflow.GateEntry{{Type: "compile"}, {Type: "test"}}
	res := RunValidators(validators, nil, dir, nil, state.New(), "do")
	if !res.Pass {
		t.Errorf("expected pass: %+v", res)
	}
	if len(res.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res.Results))
	}
	if res.Results[0].Validator != "compile" {
		t.Errorf("validator name: %s", res.Results[0].Validator)
	}
}

func TestRunValidators_NoShortCircuit(t *testing.T) {
	dir := t.TempDir()
	validators := []workflow.GateEntry{{Type: "compile"}, {Type: "bash", Arg: "echo second"}}
	res := RunValidators(validators, nil, dir, nil, state.New(), "do")
	if res.Pass {
		t.Error("expected fail (compile fails)")
	}
	if len(res.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res.Results))
	}
	if !res.Results[1].Pass || res.Results[1].Stdout != "second\n" {
		t.Errorf("second validator should have run: %+v", res.Results[1])
	}
}

func TestRunValidators_Touched(t *testing.T) {
	dc := &diff.DiffContract{FilesChanged: []string{"a_test.go"}}
	validators := []workflow.GateEntry{{Type: "touched", Arg: "*_test.*"}}
	res := RunValidators(validators, &config.Config{}, t.TempDir(), dc, state.New(), "do")
	if !res.Pass {
		t.Errorf("expected pass: %+v", res)
	}
	if res.Results[0].Validator != "touched: *_test.*" {
		t.Errorf("validator name: %s", res.Results[0].Validator)
	}
}
