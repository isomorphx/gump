package validate

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/workflow"
)

const defaultShellTimeout = 10 * time.Minute

// validationTimeout returns the shell validator budget from cfg when set; otherwise the default.
func validationTimeout(cfg *config.Config) time.Duration {
	if cfg == nil {
		return defaultShellTimeout
	}
	if cfg.ValidationTimeout > 0 {
		return cfg.ValidationTimeout
	}
	return defaultShellTimeout
}

// RunShellValidator runs a shell command in the worktree and returns a SingleResult.
// We capture stdout and stderr separately so failed validators can feed {error} and logs without mixing streams.
func RunShellValidator(command string, worktreeDir string, timeout time.Duration) *SingleResult {
	if timeout <= 0 {
		timeout = defaultShellTimeout
	}
	start := time.Now()
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = worktreeDir
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		elapsed := time.Since(start)
		return &SingleResult{Validator: "shell", Pass: false, ExitCode: -1, Stderr: err.Error(), Duration: elapsed}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var exitCode int
	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}
		return &SingleResult{
			Validator: "shell",
			Pass:      exitCode == 0,
			ExitCode:  exitCode,
			Stdout:    stdoutBuf.String(),
			Stderr:    stderrBuf.String(),
			Duration:  elapsed,
		}
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		elapsed := time.Since(start)
		return &SingleResult{
			Validator: "shell",
			Pass:      false,
			ExitCode:  -1,
			Stderr:    fmt.Sprintf("validation command timed out after %s", timeout),
			Duration:  elapsed,
		}
	}
}

func validatorConfigKey(alias string) string {
	switch alias {
	case "compile":
		return "compile_cmd"
	case "test":
		return "test_cmd"
	case "lint":
		return "lint_cmd"
	case "coverage":
		return "coverage_cmd"
	default:
		return alias + "_cmd"
	}
}

// runShellValidatorWithAvailabilityCheck resolves the command, checks if the binary is available,
// and returns a skip (optional) or fail (essential) result if not; otherwise runs the command.
func runShellValidatorWithAvailabilityCheck(cfg *config.Config, alias, command string, worktreeDir string) *SingleResult {
	binaryName, available := CheckCommandAvailable(command)
	if !available {
		if IsOptionalValidator(alias) {
			msg := fmt.Sprintf("%s: '%s' is not installed — skipping. Install %s or configure %s in gump.toml.",
				alias, binaryName, binaryName, validatorConfigKey(alias))
			return &SingleResult{
				Validator: alias + " (skipped)",
				Pass:      true,
				Skipped:   true,
				ExitCode:  0,
				Stdout:    msg,
				Stderr:    "",
			}
		}
		msg := fmt.Sprintf("%s: '%s' is not installed or not in PATH. Install %s to use the '%s' validator, or configure %s in gump.toml.",
			alias, binaryName, binaryName, alias, validatorConfigKey(alias))
		return &SingleResult{Validator: alias, Pass: false, Stderr: msg}
	}
	r := RunShellValidator(command, worktreeDir, validationTimeout(cfg))
	r.Validator = alias
	return r
}

// RunCompileValidator resolves compile command and runs it so recipes can stay stack-agnostic.
func RunCompileValidator(cfg *config.Config, worktreeDir string) *SingleResult {
	cmd, err := ResolveCommand("compile", cfg, worktreeDir)
	if err != nil {
		return &SingleResult{Validator: "compile", Pass: false, Stderr: err.Error()}
	}
	return runShellValidatorWithAvailabilityCheck(cfg, "compile", cmd, worktreeDir)
}

// RunTestValidator resolves test command and runs it.
func RunTestValidator(cfg *config.Config, worktreeDir string) *SingleResult {
	cmd, err := ResolveCommand("test", cfg, worktreeDir)
	if err != nil {
		return &SingleResult{Validator: "test", Pass: false, Stderr: err.Error()}
	}
	r := runShellValidatorWithAvailabilityCheck(cfg, "test", cmd, worktreeDir)
	// go test ./... with no Go packages exits 1 and prints "no packages to test" or "no test files"; treat as pass so we don't retry forever.
	if !r.Pass && r.ExitCode == 1 && strings.HasPrefix(strings.TrimSpace(cmd), "go test") {
		stderr := r.Stderr
		if strings.Contains(stderr, "no packages to test") || strings.Contains(stderr, "no test files") {
			r.Pass = true
			r.Validator = "test (no packages)"
		}
	}
	return r
}

