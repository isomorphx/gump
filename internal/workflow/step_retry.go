package workflow

import "strings"

// MaxAttempts returns the total attempts allowed for this step (from retry exit: N, or 1).
func (s *Step) MaxAttempts() int {
	if s == nil {
		return 1
	}
	exitN := 0
	for _, r := range s.Retry {
		if r.Exit > exitN {
			exitN = r.Exit
		}
	}
	if exitN <= 0 {
		return 1
	}
	return exitN
}

// ShouldRunWithRetryLoop is true when the retry list allows more than one attempt.
func (s *Step) ShouldRunWithRetryLoop() bool {
	return s != nil && len(s.Retry) > 0 && s.MaxAttempts() > 1
}

// StepRetryWorktreeReset is true when any retry entry requests a hard git reset before the next attempt (R3 §4.2).
func StepRetryWorktreeReset(s *Step) bool {
	if s == nil {
		return false
	}
	for _, r := range s.Retry {
		if strings.EqualFold(strings.TrimSpace(r.Worktree), "reset") {
			return true
		}
	}
	return false
}

// ResolveSessionForEngine maps v0.0.4 session keywords onto the modes the runner already implements.
func ResolveSessionForEngine(step *Step, parent SessionConfig) SessionConfig {
	if step == nil {
		if parent.Mode == "" {
			return SessionConfig{Mode: "fresh"}
		}
		return parent
	}
	eff := step.Session
	if eff.Mode == "" {
		eff = parent
	}
	if eff.Mode == "" || eff.Mode == "new" {
		return SessionConfig{Mode: "fresh"}
	}
	if eff.Mode == "from" {
		return SessionConfig{Mode: "reuse-targeted", Target: eff.Target}
	}
	return SessionConfig{Mode: "fresh"}
}

// SessionLedgerMode returns a stable session label for ledger events.
func SessionLedgerMode(eff SessionConfig, parent SessionConfig) string {
	mode := eff.Mode
	if mode == "" {
		mode = parent.Mode
	}
	if mode == "" {
		return "fresh"
	}
	return mode
}

// OutputMode maps declarative type to the state-bag / template mode used by agents.
func (s *Step) OutputMode() string {
	if s == nil {
		return "diff"
	}
	for _, g := range s.Gate {
		if g.Type != "schema" {
			continue
		}
		a := strings.TrimSpace(g.Arg)
		if a == "" || a == "plan" {
			return "plan"
		}
		if a == "review" {
			return "validate"
		}
	}
	switch s.Type {
	case "split":
		return "plan"
	case "validate":
		return "validate"
	case "artifact":
		return "artifact"
	default:
		return "diff"
	}
}

// EffectivePromptAt merges ordered retry prompt overrides for the current attempt.
func (s *Step) EffectivePromptAt(attempt int) string {
	if s == nil {
		return ""
	}
	out := s.Prompt
	for _, r := range s.Retry {
		if r.Exit > 0 {
			continue
		}
		if r.Attempt > 0 && attempt >= r.Attempt && strings.TrimSpace(r.Prompt) != "" {
			out = r.Prompt
		}
	}
	return out
}

// EffectiveAgentAt merges retry agent overrides for the current attempt.
func (s *Step) EffectiveAgentAt(attempt int, base string) string {
	if s == nil {
		return base
	}
	out := base
	for _, r := range s.Retry {
		if r.Exit > 0 {
			continue
		}
		if r.Attempt > 0 && attempt >= r.Attempt && strings.TrimSpace(r.Agent) != "" {
			out = r.Agent
		}
	}
	return out
}
