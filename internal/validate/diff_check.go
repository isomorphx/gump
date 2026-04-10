package validate

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/diff"
)

// RunTouchedValidator ensures at least one file matching the glob was changed so workflows can require test file edits.
func RunTouchedValidator(glob string, dc *diff.DiffContract) *SingleResult {
	name := "touched: " + glob
	if dc == nil || len(dc.FilesChanged) == 0 {
		return &SingleResult{Validator: name, Pass: false, Stderr: "touched: no files changed in this step"}
	}
	for _, f := range dc.FilesChanged {
		base := filepath.Base(f)
		ok, _ := filepath.Match(glob, base)
		if ok {
			return &SingleResult{Validator: name, Pass: true}
		}
	}
	return &SingleResult{
		Validator: name,
		Pass:      false,
		Stderr:    fmt.Sprintf("touched: no files matching %q were modified. Changed files: %s", glob, strings.Join(dc.FilesChanged, ", ")),
	}
}

// RunUntouchedValidator ensures no file matching the glob was changed so workflows can forbid editing certain files.
func RunUntouchedValidator(glob string, dc *diff.DiffContract) *SingleResult {
	name := "untouched: " + glob
	if dc == nil || len(dc.FilesChanged) == 0 {
		return &SingleResult{Validator: name, Pass: true}
	}
	var matched []string
	for _, f := range dc.FilesChanged {
		base := filepath.Base(f)
		ok, _ := filepath.Match(glob, base)
		if ok {
			matched = append(matched, f)
		}
	}
	if len(matched) == 0 {
		return &SingleResult{Validator: name, Pass: true}
	}
	return &SingleResult{
		Validator: name,
		Pass:      false,
		Stderr:    fmt.Sprintf("untouched: files matching %q were modified: %s", glob, strings.Join(matched, ", ")),
	}
}

// RunTestsFoundValidator ensures the project has at least one test so empty test suites fail early.
func RunTestsFoundValidator(cfg *config.Config, worktreeDir string) *SingleResult {
	cmd, err := ResolveCommand("test", cfg, worktreeDir)
	if err != nil {
		return &SingleResult{Validator: "tests_found", Pass: false, Stderr: "tests_found: " + err.Error()}
	}
	if strings.HasPrefix(cmd, "go test") {
		cmd = "go test -v -count=0 -run . " + strings.TrimSpace(strings.TrimPrefix(cmd, "go test"))
	}
	r := RunShellValidator(cmd, worktreeDir, validationTimeout(cfg))
	r.Validator = "tests_found"
	out := r.Stdout + r.Stderr
	hasTestOutput := strings.Contains(out, "=== RUN") || strings.Contains(out, "--- PASS") || strings.Contains(out, "--- FAIL")
	// With -count=0, go test may not print === RUN; accept "PASS" or "ok\t" (package result line) as tests found.
	isGoListCmd := strings.HasPrefix(cmd, "go test") && strings.Contains(cmd, "-count=0")
	hasGoListOutput := isGoListCmd && (strings.Contains(r.Stdout, "PASS") || strings.Contains(r.Stdout, "ok\t"))
	if r.Pass && strings.HasPrefix(cmd, "go test") {
		if !hasTestOutput && !hasGoListOutput {
			r.Pass = false
			r.Stderr = "tests_found: no test names found in output"
		}
		return r
	}
	if r.ExitCode >= 2 {
		r.Stderr = fmt.Sprintf("tests_found: test runner exited with code %d, likely no tests found", r.ExitCode)
		return r
	}
	if (r.ExitCode == 0 || r.ExitCode == 1) && (hasTestOutput || hasGoListOutput) {
		r.Pass = true
		return r
	}
	if r.Stdout != "" || r.Stderr != "" {
		r.Pass = true
	}
	return r
}
