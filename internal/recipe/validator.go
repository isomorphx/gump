package recipe

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ValidationError includes a path so recipe authors can fix the exact step or field that failed.
type ValidationError struct {
	Path    string
	Message string
}

func (e ValidationError) Error() string {
	if e.Path != "" {
		return e.Path + ": " + e.Message
	}
	return e.Message
}

// ValidateWarn is called for non-fatal validation warnings (e.g. session reuse target different agent). Set by cmd/cook so e2e can assert on stderr.
var ValidateWarn func(path, message string)

var validOutputValues = map[string]bool{"diff": true, "plan": true, "artifact": true, "review": true}
var validStrategyTypes = map[string]bool{"same": true, "escalate": true, "replan": true}
var validBlastRadiusValues = map[string]bool{"enforce": true, "warn": true, "off": true}

// validSessionModes allows reuse-on-retry so recipes can express "fresh first run, resume on retry" without storing session in State Bag.
var validSessionModes = map[string]bool{"reuse": true, "fresh": true, "reuse-targeted": true, "reuse-on-retry": true}
var validValidatorTypes = map[string]bool{
	"compile": true, "test": true, "lint": true, "schema": true,
	"touched": true, "untouched": true, "tests_found": true, "coverage": true, "bash": true,
}

// stepsRefRegex matches {steps.<name>.output|diff|files|session_id}
var stepsRefRegex = regexp.MustCompile(`\{steps\.(.+?)\.(output|diff|files|session_id)\}`)

// Validate runs v4 structural rules.
func Validate(r *Recipe) []ValidationError {
	var errs []ValidationError
	if r.Name == "" {
		errs = append(errs, ValidationError{Path: "recipe", Message: "name is required"})
	}
	if len(r.Steps) == 0 {
		errs = append(errs, ValidationError{Path: "recipe", Message: "at least one step is required"})
	}
	if strings.TrimSpace(r.BlastRadius) != "" && !validBlastRadiusValues[strings.TrimSpace(r.BlastRadius)] {
		errs = append(errs, ValidationError{Path: "recipe.blast_radius", Message: `blast_radius must be "enforce", "warn", or "off"`})
	}

	// max_budget on recipe.
	if r.MaxBudget < 0 {
		errs = append(errs, ValidationError{Path: "recipe.max_budget", Message: "max_budget must be > 0 if present"})
	}

	stepNamesByPath := collectStepsWithPath(r.Steps, "") // fullPath -> short name
	stepNodes := collectStepNodes(r.Steps, "")

	// max_budget on steps (and <= recipe max_budget when set).
	for _, n := range stepNodes {
		if n.step.MaxBudget != 0 {
			if n.step.MaxBudget < 0 {
				errs = append(errs, ValidationError{Path: n.path + ".max_budget", Message: "step max_budget must be > 0"})
			} else if r.MaxBudget > 0 && n.step.MaxBudget > r.MaxBudget {
				errs = append(errs, ValidationError{Path: n.path + ".max_budget", Message: fmt.Sprintf("step max_budget %.2f exceeds recipe max_budget %.2f", n.step.MaxBudget, r.MaxBudget)})
			}
		}
	}

	// Validate each scope independently so siblings can reuse step names across different parents.
	for i := range r.Steps {
		scopeSeen := make(map[string]bool)
		e := validateStep(r, &r.Steps[i], fmt.Sprintf("steps[%d]", i), "", scopeSeen, stepNamesByPath)
		errs = append(errs, e...)
	}

	// restart_from graph validations (cycle detection + restart_from+retry constraint).
	errs = append(errs, validateRestartFromGraph(r, stepNodes, stepNamesByPath)...)
	return errs
}

func collectStepsWithPath(steps []Step, prefix string) map[string]string {
	out := make(map[string]string)
	for i := range steps {
		s := &steps[i]
		p := prefix
		if p != "" {
			p += "/"
		}
		p += s.Name
		out[p] = s.Name
		if len(s.Steps) > 0 {
			for k, v := range collectStepsWithPath(s.Steps, p) {
				out[k] = v
			}
		}
	}
	return out
}

type stepNode struct {
	path string
	step Step
}

func collectStepNodes(steps []Step, prefix string) []stepNode {
	var out []stepNode
	for i := range steps {
		s := steps[i]
		p := prefix
		if p != "" {
			p += "/"
		}
		p += s.Name
		out = append(out, stepNode{path: p, step: s})
		if len(s.Steps) > 0 {
			out = append(out, collectStepNodes(s.Steps, p)...)
		}
	}
	return out
}

