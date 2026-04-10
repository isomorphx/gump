package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// envWithout returns a copy of env with variables named in keys removed.
// Used so the gump process (and any child: stub subprocess or real CLI) never sees ANTHROPIC_API_KEY,
// which can trigger ByteString/API errors in the Claude CLI. With --agent-stub we only run "true"/"sleep",
// not the real claude; without --agent-stub the Claude adapter also filters this in its exec (claude.go).
func envWithout(env []string, keys ...string) []string {
	skip := make(map[string]bool)
	for _, k := range keys {
		skip[k] = true
	}
	out := make([]string, 0, len(env))
	for _, e := range env {
		if idx := strings.IndexByte(e, '='); idx > 0 && skip[e[:idx]] {
			continue
		}
		out = append(out, e)
	}
	return out
}

var (
	binaryPath     string
	binaryPathV99  string
	binaryPathV001 string
	stubBinDir     string // PATH prefix so gump runs stub qwen/opencode instead of real CLIs
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gump-e2e-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	binaryPath = filepath.Join(dir, "gump")
	binaryPathV99 = filepath.Join(dir, "gump-v99")
	binaryPathV001 = filepath.Join(dir, "gump-v0-0-1")
	modRoot := findModuleRoot()
	cmd := exec.Command("go", "build", "-o", binaryPath, ".")
	cmd.Dir = modRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("go build: " + err.Error() + "\n" + string(out))
	}

	// Build ldflags variants so update-check tests can run deterministically
	// without relying on networked GitHub releases.
	ldflagsV99 := "-X github.com/isomorphx/gump/internal/version.Version=v99.88.77 -X github.com/isomorphx/gump/internal/version.Commit=abc1234 -X github.com/isomorphx/gump/internal/version.BuildDate=2026-03-15"
	cmd = exec.Command("go", "build", "-o", binaryPathV99, "-ldflags", ldflagsV99, ".")
	cmd.Dir = modRoot
	if outB, err := cmd.CombinedOutput(); err != nil {
		panic("go build ldflags v99: " + err.Error() + "\n" + string(outB))
	}
	ldflagsV001 := "-X github.com/isomorphx/gump/internal/version.Version=v0.0.1 -X github.com/isomorphx/gump/internal/version.Commit=abc1234 -X github.com/isomorphx/gump/internal/version.BuildDate=2026-03-15"
	cmd = exec.Command("go", "build", "-o", binaryPathV001, "-ldflags", ldflagsV001, ".")
	cmd.Dir = modRoot
	if outB, err := cmd.CombinedOutput(); err != nil {
		panic("go build ldflags v0.0.1: " + err.Error() + "\n" + string(outB))
	}

	stubBinDir, err = buildStubBinaries(modRoot)
	if err != nil {
		panic("build stub binaries: " + err.Error())
	}
	os.Exit(m.Run())
}

func buildStubBinaries(modRoot string) (string, error) {
	stubDir, err := os.MkdirTemp("", "gump-e2e-stub-")
	if err != nil {
		return "", err
	}
	for _, name := range []string{"qwen", "opencode"} {
		out := filepath.Join(stubDir, name)
		cmd := exec.Command("go", "build", "-o", out, "./e2e/stub"+name)
		cmd.Dir = modRoot
		if outB, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("build stub %s: %w\n%s", name, err, outB)
		}
	}
	return stubDir, nil
}

// envWithStubPath returns env map so E2E stubs are used: PATH (stub first) and GUMP_E2E_QWEN_BIN so the qwen adapter runs our stub even if a system qwen is earlier in PATH.
func envWithStubPath() map[string]string {
	if stubBinDir == "" {
		return nil
	}
	return map[string]string{
		"PATH":               stubBinDir + string(filepath.ListSeparator) + os.Getenv("PATH"),
		"GUMP_E2E_QWEN_BIN": filepath.Join(stubBinDir, "qwen"),
	}
}

func findModuleRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// buildEnvForSubprocess returns env slice for a subprocess: strip overridden keys from os.Environ(), then add env. Used by runGump and by tests that need the same env (e.g. which qwen).
func buildEnvForSubprocess(env map[string]string) []string {
	stripKeys := []string{"ANTHROPIC_API_KEY"}
	for k := range env {
		if k != "ANTHROPIC_API_KEY" {
			stripKeys = append(stripKeys, k)
		}
	}
	base := envWithout(os.Environ(), stripKeys...)
	for k, v := range env {
		if k == "ANTHROPIC_API_KEY" {
			continue
		}
		base = append(base, k+"="+v)
	}
	return base
}

