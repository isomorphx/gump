package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

func scenarioCostAndSessionForStep(worktree, stepName string) (cost float64, sessionID string) {
	data, err := os.ReadFile(filepath.Join(worktree, ".pudding-test-scenario.json"))
	if err != nil {
		return 0, ""
	}
	var scenario struct {
		CostUSD           float64            `json:"cost_usd"`
		CostUSDByStep     map[string]float64 `json:"cost_usd_by_step"`
		SessionIDByStep   map[string]string  `json:"session_id_by_step"`
		UniqueSessionEach bool               `json:"unique_session_each_call"`
	}
	if json.Unmarshal(data, &scenario) != nil {
		return 0, ""
	}
	if scenario.CostUSDByStep != nil {
		if v, ok := scenario.CostUSDByStep[stepName]; ok {
			cost = v
		}
	}
	if cost == 0 && scenario.CostUSD > 0 {
		cost = scenario.CostUSD
	}
	if scenario.UniqueSessionEach {
		return cost, fmt.Sprintf("stub-%d", time.Now().UnixNano())
	}
	if scenario.SessionIDByStep != nil {
		sessionID = scenario.SessionIDByStep[stepName]
	}
	return cost, sessionID
}

const (
	planMarker     = "[PUDDING:plan]"
	artifactMarker = "[PUDDING:artifact]"
	reviewMarker   = "[PUDDING:review]"
	stubSessionID  = "stub-session-id"
)

// StubAdapter simulates an agent for e2e tests: writes plan-output.json or <step>.stub and emits NDJSON so the engine sees a real stream/result flow.
type StubAdapter struct {
	mu               sync.Mutex
	lastResultByProc map[*Process]*RunResult
	lastLaunchCLI    string
}

// Launch runs stub logic in a goroutine (writes files, emits NDJSON to a pipe) and returns a Process the engine can Stream then Wait.
func (s *StubAdapter) Launch(ctx context.Context, req LaunchRequest) (*Process, error) {
	return s.run(ctx, req.Worktree, req.Prompt, req.AgentName, req.Timeout, "")
}

// Resume behaves like Launch; the stub ignores session for simplicity.
func (s *StubAdapter) Resume(ctx context.Context, req ResumeRequest) (*Process, error) {
	return s.run(ctx, req.Worktree, req.Prompt, req.AgentName, req.Timeout, req.SessionID)
}