func validateStep(rec *Recipe, s *Step, path string, scopePath string, seenNames map[string]bool, stepNamesByPath map[string]string) []ValidationError {
	var errs []ValidationError
	if s.Name == "" {
		errs = append(errs, ValidationError{Path: path, Message: "name is required"})
	} else {
		if seenNames[s.Name] {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("duplicate step name %q within scope", s.Name)})
		}
		seenNames[s.Name] = true
	}

	hasAgent := s.Agent != ""
	hasSteps := len(s.Steps) > 0
	hasForeach := strings.TrimSpace(s.Foreach) != ""
	hasWorkflowRef := strings.TrimSpace(s.Workflow) != "" || strings.TrimSpace(s.Recipe) != ""
	hasGate := len(s.Gate) > 0
	hasWith := len(s.With) > 0
	hasGuard := s.Guard.MaxTurns > 0 || s.Guard.MaxBudget > 0 || s.Guard.NoWrite != nil

	// Step name ambiguity guard: agent step and orchestration container.
	if hasAgent && hasSteps {
		errs = append(errs, ValidationError{
			Path:    path,
			Message: fmt.Sprintf("step %q has both 'agent' and 'steps'.\nHint: split into two steps: an agent step followed by an orchestration step.", s.Name),
		})
	}

	// Determine the step "form" (Agent / Gate / Orchestration).
	if hasAgent {
		// Agent Step
		if hasSteps || hasForeach || hasWorkflowRef {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q (agent step) cannot have 'steps', 'foreach', or 'workflow'", s.Name)})
		}
		if s.Output != "" && !validOutputValues[s.Output] {
			errs = append(errs, ValidationError{Path: path, Message: "output must be \"diff\", \"plan\", \"artifact\", or \"review\""})
		}
		// output default is handled by parser; validator still enforces when set.
		if s.OnFailure != nil && !hasGate && ValidateWarn != nil {
			ValidateWarn(path, "on_failure present but gate is empty — on_failure without gate has no meaning")
		}
	} else if hasGate && !hasSteps && !hasForeach && !hasWorkflowRef {
		// Gate Step
		// Gate step cannot have any "agent" or "orchestration" fields.
		if s.Agent != "" {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q is a gate step and cannot have 'agent'", s.Name)})
		}
		if strings.TrimSpace(s.Prompt) != "" {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q is a gate step and cannot have 'prompt'", s.Name)})
		}
		if strings.TrimSpace(s.Output) != "" {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q is a gate step and cannot have 'output'", s.Name)})
		}
		if len(s.Context) > 0 {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q is a gate step and cannot have 'context'", s.Name)})
		}
		if s.Session.Mode != "" && s.Session.Mode != "fresh" {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q is a gate step and cannot have 'session'", s.Name)})
		}
		if s.Timeout != "" {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q is a gate step and cannot have 'timeout'", s.Name)})
		}
		if s.HITL {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q is a gate step and cannot have 'hitl'", s.Name)})
		}
		if s.Parallel {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q is a gate step and cannot have 'parallel'", s.Name)})
		}
		if hasSteps || hasForeach || hasWorkflowRef {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q is a gate step and cannot have sub-steps/foreach/workflow", s.Name)})
		}
		// gate required and non-empty.
		if !hasGate {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("gate step %q: gate is required and must not be empty", s.Name)})
		}
	} else {
		// Orchestration Step
		hasAtLeast := hasSteps || hasForeach || hasWorkflowRef
		if !hasAtLeast {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q has no agent, gate, or sub-steps. Every step must do something.", s.Name)})
		}
		if hasAgent || strings.TrimSpace(s.Prompt) != "" || strings.TrimSpace(s.Output) != "" || len(s.Context) > 0 {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q is an orchestration step and cannot have 'agent', 'prompt', 'output', or 'context'", s.Name)})
		}
		if s.Session.Mode != "" && s.Session.Mode != "fresh" {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q is an orchestration step and cannot have 'session'", s.Name)})
		}
		if s.Timeout != "" {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q is an orchestration step and cannot have 'timeout'", s.Name)})
		}
		if s.HITL {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q is an orchestration step and cannot have 'hitl'", s.Name)})
		}
	}

	if hasWith && !hasWorkflowRef {
		errs = append(errs, ValidationError{Path: path, Message: "with requires workflow"})
	}
	if hasWorkflowRef && hasAgent {
		errs = append(errs, ValidationError{Path: path, Message: "workflow step cannot set agent"})
	}
	if hasWorkflowRef && !hasAgent && !hasSteps && !hasForeach {
		if hasGate {
			errs = append(errs, ValidationError{Path: path, Message: "workflow step cannot set gate"})
		}
		if s.Parallel {
			errs = append(errs, ValidationError{Path: path, Message: "workflow step cannot set parallel"})
		}
		if s.OnFailure != nil {
			errs = append(errs, ValidationError{Path: path, Message: "workflow step cannot set on_failure"})
		}
	}
	if hasWorkflowRef && hasSteps && !hasForeach {
		errs = append(errs, ValidationError{Path: path, Message: "workflow step cannot include inline steps unless used with foreach"})
	}
	if hasWorkflowRef && hasGuard {
		errs = append(errs, ValidationError{Path: path, Message: "guard is not allowed on workflow step"})
	}
	if hasGuard && !hasAgent {
		errs = append(errs, ValidationError{Path: path, Message: "guard is only allowed on agent steps"})
	}
	if s.GuardMaxTurnsSet && s.Guard.MaxTurns <= 0 {
		errs = append(errs, ValidationError{Path: path + ".guard.max_turns", Message: "max_turns must be > 0"})
	}
	if s.GuardMaxBudgetSet && s.Guard.MaxBudget <= 0 {
		errs = append(errs, ValidationError{Path: path + ".guard.max_budget", Message: "max_budget must be > 0"})
	}
	if s.MaxTurns > 0 && ValidateWarn != nil {
		ValidateWarn(path, "max_turns is deprecated; use guard.max_turns")
	}

	// foreach rule (plan output).
	if hasForeach {
		refStep := findStepByName(rec.Steps, s.Foreach, "")
		// Spec: foreach existence is validated during dry-run. We only enforce shape when it exists.
		if refStep != nil && refStep.Output != "plan" {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q: foreach references step %q which is not output: plan", s.Name, s.Foreach)})
		}
	}

	// session mode validation (only meaningful for agent steps).
	if s.Session.Mode != "" && !validSessionModes[s.Session.Mode] {
		errs = append(errs, ValidationError{Path: path, Message: "session must be \"reuse\", \"fresh\", \"reuse: <step-name>\", or \"reuse-on-retry\""})
	}
	if s.Session.Mode == "reuse-targeted" && s.Session.Target != "" {
		refStep := findStepByName(rec.Steps, s.Session.Target, "")
		if refStep == nil {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("session reuse target %q: step not found", s.Session.Target)})
		} else if refStep.Agent != "" && s.Agent != "" && refStep.Agent != s.Agent {
			errs = append(errs, ValidationError{
				Path: path,
				Message: fmt.Sprintf("step %q has session: reuse: %s but uses agent '%s' while '%s' uses '%s'.Hint: session reuse requires the same agent. Use session: fresh instead.",
					s.Name, s.Session.Target, s.Agent, s.Session.Target, refStep.Agent),
			})
		}
	}

	// timeout validation.
	if s.Timeout != "" {
		if _, err := time.ParseDuration(s.Timeout); err != nil {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("invalid timeout %q (must be a valid Go duration, e.g. \"5m\", \"30s\")", s.Timeout)})
		}
	}

	// gate validators validation.
	for j, v := range s.Gate {
		p := path + ".gate[" + fmt.Sprint(j) + "]"
		if !validValidatorTypes[v.Type] {
			errs = append(errs, ValidationError{Path: p, Message: fmt.Sprintf("unknown validator %q", v.Type)})
		}
		switch v.Type {
		case "touched", "untouched":
			if v.Arg == "" {
				errs = append(errs, ValidationError{Path: p, Message: fmt.Sprintf("%q requires a glob argument", v.Type)})
			}
		case "coverage":
			if v.Arg == "" || !isNumeric(v.Arg) {
				errs = append(errs, ValidationError{Path: p, Message: "\"coverage\" requires a numeric threshold"})
			}
		case "bash":
			if v.Arg == "" {
				errs = append(errs, ValidationError{Path: p, Message: "\"bash\" requires a command"})
			}
		}
	}

	// on_failure validation (v4).
	if s.OnFailure != nil {
		p := path + ".on_failure"
		if s.OnFailure.IsConditionalForm() && s.OnFailure.IsFlatForm() {
			errs = append(errs, ValidationError{Path: p, Message: "flat on_failure (retry/strategy/restart_from) and conditional on_failure (gate_fail/guard_fail/review_fail) are mutually exclusive"})
		}
		if !s.OnFailure.IsConditionalForm() {
			if s.OnFailure.Retry < 0 {
				errs = append(errs, ValidationError{Path: p + ".retry", Message: "retry must be >= 0"})
			}
			if s.OnFailure.Retry > 10 {
				errs = append(errs, ValidationError{Path: p + ".retry", Message: "retry cannot exceed 10"})
			}
			for i, e := range s.OnFailure.Strategy {
				sp := p + ".strategy[" + fmt.Sprint(i) + "]"
				if !validStrategyTypes[e.Type] {
					errs = append(errs, ValidationError{Path: sp, Message: "type must be \"same\", \"escalate\", or \"replan\""})
				}
				if (e.Type == "escalate" || e.Type == "replan") && e.Agent == "" {
					errs = append(errs, ValidationError{Path: sp, Message: fmt.Sprintf("agent is required for %q", e.Type)})
				}
			}
		} else {
			validateFailureAction := func(name string, a *FailureAction) {
				if a == nil {
					return
				}
				ap := p + "." + name
				if a.Retry < 0 {
					errs = append(errs, ValidationError{Path: ap + ".retry", Message: "retry must be >= 0"})
				}
				if a.Retry > 10 {
					errs = append(errs, ValidationError{Path: ap + ".retry", Message: "retry cannot exceed 10"})
				}
				for i, e := range a.Strategy {
					sp := ap + ".strategy[" + fmt.Sprint(i) + "]"
					if !validStrategyTypes[e.Type] {
						errs = append(errs, ValidationError{Path: sp, Message: "type must be \"same\", \"escalate\", or \"replan\""})
					}
					if (e.Type == "escalate" || e.Type == "replan") && e.Agent == "" {
						errs = append(errs, ValidationError{Path: sp, Message: fmt.Sprintf("agent is required for %q", e.Type)})
					}
				}
			}
			validateFailureAction("gate_fail", s.OnFailure.GateFail)
			validateFailureAction("guard_fail", s.OnFailure.GuardFail)
			validateFailureAction("review_fail", s.OnFailure.ReviewFail)
		}
	}

	// Recursively validate nested orchestration steps.
	fullPath := scopePath
	if fullPath != "" {
		fullPath += "/"
	}
	fullPath += s.Name
	subSeen := make(map[string]bool)
	for i := range s.Steps {
		errs = append(errs, validateStep(rec, &s.Steps[i], path+".steps["+fmt.Sprint(i)+"]", fullPath, subSeen, stepNamesByPath)...)
	}

	// State Bag refs in prompt: {steps.<name>.output} / {steps.<name>.diff} / {steps.<name>.files}.
	for _, m := range stepsRefRegex.FindAllStringSubmatch(s.Prompt, -1) {
		if len(m) != 3 { // full match + 2 capture groups
			continue
		}
		refName := strings.TrimSpace(m[1])
		refPath := strings.ReplaceAll(strings.ReplaceAll(refName, ".steps.", "/"), ".", "/")
		candidates := findStepPathsByName(stepNamesByPath, refName)
		if _, ok := stepNamesByPath[refPath]; ok {
			candidates = append(candidates, refPath)
		}
		if len(candidates) > 1 {
			seen := map[string]struct{}{}
			uniq := make([]string, 0, len(candidates))
			for _, c := range candidates {
				if _, ok := seen[c]; ok {
					continue
				}
				seen[c] = struct{}{}
				uniq = append(uniq, c)
			}
			candidates = uniq
		}
		if len(candidates) == 0 {
			// WHY: child workflows are validated in isolation; unknown refs may be intentionally injected by parent via with:/state graft.
			if ValidateWarn != nil {
				ValidateWarn(path, fmt.Sprintf("prompt references unknown step %q in {steps.%s.%s}", refName, refName, m[2]))
			}
		} else if len(candidates) > 1 {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("ambiguous step reference %q — use fully-qualified path", refName)})
		}
	}

	return errs
}