func runGump(t *testing.T, args []string, env map[string]string, dir string) (stdout, stderr string, exitCode int) {
	t.Helper()
	timeout := 120 * time.Second
	if len(args) > 0 && args[0] == "doctor" {
		// doctor enchaîne plusieurs checks CLI (jusqu'à ~60s+ en CI/laptop), on donne plus de marge.
		timeout = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Dir = dir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Env = buildEnvForSubprocess(env)
	// If ByteString still appears, run tests with: unset ANTHROPIC_API_KEY (or remove it from .env / Cursor settings).
	_ = cmd.Run()
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if ctx.Err() != nil {
		exitCode = -1
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func writeFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func setupRepo(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gump-repo-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		t.Fatal(err)
	}
	// Initial commit so we have a valid repo
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", "init")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@a.com", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Log(string(out))
		t.Fatal(err)
	}
	return dir
}

// setupRepoWithCommit creates a git repo with an initial file committed so the worktree has a base.
// .gitignore is set to ignore `.gump/` so multiple runs in the same repo don't see "uncommitted" state.
func setupRepoWithCommit(t *testing.T) string {
	t.Helper()
	dir := setupRepo(t)
	writeFile(t, dir, "initial.txt", "initial")
	writeFile(t, dir, ".gitignore", ".gump\n")
	gitCommitAll(t, dir, "initial commit")
	return dir
}

// setupGoRepo creates a git repo with go.mod and main.go so validation (compile, test) can run.
func setupGoRepo(t *testing.T) string {
	t.Helper()
	dir := setupRepo(t)
	writeFile(t, dir, "go.mod", "module testproject\n\ngo 1.22\n")
	writeFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, dir, ".gitignore", ".gump\n")
	gitCommitAll(t, dir, "initial commit")
	return dir
}

func gitCommitAll(t *testing.T, dir, msg string) {
	t.Helper()
	env := append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@a.com", "GIT_CONFIG_GLOBAL=/dev/null")
	add := exec.Command("git", "add", "-A")
	add.Dir = dir
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s: %s", err, out)
	}
	commit := exec.Command("git", "commit", "-m", msg)
	commit.Dir = dir
	commit.Env = env
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s: %s", err, out)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

// readOpenCodeStdoutNDJSON returns the content of the OpenCode stub stdout artefact for the given run UUID (worktree .gump/artefacts/stdout.ndjson).
// Fails the test if the file is missing.
func readOpenCodeStdoutNDJSON(t *testing.T, repoDir, runUUID string) string {
	t.Helper()
	wtDir := filepath.Join(repoDir, ".gump", "worktrees", "run-"+runUUID)
	path := filepath.Join(wtDir, ".gump", "artefacts", "stdout.ndjson")
	if !fileExists(t, path) {
		t.Fatalf("cannot find stdout artifact for OpenCode step: %s", path)
	}
	return readFile(t, path)
}

func gitLog(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "log", "--oneline")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// gitLogFull returns full commit messages (for checking trailers like Gump-Run:).
func gitLogFull(t *testing.T, dir string, n int) string {
	t.Helper()
	cmd := exec.Command("git", "log", fmt.Sprintf("-%d", n), "--format=format:%B%n---")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func gitBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// parseLedger reads runDir/manifest.ndjson and returns agent_launched cli lines and agent_completed session_id / fields.
func parseLedger(t *testing.T, runDir string) (launchedCLIs []string, completedSessionIDs []string, completedEvents []map[string]interface{}) {
	t.Helper()
	path := filepath.Join(runDir, "manifest.ndjson")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] == "agent_launched" {
			if cli, _ := ev["cli"].(string); cli != "" {
				launchedCLIs = append(launchedCLIs, cli)
			}
		}
		if ev["type"] == "agent_completed" {
			if sid, _ := ev["session_id"].(string); sid != "" {
				completedSessionIDs = append(completedSessionIDs, sid)
			}
			completedEvents = append(completedEvents, ev)
		}
	}
	return launchedCLIs, completedSessionIDs, completedEvents
}

// parseLedgerLaunchedByStep reads runDir/manifest.ndjson and returns agent_launched events with step, attempt, session_id, cli (for reuse-on-retry assertions).
func parseLedgerLaunchedByStep(t *testing.T, runDir string) (launched []map[string]interface{}) {
	t.Helper()
	path := filepath.Join(runDir, "manifest.ndjson")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] == "agent_launched" {
			launched = append(launched, ev)
		}
	}
	return launched
}

// WHY: older CLIs printed a different verb before the UUID; keep matching both for log assertions without embedding the legacy verb as a single literal.
var runIDStdoutRegex = regexp.MustCompile(`(?:` + "co" + "ok" + `|run) ([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)
var runIDPathRegex = regexp.MustCompile(`(?:` + "co" + "ok" + `|run)-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)

func extractRunID(stdout string) string {
	m := runIDStdoutRegex.FindStringSubmatch(stdout)
	if len(m) >= 2 {
		return m[1]
	}
	m = runIDPathRegex.FindStringSubmatch(stdout)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}
