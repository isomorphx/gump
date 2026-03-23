package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/isomorphx/gump/internal/brand"
)

const (
	claudeBin          = "claude"
	maxTurnsDefault    = 25
	maxScanTokenSize   = 1024 * 1024 // 1 MB per line so huge NDJSON lines don't grow buffer unbounded
	allowedTools       = "Bash,Read,Write,Edit,Glob,Grep"
	permissionMode     = "acceptEdits"
	outputFormat       = "stream-json"
)

var agentToModel = map[string]string{
	"claude-opus":        "opus",
	"claude-sonnet":      "sonnet",
	"claude-haiku":       "haiku",
	"claude-opus-4-6":    "claude-opus-4-6",
	"claude-sonnet-4-5":  "claude-sonnet-4-5-20250929",
}

// ClaudeAdapter runs the Claude Code CLI in the worktree with stream-json and timeout handling.
type ClaudeAdapter struct {
	mu               sync.Mutex
	lastResultByProc map[*Process]*RunResult
	lastLaunchCLI    string
}

// NewClaudeAdapter returns an adapter that invokes the `claude` CLI.
func NewClaudeAdapter() *ClaudeAdapter {
	return &ClaudeAdapter{lastResultByProc: make(map[*Process]*RunResult)}
}

func modelFlag(agentName string) string {
	if m, ok := agentToModel[agentName]; ok {
		return m
	}
	return agentName
}

func maxTurns(n int) int {
	if n <= 0 {
		return maxTurnsDefault
	}
	return n
}

// Launch starts a fresh agent run; the caller must write CLAUDE.md before calling.
func (a *ClaudeAdapter) Launch(ctx context.Context, req LaunchRequest) (*Process, error) {
	args := buildArgs(req.Prompt, req.AgentName, maxTurns(req.MaxTurns), "")
	return a.start(ctx, req.Worktree, req.Timeout, args)
}

// Resume starts a run with --resume so the agent continues the same session; if the CLI doesn't support it, fallback to Launch and log a warning.
func (a *ClaudeAdapter) Resume(ctx context.Context, req ResumeRequest) (*Process, error) {
	args := buildArgs(req.Prompt, req.AgentName, maxTurns(req.MaxTurns), req.SessionID)
	if req.SessionID == "" {
		log.Printf("claude-code: resume called with empty session_id, launching fresh")
		return a.start(ctx, req.Worktree, req.Timeout, args)
	}
	return a.start(ctx, req.Worktree, req.Timeout, args)
}

func buildArgs(prompt, agentName string, turns int, sessionID string) []string {
	args := []string{
		"-p", prompt,
		"--output-format", outputFormat,
		"--verbose",
		"--model", modelFlag(agentName),
		"--allowedTools", allowedTools,
		"--permission-mode", permissionMode,
		"--max-turns", fmt.Sprintf("%d", turns),
	}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	return args
}

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

func (a *ClaudeAdapter) start(ctx context.Context, worktree string, timeout time.Duration, args []string) (*Process, error) {
	cmd := exec.CommandContext(ctx, claudeBin, args...)
	// Don't pass ANTHROPIC_API_KEY so the CLI doesn't hit ByteString/API key errors; auth is handled by the CLI itself (e.g. OAuth).
	cmd.Env = envWithout(os.Environ(), "ANTHROPIC_API_KEY")
	artefactDir := filepath.Join(worktree, brand.StateDir(), "artefacts")
	_ = os.MkdirAll(artefactDir, 0755)
	stdoutPath := filepath.Join(artefactDir, "stdout.ndjson")
	stderrPath := filepath.Join(artefactDir, "stderr.txt")

	proc, err := Start(ctx, cmd, worktree, stdoutPath, stderrPath)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.lastLaunchCLI = claudeBin + " " + strings.Join(args, " ")
	a.mu.Unlock()
	if timeout > 0 {
		proc.TimeoutDuration = timeout
		proc.Cancel = WithTimeout(proc, timeout)
	}
	a.mu.Lock()
	a.lastResultByProc[proc] = nil
	a.mu.Unlock()
	return proc, nil
}

// Stream returns a channel of events from the process stdout (NDJSON). Each line is parsed best-effort; invalid lines are emitted as type "raw". The last type=result is stored for Wait().
func (a *ClaudeAdapter) Stream(process *Process) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(process.Stdout)
		scanner.Buffer(nil, maxScanTokenSize)
		for scanner.Scan() {
			line := scanner.Bytes()
			raw := make([]byte, len(line))
			copy(raw, line)
			ev := parseStreamLine(line, raw)
			if ev.Type == "result" {
				res, err := ParseResultJSON(line)
				if err == nil {
					a.mu.Lock()
					a.lastResultByProc[process] = res
					a.mu.Unlock()
				}
			}
			ch <- ev
		}
		if scanner.Err() == bufio.ErrTooLong {
			ch <- StreamEvent{Type: "raw", Raw: []byte("(line exceeded 1MB buffer)\n")}
		}
		_ = process.Stdout.Close()
	}()
	return ch
}

func parseStreamLine(line, raw []byte) StreamEvent {
	var base struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(line, &base); err != nil {
		return StreamEvent{Type: "raw", Raw: raw}
	}
	switch base.Type {
	case "system", "rate_limit_event", "result":
		return StreamEvent{Type: base.Type, Raw: raw}
	case "assistant":
		return StreamEvent{Type: "assistant", Raw: raw}
	case "user":
		return StreamEvent{Type: "user", Raw: raw}
	default:
		return StreamEvent{Type: "raw", Raw: raw}
	}
}

// Wait blocks until the process exits, then returns the RunResult from the last type=result in the stream, or compat mode if none was seen.
func (a *ClaudeAdapter) Wait(process *Process) (*RunResult, error) {
	if process.Cancel != nil {
		defer process.Cancel()
	}
	_ = process.Cmd.Wait() // ProcessState is set regardless of exit status
	a.mu.Lock()
	last := a.lastResultByProc[process]
	delete(a.lastResultByProc, process)
	a.mu.Unlock()

	exitCode := 0
	if process.Cmd.ProcessState != nil {
		exitCode = process.Cmd.ProcessState.ExitCode()
	}

	// Timeout always wins: spec requires ExitCode=-1, IsError=true; we keep partial metrics from last type=result if any.
	if process.TimedOut {
		dur := process.TimeoutDuration
		if dur == 0 {
			dur = KillGrace
		}
		res := &RunResult{
			ExitCode: -1,
			IsError:  true,
			Result:   "Process killed due to timeout after " + dur.String(),
		}
		if last != nil {
			res.DurationMs = last.DurationMs
			res.DurationAPI = last.DurationAPI
			res.InputTokens = last.InputTokens
			res.OutputTokens = last.OutputTokens
			res.CacheCreationTokens = last.CacheCreationTokens
			res.CacheReadTokens = last.CacheReadTokens
			res.CostUSD = last.CostUSD
			res.NumTurns = last.NumTurns
			res.ModelUsage = last.ModelUsage
		}
		return res, nil
	}

	if last != nil {
		last.ExitCode = exitCode
		if last.IsError && last.DurationAPI == 0 {
			return nil, fmt.Errorf("claude-code: agent never reached API (is_error=true, duration_api_ms=0) — check auth/network")
		}
		return last, nil
	}

	log.Printf("claude-code: failed to parse JSON output, falling back to compat mode")
	return &RunResult{ExitCode: exitCode, Result: ""}, nil
}

// LastLaunchCLI returns the full CLI command used for the last Launch or Resume (for ledger/debug).
func (a *ClaudeAdapter) LastLaunchCLI() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastLaunchCLI
}
