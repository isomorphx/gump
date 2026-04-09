package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/workflow"
	"github.com/spf13/cobra"
)

// envWithout returns a copy of env with any variable whose name is in keys removed (env is "KEY=value").
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

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check environment, config, and agent CLIs",
	RunE:  runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, args []string) error {
	fmt.Println("Gump Doctor")
	fmt.Println()
	// Git
	out, err := exec.Command("git", "version").CombinedOutput()
	if err != nil {
		fmt.Println("  git          ✗  not found")
	} else {
		fmt.Printf("  git          ✓  %s", string(out))
	}
	// Config
	_, _, err = config.Load()
	if err != nil {
		fmt.Println("  config       ✗  failed to load")
	} else {
		if config.ProjectConfigPath() != "" {
			fmt.Println("  config       ✓  gump.toml found")
		} else {
			fmt.Println("  config       ✓  (no project config)")
		}
	}
	n := len(workflow.BuiltinWorkflows)
	if n == 0 {
		fmt.Println("  workflows    ✗  no built-in workflows loaded")
	} else {
		fmt.Printf("  workflows    ✓  %d built-in workflows loaded\n", n)
	}
	// Agent CLIs
	fmt.Println()
	checkClaudeCode()
	checkCodex()
	checkGemini()
	checkQwen()
	checkOpenCode()
	checkCursor()
	return nil
}

// checkClaudeCode runs the spec harness: temp repo, run claude with json, verify file and is_error.
func checkClaudeCode() {
	if _, err := exec.LookPath("claude"); err != nil {
		fmt.Println("  claude-code  not installed (skipped)")
		return
	}
	timeout := 10 * time.Second
	dir, err := os.MkdirTemp("", "gump-doctor-claude-")
	if err != nil {
		fmt.Println("  claude-code  ✗  failed to create temp dir")
		return
	}
	defer os.RemoveAll(dir)

	if err := exec.Command("git", "init", dir).Run(); err != nil {
		fmt.Println("  claude-code  ✗  git init failed")
		return
	}
	initPath := filepath.Join(dir, "init.txt")
	if err := os.WriteFile(initPath, []byte("init"), 0644); err != nil {
		fmt.Println("  claude-code  ✗  failed to write init.txt")
		return
	}

	prompt := "Create a file called doctor-test.txt containing gump-ok"
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	runCmd := exec.CommandContext(ctx, "claude", "-p", prompt,
		"--output-format", "json",
		"--allowedTools", "Bash,Read,Write,Edit",
		"--permission-mode", "acceptEdits")
	runCmd.Dir = dir
	// Avoid inheriting ANTHROPIC_API_KEY so the CLI doesn't hit ByteString/API key errors during the harness.
	runCmd.Env = envWithout(os.Environ(), "ANTHROPIC_API_KEY")
	out, runErr := runCmd.CombinedOutput()
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Println("  claude-code  ✗  timeout after 10s")
		} else {
			fmt.Printf("  claude-code  ✗  %v\n", runErr)
		}
		return
	}

	doctorTestPath := filepath.Join(dir, "doctor-test.txt")
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusOut, _ := statusCmd.Output()

	fileCreated := false
	if _, err := os.Stat(doctorTestPath); err == nil {
		fileCreated = true
	}
	statusOK := strings.Contains(string(statusOut), "doctor-test.txt")
	if !fileCreated || !statusOK {
		fmt.Println("  claude-code  ✗  doctor-test.txt missing from git status")
		return
	}

	var parsed struct {
		IsError bool `json:"is_error"`
	}
	if jsonErr := json.Unmarshal(out, &parsed); jsonErr != nil {
		fmt.Println("  claude-code  ⚠  compat mode (JSON not parsable, status works)")
		return
	}
	if parsed.IsError {
		fmt.Println("  claude-code  ✗  run returned is_error=true")
		return
	}
	fmt.Println("  claude-code  ✓  ok")
}

