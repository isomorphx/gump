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
	cursorOutputFormat = "stream-json"
)

var cursorAgentToModel = map[string]string{
	"cursor-sonnet":          "claude-4.6-sonnet-medium",
	"cursor-sonnet-thinking": "claude-4.6-sonnet-medium-thinking",
	"cursor-opus":            "claude-4.6-opus-high",
	"cursor-opus-thinking":   "claude-4.6-opus-high-thinking",
	"cursor-gpt5":            "gpt-5.4-medium",
	"cursor-gemini":          "gemini-3.1-pro",
}

func cursorModelFlag(agentName string) string {
	if agentName == "cursor" {
		return ""
	}
	if m, ok := cursorAgentToModel[agentName]; ok {
		return m
	}
	if strings.HasPrefix(agentName, "cursor-") {
		return strings.TrimPrefix(agentName, "cursor-")
	}
	return ""
}

func cursorBinary() string {
	if p := os.Getenv("GUMP_E2E_CURSOR_BIN"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "cursor-agent"
	}
	return filepath.Join(home, ".local", "bin", "cursor-agent")
}

type CursorAdapter struct {
	mu               sync.Mutex
	lastLaunchCLI    string
	lastResultByProc map[*Process]*cursorAccumulator
	maxTurnsWarned   bool
}

type cursorAccumulator struct {
	seenResult bool
	result     RunResult
	start      time.Time
}

func NewCursorAdapter() *CursorAdapter {
	return &CursorAdapter{lastResultByProc: make(map[*Process]*cursorAccumulator)}
}

func (a *CursorAdapter) Launch(ctx context.Context, req LaunchRequest) (*Process, error) {
	a.warnMaxTurns(req.MaxTurns)
	args := cursorBuildArgs(req.Prompt, req.Worktree, req.AgentName, "")
	return a.start(ctx, req.Worktree, req.Timeout, args)
}

func (a *CursorAdapter) Resume(ctx context.Context, req ResumeRequest) (*Process, error) {
	a.warnMaxTurns(req.MaxTurns)
	if req.SessionID == "" {
		log.Printf("cursor: resume called with empty session_id, launching fresh")
		return a.Launch(ctx, LaunchRequest{
			Worktree: req.Worktree, Prompt: req.Prompt, AgentName: req.AgentName,
			Timeout: req.Timeout, MaxTurns: req.MaxTurns,
		})
	}
	args := cursorBuildArgs(req.Prompt, req.Worktree, req.AgentName, req.SessionID)
	return a.start(ctx, req.Worktree, req.Timeout, args)
}

func (a *CursorAdapter) warnMaxTurns(maxTurns int) {
	if maxTurns <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.maxTurnsWarned {
		return
	}
	a.maxTurnsWarned = true
	log.Printf("cursor: max_turns is not supported by Cursor Agent CLI, ignoring")
}

func cursorBuildArgs(prompt, worktree, agentName, resumeID string) []string {
	args := []string{
		"-p", prompt,
		"--output-format", cursorOutputFormat,
		"--yolo",
		"--trust",
		"--workspace", worktree,
	}
	if m := cursorModelFlag(agentName); m != "" {
		args = append(args, "--model", m)
	}
	if resumeID != "" {
		args = append(args, "--resume", resumeID)
	}
	return args
}

func (a *CursorAdapter) start(ctx context.Context, worktree string, timeout time.Duration, args []string) (*Process, error) {
	bin := cursorBinary()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = worktree

	artefactDir := filepath.Join(worktree, brand.StateDir(), "artefacts")
	_ = os.MkdirAll(artefactDir, 0755)
	stdoutPath := filepath.Join(artefactDir, "stdout.ndjson")
	stderrPath := filepath.Join(artefactDir, "stderr.txt")

	proc, err := Start(ctx, cmd, worktree, stdoutPath, stderrPath)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.lastLaunchCLI = filepath.Base(bin) + " " + strings.Join(args, " ")
	a.lastResultByProc[proc] = &cursorAccumulator{start: time.Now()}
	a.mu.Unlock()

	if timeout > 0 {
		proc.TimeoutDuration = timeout
		proc.Cancel = WithTimeout(proc, timeout)
	}
	return proc, nil
}

