package agent

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Ensure Process and RunResult stay in sync with the adapter implementations.

// AgentAdapter is the contract for running coding agents (Claude Code, etc.) so the engine
// stays provider-agnostic and can swap adapters for tests or different CLIs.
type AgentAdapter interface {
	Launch(ctx context.Context, req LaunchRequest) (*Process, error)
	Resume(ctx context.Context, req ResumeRequest) (*Process, error)
	Stream(process *Process) <-chan StreamEvent
	Wait(process *Process) (*RunResult, error)
	// LastLaunchCLI returns the full CLI command used for the last Launch or Resume (for ledger/debug).
	LastLaunchCLI() string
}

// LaunchRequest carries everything needed to start a fresh agent run in the worktree.
type LaunchRequest struct {
	Worktree  string
	Prompt    string
	AgentName string
	Timeout   time.Duration
	MaxTurns  int
	// StepPath is the fully-qualified engine step path (e.g. decompose/t1/impl); used by StubAdapter for per-step fixtures.
	StepPath string
}

// ResumeRequest is like LaunchRequest but asks the provider to resume an existing session;
// adapters that don't support resume fall back to Launch and log a warning.
type ResumeRequest struct {
	Worktree  string
	Prompt    string
	AgentName string
	SessionID string
	Timeout   time.Duration
	MaxTurns  int
	StepPath string
}

// Process represents a running agent subprocess; the engine streams stdout and then Waits.
// TimedOut and TimeoutDuration are set when the process is killed by timeout so Wait can return the spec RunResult.
type Process struct {
	Cmd              *exec.Cmd
	Stdout           io.ReadCloser
	Stderr           io.ReadCloser
	StdoutFile       string
	StderrFile       string
	Cancel           context.CancelFunc
	TimedOut         bool
	TimeoutDuration  time.Duration
	partialMu        sync.RWMutex
	partial          RunResult
}

// StreamEvent is one line from the agent's NDJSON stdout; Type drives terminal display,
// Raw is always written to the artefact file.
type StreamEvent struct {
	Type string
	Raw  []byte
}

// RunResult is the canonical outcome of an agent run. Success is defined by type=result
// and is_error==false in the stream — never by process exit code.
type RunResult struct {
	ExitCode            int
	SessionID            string
	IsError              bool
	DurationMs           int
	DurationAPI          int
	InputTokens          int
	OutputTokens         int
	CacheCreationTokens  int
	CacheReadTokens      int
	CostUSD              float64
	ModelUsage           map[string]ModelMetrics
	Result               string
	NumTurns             int
}

// ModelMetrics holds per-model token and cost breakdown for reporting.
type ModelMetrics struct {
	InputTokens          int
	OutputTokens         int
	CacheCreationTokens  int
	CacheReadTokens      int
	CostUSD              float64
}

// AddPartialMetrics accumulates best-effort metrics seen before process completion.
func (p *Process) AddPartialMetrics(delta RunResult) {
	if p == nil {
		return
	}
	p.partialMu.Lock()
	defer p.partialMu.Unlock()
	p.partial.InputTokens += delta.InputTokens
	p.partial.OutputTokens += delta.OutputTokens
	p.partial.CacheReadTokens += delta.CacheReadTokens
	p.partial.CacheCreationTokens += delta.CacheCreationTokens
	p.partial.NumTurns += delta.NumTurns
	p.partial.CostUSD += delta.CostUSD
}

// PartialMetrics returns metrics accumulated so far (used before kill paths).
func (p *Process) PartialMetrics() RunResult {
	if p == nil {
		return RunResult{}
	}
	p.partialMu.RLock()
	defer p.partialMu.RUnlock()
	return p.partial
}
