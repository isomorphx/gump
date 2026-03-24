package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/isomorphx/gump/internal/brand"
)

const opencodeBin = "opencode"

// Session ID from OpenCode has a ses_ prefix; resume must use the same provider's format.
var opencodeSessionIDRegex = regexp.MustCompile(`^ses_`)

var opencodeAgentToModel = map[string]string{
	"opencode-sonnet": "anthropic/claude-sonnet-4-5",
	"opencode-opus":   "anthropic/claude-opus-4-5",
	"opencode-haiku":  "anthropic/claude-haiku-4-5",
	"opencode-gpt5":   "openai/gpt-5.3",
	"opencode-gemini": "google/gemini-2.5-pro",
}

func opencodeModelFlag(agentName string) string {
	if agentName == "opencode" {
		return ""
	}
	if m, ok := opencodeAgentToModel[agentName]; ok {
		return m
	}
	if strings.HasPrefix(agentName, "opencode-") {
		return strings.TrimPrefix(agentName, "opencode-")
	}
	return ""
}

// OpenCodeAdapter runs the OpenCode CLI; stdout is file-backed because the CLI blocks on pipe.
type OpenCodeAdapter struct {
	mu              sync.Mutex
	lastLaunchCLI   string
	maxTurnsWarned  bool
	// file-backed run: we keep handles to close after Wait and to read stdout
	procFiles map[*Process]*opencodeProcFiles
}

type opencodeProcFiles struct {
	stdout *os.File
	stderr *os.File
}

// NewOpenCodeAdapter returns an adapter that invokes the `opencode` CLI.
func NewOpenCodeAdapter() *OpenCodeAdapter {
	return &OpenCodeAdapter{procFiles: make(map[*Process]*opencodeProcFiles)}
}

// Launch starts a fresh run; the caller must write AGENTS.md before calling.
func (a *OpenCodeAdapter) Launch(ctx context.Context, req LaunchRequest) (*Process, error) {
	a.mu.Lock()
	if req.MaxTurns > 0 && !a.maxTurnsWarned {
		a.maxTurnsWarned = true
		a.mu.Unlock()
		log.Printf("opencode: max_turns is not supported by OpenCode CLI, ignoring")
	} else {
		a.mu.Unlock()
	}
	args := opencodeBuildArgs(req.Prompt, req.AgentName, req.Worktree, "")
	return a.start(ctx, req.Worktree, req.Timeout, args)
}

// Resume continues the same session; invalid session ID format triggers Launch so cross-provider never receives wrong ID.
func (a *OpenCodeAdapter) Resume(ctx context.Context, req ResumeRequest) (*Process, error) {
	a.mu.Lock()
	if req.MaxTurns > 0 && !a.maxTurnsWarned {
		a.maxTurnsWarned = true
		a.mu.Unlock()
		log.Printf("opencode: max_turns is not supported by OpenCode CLI, ignoring")
	} else {
		a.mu.Unlock()
	}
	if req.SessionID == "" {
		log.Printf("opencode: resume called with empty session_id, launching fresh")
		return a.Launch(ctx, LaunchRequest{
			Worktree: req.Worktree, Prompt: req.Prompt, AgentName: req.AgentName,
			Timeout: req.Timeout, MaxTurns: req.MaxTurns,
		})
	}
	if !opencodeSessionIDRegex.MatchString(req.SessionID) {
		log.Printf("opencode: invalid session_id format (expected ses_ prefix), launching fresh")
		return a.Launch(ctx, LaunchRequest{
			Worktree: req.Worktree, Prompt: req.Prompt, AgentName: req.AgentName,
			Timeout: req.Timeout, MaxTurns: req.MaxTurns,
		})
	}
	args := opencodeBuildArgs(req.Prompt, req.AgentName, req.Worktree, req.SessionID)
	return a.start(ctx, req.Worktree, req.Timeout, args)
}

func opencodeBuildArgs(prompt, agentName, worktree, sessionID string) []string {
	args := []string{"run"}
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	}
	args = append(args, prompt)
	args = append(args, "--format", "json")
	if m := opencodeModelFlag(agentName); m != "" {
		args = append(args, "--model", m)
	}
	args = append(args, "--dir", worktree)
	return args
}

