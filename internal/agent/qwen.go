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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	qwenBin             = "qwen"
	qwenStreamJSON      = "stream-json"
	qwenMaxTurnsDefault = 25
)

// Session ID from Qwen is a standard UUID; resume must use the same provider's format.
var qwenSessionIDRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var qwenAgentToModel = map[string]string{
	"qwen-coder":      "qwen3-coder",
	"qwen-coder-plus": "qwen3-coder-plus",
	"qwen-plus":       "qwen3-coder-plus",
	"qwen-local":      "", // local model, variable
}

// Allowed tools for reproducible offline runs; web_search, task, etc. are excluded by design.
var qwenAllowedTools = []string{"list_directory", "read_file", "grep_search", "glob", "edit", "write_file", "run_shell_command"}

func qwenModelFlag(agentName string) string {
	if agentName == "qwen" {
		return ""
	}
	if m, ok := qwenAgentToModel[agentName]; ok {
		return m
	}
	if strings.HasPrefix(agentName, "qwen-") {
		return strings.TrimPrefix(agentName, "qwen-")
	}
	return ""
}

func qwenMaxTurns(n int) int {
	if n <= 0 {
		return qwenMaxTurnsDefault
	}
	return n
}

// QwenAdapter runs the Qwen Code CLI with stream-json and timeout handling.
type QwenAdapter struct {
	mu               sync.Mutex
	lastResultByProc map[*Process]*qwenAccumulator
	lastLaunchCLI    string
}

type qwenAccumulator struct {
	SessionID   string
	LastResult  string
	ResultLine  []byte
	StartTime   time.Time
}

// NewQwenAdapter returns an adapter that invokes the `qwen` CLI.
func NewQwenAdapter() *QwenAdapter {
	return &QwenAdapter{lastResultByProc: make(map[*Process]*qwenAccumulator)}
}

// Launch starts a fresh run; the caller must write QWEN.md before calling.
func (a *QwenAdapter) Launch(ctx context.Context, req LaunchRequest) (*Process, error) {
	args := qwenBuildArgs(req.Prompt, req.AgentName, qwenMaxTurns(req.MaxTurns), "")
	return a.start(ctx, req.Worktree, req.Timeout, args)
}

// Resume continues the same session; invalid session ID format triggers Launch so cross-provider never receives wrong ID.
func (a *QwenAdapter) Resume(ctx context.Context, req ResumeRequest) (*Process, error) {
	if req.SessionID == "" {
		log.Printf("qwen: resume called with empty session_id, launching fresh")
		return a.Launch(ctx, LaunchRequest{
			Worktree: req.Worktree, Prompt: req.Prompt, AgentName: req.AgentName,
			Timeout: req.Timeout, MaxTurns: req.MaxTurns,
		})
	}
	if !qwenSessionIDRegex.MatchString(req.SessionID) {
		log.Printf("qwen: invalid session_id format (expected UUID), launching fresh")
		return a.Launch(ctx, LaunchRequest{
			Worktree: req.Worktree, Prompt: req.Prompt, AgentName: req.AgentName,
			Timeout: req.Timeout, MaxTurns: req.MaxTurns,
		})
	}
	args := qwenBuildArgs(req.Prompt, req.AgentName, qwenMaxTurns(req.MaxTurns), req.SessionID)
	return a.start(ctx, req.Worktree, req.Timeout, args)
}

func qwenBuildArgs(prompt, agentName string, turns int, sessionID string) []string {
	args := []string{"-p", prompt, "--output-format", qwenStreamJSON, "--yolo"}
	if sessionID != "" {
		args = append([]string{"--resume", sessionID}, args...)
	}
	if m := qwenModelFlag(agentName); m != "" {
		args = append(args, "-m", m)
	}
	args = append(args, "--max-session-turns", strconv.Itoa(turns))
	args = append(args, "--allowed-tools")
	args = append(args, qwenAllowedTools...)
	return args
}

// qwenBinary returns the qwen CLI path: PUDDING_E2E_QWEN_BIN if set (E2E stubs), else "qwen".
func qwenBinary() string {
	if p := os.Getenv("PUDDING_E2E_QWEN_BIN"); p != "" {
		return p
	}
	return qwenBin
}