func (a *CursorAdapter) Stream(process *Process) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(process.Stdout)
		scanner.Buffer(nil, maxScanTokenSize)
		for scanner.Scan() {
			line := scanner.Bytes()
			raw := make([]byte, len(line))
			copy(raw, line)
			ev := a.parseCursorLine(line, raw, process)
			ch <- ev
		}
		if scanner.Err() == bufio.ErrTooLong {
			ch <- StreamEvent{Type: "raw", Raw: []byte("(line exceeded 1MB buffer)\n")}
		}
		_ = process.Stdout.Close()
	}()
	return ch
}

func (a *CursorAdapter) parseCursorLine(line, raw []byte, process *Process) StreamEvent {
	var base struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(line, &base)
	switch base.Type {
	case "system/init", "user":
		return StreamEvent{Type: "system", Raw: raw}
	case "assistant":
		return StreamEvent{Type: "assistant", Raw: raw}
	case "tool_call/started":
		return StreamEvent{Type: "assistant", Raw: raw}
	case "tool_call/completed":
		return StreamEvent{Type: "user", Raw: raw}
	case "result":
		res := parseCursorResult(line)
		a.mu.Lock()
		acc, ok := a.lastResultByProc[process]
		if !ok {
			acc = &cursorAccumulator{start: time.Now()}
			a.lastResultByProc[process] = acc
		}
		acc.seenResult = true
		acc.result = res
		a.mu.Unlock()
		process.AddPartialMetrics(RunResult{
			InputTokens:         res.InputTokens,
			OutputTokens:        res.OutputTokens,
			CacheCreationTokens: res.CacheCreationTokens,
			CacheReadTokens:     res.CacheReadTokens,
		})
		return StreamEvent{Type: "result", Raw: raw}
	default:
		if base.Type != "" {
			log.Printf("cursor: unknown stream event type %q", base.Type)
		}
		return StreamEvent{Type: "raw", Raw: raw}
	}
}

func parseCursorResult(line []byte) RunResult {
	var payload struct {
		SessionID     string `json:"session_id"`
		IsError       bool   `json:"is_error"`
		DurationMs    int    `json:"duration_ms"`
		DurationAPIMs int    `json:"duration_api_ms"`
		Result        string `json:"result"`
		Usage         *struct {
			InputTokens      int `json:"inputTokens"`
			OutputTokens     int `json:"outputTokens"`
			CacheWriteTokens int `json:"cacheWriteTokens"`
			CacheReadTokens  int `json:"cacheReadTokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(line, &payload)
	res := RunResult{
		SessionID:   payload.SessionID,
		IsError:     payload.IsError,
		DurationMs:  payload.DurationMs,
		DurationAPI: payload.DurationAPIMs,
		Result:      payload.Result,
		ModelUsage:  map[string]ModelMetrics{},
	}
	if payload.Usage != nil {
		res.InputTokens = payload.Usage.InputTokens
		res.OutputTokens = payload.Usage.OutputTokens
		res.CacheCreationTokens = payload.Usage.CacheWriteTokens
		res.CacheReadTokens = payload.Usage.CacheReadTokens
	}
	return res
}

func (a *CursorAdapter) Wait(process *Process) (*RunResult, error) {
	if process.Cancel != nil {
		defer process.Cancel()
	}
	_ = process.Cmd.Wait()

	a.mu.Lock()
	acc := a.lastResultByProc[process]
	delete(a.lastResultByProc, process)
	a.mu.Unlock()

	exitCode := 0
	if process.Cmd.ProcessState != nil {
		exitCode = process.Cmd.ProcessState.ExitCode()
	}
	if process.TimedOut {
		dur := process.TimeoutDuration
		if dur == 0 {
			dur = KillGrace
		}
		return &RunResult{
			ExitCode: -1,
			IsError:  true,
			Result:   "Process killed due to timeout after " + dur.String(),
		}, nil
	}
	if acc == nil || !acc.seenResult {
		log.Printf("cursor: failed to parse JSONL output, falling back to compat mode")
		return &RunResult{ExitCode: exitCode, Result: ""}, nil
	}
	res := acc.result
	res.ExitCode = exitCode
	if res.DurationMs == 0 && !acc.start.IsZero() {
		res.DurationMs = int(time.Since(acc.start).Milliseconds())
	}
	if res.IsError && res.DurationAPI == 0 {
		return nil, fmt.Errorf("cursor: infrastructure error (is_error=true, duration_api_ms=0)")
	}
	return &res, nil
}

func (a *CursorAdapter) LastLaunchCLI() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastLaunchCLI
}
