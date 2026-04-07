package recipe

import (
	"strings"
)

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Recipe is the v4 mapping: name, description, steps, with optional max_budget.
type Recipe struct {
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	BlastRadius string              `yaml:"blast_radius"`
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

// MaxAttempts caps global step attempts (engine globalStepAttempts).
// Flat form: OnFailure.Retry is total attempts (legacy).
// Conditional form: max of each branch's Retry plus one initial attempt; 1 if all zero.
func (s *Step) MaxAttempts() int {
	if s.OnFailure == nil {
		return 1
	}
	o := s.OnFailure
	if !o.IsConditionalForm() {
		if o.Retry <= 0 {
			return 1
		}
		return o.Retry
	}
	m := 0
	if o.GateFail != nil {
		m = maxInt(m, o.GateFail.Retry)
	}
	if o.GuardFail != nil {
		m = maxInt(m, o.GuardFail.Retry)
	}
	if o.ReviewFail != nil {
		m = maxInt(m, o.ReviewFail.Retry)
	}
	if m <= 0 {
		return 1
	}
	return m + 1
}

// RestartFromWithoutStrategy is true when on_failure only requests restart_from (no same/escalate/replan slots).
func (s *Step) RestartFromWithoutStrategy() bool {
	if s.OnFailure == nil || strings.TrimSpace(s.OnFailure.RestartFrom) == "" {
		return false
	}
	return len(s.ExpandedOnFailureStrategy()) == 0
}

// ShouldRunWithRetryLoop is true when any on_failure branch allows retries or restart_from-only applies.
func (s *Step) ShouldRunWithRetryLoop() bool {
	if s.OnFailure == nil {
		return false
	}
	o := s.OnFailure
	if o.Retry > 0 {
		return true
	}
	if o.GateFail != nil && o.GateFail.Retry > 0 {
		return true
	}
	if o.GuardFail != nil && o.GuardFail.Retry > 0 {
		return true
	}
	if o.ReviewFail != nil && o.ReviewFail.Retry > 0 {
		return true
	}
	return s.RestartFromWithoutStrategy()
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
	GateFail    *FailureAction  `yaml:"gate_fail,omitempty"`
	GuardFail   *FailureAction  `yaml:"guard_fail,omitempty"`
	ReviewFail  *FailureAction  `yaml:"review_fail,omitempty"`
}

type FailureAction struct {
	Retry       int             `yaml:"retry"`
	Strategy    []StrategyEntry `yaml:"strategy,omitempty"`
	RestartFrom string          `yaml:"restart_from,omitempty"`
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

// IsConditionalForm reports whether on_failure uses source-specific routing.
func (o *OnFailure) IsConditionalForm() bool {
	if o == nil {
		return false
	}
	return o.GateFail != nil || o.GuardFail != nil || o.ReviewFail != nil
}

// IsFlatForm reports whether on_failure uses the legacy shape.
func (o *OnFailure) IsFlatForm() bool {
	if o == nil {
		return false
	}
	return o.Retry != 0 || len(o.Strategy) > 0 || strings.TrimSpace(o.RestartFrom) != ""
}

// ActionForFailureSource resolves the effective policy for a fail source.
// WHY: conditional policies must still fall back to gate_fail by default.
func (o *OnFailure) ActionForFailureSource(source string) *FailureAction {
	if o == nil {
		return nil
	}
	if !o.IsConditionalForm() {
		return &FailureAction{
			Retry:       o.Retry,
			Strategy:    o.Strategy,
			RestartFrom: o.RestartFrom,
		}
	}
	switch source {
	case "guard_fail":
		if o.GuardFail != nil {
			return o.GuardFail
		}
	case "review_fail":
		if o.ReviewFail != nil {
			return o.ReviewFail
		}
	case "gate_fail":
		if o.GateFail != nil {
			return o.GateFail
		}
	}
	if o.GateFail != nil {
		return o.GateFail
	}
	return nil
}