func (a *OpenCodeAdapter) start(ctx context.Context, worktree string, timeout time.Duration, args []string) (*Process, error) {
	artefactDir := filepath.Join(worktree, brand.StateDir(), "artefacts")
	_ = os.MkdirAll(artefactDir, 0755)
	stdoutPath := filepath.Join(artefactDir, "stdout.ndjson")
	stderrPath := filepath.Join(artefactDir, "stderr.txt")

	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return nil, err
	}
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		stdoutFile.Close()
		return nil, err
	}

	cmd := exec.CommandContext(ctx, opencodeBin, args...)
	cmd.Dir = worktree
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	if err := cmd.Start(); err != nil {
		stdoutFile.Close()
		stderrFile.Close()
		return nil, err
	}

	proc := &Process{
		Cmd:        cmd,
		Stdout:     io.NopCloser(strings.NewReader("")),
		Stderr:     io.NopCloser(bytes.NewReader(nil)),
		StdoutFile: stdoutPath,
		StderrFile: stderrPath,
	}
	if timeout > 0 {
		proc.TimeoutDuration = timeout
		proc.Cancel = WithTimeout(proc, timeout)
	}

	a.mu.Lock()
	a.lastLaunchCLI = opencodeBin + " " + strings.Join(args, " ")
	a.procFiles[proc] = &opencodeProcFiles{stdout: stdoutFile, stderr: stderrFile}
	a.mu.Unlock()

	return proc, nil
}

