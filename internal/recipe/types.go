package recipe

// Recipe is the v4 mapping: name, description, steps, with optional max_budget.
type Recipe struct {
	Name        string  `yaml:"name"`
	Description string  `yaml:"description"`
	Steps       []Step  `yaml:"steps"`
	MaxBudget   float64 `yaml:"max_budget"`
}

// SessionConfig is the parsed session policy: reuse, fresh, or reuse-targeted (reuse: step-name).
type SessionConfig struct {
	Mode   string `yaml:"-"` // "reuse", "fresh", "reuse-targeted"
	Target string `yaml:"-"` // step name when Mode == "reuse-targeted"
}

// Step is one workflow step; behavior is inferred from fields (no type:).
// Output applies only to atomic steps (agent set, no steps); default "diff".
type Step struct {
	Name   string `yaml:"name"`
	Agent  string `yaml:"agent"`
	Prompt string `yaml:"prompt"`

	Output string `yaml:"output"` // "diff" (default), "plan", "artifact", "review"

	Context []ContextSource `yaml:"context"`
	Session SessionConfig    `yaml:"-"`
	Timeout string           `yaml:"timeout"`

	// v4 budget gates the cost model only (runtime is unchanged in spec M1).
	MaxBudget float64 `yaml:"max_budget"`
	HITL      bool    `yaml:"hitl"`

	// v4 fields (exposed for dry-run/tests; parser normalises them).
	Gate      []Validator `yaml:"-"`
	OnFailure *OnFailure  `yaml:"-"`

	// Orchestration step fields.
	Steps    []Step `yaml:"steps"`
	Foreach  string `yaml:"foreach"` // name of step with output: plan to iterate
	Recipe   string `yaml:"recipe"`
	Parallel bool   `yaml:"parallel"`

	MaxTurns int `yaml:"max_turns"`

	// Legacy fields used by the current engine (spec M3 updates engine; in M1 we only normalise).
	Validate []Validator  `yaml:"-"`
	Retry    *RetryPolicy `yaml:"-"`
}

// RetryPolicy defines max attempts and the strategy list (same / escalate / replan).
type RetryPolicy struct {
	MaxAttempts int             `yaml:"max_attempts"`
	Strategy    []StrategyEntry `yaml:"strategy"`
}

// OnFailure is the v4 replacement for RetryPolicy (parser normalises it).
type OnFailure struct {
	Retry       int             `yaml:"retry"`        // max attempts (incl. first)
	Strategy    []StrategyEntry `yaml:"strategy"`
	RestartFrom string          `yaml:"restart_from"`
}

// StrategyEntry is one strategy slot; Count expands shorthand (e.g. same: 3).
type StrategyEntry struct {
	Type  string `yaml:"type"`
	Agent string `yaml:"agent"`
	Count int    `yaml:"count"`
}

// Validator is a single validation rule (compile, test, touched: glob, etc.).
type Validator struct {
	Type string `yaml:"type"`
	Arg  string `yaml:"arg"`
}

// ContextSource is a single context entry. Spec format: - file: "path" or - bash: "cmd".
type ContextSource struct {
	File string `yaml:"file"`
	Bash string `yaml:"bash"`
}
