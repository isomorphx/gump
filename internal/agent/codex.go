package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	codexBin       = "codex"
	truncAssistant = 200
	truncTool      = 100
)

// codexAgentToModel maps recipe agent names to Codex -m flag so users get predictable models without knowing CLI identifiers.
var codexAgentToModel = map[string]string{
	"codex-gpt5":  "gpt-5.3-codex",
	"codex-gpt53": "gpt-5.3-codex",
	"codex-gpt52": "gpt-5.2-codex",
	"codex-gpt51": "gpt-5.1-codex",
	"codex-o3":    "o3-codex",
	"codex-spark": "gpt-5.3-codex-spark",
}

func codexModelFlag(agentName string) string {
	if agentName == "codex" {
		return ""
	}
	if m, ok := codexAgentToModel[agentName]; ok {
		return m
	}
	if strings.HasPrefix(agentName, "codex-") {
		return strings.TrimPrefix(agentName, "codex-")
	}
	return ""
}

// CodexAdapter runs the Codex CLI with --json, --full-auto, and timeout handling.
type CodexAdapter struct {
	mu               sync.Mutex
	lastResultByProc map[*Process]*codexAccumulator
	lastLaunchCLI    string
	maxTurnsWarned   bool
}

type codexAccumulator struct {
	SessionID     string
	NumTurns      int
	InputTokens   int
	OutputTokens  int
	CacheRead     int
	LastMessage   string
	HasTurnDone   bool
	StreamError   string
	StartTime     time.Time
}

// NewCodexAdapter returns an adapter that invokes the `codex` CLI.
func NewCodexAdapter() *CodexAdapter {
	return &CodexAdapter{lastResultByProc: make(map[*Process]*codexAccumulator)}
}

// Launch starts a fresh run; the caller must write AGENTS.md before calling when using Codex.
func (a *CodexAdapter) Launch(ctx context.Context, req LaunchRequest) (*Process, error) {
	if req.MaxTurns > 0 && !a.maxTurnsWarned {
		a.mu.Lock()
		if !a.maxTurnsWarned {
			a.maxTurnsWarned = true
			a.mu.Unlock()
			log.Printf("codex: max_turns is not supported by Codex CLI, ignoring")
		} else {
			a.mu.Unlock()
		}
	}
	args := codexBuildArgs(req.Prompt, req.AgentName, false, "")
	return a.start(ctx, req.Worktree, req.Timeout, args)
}

// Resume continues the session by thread ID so the next step sees the same context.
func (a *CodexAdapter) Resume(ctx context.Context, req ResumeRequest) (*Process, error) {
	if req.SessionID == "" {
		log.Printf("codex: resume called with empty session_id, launching fresh")
		return a.Launch(ctx, LaunchRequest{
			Worktree: req.Worktree, Prompt: req.Prompt, AgentName: req.AgentName,
			Timeout: req.Timeout, MaxTurns: req.MaxTurns,
		})
	}
	args := codexBuildArgs(req.Prompt, req.AgentName, true, req.SessionID)
	return a.start(ctx, req.Worktree, req.Timeout, args)
}

func codexBuildArgs(prompt, agentName string, resume bool, threadID string) []string {
	args := []string{"exec"}
	if resume && threadID != "" {
		args = append(args, "resume", threadID)
	}
	args = append(args, prompt, "--json", "--full-auto", "-C", "", "--skip-git-repo-check")
	if m := codexModelFlag(agentName); m != "" {
		args = append(args, "-m", m)
	}
	return args
}

func (a *CodexAdapter) start(ctx context.Context, worktree string, timeout time.Duration, args []string) (*Process, error) {
	for i, v := range args {
		if v == "-C" && i+1 < len(args) {
			args[i+1] = worktree
			break
		}
	}
	cmd := exec.CommandContext(ctx, codexBin, args...)
	cmd.Dir = worktree
	artefactDir := filepath.Join(worktree, ".pudding", "artefacts")
	_ = os.MkdirAll(artefactDir, 0755)
	stdoutPath := filepath.Join(artefactDir, "stdout.ndjson")
	stderrPath := filepath.Join(artefactDir, "stderr.txt")

	proc, err := Start(ctx, cmd, worktree, stdoutPath, stderrPath)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.lastLaunchCLI = codexBin + " " + strings.Join(args, " ")
	acc := &codexAccumulator{StartTime: time.Now()}
	a.lastResultByProc[proc] = acc
	a.mu.Unlock()
	if timeout > 0 {
		proc.TimeoutDuration = timeout
		proc.Cancel = WithTimeout(proc, timeout)
	}
	return proc, nil
}

