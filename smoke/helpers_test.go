//go:build smoke

package smoke

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// #region agent log
const debugLogPath = "/Users/natsuki17/dev/gump/.cursor/debug-5773e0.log"
func writeDebugLog(data map[string]interface{}) {
	data["sessionId"] = "5773e0"
	data["timestamp"] = time.Now().UnixMilli()
	data["location"] = "smoke/helpers_test.go:runGump"
	data["message"] = "gump non-zero exit"
	data["hypothesisId"] = "H1"
	line, _ := json.Marshal(data)
	f, err := os.OpenFile(debugLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}
// #endregion

// requireAgent skips the test when the agent CLI is not installed so the smoke suite
// reports SKIP instead of FAIL for missing providers.
func requireAgent(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s CLI not installed, skipping", name)
	}
}

// setupSmokeRepo creates a minimal Go repo under t.TempDir() so each smoke test
// runs in isolation and cleanup is automatic.
func setupSmokeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Minimal module so compile/test validators can run.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/smoketest\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Ignore only runtime state so `.gump/conventions.md` can remain committable.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".gump/runs/\n.gump/worktrees/\n.gump/out/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	conventionsDir := filepath.Join(dir, ".gump")
	if err := os.MkdirAll(conventionsDir, 0755); err != nil {
		t.Fatal(err)
	}
	// WHY: module path example.com/smoketest leads models to emit package smoketest; root must stay package main with main.go.
	if err := os.WriteFile(filepath.Join(conventionsDir, "conventions.md"), []byte("# Conventions\n\nAll Go files in the repository root must use `package main` (same as main.go) so `go build` and `go test` succeed.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@a.com", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// writeSpec writes spec.md and commits so run has a clean spec to read.
func writeSpec(t *testing.T, repoDir, content string) {
	t.Helper()
	p := filepath.Join(repoDir, "spec.md")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "add", "spec.md")
	runGit(t, repoDir, "commit", "-m", "add spec")
}

// writeFile writes a file under the repo and commits it so run worktrees see it.
func writeFile(t *testing.T, repoDir, relPath, content string) {
	t.Helper()
	p := filepath.Join(repoDir, relPath)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "add", "-f", relPath)
	runGit(t, repoDir, "commit", "-m", "add "+relPath)
}

// writeWorkflow writes a custom workflow YAML under .gump/workflows/ for smoke tests.
func writeWorkflow(t *testing.T, repoDir, name, yaml string) {
	t.Helper()
	workflowsDir := filepath.Join(repoDir, ".gump", "workflows")
	if err := os.MkdirAll(workflowsDir, 0755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(workflowsDir, name+".yaml")
	if err := os.WriteFile(p, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "add", "-f", ".gump/workflows/"+name+".yaml")
	runGit(t, repoDir, "commit", "-m", "add workflow "+name)
}

var (
	gumpBinPath string
	gumpBinErr  error
	gumpBinOnce sync.Once
)

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

func ensureSmokeGumpBin() (string, error) {
	gumpBinOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "gump-smoke-bin-")
		if err != nil {
			gumpBinErr = err
			return
		}
		gumpBinPath = filepath.Join(tmpDir, "gump")

		moduleRoot := findModuleRoot()
		cmd := exec.Command("go", "build", "-o", gumpBinPath, ".")
		cmd.Dir = moduleRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			gumpBinErr = err
			_ = out
			return
		}
	})
	return gumpBinPath, gumpBinErr
}

// runGump runs the gump binary with a 5-minute timeout.
func runGump(t *testing.T, repoDir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	bin, err := ensureSmokeGumpBin()
	if err != nil || bin == "" {
		t.Fatalf("failed to build gump binary for smoke tests: %v", err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = repoDir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Env = os.Environ()
	// 5 minutes per invocation so a full smoke run stays bounded.
	if err := runWithTimeout(cmd, 5*time.Minute); err != nil {
		stdoutStr := outBuf.String()
		stderrStr := errBuf.String()
		code := -1
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ProcessState != nil {
			code = exitErr.ProcessState.ExitCode()
		}
		// #region agent log
		writeDebugLog(map[string]interface{}{
			"data": map[string]interface{}{
				"args": args, "exitCode": code,
				"stdout": stdoutStr, "stderr": stderrStr,
			},
		})
		// #endregion
		return stdoutStr, stderrStr, code
	}
	return outBuf.String(), errBuf.String(), 0
}

// runWithTimeout prevents a single long run from hanging the smoke run.
func runWithTimeout(cmd *exec.Cmd, d time.Duration) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		_ = cmd.Process.Kill()
		<-done
		return exec.ErrNotFound
	}
}

// latestRunDir picks the most recent run by directory mtime.
func latestRunDir(t *testing.T, repoDir string) string {
	t.Helper()
	runsDir := filepath.Join(repoDir, ".gump", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no run dirs in .gump/runs")
	}
	type ent struct {
		name  string
		mtime time.Time
	}
	var dirs []ent
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, ent{e.Name(), info.ModTime()})
	}
	if len(dirs) == 0 {
		t.Fatal("no run dirs in .gump/runs")
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mtime.After(dirs[j].mtime) })
	return filepath.Join(runsDir, dirs[0].name)
}


// assertRunPass ensures the last run completed successfully so we don't apply or assert
// on a failed run.
func assertRunPass(t *testing.T, repoDir string) {
	t.Helper()
	runDir := latestRunDir(t, repoDir)
	data, err := os.ReadFile(filepath.Join(runDir, "status.json"))
	if err != nil {
		t.Fatalf("read status.json: %v", err)
	}
	var st struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("status.json: %v", err)
	}
	if st.Status != "pass" {
		t.Fatalf("expected run status pass, got %s", st.Status)
	}
}


// assertFileExists checks that a path exists in the repo (after apply, files are in repo root).
func assertFileExists(t *testing.T, repoDir, path string) {
	t.Helper()
	full := filepath.Join(repoDir, path)
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("file %s: %v", path, err)
	}
}

// assertGoTestPasses confirms the code produced by the agent compiles and tests pass.
func assertGoTestPasses(t *testing.T, repoDir string) {
	t.Helper()
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test ./...: %v\n%s", err, out)
	}
}

// applyAndReset applies the last run then resets to main so the next test starts clean.
// Must only be called when the run passed.
func applyAndReset(t *testing.T, repoDir string) {
	t.Helper()
	if _, _, code := runGump(t, repoDir, "apply"); code != 0 {
		t.Fatalf("gump apply failed with exit %d", code)
	}
	runGit(t, repoDir, "checkout", "main")
	runGit(t, repoDir, "clean", "-fd")
}

// readLedger returns parsed NDJSON events from the latest run's manifest so tests can
// assert on agent_launched, step_started, etc.
func readLedger(t *testing.T, repoDir string) []map[string]interface{} {
	t.Helper()
	runDir := latestRunDir(t, repoDir)
	path := filepath.Join(runDir, "manifest.ndjson")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		out = append(out, ev)
	}
	return out
}
