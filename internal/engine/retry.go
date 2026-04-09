package engine

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/template"
	"github.com/isomorphx/gump/internal/validate"
	"github.com/isomorphx/gump/internal/workflow"
)

// ErrRetryValidateNeedsR5 is returned when a retry entry uses validate: before workflow validators exist (R5).
var ErrRetryValidateNeedsR5 = errors.New("retry validate condition requires workflow validators (R5)")

// ValidatorInvoker is implemented by R5 to run nested validator workflows from retry conditions.
type ValidatorInvoker interface {
	InvokeValidator(workflowPath string, inputs map[string]string) (bool, error)
}

// RetryEvaluator applies ordered retry entries with sticky overrides (R4 / Archi §6).
type RetryEvaluator struct {
	Entries       []workflow.RetryEntry
	StepPath      string
	stepBaseAgent string

	stickyAgent    string
	stickySession  string
	stickyWorktree string
	stickyPrompt   string

	lastResolvedAgent string
}

// NewRetryEvaluator builds an evaluator for one step run; stepBaseAgent is the declared step agent (attempt 1).
func NewRetryEvaluator(entries []workflow.RetryEntry, stepPath, stepBaseAgent string) *RetryEvaluator {
	base := strings.TrimSpace(stepBaseAgent)
	return &RetryEvaluator{
		Entries:           entries,
		StepPath:          stepPath,
		stepBaseAgent:     base,
		lastResolvedAgent: base,
	}
}

// MaxAttempt is the maximum attempt number allowed (from exit:), same cap as workflow.Step.MaxAttempts.
func (re *RetryEvaluator) MaxAttempt() int {
	max := 0
	for _, e := range re.Entries {
		if e.Exit > max {
			max = e.Exit
		}
	}
	return max
}

// RetryDecision is the resolved retry policy before the next GET.
type RetryDecision struct {
	Action       string
	Agent        string
	Session      string
	Worktree     string
	Prompt       string
	MatchedEntry int
}

// gatePassState records whether a gate key was written after the last gate phase.
type gatePassState struct {
	Known bool
	Pass  bool
}

// GateStatesFromStep rebuilds per-gate outcomes for retry conditions (not: gate.<name>).
func GateStatesFromStep(st *state.State, stepPath string, gates []workflow.GateEntry) map[string]gatePassState {
	out := make(map[string]gatePassState)
	if st == nil {
		return out
	}
	for i := range gates {
		name := validate.GateStateFieldName(gates, i)
		raw := strings.TrimSpace(st.Get(stepPath + ".gate." + name))
		if raw == "" {
			out[name] = gatePassState{Known: false, Pass: false}
			continue
		}
		out[name] = gatePassState{Known: true, Pass: raw == "true"}
	}
	return out
}