// RunLintValidator resolves lint command and runs it.
func RunLintValidator(cfg *config.Config, worktreeDir string) *SingleResult {
	cmd, err := ResolveCommand("lint", cfg, worktreeDir)
	if err != nil {
		return &SingleResult{Validator: "lint (skipped)", Pass: true, Skipped: true, Stdout: "lint: no known linter detected for this project — skipping. Configure lint_cmd in gump.toml."}
	}
	return runShellValidatorWithAvailabilityCheck(cfg, "lint", cmd, worktreeDir)
}

// RunBashValidator runs the validator's Arg as the shell command for fully custom checks.
func RunBashValidator(v workflow.GateEntry, worktreeDir string, cfg *config.Config) *SingleResult {
	r := RunShellValidator(v.Arg, worktreeDir, validationTimeout(cfg))
	r.Validator = "bash: " + v.Arg
	return r
}

var (
	goTotalRE    = regexp.MustCompile(`total:\s+\(statements\)\s+(\d+\.\d+)%`)
	goCoverageRE = regexp.MustCompile(`coverage:\s+(\d+\.?\d*)%`)
	anyPctRE     = regexp.MustCompile(`(\d+\.?\d*)%`)
)

// RunCoverageValidator runs the coverage command and enforces a minimum percentage for quality gates.
func RunCoverageValidator(threshold string, cfg *config.Config, worktreeDir string) *SingleResult {
	thresh, err := strconv.ParseFloat(threshold, 64)
	if err != nil || threshold == "" {
		return &SingleResult{Validator: "coverage: " + threshold, Pass: false, Stderr: "coverage: invalid threshold '" + threshold + "'"}
	}
	cmd, err := ResolveCommand("coverage", cfg, worktreeDir)
	if err != nil {
		return &SingleResult{Validator: "coverage (skipped)", Pass: true, Skipped: true, Stdout: "coverage: no known coverage tool detected for this project — skipping. Configure coverage_cmd in gump.toml."}
	}
	binaryName, available := CheckCommandAvailable(cmd)
	if !available {
		msg := fmt.Sprintf("coverage: '%s' is not installed — skipping. Install %s or configure coverage_cmd in gump.toml.", binaryName, binaryName)
		return &SingleResult{Validator: "coverage (skipped)", Pass: true, Skipped: true, ExitCode: 0, Stdout: msg}
	}
	r := RunShellValidator(cmd, worktreeDir, validationTimeout(cfg))
	r.Validator = "coverage: " + threshold
	if !r.Pass {
		r.Stderr = "coverage: command failed: " + r.Stderr
		return r
	}
	stdout := r.Stdout
	var pct float64
	if all := goTotalRE.FindAllStringSubmatch(stdout, -1); len(all) > 0 && len(all[len(all)-1]) > 1 {
		pct, _ = strconv.ParseFloat(all[len(all)-1][1], 64)
	} else if all := goCoverageRE.FindAllStringSubmatch(stdout, -1); len(all) > 0 && len(all[len(all)-1]) > 1 {
		pct, _ = strconv.ParseFloat(all[len(all)-1][1], 64)
	} else if m := anyPctRE.FindStringSubmatch(stdout); len(m) > 1 {
		pct, _ = strconv.ParseFloat(m[1], 64)
	}
	if pct == 0 {
		if m := anyPctRE.FindStringSubmatch(r.Stderr); len(m) > 1 {
			pct, _ = strconv.ParseFloat(m[1], 64)
		}
	}
	if pct == 0 && !anyPctRE.MatchString(stdout) && !anyPctRE.MatchString(r.Stderr) {
		return &SingleResult{Validator: r.Validator, Pass: false, Stderr: "coverage: could not parse coverage percentage from output"}
	}
	if pct >= thresh {
		r.Pass = true
		r.Stdout = fmt.Sprintf("coverage: %.1f%% (threshold: %.1f%%)", pct, thresh)
		return r
	}
	r.Pass = false
	r.Stderr = fmt.Sprintf("coverage: %.1f%% is below threshold %.1f%%", pct, thresh)
	return r
}
