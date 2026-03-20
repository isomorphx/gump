package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runPuddingBin(t *testing.T, binPath string, args []string, env map[string]string, dir string) (stdout, stderr string, exitCode int) {
	t.Helper()

	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Env = buildEnvForSubprocess(env)
	_ = cmd.Run()
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func cachePathFromHome(home string) string {
	return filepath.Join(home, ".pudding", "cache", "update-check.json")
}

func writeCache(t *testing.T, home string, checkedAt time.Time, latestVersion string) {
	t.Helper()
	path := cachePathFromHome(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := map[string]string{
		"checked_at":     checkedAt.UTC().Format(time.RFC3339),
		"latest_version": latestVersion,
	}
	data, _ := json.Marshal(payload)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readCacheCheckedAt(t *testing.T, home string) time.Time {
	t.Helper()
	path := cachePathFromHome(home)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		CheckedAt string `json:"checked_at"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339, payload.CheckedAt)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestE2E_VersionLDFlags(t *testing.T) {
	bin := binaryPathV99
	stdout, _, code := runPuddingBin(t, bin, []string{"--version"}, nil, "")
	if code != 0 {
		t.Fatalf("exit code %d stdout=%q", code, stdout)
	}
	if !strings.Contains(stdout, "v99.88.77") || !strings.Contains(stdout, "abc1234") || !strings.Contains(stdout, "2026-03-15") {
		t.Fatalf("stdout %q does not contain expected fields", stdout)
	}
}

func TestE2E_UpdateCheckSilencedOnDev(t *testing.T) {
	dir := setupRepoWithCommit(t)
	_, stderr, code := runPudding(t, []string{"cookbook", "list"}, nil, dir)
	if code != 0 {
		t.Fatalf("exit code %d stderr=%q", code, stderr)
	}
	if strings.Contains(stderr, "new version") || strings.Contains(stderr, "pudding.build") {
		t.Fatalf("unexpected update message on dev build: stderr=%q", stderr)
	}
}

func TestE2E_UpdateCheckDisabledByEnv(t *testing.T) {
	dir := setupRepoWithCommit(t)
	home := t.TempDir()
	writeCache(t, home, time.Now(), "v99.0.0")

	bin := binaryPathV001
	env := map[string]string{
		"HOME":                   home,
		"PUDDING_NO_UPDATE_CHECK": "1",
	}
	_, stderr, code := runPuddingBin(t, bin, []string{"cookbook", "list"}, env, dir)
	if code != 0 {
		t.Fatalf("exit code %d stderr=%q", code, stderr)
	}
	if strings.Contains(stderr, "new version") {
		t.Fatalf("unexpected update message when disabled by env: stderr=%q", stderr)
	}
}

func TestE2E_UpdateCheckDisabledByTOML(t *testing.T) {
	dir := setupRepoWithCommit(t)
	home := t.TempDir()
	writeCache(t, home, time.Now(), "v99.0.0")

	// Project config disables update check.
	if err := os.WriteFile(filepath.Join(dir, "pudding.toml"), []byte(`
[update]
check = false
`), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := binaryPathV001
	env := map[string]string{
		"HOME": home,
	}
	_, stderr, code := runPuddingBin(t, bin, []string{"cookbook", "list"}, env, dir)
	if code != 0 {
		t.Fatalf("exit code %d stderr=%q", code, stderr)
	}
	if strings.Contains(stderr, "new version") {
		t.Fatalf("unexpected update message when disabled by TOML: stderr=%q", stderr)
	}
}

func TestE2E_UpdateCheckCacheFreshShowsMessage(t *testing.T) {
	dir := setupRepoWithCommit(t)
	home := t.TempDir()
	writeCache(t, home, time.Now(), "v99.0.0")

	bin := binaryPathV001
	env := map[string]string{"HOME": home}
	_, stderr, code := runPuddingBin(t, bin, []string{"cookbook", "list"}, env, dir)
	if code != 0 {
		t.Fatalf("exit code %d stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "v99.0.0") || !strings.Contains(stderr, "pudding.build/install.sh") || !strings.Contains(stderr, "brew upgrade pudding") {
		t.Fatalf("expected update message in stderr: %q", stderr)
	}
}

func TestE2E_UpdateCheckCacheExpiredTriggersHTTP(t *testing.T) {
	dir := setupRepoWithCommit(t)
	home := t.TempDir()
	writeCache(t, home, time.Now().Add(-25*time.Hour), "v0.0.1")

	// Mock GitHub endpoint.
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/isomorphx/pudding/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v99.0.0"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	bin := binaryPathV001
	env := map[string]string{
		"HOME":                 home,
		"PUDDING_UPDATE_URL":  srv.URL + "/repos/isomorphx/pudding/releases/latest",
	}

	before := readCacheCheckedAt(t, home)
	_, stderr, code := runPuddingBin(t, bin, []string{"cookbook", "list"}, env, dir)
	if code != 0 {
		t.Fatalf("exit code %d stderr=%q", code, stderr)
	}
	after := readCacheCheckedAt(t, home)
	if !after.After(before) {
		t.Fatalf("expected cache checked_at to be updated (before=%s after=%s)", before, after)
	}
	if !strings.Contains(stderr, "v99.0.0") {
		t.Fatalf("expected update message in stderr after cache update: %q", stderr)
	}
}

func TestE2E_UpdateCheckMessageNotPrintedOnCommandFailure(t *testing.T) {
	dir := setupRepoWithCommit(t)
	home := t.TempDir()
	writeCache(t, home, time.Now(), "v99.0.0")

	bin := binaryPathV001
	env := map[string]string{"HOME": home}
	_, stderr, code := runPuddingBin(t, bin, []string{"cook", "nonexistent-spec.md", "--recipe", "freeform"}, env, dir)
	if code == 0 {
		t.Fatalf("expected failure exit code, got 0 stderr=%q", stderr)
	}
	if strings.Contains(stderr, "new version") || strings.Contains(stderr, "pudding.build/install.sh") {
		t.Fatalf("update message must not appear on command failure: stderr=%q", stderr)
	}
}

func TestE2E_UpdateCheckNotTriggeredOnHelpOrVersionFlags(t *testing.T) {
	dir := setupRepoWithCommit(t)
	home := t.TempDir()
	writeCache(t, home, time.Now(), "v99.0.0")

	bin := binaryPathV001
	env := map[string]string{"HOME": home}

	_, stderr, code := runPuddingBin(t, bin, []string{"--help"}, env, dir)
	if code != 0 {
		t.Fatalf("--help should succeed, code=%d stderr=%q", code, stderr)
	}
	if strings.Contains(stderr, "new version") {
		t.Fatalf("unexpected update message on --help: stderr=%q", stderr)
	}

	_, stderr, code = runPuddingBin(t, bin, []string{"--version"}, env, dir)
	if code != 0 {
		t.Fatalf("--version should succeed, code=%d stderr=%q", code, stderr)
	}
	if strings.Contains(stderr, "new version") {
		t.Fatalf("unexpected update message on --version: stderr=%q", stderr)
	}

	_, stderr, code = runPuddingBin(t, bin, []string{"cook", "--help"}, env, dir)
	if code != 0 {
		t.Fatalf("cook --help should succeed, code=%d stderr=%q", code, stderr)
	}
	if strings.Contains(stderr, "new version") {
		t.Fatalf("unexpected update message on cook --help: stderr=%q", stderr)
	}
}