func (s *StubAdapter) run(ctx context.Context, worktree, prompt, agentName string, timeout time.Duration, sessionID string) (*Process, error) {
	if s.lastResultByProc == nil {
		s.lastResultByProc = make(map[*Process]*RunResult)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	artefactDir := filepath.Join(worktree, ".pudding", "artefacts")
	outDir := filepath.Join(worktree, ".pudding", "out")
	_ = os.MkdirAll(artefactDir, 0755)
	_ = os.MkdirAll(outDir, 0755)
	stdoutPath := filepath.Join(artefactDir, "stdout.ndjson")
	stderrPath := filepath.Join(artefactDir, "stderr.txt")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		pr.Close()
		pw.Close()
		return nil, err
	}
	stderrFile, _ := os.Create(stderrPath)

	// When the engine asks for a timeout (E2E 7), run a long sleep so the timeout goroutine can SIGTERM/SIGKILL it.
	cmdExe := "true"
	cmdArgs := []string{}
	if timeout > 0 && strings.Contains(prompt, "[PUDDING:timeout]") {
		cmdExe = "sleep"
		cmdArgs = []string{"3600"}
	}
	s.mu.Lock()
	s.lastLaunchCLI = cmdExe + " " + strings.Join(cmdArgs, " ")
	if sessionID != "" {
		s.lastLaunchCLI += " --resume " + sessionID
	}
	s.mu.Unlock()
	cmd := exec.CommandContext(ctx, cmdExe, cmdArgs...)
	proc := &Process{
		Cmd:        cmd,
		Stdout:     pr,
		Stderr:     io.NopCloser(bytes.NewReader(nil)),
		StdoutFile: stdoutPath,
		StderrFile: stderrPath,
	}
	if err := cmd.Start(); err != nil {
		stdoutFile.Close()
		stderrFile.Close()
		pr.Close()
		pw.Close()
		return nil, err
	}

	if timeout > 0 && cmdExe == "sleep" {
		proc.TimeoutDuration = timeout
		proc.Cancel = WithTimeout(proc, timeout)
	}

	s.mu.Lock()
	s.lastResultByProc[proc] = nil
	s.mu.Unlock()

	go func() {
		defer pw.Close()
		defer stdoutFile.Close()
		defer stderrFile.Close()

		stepName := extractStepNameFromPrompt(prompt)
		costUSD, sessOverride := scenarioCostAndSessionForStep(worktree, stepName)
		stubSid := stubSessionID
		if sessOverride != "" {
			stubSid = sessOverride
		}
		isPlan := strings.Contains(prompt, planMarker)
		isArtifact := strings.Contains(prompt, artifactMarker)
		isReview := strings.Contains(prompt, reviewMarker)
		if isPlan {
			planPath := filepath.Join(outDir, "plan.json")
			planContent := []byte(`[
  {"name": "task-1", "description": "Stub task 1", "files": ["file1.go"]},
  {"name": "task-2", "description": "Stub task 2", "files": ["file2.go"]}
]`)
			if custom, err := os.ReadFile(filepath.Join(worktree, ".pudding-test-plan.json")); err == nil {
				planContent = custom
			}
			_ = os.WriteFile(planPath, planContent, 0644)
			filename := stepName + ".stub"
			if stepName == "" {
				filename = "step.stub"
			}
			_ = os.WriteFile(filepath.Join(worktree, filename), []byte("stub output"), 0644)
		} else if isArtifact {
			artifactPath := filepath.Join(outDir, "artifact.txt")
			_ = os.WriteFile(artifactPath, []byte("stub artifact output for "+stepName), 0644)
			filename := stepName + ".stub"
			if stepName == "" {
				filename = "step.stub"
			}
			_ = os.WriteFile(filepath.Join(worktree, filename), []byte("stub output"), 0644)
		} else if isReview {
			reviewPath := filepath.Join(outDir, "review.json")
			if body := reviewJSONFromScenario(worktree, stepName); body != "" {
				_ = os.WriteFile(reviewPath, []byte(body), 0644)
			} else {
				_ = os.WriteFile(reviewPath, []byte(`{"pass":true,"comment":"stub review ok"}`), 0644)
			}
			if applyTestScenarioWithStep(worktree, stepName) {
				// scenario supplies files (e.g. compile gate)
			} else {
				filename := stepName + ".stub"
				if stepName == "" {
					filename = "step.stub"
				}
				_ = os.WriteFile(filepath.Join(worktree, filename), []byte("stub output"), 0644)
			}
		} else {
			if applyTestScenarioWithStep(worktree, stepName) {
				// Scenario file defined code files; no .stub
			} else {
				filename := stepName + ".stub"
				if stepName == "" {
					filename = "step.stub"
				}
				_ = os.WriteFile(filepath.Join(worktree, filename), []byte("stub output"), 0644)
			}
		}

		writeLineObserved(pw, stdoutFile, `{"type":"system","subtype":"init","session_id":"`+stubSid+`"}`)
		writeLineObserved(pw, stdoutFile, `{"type":"assistant","message":{"content":[{"type":"text","text":"stub done"}]}}`)
		result := fmt.Sprintf(`{"type":"result","session_id":"%s","is_error":false,"duration_ms":0,"duration_api_ms":0,"num_turns":1,"result":"ok","usage":{},"total_cost_usd":%g}`, stubSid, costUSD)
		writeLineObserved(pw, stdoutFile, result)
	}()

	return proc, nil
}

