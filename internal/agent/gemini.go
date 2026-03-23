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

	"github.com/isomorphx/gump/internal/brand"
)

const (
	geminiBin         = "gemini"
	geminiStreamJSON  = "stream-json"
)

var geminiAgentToModel = map[string]string{
	"gemini-flash":      "gemini-3-flash-preview",
	"gemini-pro":        "gemini-3.1-pro-preview",
	"gemini-flash-lite": "gemini-3.1-flash-lite-preview",
}

func geminiModelFlag(agentName string) string {
	if agentName == "gemini" {
		return ""
	}
	if m, ok := geminiAgentToModel[agentName]; ok {
		return m
	}
	if strings.HasPrefix(agentName, "gemini-") {
		return strings.TrimPrefix(agentName, "gemini-")
	}
	return ""
}

// GeminiAdapter runs the Gemini CLI with stream-json and timeout handling.
type GeminiAdapter struct {
	mu               sync.Mutex
	lastResultByProc map[*Process]*geminiAccumulator
	lastLaunchCLI    string
	maxTurnsWarned   bool
}

type geminiAccumulator struct {
	SessionID    string
	LastMessage  string
	ResultStatus string
	DurationMs   int
	InputTokens  int
	OutputTokens int
	Cached       int
	ToolCalls    int
	StartTime    time.Time
}

// NewGeminiAdapter returns an adapter that invokes the `gemini` CLI.
func NewGeminiAdapter() *GeminiAdapter {
	return &GeminiAdapter{lastResultByProc: make(map[*Process]*geminiAccumulator)}
}

// Launch starts a fresh run; the caller must write GEMINI.md before calling when using Gemini.
func (a *GeminiAdapter) Launch(ctx context.Context, req LaunchRequest) (*Process, error) {
	if req.MaxTurns > 0 && !a.maxTurnsWarned {
		a.mu.Lock()
		if !a.maxTurnsWarned {
			a.maxTurnsWarned = true
			a.mu.Unlock()
			log.Printf("gemini: max_turns is not supported by Gemini CLI, ignoring")
		} else {
			a.mu.Unlock()
		}
	}
	args := geminiBuildArgs(req.Prompt, req.AgentName, false)
	return a.start(ctx, req.Worktree, req.Timeout, args)
}

// Resume adds --resume (no ID; Gemini resumes last session for the project/cwd).
func (a *GeminiAdapter) Resume(ctx context.Context, req ResumeRequest) (*Process, error) {
	if req.SessionID == "" {
		log.Printf("gemini: resume called with empty session_id, launching fresh")
		return a.Launch(ctx, LaunchRequest{
			Worktree: req.Worktree, Prompt: req.Prompt, AgentName: req.AgentName,
			Timeout: req.Timeout, MaxTurns: req.MaxTurns,
		})
	}
	args := geminiBuildArgs(req.Prompt, req.AgentName, true)
	return a.start(ctx, req.Worktree, req.Timeout, args)
}

func geminiBuildArgs(prompt, agentName string, resume bool) []string {
	args := []string{"-p", prompt, "--output-format", geminiStreamJSON, "--yolo"}
	if resume {
		args = append([]string{"--resume"}, args...)
	}
	if m := geminiModelFlag(agentName); m != "" {
		args = append(args, "-m", m)
	}
	return args
}

func (a *GeminiAdapter) start(ctx context.Context, worktree string, timeout time.Duration, args []string) (*Process, error) {
	cmd := exec.CommandContext(ctx, geminiBin, args...)
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
	a.lastLaunchCLI = geminiBin + " " + strings.Join(args, " ")
	acc := &geminiAccumulator{StartTime: time.Now()}
	a.lastResultByProc[proc] = acc
	a.mu.Unlock()
	if timeout > 0 {
		proc.TimeoutDuration = timeout
		proc.Cancel = WithTimeout(proc, timeout)
	}
	return proc, nil
}