// Stream parses Codex NDJSON and emits StreamEvents; accumulates thread_id, usage, and last agent message for Wait.
func (a *CodexAdapter) Stream(process *Process) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(process.Stdout)
		scanner.Buffer(nil, maxScanTokenSize)
		for scanner.Scan() {
			line := scanner.Bytes()
			raw := make([]byte, len(line))
			copy(raw, line)
			ev := a.parseCodexLine(line, raw, process)
			ch <- ev
		}
		if scanner.Err() == bufio.ErrTooLong {
			ch <- StreamEvent{Type: "raw", Raw: []byte("(line exceeded 1MB buffer)\n")}
		}
		_ = process.Stdout.Close()
	}()
	return ch
}

func (a *CodexAdapter) parseCodexLine(line, raw []byte, process *Process) StreamEvent {
	var base struct {
		Type string          `json:"type"`
		Item *struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Text     string `json:"text"`
			Command  string `json:"command"`
			ExitCode *int   `json:"exit_code"`
			Status   string `json:"status"`
		} `json:"item"`
		ThreadID string `json:"thread_id"`
		Usage    *struct {
			InputTokens     int `json:"input_tokens"`
			CachedInput     int `json:"cached_input_tokens"`
			OutputTokens    int `json:"output_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(line, &base)

	a.mu.Lock()
	acc, ok := a.lastResultByProc[process]
	if !ok {
		acc = &codexAccumulator{StartTime: time.Now()}
		a.lastResultByProc[process] = acc
	}
	a.mu.Unlock()

	switch base.Type {
	case "thread.started":
		if base.ThreadID != "" {
			acc.SessionID = base.ThreadID
		}
		return StreamEvent{Type: "system", Raw: raw}
	case "turn.started":
		return StreamEvent{Type: "system", Raw: raw}
	case "turn.completed":
		acc.HasTurnDone = true
		acc.NumTurns++
		if base.Usage != nil {
			acc.InputTokens += base.Usage.InputTokens
			acc.OutputTokens += base.Usage.OutputTokens
			acc.CacheRead += base.Usage.CachedInput
		}
		return StreamEvent{Type: "result", Raw: raw}
	case "item.completed":
		if base.Item != nil {
			switch base.Item.Type {
			case "agent_message":
				acc.LastMessage = base.Item.Text
				return StreamEvent{Type: "assistant", Raw: raw}
			case "reasoning":
				return StreamEvent{Type: "assistant", Raw: raw}
			case "command_execution":
				return StreamEvent{Type: "user", Raw: raw}
			case "file_change":
				return StreamEvent{Type: "user", Raw: raw}
			case "error":
				if base.Item.Text != "" {
					acc.StreamError = base.Item.Text
					log.Printf("codex: item.completed type=error: %s", trunc(base.Item.Text, 200))
				}
				return StreamEvent{Type: "assistant", Raw: raw}
			}
		}
		return StreamEvent{Type: "raw", Raw: raw}
	case "item.started":
		if base.Item != nil && base.Item.Type == "command_execution" {
			return StreamEvent{Type: "assistant", Raw: raw}
		}
		return StreamEvent{Type: "raw", Raw: raw}
	default:
		if base.Type == "error" {
			acc.StreamError = string(line)
			acc.HasTurnDone = false
		}
		return StreamEvent{Type: "raw", Raw: raw}
	}
}

// Wait returns RunResult from accumulated stream state; compat mode if no turn.completed seen.
func (a *CodexAdapter) Wait(process *Process) (*RunResult, error) {
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

	if acc == nil {
		acc = &codexAccumulator{}
	}
	durationMs := 0
	if !acc.StartTime.IsZero() {
		durationMs = int(time.Since(acc.StartTime).Milliseconds())
	}
	res := &RunResult{
		ExitCode:       exitCode,
		SessionID:      acc.SessionID,
		NumTurns:       acc.NumTurns,
		InputTokens:    acc.InputTokens,
		OutputTokens:   acc.OutputTokens,
		CacheReadTokens: acc.CacheRead,
		Result:         acc.LastMessage,
		DurationMs:     durationMs,
		ModelUsage:     map[string]ModelMetrics{},
	}
	if acc.SessionID == "" {
		log.Printf("codex: no thread.started.thread_id in stream")
	}
	// Top-level or unrecovered item error: no turn.completed means infra failure.
	if !acc.HasTurnDone {
		res.IsError = true
		if acc.StreamError != "" {
			res.Result = acc.StreamError
		}
	}
	if res.Result == "" && res.IsError && acc.LastMessage != "" {
		res.Result = acc.LastMessage
	}
	// Compat mode when CLI output format changes: we still have exit code and the worktree diff is the source of truth.
	if !acc.HasTurnDone && acc.SessionID == "" && acc.StreamError == "" {
		log.Printf("codex: failed to parse JSONL output, falling back to compat mode")
		return &RunResult{ExitCode: exitCode, Result: ""}, nil
	}
	return res, nil
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// LastLaunchCLI returns the full CLI command used for the last Launch or Resume.
func (a *CodexAdapter) LastLaunchCLI() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastLaunchCLI
}