// writeLineObserved mirrors process.Start: raw NDJSON to the stream reader, timestamp-prefixed lines to the artefact file.
func writeLineObserved(stream io.Writer, stdoutFile *os.File, line string) {
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	fmt.Fprintf(stdoutFile, "%s %s\n", ts, line)
	_, _ = stream.Write([]byte(line + "\n"))
}

func isReplanSubTaskContext(worktree string) bool {
	for _, name := range AllProviderContextFiles {
		body, err := os.ReadFile(filepath.Join(worktree, name))
		if err != nil {
			continue
		}
		if strings.Contains(string(body), "replan sub-task") || strings.Contains(string(body), "Implementation (replan") {
			return true
		}
	}
	return false
}

// attemptFromContextFile reads provider context files in worktree and returns the attempt number if "Attempt N/M" is present, else 1.
func restartCycleFromWorktree(worktree string) int {
	b, err := os.ReadFile(filepath.Join(worktree, ".pudding", "restart-cycle"))
	if err != nil {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &n)
	return n
}

// reviewJSONFromScenario returns custom review.json body when scenario sets review_by_cycle or review_by_step.
func reviewJSONFromScenario(worktree, stepName string) string {
	data, err := os.ReadFile(filepath.Join(worktree, ".pudding-test-scenario.json"))
	if err != nil {
		return ""
	}
	var scenario struct {
		ReviewByCycle map[string]string `json:"review_by_cycle"`
		ReviewByStep  map[string]string `json:"review_by_step"`
	}
	if json.Unmarshal(data, &scenario) != nil {
		return ""
	}
	cycle := restartCycleFromWorktree(worktree)
	if scenario.ReviewByCycle != nil {
		if body, ok := scenario.ReviewByCycle[strconv.Itoa(cycle)]; ok && body != "" {
			return body
		}
	}
	if stepName != "" && scenario.ReviewByStep != nil {
		if body, ok := scenario.ReviewByStep[stepName]; ok && body != "" {
			return body
		}
	}
	return ""
}

func attemptFromContextFile(worktree string) int {
	for _, name := range AllProviderContextFiles {
		body, err := os.ReadFile(filepath.Join(worktree, name))
		if err != nil {
			continue
		}
		s := string(body)
		if m := regexp.MustCompile(`(?i)(?:retry )?attempt (\d+) of (\d+)`).FindStringSubmatch(s); len(m) >= 2 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
		if m := regexp.MustCompile(`Attempt (\d+)/(\d+)`).FindStringSubmatch(s); len(m) >= 2 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
	}
	return 1
}

// applyTestScenario reads .pudding-test-scenario.json and creates the listed files so e2e can drive validation with real code.
// When "by_attempt" is present, the attempt number is detected from the context file (Attempt N/M) and the matching files for that attempt are used, merged with root "files".
// When "by_step" is present, stepName (from the prompt) selects which files to merge so parallel steps can write disjoint files.
func applyTestScenario(worktree string) bool {
	return applyTestScenarioWithStep(worktree, "")
}
func applyTestScenarioWithStep(worktree string, stepName string) bool {
	data, err := os.ReadFile(filepath.Join(worktree, ".pudding-test-scenario.json"))
	if err != nil {
		return false
	}
	var scenario struct {
		Files     map[string]string            `json:"files"`
		ByAttempt map[string]struct{ Files map[string]string `json:"files"` } `json:"by_attempt"`
		ByStep    map[string]struct{ Files map[string]string `json:"files"` } `json:"by_step"`
		ByRestart map[string]struct{ Files map[string]string `json:"files"` } `json:"by_restart"`
	}
	if err := json.Unmarshal(data, &scenario); err != nil {
		return false
	}
	merged := make(map[string]string)
	if scenario.Files != nil {
		for k, v := range scenario.Files {
			merged[k] = v
		}
	}
	if stepName != "" && scenario.ByStep != nil {
		if s, ok := scenario.ByStep[stepName]; ok && s.Files != nil {
			for k, v := range s.Files {
				merged[k] = v
			}
		}
	}
	// Replan sub-tasks have no "Attempt N/M" in context; use only root files so e2e R6 can pass (sub-tasks get correct code).
	if len(scenario.ByAttempt) > 0 && !isReplanSubTaskContext(worktree) {
		attempt := attemptFromContextFile(worktree)
		if b, err := os.ReadFile(filepath.Join(worktree, ".pudding", "group-attempt")); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				attempt = n
			}
		}
		key := strconv.Itoa(attempt)
		if a, ok := scenario.ByAttempt[key]; ok && a.Files != nil {
			for k, v := range a.Files {
				merged[k] = v
			}
		}
	}
	cycle := restartCycleFromWorktree(worktree)
	if len(scenario.ByRestart) > 0 && cycle > 0 {
		key := strconv.Itoa(cycle)
		if a, ok := scenario.ByRestart[key]; ok && a.Files != nil {
			for k, v := range a.Files {
				merged[k] = v
			}
		}
	}
	if len(merged) == 0 {
		return false
	}
	for path, content := range merged {
		full := filepath.Join(worktree, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			continue
		}
		_ = os.WriteFile(full, []byte(content), 0644)
	}
	return true
}