func findStepByName(steps []Step, name string, prefix string) *Step {
	for i := range steps {
		s := &steps[i]
		p := prefix
		if p != "" {
			p += "/"
		}
		p += s.Name
		if s.Name == name {
			return s
		}
		if len(s.Steps) > 0 {
			if found := findStepByName(s.Steps, name, p); found != nil {
				return found
			}
		}
	}
	return nil
}

// FindStepByName is exported for CLI and tools that resolve a step by short name.
func FindStepByName(steps []Step, name string, prefix string) *Step {
	return findStepByName(steps, name, prefix)
}

func findStepPathsByName(stepNamesByPath map[string]string, name string) []string {
	var out []string
	for path, n := range stepNamesByPath {
		if n == name {
			out = append(out, path)
		}
	}
	return out
}

func validateRestartFromGraph(rec *Recipe, nodes []stepNode, stepNamesByPath map[string]string) []ValidationError {
	// Build adjacency list: stepPath -> restartFromTargetPath
	// restart_from targets reference step *names*, which may be ambiguous across scopes;
	// for determinism we require unambiguous matches.
	var errs []ValidationError
	nodesByPath := make(map[string]stepNode, len(nodes))
	for _, n := range nodes {
		nodesByPath[n.path] = n
	}

	adj := make(map[string][]string)
	for _, n := range nodes {
		if n.step.OnFailure == nil {
			continue
		}
		type restartPolicy struct {
			path        string
			retry       int
			restartFrom string
		}
		var policies []restartPolicy
		if n.step.OnFailure.IsConditionalForm() {
			add := func(name string, action *FailureAction) {
				if action == nil || strings.TrimSpace(action.RestartFrom) == "" {
					return
				}
				policies = append(policies, restartPolicy{
					path:        n.path + ".on_failure." + name,
					retry:       action.Retry,
					restartFrom: action.RestartFrom,
				})
			}
			add("gate_fail", n.step.OnFailure.GateFail)
			add("guard_fail", n.step.OnFailure.GuardFail)
			add("review_fail", n.step.OnFailure.ReviewFail)
		} else if strings.TrimSpace(n.step.OnFailure.RestartFrom) != "" {
			policies = append(policies, restartPolicy{
				path:        n.path + ".on_failure",
				retry:       n.step.OnFailure.Retry,
				restartFrom: n.step.OnFailure.RestartFrom,
			})
		}
		for _, pol := range policies {
			if pol.retry <= 0 {
				errs = append(errs, ValidationError{
					Path:    pol.path,
					Message: fmt.Sprintf("step '%s' has restart_from without retry limit. This would create an infinite loop.\nHint: add 'retry: N' to on_failure to limit the number of restarts.", n.step.Name),
				})
				// Continue graph build to also surface cycles when possible.
			}
			targetName := strings.TrimSpace(pol.restartFrom)
			candidates := findStepPathsByName(stepNamesByPath, targetName)
			groupPath := parentStepPath(n.path)
			filtered := make([]string, 0, len(candidates))
			for _, c := range candidates {
				if parentStepPath(c) == groupPath || strings.HasPrefix(c, n.path+"/") {
					filtered = append(filtered, c)
				}
			}
			candidates = filtered
			if len(candidates) == 0 {
				errs = append(errs, ValidationError{
					Path:    pol.path + ".restart_from",
					Message: fmt.Sprintf("restart_from target %q: step not found in same group", targetName),
				})
				continue
			}
			if len(candidates) > 1 {
				errs = append(errs, ValidationError{
					Path:    pol.path + ".restart_from",
					Message: fmt.Sprintf("restart_from target %q is ambiguous — use fully-qualified path", targetName),
				})
				continue
			}
			targetPath := candidates[0]
			adj[n.path] = append(adj[n.path], targetPath)
		}
	}

	// DFS cycle detection.
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[string]int, len(nodes))
	var stack []string
	var dfs func(path string)
	dfs = func(path string) {
		state[path] = visiting
		stack = append(stack, path)
		for _, to := range adj[path] {
			if state[to] == unvisited {
				dfs(to)
			} else if state[to] == visiting {
				// Back-edge: cycle from `to` to end of stack.
				var idx int = -1
				for i := range stack {
					if stack[i] == to {
						idx = i
						break
					}
				}
				if idx != -1 {
					cyclePaths := append([]string{}, stack[idx:]...)
					cyclePaths = append(cyclePaths, to)
					// Convert to names.
					var names []string
					for _, p := range cyclePaths {
						if nn, ok := nodesByPath[p]; ok {
							names = append(names, nn.step.Name)
						} else {
							names = append(names, p)
						}
					}
					errs = append(errs, ValidationError{
						Path:    path + ".on_failure.restart_from",
						Message: "cycle detected in restart_from: " + strings.Join(names, " → ") + "\nHint: ensure restart_from chains are acyclic. Each step should only restart to an earlier step.",
					})
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[path] = done
	}
	for _, n := range nodes {
		if state[n.path] == unvisited {
			dfs(n.path)
		}
	}
	return errs
}

func parentStepPath(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i]
	}
	return ""
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
