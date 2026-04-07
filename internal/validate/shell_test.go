package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/recipe"
)

func TestRunShellValidator_ExitZero(t *testing.T) {
	dir := t.TempDir()
	r := RunShellValidator("echo ok", dir, 0)
	r.Validator = "test"
	if !r.Pass {
		t.Errorf("expected pass: %+v", r)
	}
	if r.ExitCode != 0 {
		t.Errorf("exit code: %d", r.ExitCode)
	}
	if r.Stdout != "ok\n" {
		t.Errorf("stdout: %q", r.Stdout)
	}
}

func TestRunShellValidator_ExitNonZero(t *testing.T) {
	dir := t.TempDir()
	r := RunShellValidator("exit 2", dir, 0)
	if r.Pass {
		t.Errorf("expected fail")
	}
	if r.ExitCode != 2 {
		t.Errorf("exit code: %d", r.ExitCode)
	}
}

func TestRunCompileValidator_ResolveFails(t *testing.T) {
	dir := t.TempDir()
	r := RunCompileValidator(nil, dir)
	if r.Pass {
		t.Error("expected fail")
	}
	if r.Stderr == "" {
		t.Error("expected stderr")
	}
}

func TestRunBashValidator_CustomCommand(t *testing.T) {
	dir := t.TempDir()
	r := RunBashValidator(recipe.Validator{Type: "bash", Arg: "test -f " + filepath.Join(dir, "x")}, dir, nil)
	if r.Pass {
		t.Error("expected fail when file missing")
	}
	writeFile(t, dir, "x", "ok")
	r2 := RunBashValidator(recipe.Validator{Type: "bash", Arg: "test -f x"}, dir, nil)
	if !r2.Pass {
		t.Error("expected pass when file exists")
	}
}

func TestRunCoverageValidator_InvalidThreshold(t *testing.T) {
	r := RunCoverageValidator("abc", nil, t.TempDir())
	if r.Pass {
		t.Error("expected fail")
	}
	if r.Stderr == "" {
		t.Error("expected stderr")
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// T1: lint skipped when binary absent (use a non-existent binary via config)
func TestRunLintValidator_SkippedWhenBinaryAbsent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module x\n")
	cfg := &config.Config{LintCmd: "nonexistent-golangci-lint-xyz run"}
	r := RunLintValidator(cfg, dir)
	if !r.Pass {
		t.Errorf("expected pass (skip): %+v", r)
	}
	if !r.Skipped {
		t.Error("expected Skipped true")
	}
	if !strings.Contains(r.Stdout, "golangci-lint") && !strings.Contains(r.Stdout, "nonexistent-golangci-lint-xyz") {
		t.Errorf("stdout should mention binary: %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "not installed") || !strings.Contains(r.Stdout, "skipping") {
		t.Errorf("stdout should contain not installed and skipping: %q", r.Stdout)
	}
	if !strings.Contains(r.Validator, "skipped") {
		t.Errorf("validator name should contain skipped: %q", r.Validator)
	}
}

// T2: compile fails with clear message when binary absent
func TestRunCompileValidator_FailsWithClearMessageWhenBinaryAbsent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module x\n")
	cfg := &config.Config{CompileCmd: "nonexistent-go-xyz build ./..."}
	r := RunCompileValidator(cfg, dir)
	if r.Pass {
		t.Error("expected fail")
	}
	if r.Skipped {
		t.Error("compile should not be skipped")
	}
	if !strings.Contains(r.Stderr, "not installed") {
		t.Errorf("stderr should contain not installed: %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "compile_cmd") || !strings.Contains(r.Stderr, "gump.toml") {
		t.Errorf("stderr should guide user to config: %q", r.Stderr)
	}
}

// T3: lint runs when binary present (use "true" as the command so it always succeeds)
func TestRunLintValidator_RunsWhenBinaryPresent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module x\n")
	cfg := &config.Config{LintCmd: "true"}
	r := RunLintValidator(cfg, dir)
	if r.Skipped {
		t.Error("expected not skipped when command is available")
	}
	// Pass and ExitCode depend on "true" succeeding (exit 0)
	if r.ExitCode != 0 {
		t.Errorf("expected exit 0 from true, got %d", r.ExitCode)
	}
	if !r.Pass {
		t.Errorf("expected pass: %+v", r)
	}
}

// T4: coverage skipped when binary absent
func TestRunCoverageValidator_SkippedWhenBinaryAbsent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Cargo.toml", "[package]\nname = \"x\"\nversion = \"0.1.0\"\n")
	cfg := &config.Config{CoverageCmd: "nonexistent-cargo-xyz tarpaulin --out Stdout"}
	r := RunCoverageValidator("80", cfg, dir)
	if !r.Pass {
		t.Errorf("expected pass (skip): %+v", r)
	}
	if !r.Skipped {
		t.Error("expected Skipped true")
	}
	if !strings.Contains(r.Stdout, "cargo") && !strings.Contains(r.Stdout, "nonexistent-cargo-xyz") {
		t.Errorf("stdout should mention binary: %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "not installed") || !strings.Contains(r.Stdout, "skipping") {
		t.Errorf("stdout should contain not installed and skipping: %q", r.Stdout)
	}
}

// T7: ValidationResult.Pass is true when all non-skipped pass (skipped count as pass)
func TestRunValidators_PassTrueWithSkips(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module x\n")
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	cfg := &config.Config{LintCmd: "nonexistent-xyz run"} // lint will be skipped
	validators := []recipe.Validator{
		{Type: "compile"},
		{Type: "test"},
		{Type: "lint"},
	}
	vr := RunValidators(validators, cfg, dir, nil, nil, "")
	if !vr.Pass {
		t.Errorf("expected Pass true (compile+test pass, lint skipped): %+v", vr)
	}
	skipped := 0
	for _, r := range vr.Results {
		if r.Skipped {
			skipped++
		}
	}
	if skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", skipped)
	}
}

// T8: ValidationResult.Pass is false when an essential validator fails (compile fail, test pass, lint skipped)
func TestRunValidators_PassFalseWhenEssentialFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module x\n")
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	cfg := &config.Config{
		CompileCmd: "nonexistent-go-xyz build",
		TestCmd:    "true",
		LintCmd:    "nonexistent-lint-xyz run",
	}
	validators := []recipe.Validator{
		{Type: "compile"}, // fails (essential)
		{Type: "test"},    // passes (essential)
		{Type: "lint"},    // skipped (optional)
	}
	vr := RunValidators(validators, cfg, dir, nil, nil, "")
	if vr.Pass {
		t.Error("expected Pass false when compile fails")
	}
	var hasFail, hasPass, hasSkip bool
	for _, r := range vr.Results {
		if !r.Pass && !r.Skipped {
			hasFail = true
		}
		if r.Pass && !r.Skipped {
			hasPass = true
		}
		if r.Skipped {
			hasSkip = true
		}
	}
	if !hasFail {
		t.Error("expected at least one failed validator")
	}
	if !hasPass {
		t.Error("expected test to pass")
	}
	if !hasSkip {
		t.Error("expected lint to be skipped")
	}
}

// TestRunTestValidator_NoPackagesToTest ensures go test with no Go packages (e.g. only go.mod) is treated as pass so retries don't loop forever.
func TestRunTestValidator_NoPackagesToTest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module x\n")
	cfg := (*config.Config)(nil)
	r := RunTestValidator(cfg, dir)
	if !r.Pass {
		t.Errorf("expected pass (no packages): %+v", r)
	}
	if !strings.Contains(r.Validator, "no packages") {
		t.Errorf("Validator should contain 'no packages', got %q", r.Validator)
	}
}