// Stream parses Gemini NDJSON and emits StreamEvents; accumulates init.session_id, result.stats, last assistant message.
func (a *GeminiAdapter) Stream(process *Process) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(process.Stdout)
		scanner.Buffer(nil, maxScanTokenSize)
		for scanner.Scan() {
			line := scanner.Bytes()
			raw := make([]byte, len(line))
			copy(raw, line)
			ev := a.parseGeminiLine(line, raw, process)
			ch <- ev
		}
		if scanner.Err() == bufio.ErrTooLong {
			ch <- StreamEvent{Type: "raw", Raw: []byte("(line exceeded 1MB buffer)\n")}
		}
		_ = process.Stdout.Close()
	}()
	return ch
}

func (a *GeminiAdapter) parseGeminiLine(line, raw []byte, process *Process) StreamEvent {
	var base struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content string `json:"content"`
		Status  string `json:"status"`
		SessionID string `json:"session_id"`
		Stats   *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			Cached       int `json:"cached"`
			DurationMs   int `json:"duration_ms"`
			ToolCalls    int `json:"tool_calls"`
		} `json:"stats"`
	}
	_ = json.Unmarshal(line, &base)

	a.mu.Lock()
	acc, ok := a.lastResultByProc[process]
	if !ok {
		acc = &geminiAccumulator{StartTime: time.Now()}
		a.lastResultByProc[process] = acc
	}
	a.mu.Unlock()

	switch base.Type {
	case "init":
		if base.SessionID != "" {
			acc.SessionID = base.SessionID
		}
		return StreamEvent{Type: "system", Raw: raw}
	case "message":
		if base.Role == "assistant" {
			if base.Content != "" {
				acc.LastMessage = base.Content
			}
			return StreamEvent{Type: "assistant", Raw: raw}
		}
		return StreamEvent{Type: "user", Raw: raw}
	case "tool_use":
		return StreamEvent{Type: "assistant", Raw: raw}
	case "tool_result":
		return StreamEvent{Type: "user", Raw: raw}
	case "result":
		acc.ResultStatus = base.Status
		if base.Stats != nil {
			acc.DurationMs = base.Stats.DurationMs
			acc.InputTokens = base.Stats.InputTokens
			acc.OutputTokens = base.Stats.OutputTokens
			acc.Cached = base.Stats.Cached
			acc.ToolCalls = base.Stats.ToolCalls
		}
		return StreamEvent{Type: "result", Raw: raw}
	default:
		return StreamEvent{Type: "raw", Raw: raw}
	}
}

// Wait returns RunResult from accumulated stream state; compat mode if no result event seen.
func (a *GeminiAdapter) Wait(process *Process) (*RunResult, error) {
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
		acc = &geminiAccumulator{}
	}
	durationMs := acc.DurationMs
	if durationMs == 0 && !acc.StartTime.IsZero() {
		durationMs = int(time.Since(acc.StartTime).Milliseconds())
	}
	// NumTurns: Gemini stream-json does not expose turn count; tool_calls is the only usable proxy for reporting.
	res := &RunResult{
		ExitCode:          exitCode,
		SessionID:         acc.SessionID,
		NumTurns:          acc.ToolCalls,
		InputTokens:       acc.InputTokens,
		OutputTokens:      acc.OutputTokens,
		CacheReadTokens:   acc.Cached,
		Result:            acc.LastMessage,
		DurationMs:        durationMs,
		ModelUsage:        map[string]ModelMetrics{},
	}
	if acc.SessionID == "" {
		log.Printf("gemini: no init.session_id in stream")
	}
	if acc.ResultStatus != "" && acc.ResultStatus != "success" {
		res.IsError = true
	}
	if acc.ResultStatus == "" {
		log.Printf("gemini: failed to parse JSONL output, falling back to compat mode")
		return &RunResult{ExitCode: exitCode, IsError: true, Result: ""}, nil
	}
	return res, nil
}

// LastLaunchCLI returns the full CLI command used for the last Launch or Resume.
func (a *GeminiAdapter) LastLaunchCLI() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastLaunchCLI
}