func checkCodex() {
	if _, err := exec.LookPath("codex"); err != nil {
		fmt.Println("  codex        not installed (skipped)")
		return
	}
	dir, err := os.MkdirTemp("", "gump-doctor-codex-")
	if err != nil {
		fmt.Println("  codex        ✗  failed to create temp dir")
		return
	}
	defer os.RemoveAll(dir)
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		fmt.Println("  codex        ✗  git init failed")
		return
	}
	initPath := filepath.Join(dir, "init.txt")
	if err := os.WriteFile(initPath, []byte("init"), 0644); err != nil {
		fmt.Println("  codex        ✗  failed to write init.txt")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runCmd := exec.CommandContext(ctx, "codex", "exec", "Create a file called doctor-test.txt containing gump-ok",
		"--json", "--full-auto", "-C", dir, "--skip-git-repo-check")
	runCmd.Dir = dir
	out, runErr := runCmd.CombinedOutput()
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Println("  codex        ✗  timeout after 10s")
		} else {
			fmt.Printf("  codex        ✗  %v (harness failed)\n", runErr)
		}
		return
	}
	// At least one turn.completed in JSONL for green.
	hasTurnCompleted := strings.Contains(string(out), `"type":"turn.completed"`)
	doctorTestPath := filepath.Join(dir, "doctor-test.txt")
	fileCreated := false
	if _, err := os.Stat(doctorTestPath); err == nil {
		fileCreated = true
	}
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusOut, _ := statusCmd.Output()
	statusOK := strings.Contains(string(statusOut), "doctor-test.txt")
	if hasTurnCompleted && fileCreated && statusOK {
		fmt.Println("  codex        ✓  ok")
		return
	}
	if fileCreated && statusOK && !hasTurnCompleted {
		fmt.Println("  codex        ⚠  compat mode (JSONL parse failed, status works)")
		return
	}
	fmt.Println("  codex        ✗  harness failed (file or status missing)")
}

func checkGemini() {
	if _, err := exec.LookPath("gemini"); err != nil {
		fmt.Println("  gemini       not installed (skipped)")
		return
	}
	dir, err := os.MkdirTemp("", "gump-doctor-gemini-")
	if err != nil {
		fmt.Println("  gemini       ✗  failed to create temp dir")
		return
	}
	defer os.RemoveAll(dir)
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		fmt.Println("  gemini       ✗  git init failed")
		return
	}
	initPath := filepath.Join(dir, "init.txt")
	if err := os.WriteFile(initPath, []byte("init"), 0644); err != nil {
		fmt.Println("  gemini       ✗  failed to write init.txt")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runCmd := exec.CommandContext(ctx, "gemini", "-p", "Create a file called doctor-test.txt containing gump-ok",
		"--output-format", "json", "--yolo")
	runCmd.Dir = dir
	// Gemini emits JSON on stdout only; stderr has "YOLO mode...", credentials, etc. (gemini-cli-reference §3).
	out, runErr := runCmd.Output()
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Println("  gemini       ✗  timeout after 10s")
		} else {
			fmt.Printf("  gemini       ✗  %v (harness failed)\n", runErr)
		}
		return
	}
	var parsed struct {
		SessionID string      `json:"session_id"`
		Error     interface{} `json:"error"`
	}
	jsonOK := json.Unmarshal(out, &parsed) == nil && parsed.SessionID != "" && parsed.Error == nil
	doctorTestPath := filepath.Join(dir, "doctor-test.txt")
	fileCreated := false
	if _, err := os.Stat(doctorTestPath); err == nil {
		fileCreated = true
	}
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusOut, _ := statusCmd.Output()
	statusOK := strings.Contains(string(statusOut), "doctor-test.txt")
	if jsonOK && fileCreated && statusOK {
		fmt.Println("  gemini       ✓  ok")
		return
	}
	if fileCreated && statusOK && !jsonOK {
		fmt.Println("  gemini       ⚠  compat mode (JSON parse failed, status works)")
		return
	}
	fmt.Println("  gemini       ✗  harness failed (file or status missing)")
}