// Stream tails file-backed stdout so OpenCode participates in live turn display.
func (a *OpenCodeAdapter) Stream(process *Process) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go func() {
		defer close(ch)
		f, err := os.Open(process.StdoutFile)
		if err != nil {
			return
		}
		defer f.Close()
		offset := int64(0)
		remain := ""
		deadTicks := 0
		finishedSeen := false
		for {
			buf := make([]byte, 64*1024)
			n, _ := f.ReadAt(buf, offset)
			if n > 0 {
				offset += int64(n)
				blob := remain + string(buf[:n])
				lines := strings.Split(blob, "\n")
				remain = lines[len(lines)-1]
				for _, line := range lines[:len(lines)-1] {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					raw := []byte(line)
					evType := "raw"
					var base struct {
						Type string `json:"type"`
						Part *struct {
							Tokens *struct {
								Input  int `json:"input"`
								Output int `json:"output"`
								Cache  *struct {
									Read int `json:"read"`
								} `json:"cache"`
							} `json:"tokens"`
						} `json:"part"`
					}
					if json.Unmarshal(raw, &base) == nil {
						switch base.Type {
						case "step_start", "tool_use", "text":
							evType = "assistant"
						case "tool_result":
							evType = "user"
						case "step_finish":
							evType = "result"
							finishedSeen = true
							if base.Part != nil && base.Part.Tokens != nil {
								cacheRead := 0
								if base.Part.Tokens.Cache != nil {
									cacheRead = base.Part.Tokens.Cache.Read
								}
								process.AddPartialMetrics(RunResult{
									InputTokens:    base.Part.Tokens.Input,
									OutputTokens:   base.Part.Tokens.Output,
									CacheReadTokens: cacheRead,
									NumTurns:       1,
								})
							}
						default:
							evType = "raw"
						}
					}
					ch <- StreamEvent{Type: evType, Raw: raw}
				}
				deadTicks = 0
			} else {
				if finishedSeen {
					deadTicks++
					if deadTicks > 4 {
						if strings.TrimSpace(remain) != "" {
							ch <- StreamEvent{Type: "raw", Raw: []byte(strings.TrimSpace(remain))}
						}
						return
					}
					time.Sleep(50 * time.Millisecond)
					continue
				}
				if process.Cmd != nil && process.Cmd.Process != nil {
					if err := syscall.Kill(process.Cmd.Process.Pid, 0); err != nil {
						deadTicks++
					}
				}
				if deadTicks > 4 {
					if strings.TrimSpace(remain) != "" {
						ch <- StreamEvent{Type: "raw", Raw: []byte(strings.TrimSpace(remain))}
					}
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()
	return ch
}

// Wait blocks until the process exits, then reads the file-backed stdout and aggregates RunResult from step_finish/text events.
func (a *OpenCodeAdapter) Wait(process *Process) (*RunResult, error) {
	if process.Cancel != nil {
		defer process.Cancel()
	}
	_ = process.Cmd.Wait()

	a.mu.Lock()
	files := a.procFiles[process]
	delete(a.procFiles, process)
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
		if files != nil {
			files.stdout.Close()
			files.stderr.Close()
		}
		return &RunResult{
			ExitCode: -1,
			IsError:  true,
			Result:   "Process killed due to timeout after " + dur.String(),
		}, nil
	}

	if files == nil {
		return &RunResult{ExitCode: exitCode}, nil
	}
	defer files.stdout.Close()
	defer files.stderr.Close()

	// Read stdout from the start; file is fully written after process exit.
	_, _ = files.stdout.Seek(0, 0)
	agg := opencodeAggregateFromReader(files.stdout)

	// Compat: no parseable step_finish so we cannot trust metrics; fail open so diff remains recoverable.
	if agg.NumTurns == 0 {
		log.Printf("opencode: failed to parse events, falling back to compat mode")
		return &RunResult{ExitCode: exitCode, Result: agg.Result}, nil
	}

	res := &RunResult{
		ExitCode:           exitCode,
		SessionID:          agg.SessionID,
		IsError:            agg.IsError,
		DurationMs:         agg.DurationMs,
		NumTurns:           agg.NumTurns,
		Result:             agg.Result,
		InputTokens:        agg.InputTokens,
		OutputTokens:       agg.OutputTokens,
		CacheReadTokens:    agg.CacheReadTokens,
		ModelUsage:         map[string]ModelMetrics{},
	}

	if agg.SessionID == "" {
		log.Printf("opencode: no sessionID in events")
	}
	if agg.NumTurns > 0 && agg.InputTokens == 0 {
		log.Printf("opencode: step_finish events found but no token metrics — compat mode")
	}

	if exitCode != 0 {
		return nil, errOpenCodeCLIFail
	}

	return res, nil
}

var errOpenCodeCLIFail error = &errorOpenCodeCLI{}

type errorOpenCodeCLI struct{}

func (e *errorOpenCodeCLI) Error() string { return "opencode: CLI exited non-zero (e.g. invalid flag or auth)" }

type opencodeAggregate struct {
	SessionID       string
	IsError         bool
	DurationMs      int
	NumTurns        int
	Result          string
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int
	firstTs         int64
	lastTs          int64
	hasStopReason   bool
}

func opencodeAggregateFromReader(r io.Reader) opencodeAggregate {
	var agg opencodeAggregate
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var base struct {
			Type      string `json:"type"`
			Timestamp int64  `json:"timestamp"`
			SessionID string `json:"sessionID"`
			Part      *struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Reason string `json:"reason"`
				Tokens *struct {
					Input  int `json:"input"`
					Output int `json:"output"`
					Cache  *struct {
						Read int `json:"read"`
					} `json:"cache"`
				} `json:"tokens"`
			} `json:"part"`
		}
		if json.Unmarshal(line, &base) != nil {
			continue
		}
		if base.SessionID != "" {
			agg.SessionID = base.SessionID
		}
		if base.Timestamp > 0 {
			if agg.firstTs == 0 {
				agg.firstTs = base.Timestamp
			}
			agg.lastTs = base.Timestamp
		}
		switch base.Type {
		case "text":
			if base.Part != nil && base.Part.Text != "" {
				agg.Result = base.Part.Text
			}
		case "step_finish":
			agg.NumTurns++
			if base.Part != nil {
				if base.Part.Reason == "stop" || base.Part.Reason == "completed" {
					agg.hasStopReason = true
				}
				if base.Part.Tokens != nil {
					agg.InputTokens += base.Part.Tokens.Input
					agg.OutputTokens += base.Part.Tokens.Output
					if base.Part.Tokens.Cache != nil {
						agg.CacheReadTokens += base.Part.Tokens.Cache.Read
					}
				}
			}
		}
	}
	if agg.lastTs > agg.firstTs {
		agg.DurationMs = int(agg.lastTs - agg.firstTs)
	}
	agg.IsError = !agg.hasStopReason
	if agg.NumTurns == 0 && agg.SessionID == "" {
		agg.IsError = true
	}
	return agg
}

// LastLaunchCLI returns the full CLI command used for the last Launch or Resume.
func (a *OpenCodeAdapter) LastLaunchCLI() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastLaunchCLI
}
