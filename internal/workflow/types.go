package workflow

// Workflow is the v0.0.4 root document (replaces the old recipe shape for parsing and validation).
type Workflow struct {
	Name        string
	MaxBudget   float64
	MaxTimeout  string
	MaxTokens   int
	Steps       []Step
}

// Step is one unit in the workflow graph: code execution, split orchestration, validate, gate-only, parallel group, or workflow call.
type Step struct {
	Name        string
	Type        string
	Prompt      string
	Context     []ContextSource
	Worktree    string
	Session     SessionConfig
	Agent       string
	Guard       Guard
	HITL        string
	Gate        []GateEntry
	Retry       []RetryEntry
	Each        []Step
	Parallel    bool
	Steps       []Step
	Workflow    string
	With        map[string]string
	GetWorkflow *GetWorkflowSpec
}

// GetWorkflowSpec is a nested workflow run during GET (R5); state is grafted under Name for prompt resolution.
type GetWorkflowSpec struct {
	Name string
	Path string
	With map[string]string
}

// SessionConfig selects whether the agent starts clean or continues another step’s session.
type SessionConfig struct {
	Mode   string
	Target string
}

// Guard holds optional circuit-breakers for an agent run.
type Guard struct {
	MaxTurns  int
	MaxBudget float64
	MaxTokens int
	MaxTime   string
	NoWrite   *bool
}

// GateEntry is one check in the gate phase (compile, external validate workflow, etc.).
type GateEntry struct {
	Type string
	Arg  string
	With map[string]string
}

// RetryEntry is one branch in the ordered retry policy (v0.0.4 replaces on_failure/strategy).
type RetryEntry struct {
	Attempt  int
	Not      string
	Validate string
	Exit     int
	With     map[string]string
	Agent    string
	Session  string
	Worktree string
	Prompt   string
}

// ContextSource is materialized context (file path or shell snippet).
type ContextSource struct {
	Type  string
	Value string
}

// Warning records a deprecated keyword still present in YAML; parsing continues.
type Warning struct {
	Message string
}

// OnFailureCompat mirrors the pre-R3 execution model so the engine can keep one retry loop until it natively understands retry: (R3).
type OnFailureCompat struct {
	Retry       int
	Strategy    []StrategyEntryCompat
	RestartFrom string
	GateFail    *FailureActionCompat
	GuardFail   *FailureActionCompat
	ReviewFail  *FailureActionCompat
}

// FailureActionCompat is one branch under legacy conditional on_failure.
type FailureActionCompat struct {
	Retry       int
	Strategy    []StrategyEntryCompat
	RestartFrom string
}

// StrategyEntryCompat is one strategy slot for the compat layer.
type StrategyEntryCompat struct {
	Type  string
	Agent string
	Count int
}