// Evaluate picks overrides for the upcoming attempt (attempt >= 2).
func (re *RetryEvaluator) Evaluate(attempt int, gateStates map[string]gatePassState, validator ValidatorInvoker, resolveCtx *state.ResolveContext) (*RetryDecision, error) {
	if re == nil {
		return &RetryDecision{Action: "retry", MatchedEntry: -1}, nil
	}
	// WHY: exit: N caps how many attempts may run; scheduling attempt N+1 after N failures must stop (R3 parity).
	maxE := re.MaxAttempt()
	if maxE > 0 && attempt > maxE {
		return &RetryDecision{Action: "fatal", MatchedEntry: -1}, nil
	}

	active := make(map[string]string)
	lastMatchIdx := -1
	for i, e := range re.Entries {
		if e.Exit > 0 {
			continue
		}
		ok, err := re.entryMatches(&e, attempt, gateStates, validator, resolveCtx)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		lastMatchIdx = i
		if s := strings.TrimSpace(e.Agent); s != "" {
			active["agent"] = s
		}
		if strings.TrimSpace(e.Session) != "" {
			active["session"] = strings.TrimSpace(e.Session)
		}
		if s := strings.TrimSpace(e.Worktree); s != "" {
			active["worktree"] = s
		}
		if strings.TrimSpace(e.Prompt) != "" {
			active["prompt"] = e.Prompt
		}
	}
	for k, v := range active {
		switch k {
		case "agent":
			re.stickyAgent = v
		case "session":
			re.stickySession = v
		case "worktree":
			re.stickyWorktree = v
		case "prompt":
			re.stickyPrompt = v
		}
	}

	dec := &RetryDecision{
		Action:       "retry",
		Agent:        re.stickyAgent,
		Session:      re.stickySession,
		Worktree:     re.stickyWorktree,
		Prompt:       re.stickyPrompt,
		MatchedEntry: lastMatchIdx,
	}

	effAgent := strings.TrimSpace(dec.Agent)
	if effAgent == "" {
		effAgent = re.stepBaseAgent
	}
	// WHY: switching models without a fresh session leaks prior tool context across providers.
	if effAgent != re.lastResolvedAgent && dec.Session == "" {
		dec.Session = "new"
		re.stickySession = "new"
		fmt.Fprintf(os.Stderr, "[%s] agent changed from %s to %s, forcing new session\n", brand.Lower(), re.lastResolvedAgent, effAgent)
	}
	// WHY: reset drops tracked files; an old session may still reference paths the agent “remembers”.
	if strings.EqualFold(strings.TrimSpace(dec.Worktree), "reset") && dec.Session == "" {
		fmt.Fprintf(os.Stderr, "[%s] worktree reset without new session — agent may reference files that no longer exist\n", brand.Lower())
	}

	re.lastResolvedAgent = effAgent
	return dec, nil
}

func (re *RetryEvaluator) entryMatches(e *workflow.RetryEntry, attempt int, gateStates map[string]gatePassState, validator ValidatorInvoker, resolveCtx *state.ResolveContext) (bool, error) {
	hasCond := false
	ok := true
	if e.Attempt > 0 {
		hasCond = true
		ok = ok && attempt >= e.Attempt
	}
	if strings.TrimSpace(e.Not) != "" {
		hasCond = true
		gn, err := parseGateRef(e.Not)
		if err != nil {
			return false, err
		}
		t := gateStates[gn]
		// WHY: unknown gate must not fire `not:` so we never escalate on missing telemetry.
		ok = ok && t.Known && !t.Pass
	}
	if strings.TrimSpace(e.Validate) != "" {
		hasCond = true
		if validator == nil {
			return false, ErrRetryValidateNeedsR5
		}
		inputs := make(map[string]string)
		for k, v := range e.With {
			inputs[k] = template.Resolve(v, resolveCtx)
		}
		pass, err := validator.InvokeValidator(strings.TrimSpace(e.Validate), inputs)
		if err != nil {
			return false, err
		}
		ok = ok && pass
	}
	if !hasCond {
		return false, nil
	}
	return ok, nil
}

func parseGateRef(not string) (string, error) {
	s := strings.TrimSpace(not)
	const p = "gate."
	if !strings.HasPrefix(strings.ToLower(s), p) {
		return "", fmt.Errorf("retry not: value %q must use prefix %q", not, p)
	}
	name := strings.TrimSpace(s[len(p):])
	if name == "" {
		return "", fmt.Errorf("retry not: empty gate name")
	}
	return name, nil
}

func ledgerOverridesFromDecision(d *RetryDecision) map[string]string {
	if d == nil {
		return map[string]string{}
	}
	m := make(map[string]string)
	if s := strings.TrimSpace(d.Agent); s != "" {
		m["agent"] = s
	}
	if s := strings.TrimSpace(d.Session); s != "" {
		m["session"] = s
	}
	if s := strings.TrimSpace(d.Worktree); s != "" {
		m["worktree"] = s
	}
	if strings.TrimSpace(d.Prompt) != "" {
		// WHY: audit trail must show a prompt policy is active without dumping full template text into the ledger.
		m["prompt"] = "overridden"
	}
	return m
}
