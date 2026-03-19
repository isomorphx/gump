package recipe

// Recipe is the v3 mapping: name, description, steps. Top-level review is removed so validation is explicit as a final step in steps.
type Recipe struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Steps       []Step `yaml:"steps"`
}

// SessionConfig is the parsed session policy: reuse, fresh, or reuse-targeted (reuse: step-name).
type SessionConfig struct {
	Mode   string `yaml:"-"` // "reuse", "fresh", "reuse-targeted"
	Target string `yaml:"-"` // step name when Mode == "reuse-targeted"
}

// Step is one workflow step; behavior is inferred from fields (no type:).
// Output applies only to atomic steps (agent set, no steps); default "diff".
type Step struct {
	Name     string         `yaml:"name"`
	Agent    string         `yaml:"agent"`
	Prompt   string         `yaml:"prompt"`
	Output   string         `yaml:"output"` // "diff" (default), "plan", "artifact"
	Validate []Validator    `yaml:"validate"`
	Retry    *RetryPolicy   `yaml:"retry"`
	Steps    []Step         `yaml:"steps"`
	Foreach  string         `yaml:"foreach"` // name of step with output: plan to iterate
	Recipe   string         `yaml:"recipe"`
	Parallel bool           `yaml:"parallel"`
	Session  SessionConfig  `yaml:"-"`
	Context  []ContextSource `yaml:"context"`
	Timeout  string         `yaml:"timeout"`
	MaxTurns int            `yaml:"max_turns"`
}

// RetryPolicy defines max attempts and the strategy list (same / escalate / replan).
type RetryPolicy struct {
	MaxAttempts int             `yaml:"max_attempts"`
	Strategy    []StrategyEntry `yaml:"strategy"`
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