// Stream reads NDJSON from the process stdout and sends events; stores the last type=result for Wait.
func (s *StubAdapter) Stream(process *Process) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(process.Stdout)
		scanner.Buffer(nil, 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			raw := make([]byte, len(line))
			copy(raw, line)
			var base struct {
				Type string `json:"type"`
			}
			evType := "raw"
			if json.Unmarshal(line, &base) == nil {
				evType = base.Type
			}
			if evType == "result" {
				res, err := ParseResultJSON(line)
				if err == nil {
					s.mu.Lock()
					s.lastResultByProc[process] = res
					s.mu.Unlock()
				}
			}
			ch <- StreamEvent{Type: evType, Raw: raw}
		}
		_ = process.Stdout.Close()
	}()
	return ch
}

// Wait blocks until the stub process exits, then returns the last type=result or compat RunResult. On timeout (process.TimedOut), returns a failure RunResult so the engine treats it as step failure.
func (s *StubAdapter) Wait(process *Process) (*RunResult, error) {
	if process.Cancel != nil {
		defer process.Cancel()
	}
	_ = process.Cmd.Wait()
	s.mu.Lock()
	last := s.lastResultByProc[process]
	delete(s.lastResultByProc, process)
	s.mu.Unlock()

	exitCode := 0
	if process.Cmd.ProcessState != nil {
		exitCode = process.Cmd.ProcessState.ExitCode()
	}
	if process.TimedOut {
		return &RunResult{ExitCode: -1, IsError: true, Result: "Process killed due to timeout after " + process.TimeoutDuration.String()}, nil
	}
	if last != nil {
		last.ExitCode = exitCode
		return last, nil
	}
	return &RunResult{ExitCode: exitCode}, nil
}

// LastLaunchCLI returns the command used for the last Launch/Resume (for ledger/debug).
func (s *StubAdapter) LastLaunchCLI() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastLaunchCLI
}

// StubResolver returns the same stub for any agent name so e2e tests can run without real CLIs.
type StubResolver struct {
	Stub *StubAdapter
}

// AdapterFor implements AdapterResolver; always returns the single StubAdapter.
func (s *StubResolver) AdapterFor(agentName string) (AgentAdapter, error) {
	if s.Stub == nil {
		s.Stub = &StubAdapter{}
	}
	return s.Stub, nil
}

func extractStepNameFromPrompt(prompt string) string {
	const prefix = "[PUDDING:step:"
	idx := strings.Index(prompt, prefix)
	if idx < 0 {
		return ""
	}
	start := idx + len(prefix)
	end := strings.Index(prompt[start:], "]")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(prompt[start : start+end])
}
