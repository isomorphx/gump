package ledger

// Event is the common interface for all ledger events. Emit adds ts and type from EventType().
type Event interface {
	EventType() string
}

// RunStarted is emitted at the very start of a run so the ledger has full run context.
// WHY: G1 rebrands ledger domain objects from cook->run.
type RunStarted struct {
	RunID     string            `json:"run_id"`
	Workflow  string            `json:"workflow"`
	Spec      string            `json:"spec"`
	Commit    string            `json:"commit"`
	Branch    string            `json:"branch"`
	AgentsCLI map[string]string `json:"agents_cli"`
	MaxBudget float64           `json:"max_budget,omitempty"`
}

func (RunStarted) EventType() string { return "run_started" }

// StepStarted marks the beginning of one step attempt; used for timing and retry counts.
type StepStarted struct {
	Step        string `json:"step"`
	Agent       string `json:"agent"`
	OutputMode  string `json:"output_mode"`
	Item        string `json:"item"`
	Attempt     int    `json:"attempt"`
	SessionMode string `json:"session_mode"`
}

func (StepStarted) EventType() string { return "step_started" }

// GroupStarted marks the beginning of a composite group (with optional foreach).
type GroupStarted struct {
	Step      string `json:"step"`
	Foreach   string `json:"foreach"`
	Parallel  bool   `json:"parallel"`
	TaskCount int    `json:"task_count"`
}

func (GroupStarted) EventType() string { return "group_started" }

// AgentLaunched is emitted after the agent process starts so we can correlate CLI and prompt.
type AgentLaunched struct {
	Step          string `json:"step"`
	CLI           string `json:"cli"`
	Worktree      string `json:"worktree"`
	Agent         string `json:"agent"`
	SessionID     string `json:"session_id"`
	SessionSource string `json:"session_source"`
	PromptHash    string `json:"prompt_hash"`
}

func (AgentLaunched) EventType() string { return "agent_launched" }

// AgentCompleted is emitted after Wait(); artifacts must already be written (invariant).
type AgentCompleted struct {
	Step       string            `json:"step"`
	ExitCode   int               `json:"exit_code"`
	DurationMs int               `json:"duration_ms"`
	TokensIn   int               `json:"tokens_in"`
	TokensOut  int               `json:"tokens_out"`
	CostUSD    float64           `json:"cost_usd"`
	SessionID  string            `json:"session_id"`
	IsError    bool              `json:"is_error"`
	Artifacts  map[string]string `json:"artifacts"`
}

func (AgentCompleted) EventType() string { return "agent_completed" }

