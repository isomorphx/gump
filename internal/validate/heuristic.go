package validate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/isomorphx/pudding/internal/config"
)

// IsOptionalValidator returns true for lint and coverage; they are skipped when the tool is not installed.
// compile and test are essential and fail with a clear message when the tool is absent.
func IsOptionalValidator(alias string) bool {
	return alias == "lint" || alias == "coverage"
}

// CheckCommandAvailable checks whether the first word of the command (the binary) is executable.
// Returns the binary name and true if found, or the name and false if absent.
// For commands containing $( or ` (substitution), returns (command, true) and lets the shell fail if needed.
func CheckCommandAvailable(command string) (binaryName string, available bool) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return "", false
	}
	// Do not try to resolve commands with shell substitution.
	if strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") {
		return command, true
	}
	// For chained commands, take the part before && or || only (not single & or |).
	if idx := strings.Index(cmd, " && "); idx >= 0 {
		cmd = strings.TrimSpace(cmd[:idx])
	} else if idx := strings.Index(cmd, " || "); idx >= 0 {
		cmd = strings.TrimSpace(cmd[:idx])
	}
	// First word is the binary (possibly a path).
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return "", false
	}
	binaryName = parts[0]
	_, err := exec.LookPath(binaryName)
	return binaryName, err == nil
}

// ResolveCommand picks the concrete shell command for a validation alias.
// Recipes stay stack-agnostic (e.g. "compile") while each repo gets the right command; config wins so teams can pin a command.
func ResolveCommand(alias string, cfg *config.Config, worktreeDir string) (string, error) {
	switch alias {
	case "compile":
		if cfg != nil && cfg.CompileCmd != "" {
			return cfg.CompileCmd, nil
		}
		if p := filepath.Join(worktreeDir, "go.mod"); pathExists(p) {
			return "go build ./...", nil
		}
		if p := filepath.Join(worktreeDir, "package.json"); pathExists(p) {
			return "npm run build", nil
		}
		if p := filepath.Join(worktreeDir, "Cargo.toml"); pathExists(p) {
			return "cargo build", nil
		}
		if makefileHasTarget(worktreeDir, "build") {
			return "make build", nil
		}
		if p := filepath.Join(worktreeDir, "pyproject.toml"); pathExists(p) {
			return `python -m py_compile $(find . -name '*.py' -not -path './.*')`, nil
		}
		return "", fmt.Errorf("cannot resolve 'compile': no known build system detected. Configure compile_cmd in pudding.toml")
	case "test":
		if cfg != nil && cfg.TestCmd != "" {
			return cfg.TestCmd, nil
		}
		if p := filepath.Join(worktreeDir, "go.mod"); pathExists(p) {
			return "go test ./...", nil
		}
		if p := filepath.Join(worktreeDir, "package.json"); pathExists(p) {
			return "npm test", nil
		}
		if p := filepath.Join(worktreeDir, "Cargo.toml"); pathExists(p) {
			return "cargo test", nil
		}
		if makefileHasTarget(worktreeDir, "test") {
			return "make test", nil
		}
		if p := filepath.Join(worktreeDir, "pyproject.toml"); pathExists(p) {
			return "python -m pytest", nil
		}
		return "", fmt.Errorf("cannot resolve 'test': no known test runner detected. Configure test_cmd in pudding.toml")
	case "lint":
		if cfg != nil && cfg.LintCmd != "" {
			return cfg.LintCmd, nil
		}
		if p := filepath.Join(worktreeDir, "go.mod"); pathExists(p) {
			return "golangci-lint run", nil
		}
		if p := filepath.Join(worktreeDir, "package.json"); pathExists(p) {
			return "npm run lint", nil
		}
		if p := filepath.Join(worktreeDir, "Cargo.toml"); pathExists(p) {
			return "cargo clippy", nil
		}
		return "", fmt.Errorf("cannot resolve 'lint': no known linter detected. Configure lint_cmd in pudding.toml")
	case "coverage":
		if cfg != nil && cfg.CoverageCmd != "" {
			return cfg.CoverageCmd, nil
		}
		if p := filepath.Join(worktreeDir, "go.mod"); pathExists(p) {
			return "go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out", nil
		}
		if p := filepath.Join(worktreeDir, "package.json"); pathExists(p) {
			return "npm test -- --coverage", nil
		}
		if p := filepath.Join(worktreeDir, "Cargo.toml"); pathExists(p) {
			return "cargo tarpaulin --out Stdout", nil
		}
		return "", fmt.Errorf("cannot resolve 'coverage': no known coverage tool detected. Configure coverage_cmd in pudding.toml")
	default:
		return "", fmt.Errorf("unknown alias %q", alias)
	}
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// makefileHasTarget returns true if the Makefile defines the given target so we can suggest make build/test.
func makefileHasTarget(worktreeDir, target string) bool {
	data, err := os.ReadFile(filepath.Join(worktreeDir, "Makefile"))
	if err != nil {
		return false
	}
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:`)
	return re.MatchString(string(data))
}
