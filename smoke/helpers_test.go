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
	"testing"
	"time"
)

// #region agent log
const debugLogPath = "/Users/natsuki17/dev/pudding/.cursor/debug-5773e0.log"
func writeDebugLog(data map[string]interface{}) {
	data["sessionId"] = "5773e0"
	data["timestamp"] = time.Now().UnixMilli()
	data["location"] = "smoke/helpers_test.go:runPudding"
	data["message"] = "pudding non-zero exit"
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
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".pudding\n"), 0644); err != nil {
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

// writeSpec writes spec.md and commits so cook has a clean spec to read.
func writeSpec(t *testing.T, repoDir, content string) {
	t.Helper()
	p := filepath.Join(repoDir, "spec.md")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "add", "spec.md")
	runGit(t, repoDir, "commit", "-m", "add spec")
}

// writeFile writes a file under the repo and commits it so cook worktrees see it (e.g. .pudding-test-scenario.json, .pudding-test-plan.json).
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

// writeRecipe writes a custom recipe so cross-provider and session/retry tests can use their own steps.
func writeRecipe(t *testing.T, repoDir, name, yaml string) {
	t.Helper()
	recipesDir := filepath.Join(repoDir, ".pudding", "recipes")
	if err := os.MkdirAll(recipesDir, 0755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(recipesDir, name+".yaml")
	if err := os.WriteFile(p, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "add", "-f", ".pudding/recipes/"+name+".yaml")
	runGit(t, repoDir, "commit", "-m", "add recipe "+name)
}

// runPudding runs the pudding binary from PATH (as installable by users) with a 5-minute timeout
// so long cooks don't hang the suite.
func runPudding(t *testing.T, repoDir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	bin, err := exec.LookPath("pudding")
	if err != nil {
		t.Fatalf("pudding not in PATH: %v (run: go install)", err)
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

// runWithTimeout prevents a single long cook from hanging the smoke run.
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

// latestCookDir picks the most recent cook by directory mtime so assertions target
// the run we just performed when multiple cooks exist (e.g. after GC test).
func latestCookDir(t *testing.T, repoDir string) string {
	t.Helper()
	cooksDir := filepath.Join(repoDir, ".pudding", "cooks")
	entries, err := os.ReadDir(cooksDir)
	if err != nil {
		t.Fatalf("list cooks: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no cook dirs in .pudding/cooks")
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
		t.Fatal("no cook dirs in .pudding/cooks")
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mtime.After(dirs[j].mtime) })
	return filepath.Join(cooksDir, dirs[0].name)
}

// assertCookPass ensures the last cook completed successfully so we don't apply or assert
// on a failed run.
func assertCookPass(t *testing.T, repoDir string) {
	t.Helper()
	cookDir := latestCookDir(t, repoDir)
	data, err := os.ReadFile(filepath.Join(cookDir, "status.json"))
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
		t.Fatalf("expected cook status pass, got %s", st.Status)
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

// applyAndReset applies the last cook then resets to main so the next test starts clean.
// Must only be called when the cook passed.
func applyAndReset(t *testing.T, repoDir string) {
	t.Helper()
	if _, _, code := runPudding(t, repoDir, "apply"); code != 0 {
		t.Fatalf("pudding apply failed with exit %d", code)
	}
	runGit(t, repoDir, "checkout", "main")
	runGit(t, repoDir, "clean", "-fd")
}

// readLedger returns parsed NDJSON events from the latest cook's manifest so tests can
// assert on agent_launched, step_started, etc.
func readLedger(t *testing.T, repoDir string) []map[string]interface{} {
	t.Helper()
	cookDir := latestCookDir(t, repoDir)
	path := filepath.Join(cookDir, "manifest.ndjson")
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