type AgentKilled struct {
	Step         string  `json:"step"`
	Reason       string  `json:"reason"`
	DurationMs   int     `json:"duration_ms"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	TurnsPartial int     `json:"turns_partial"`
}

func (AgentKilled) EventType() string { return "agent_killed" }

// StateBagUpdated is emitted after each Set so report can show which outputs were stored.
type StateBagUpdated struct {
	Key      string `json:"key"`
	Artifact string `json:"artifact"`
}

func (StateBagUpdated) EventType() string { return "state_bag_updated" }

// StateBagScopeReset is emitted when a group retry resets scope (keys moved to prev).
type StateBagScopeReset struct {
	Group string   `json:"group"`
	Keys  []string `json:"keys"`
}

func (StateBagScopeReset) EventType() string { return "state_bag_scope_reset" }

// GateStarted is emitted before running gate checks so audits know what was evaluated.
type GateStarted struct {
	Step   string   `json:"step"`
	Checks []string `json:"checks"`
}

func (GateStarted) EventType() string { return "gate_started" }

// GatePassed is emitted when all gate checks pass; artifact path must already exist.
type GatePassed struct {
	Step     string `json:"step"`
	Artifact string `json:"artifact"`
}

func (GatePassed) EventType() string { return "gate_passed" }

// GateFailed is emitted when at least one gate check fails; reason is first stderr line.
type GateFailed struct {
	Step     string `json:"step"`
	Reason   string `json:"reason"`
	Artifact string `json:"artifact"`
}

func (GateFailed) EventType() string { return "gate_failed" }

type GuardTriggered struct {
	Step   string `json:"step"`
	Guard  string `json:"guard"`
	Reason string `json:"reason"`
}

func (GuardTriggered) EventType() string { return "guard_triggered" }

// HITLPaused is emitted when the cook blocks for human review before continuing.
type HITLPaused struct {
	Step string `json:"step"`
}

func (HITLPaused) EventType() string { return "hitl_paused" }

// HITLResumed is emitted after the operator continues or aborts a HITL pause.
type HITLResumed struct {
	Step   string `json:"step"`
	Action string `json:"action"`
}

func (HITLResumed) EventType() string { return "hitl_resumed" }

// BudgetExceeded is emitted when spend crosses a configured limit so the ledger shows why the run stopped.
type BudgetExceeded struct {
	Step     string  `json:"step"`
	Scope    string  `json:"scope"`
	MaxUSD   float64 `json:"max_usd"`
	SpentUSD float64 `json:"spent_usd"`
}

func (BudgetExceeded) EventType() string { return "budget_exceeded" }

// RetryTriggered is emitted when the retry engine decides to retry (before worktree reset).
type RetryTriggered struct {
	Step     string `json:"step"`
	Attempt  int    `json:"attempt"`
	Strategy string `json:"strategy"`
	Scope    string `json:"scope"`
}

func (RetryTriggered) EventType() string { return "retry_triggered" }

// ReplanTriggered is emitted when replan produces a valid plan (artifact path must exist).
type ReplanTriggered struct {
	Step     string `json:"step"`
	Agent    string `json:"agent"`
	Artifact string `json:"artifact"`
}

func (ReplanTriggered) EventType() string { return "replan_triggered" }

// StepCompleted is emitted at the end of a step (pass or fatal); artifacts already written.
// Commit is the worktree HEAD after this step (for replay: restore worktree from this commit before next step).
type StepCompleted struct {
	Step       string            `json:"step"`
	Status     string            `json:"status"`
	DurationMs int               `json:"duration_ms"`
	Artifacts  map[string]string `json:"artifacts"`
	Commit     string            `json:"commit,omitempty"`
}

func (StepCompleted) EventType() string { return "step_completed" }

// ReplayStarted is emitted at the start of a replay run; references the original fatal cook.
type ReplayStarted struct {
	OriginalCookID string `json:"original_cook_id"`
	FromStep       string `json:"from_step"`
	RestoredCommit string `json:"restored_commit"`
}

func (ReplayStarted) EventType() string { return "replay_started" }

// GroupCompleted is emitted at the end of a composite group.
type GroupCompleted struct {
	Step       string `json:"step"`
	Status     string `json:"status"`
	Iterations int    `json:"iterations"`
	Attempts   int    `json:"attempts"`
	DurationMs int    `json:"duration_ms"`
}

func (GroupCompleted) EventType() string { return "group_completed" }

// GroupRetry is emitted at the start of a group retry (after worktree reset, before re-run).
type GroupRetry struct {
	Step     string `json:"step"`
	Attempt  int    `json:"attempt"`
	Strategy string `json:"strategy"`
}

func (GroupRetry) EventType() string { return "group_retry" }

// CircuitBreaker is emitted when all retries are exhausted and the step is marked fatal.
type CircuitBreaker struct {
	Step          string `json:"step"`
	Scope         string `json:"scope"`
	Reason        string `json:"reason"`
	TotalAttempts int    `json:"total_attempts"`
}

func (CircuitBreaker) EventType() string { return "circuit_breaker" }

// RunCompleted is emitted at the very end of Run(); state-bag and final-diff must already be written.
type RunCompleted struct {
	RunID      string            `json:"run_id"`
	Status     string            `json:"status"`
	DurationMs int               `json:"duration_ms"`
	TotalCost  float64           `json:"total_cost_usd"`
	Artifacts  map[string]string `json:"artifacts"`
}

func (RunCompleted) EventType() string { return "run_completed" }

// Legacy aliases kept to avoid breaking non-G1 internal call sites.
type CookStarted = RunStarted
type CookCompleted = RunCompleted