func (a *QwenAdapter) start(ctx context.Context, worktree string, timeout time.Duration, args []string) (*Process, error) {
	bin := qwenBinary()
	cmd := exec.CommandContext(ctx, bin, args...)
	// E2E: stub needs worktree path to write sentinel (cwd can differ on some platforms).
	if os.Getenv("PUDDING_E2E_QWEN_BIN") != "" {
		cmd.Env = append(os.Environ(), "PUDDING_WORKTREE="+worktree)
	}
	artefactDir := filepath.Join(worktree, ".pudding", "artefacts")
	_ = os.MkdirAll(artefactDir, 0755)
	stdoutPath := filepath.Join(artefactDir, "stdout.ndjson")
	stderrPath := filepath.Join(artefactDir, "stderr.txt")

	proc, err := Start(ctx, cmd, worktree, stdoutPath, stderrPath)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	// Log as "qwen ..." so ledger stays readable; actual exec uses bin (may be stub path).
	a.lastLaunchCLI = qwenBin + " " + strings.Join(args, " ")
	acc := &qwenAccumulator{StartTime: time.Now()}
	a.lastResultByProc[proc] = acc
	a.mu.Unlock()
	if timeout > 0 {
		proc.TimeoutDuration = timeout
		proc.Cancel = WithTimeout(proc, timeout)
	}
	return proc, nil
}

// Stream parses Qwen NDJSON and emits StreamEvents; accumulates system init session_id and type=result for Wait.
func (a *QwenAdapter) Stream(process *Process) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(process.Stdout)
		scanner.Buffer(nil, maxScanTokenSize)
		for scanner.Scan() {
			line := scanner.Bytes()
			raw := make([]byte, len(line))
			copy(raw, line)
			ev := a.parseQwenLine(line, raw, process)
			ch <- ev
		}
		if scanner.Err() == bufio.ErrTooLong {
			ch <- StreamEvent{Type: "raw", Raw: []byte("(line exceeded 1MB buffer)\n")}
		}
		_ = process.Stdout.Close()
	}()
	return ch
}

func (a *QwenAdapter) parseQwenLine(line, raw []byte, process *Process) StreamEvent {
	var base struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message *struct {
			Content []struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Name  string `json:"name"`
				Input *struct {
					FilePath string `json:"file_path"`
				} `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(line, &base)

	a.mu.Lock()
	acc, ok := a.lastResultByProc[process]
	if !ok {
		acc = &qwenAccumulator{StartTime: time.Now()}
		a.lastResultByProc[process] = acc
	}
	a.mu.Unlock()

	switch base.Type {
	case "system":
		if base.Subtype == "init" {
			var init struct {
				SessionID string `json:"session_id"`
			}
			if json.Unmarshal(line, &init) == nil && init.SessionID != "" {
				acc.SessionID = init.SessionID
			}
		}
		return StreamEvent{Type: "system", Raw: raw}
	case "assistant":
		if base.Message != nil {
			for _, c := range base.Message.Content {
				if c.Type == "text" && c.Text != "" {
					acc.LastResult = c.Text
				}
			}
		}
		return StreamEvent{Type: "assistant", Raw: raw}
	case "user":
		return StreamEvent{Type: "user", Raw: raw}
	case "result":
		acc.ResultLine = make([]byte, len(line))
		copy(acc.ResultLine, line)
		return StreamEvent{Type: "result", Raw: raw}
	default:
		return StreamEvent{Type: "raw", Raw: raw}
	}
}

// Wait returns RunResult from the last type=result; compat mode if none seen so diff remains recoverable.
func (a *QwenAdapter) Wait(process *Process) (*RunResult, error) {
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
		acc = &qwenAccumulator{}
	}

	if len(acc.ResultLine) > 0 {
		res, err := ParseQwenResultJSON(acc.ResultLine)
		if err == nil {
			res.ExitCode = exitCode
			if res.Result == "" && acc.LastResult != "" {
				res.Result = acc.LastResult
			}
			if acc.SessionID != "" && res.SessionID == "" {
				res.SessionID = acc.SessionID
			}
			if res.IsError && res.DurationAPI == 0 {
				return nil, fmt.Errorf("qwen: infrastructure error (is_error=true, duration_api_ms=0)")
			}
			return res, nil
		}
	}

	log.Printf("qwen: failed to parse JSON output, falling back to compat mode")
	return &RunResult{ExitCode: exitCode, Result: ""}, nil
}

// LastLaunchCLI returns the full CLI command used for the last Launch or Resume.
func (a *QwenAdapter) LastLaunchCLI() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastLaunchCLI
}