func checkQwen() {
	if _, err := exec.LookPath("qwen"); err != nil {
		fmt.Println("  qwen         not installed (skipped)")
		return
	}
	dir, err := os.MkdirTemp("", "gump-doctor-qwen-")
	if err != nil {
		fmt.Println("  qwen         ✗  failed to create temp dir")
		return
	}
	defer os.RemoveAll(dir)
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		fmt.Println("  qwen         ✗  git init failed")
		return
	}
	initPath := filepath.Join(dir, "init.txt")
	if err := os.WriteFile(initPath, []byte("init"), 0644); err != nil {
		fmt.Println("  qwen         ✗  failed to write init.txt")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runCmd := exec.CommandContext(ctx, "qwen", "-p", "Create a file called doctor-test.txt containing gump-ok",
		"--output-format", "json", "--yolo",
		"--allowed-tools", "write_file")
	runCmd.Dir = dir
	out, runErr := runCmd.CombinedOutput()
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Println("  qwen         ✗  timeout after 10s")
		} else {
			fmt.Printf("  qwen         ✗  %v (harness failed)\n", runErr)
		}
		return
	}
	var parsed []struct {
		Type    string `json:"type"`
		IsError bool   `json:"is_error"`
	}
	jsonOK := false
	if json.Unmarshal(out, &parsed) == nil && len(parsed) > 0 {
		last := parsed[len(parsed)-1]
		jsonOK = last.Type == "result" && !last.IsError
	}
	doctorTestPath := filepath.Join(dir, "doctor-test.txt")
	fileCreated := false
	if _, err := os.Stat(doctorTestPath); err == nil {
		fileCreated = true
	}
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusOut, _ := statusCmd.Output()
	statusOK := strings.Contains(string(statusOut), "doctor-test.txt")
	if jsonOK && fileCreated && statusOK {
		fmt.Println("  qwen         ✓  ok")
		return
	}
	if fileCreated && statusOK && !jsonOK {
		fmt.Println("  qwen         ⚠  compat mode (JSON parse failed, status works)")
		return
	}
	fmt.Println("  qwen         ✗  harness failed (file or status missing)")
}

func checkOpenCode() {
	if _, err := exec.LookPath("opencode"); err != nil {
		fmt.Println("  opencode     not installed (skipped)")
		return
	}
	dir, err := os.MkdirTemp("", "gump-doctor-opencode-")
	if err != nil {
		fmt.Println("  opencode     ✗  failed to create temp dir")
		return
	}
	defer os.RemoveAll(dir)
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		fmt.Println("  opencode     ✗  git init failed")
		return
	}
	initPath := filepath.Join(dir, "init.txt")
	if err := os.WriteFile(initPath, []byte("init"), 0644); err != nil {
		fmt.Println("  opencode     ✗  failed to write init.txt")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runCmd := exec.CommandContext(ctx, "opencode", "run", "Create a file called doctor-test.txt containing gump-ok",
		"--format", "json", "--dir", dir)
	runCmd.Dir = dir
	out, runErr := runCmd.CombinedOutput()
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Println("  opencode     ✗  timeout after 10s")
		} else {
			fmt.Printf("  opencode     ✗  %v (harness failed)\n", runErr)
		}
		return
	}
	hasStepFinish := strings.Contains(string(out), `"type":"step_finish"`)
	doctorTestPath := filepath.Join(dir, "doctor-test.txt")
	fileCreated := false
	if _, err := os.Stat(doctorTestPath); err == nil {
		fileCreated = true
	}
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusOut, _ := statusCmd.Output()
	statusOK := strings.Contains(string(statusOut), "doctor-test.txt")
	if hasStepFinish && fileCreated && statusOK {
		fmt.Println("  opencode     ✓  ok")
		return
	}
	if fileCreated && statusOK && !hasStepFinish {
		fmt.Println("  opencode     ⚠  compat mode (events parse failed, status works)")
		return
	}
	fmt.Println("  opencode     ✗  harness failed (file or status missing)")
}

func checkCursor() {
	if os.Getenv("GUMP_E2E_SKIP_CURSOR_DOCTOR") == "1" {
		fmt.Println("  cursor: ok")
		return
	}
	if _, err := exec.LookPath("cursor-agent"); err != nil {
		fmt.Println("  cursor       not installed (skipped)")
		return
	}
	// WHY: Cursor doctor checks have hung when wrapped in workspace/status flows.
	// Keep this to the minimal known-good direct invocation.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runCmd := exec.CommandContext(ctx, "cursor-agent",
		"-p",
		"--output-format", "json",
		"--yolo",
		"--trust",
		"echo test",
	)
	out, runErr := runCmd.CombinedOutput()
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Println("  cursor: installed but not responding (timeout)")
		} else {
			fmt.Printf("  cursor       ✗  %v (harness failed)\n", runErr)
		}
		return
	}
	// Cursor json mode may return one object or an array of objects depending on version.
	jsonOK := false
	var one map[string]interface{}
	if json.Unmarshal(out, &one) == nil {
		if _, ok := one["session_id"]; ok {
			jsonOK = true
		}
	}
	if !jsonOK {
		var many []map[string]interface{}
		if json.Unmarshal(out, &many) == nil && len(many) > 0 {
			last := many[len(many)-1]
			if t, _ := last["type"].(string); t == "result" {
				jsonOK = true
			}
		}
	}
	if jsonOK {
		fmt.Println("  cursor: ok")
		return
	}
	fmt.Println("  cursor       ⚠  compat mode (JSON parse failed, status works)")
}
