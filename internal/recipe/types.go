package recipe

import "strings"

// Recipe is the v4 mapping: name, description, steps, with optional max_budget.
type Recipe struct {
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	Steps       []Step              `yaml:"steps"`
	MaxBudget   float64             `yaml:"max_budget"`
	Inputs      map[string]InputDef `yaml:"inputs"`
}

type InputDef struct {
	Required bool   `yaml:"required"`
	Default  string `yaml:"default"`
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
	Session SessionConfig   `yaml:"-"`
	Timeout string          `yaml:"timeout"`

	// v4 budget gates the cost model only (runtime is unchanged in spec M1).
	MaxBudget float64 `yaml:"max_budget"`
	HITL      bool    `yaml:"hitl"`

	// v4 fields (exposed for dry-run/tests; parser normalises them).
	Gate      []Validator `yaml:"-"`
	OnFailure *OnFailure  `yaml:"-"`

	// Orchestration step fields.
	Steps    []Step            `yaml:"steps"`
	Foreach  string            `yaml:"foreach"` // name of step with output: plan to iterate
	Recipe   string            `yaml:"recipe"`  // legacy alias for Workflow
	Workflow string            `yaml:"workflow"`
	With     map[string]string `yaml:"with"`
	Parallel bool              `yaml:"parallel"`
	Guard    Guard             `yaml:"guard"`
	// WHY: lets validator reject explicit max_turns: 0 while keeping omitted value optional.
	GuardMaxTurnsSet bool `yaml:"-"`
	// WHY: lets validator reject explicit max_budget: 0 while keeping omitted value optional.
	GuardMaxBudgetSet bool `yaml:"-"`

	MaxTurns int `yaml:"max_turns"`
}

type Guard struct {
	MaxTurns  int     `yaml:"max_turns"`
	MaxBudget float64 `yaml:"max_budget"`
	NoWrite   *bool   `yaml:"no_write"`
}

// MaxAttempts is the total allowed gate attempts for this step (first try + retries); 1 means no retry loop.
func (s *Step) MaxAttempts() int {
	if s.OnFailure == nil || s.OnFailure.Retry <= 0 {
		return 1
	}
	return s.OnFailure.Retry
}

// RestartFromWithoutStrategy is true when on_failure only requests restart_from (no same/escalate/replan slots).
func (s *Step) RestartFromWithoutStrategy() bool {
	if s.OnFailure == nil || strings.TrimSpace(s.OnFailure.RestartFrom) == "" {
		return false
	}
	return len(s.ExpandedOnFailureStrategy()) == 0
}

// ShouldRunWithRetryLoop is true when the step uses RunWithRetry (retries and/or restart_from-only).
func (s *Step) ShouldRunWithRetryLoop() bool {
	if s.OnFailure == nil {
		return false
	}
	return s.MaxAttempts() > 1 || s.RestartFromWithoutStrategy()
}

// ExpandedOnFailureStrategy expands shorthand strategy entries for the engine retry loop.
func (s *Step) ExpandedOnFailureStrategy() []StrategyEntry {
	if s.OnFailure == nil {
		return nil
	}
	return ExpandStrategy(s.OnFailure.Strategy)
}

// OnFailure is the v4 replacement for legacy v3 retry (parser normalises it).
type OnFailure struct {
	Retry       int             `yaml:"retry"` // max attempts (incl. first)
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
