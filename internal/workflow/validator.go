package workflow

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ValidateWarn is invoked for non-fatal validation issues (dead code after exit, etc.).
var ValidateWarn func(path, message string)

// ValidationError is one structural problem with a stable path for authors.
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

var validStepTypes = map[string]bool{"code": true, "split": true, "validate": true}
var validHitl = map[string]bool{"": true, "before_gate": true, "after_gate": true}
var validGateTypes = map[string]bool{
	"compile": true, "test": true, "lint": true, "schema": true,
	"touched": true, "untouched": true, "tests_found": true, "coverage": true, "bash": true, "validate": true,
}

var stepsRefRegex = regexp.MustCompile(`\{steps\.(.+?)\.(output|diff|files|session_id)\}`)

// Validate checks the workflow after parsing and applies type-derived defaults.
func Validate(wf *Workflow) []error {
	if wf == nil {
		return []error{ValidationError{Path: "workflow", Message: "nil workflow"}}
	}
	ApplyDefaults(wf)

	var errs []error
	if strings.TrimSpace(wf.Name) == "" {
		errs = append(errs, ValidationError{Path: "workflow.name", Message: "name is required"})
	}
	if len(wf.Steps) == 0 {
		errs = append(errs, ValidationError{Path: "workflow.steps", Message: "at least one step is required"})
	}

	stepNamesByPath := collectStepsWithPath(wf.Steps, "")
	seenRoot := make(map[string]bool)
	for i := range wf.Steps {
		errs = append(errs, validateStep(&wf.Steps[i], fmt.Sprintf("steps[%d]", i), "", seenRoot, stepNamesByPath, wf)...)
	}
	return errs
}

// ApplyDefaults sets worktree and implicit no_write from step type (v0.0.4).
func ApplyDefaults(wf *Workflow) {
	var walk func(*Step)
	walk = func(s *Step) {
		if s == nil {
			return
		}
		gateOnly := isGateOnlyStep(s)
		if !gateOnly {
			if s.Worktree == "" {
				switch s.Type {
				case "code":
					s.Worktree = "read-write"
				case "split", "validate":
					s.Worktree = "read-only"
				}
			}
			if s.Guard.NoWrite == nil {
				switch s.Type {
				case "split", "validate":
					v := true
					s.Guard.NoWrite = &v
				}
			}
		}
		for i := range s.Each {
			walk(&s.Each[i])
		}
		for i := range s.Steps {
			walk(&s.Steps[i])
		}
	}
	for i := range wf.Steps {
		walk(&wf.Steps[i])
	}
}

func isGateOnlyStep(s *Step) bool {
	if strings.TrimSpace(s.Workflow) != "" {
		return false
	}
	if len(s.Steps) > 0 || len(s.Each) > 0 {
		return false
	}
	return s.Type == "" && s.Agent == "" && strings.TrimSpace(s.Prompt) == ""
}

// IsGateOnlyStep reports steps that run only the gate phase (no agent, no type, no prompt).
func IsGateOnlyStep(s *Step) bool {
	return isGateOnlyStep(s)
}

func isWorkflowCallStep(s *Step) bool {
	return strings.TrimSpace(s.Workflow) != ""
}

func isParallelGroupStep(s *Step) bool {
	return s.Parallel && len(s.Steps) > 0 && s.Type == "" && s.Agent == "" && strings.TrimSpace(s.Prompt) == "" && strings.TrimSpace(s.Workflow) == ""
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
		for k, v := range collectStepsWithPath(s.Each, p) {
			out[k] = v
		}
		for k, v := range collectStepsWithPath(s.Steps, p) {
			out[k] = v
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
		out = append(out, collectStepNodes(s.Each, p)...)
		out = append(out, collectStepNodes(s.Steps, p)...)
	}
	return out
}

func validateStep(s *Step, path, scopePath string, seenNames map[string]bool, stepNamesByPath map[string]string, wf *Workflow) []error {
	var errs []error
	n := strings.TrimSpace(s.Name)
	if n == "" {
		errs = append(errs, ValidationError{Path: path + ".name", Message: "step name is required"})
	} else {
		if seenNames[n] {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("duplicate step name %q within scope", n)})
		}
		seenNames[n] = true
	}

	gateOnly := isGateOnlyStep(s)
	if gateOnly {
		if len(s.Gate) == 0 {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("gate-only step %q must have a non-empty gate", s.Name)})
		}
		if len(s.Prompt) > 0 || len(s.Context) > 0 {
			errs = append(errs, ValidationError{Path: path, Message: "gate-only step cannot have prompt or context"})
		}
		if len(s.Retry) > 0 {
			errs = append(errs, ValidationError{Path: path, Message: "gate-only step cannot have retry (use agent: pass if retries are required)"})
		}
		if len(s.Each) > 0 {
			errs = append(errs, ValidationError{Path: path, Message: "gate-only step cannot have each:"})
		}
	} else if isWorkflowCallStep(s) || isParallelGroupStep(s) {
		// WHY: workflow-call and parallel containers omit type: by design (spec §5, E2E-R1-14/R1-15).
	} else {
		if strings.TrimSpace(s.Type) == "" || !validStepTypes[s.Type] {
			errs = append(errs, ValidationError{Path: path + ".type", Message: "type must be code, split, or validate"})
		}
	}

	if s.Type == "split" && len(s.Each) == 0 {
		errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("split step %q must have 'each:'", s.Name)})
	}
	if s.Type != "split" && len(s.Each) > 0 {
		errs = append(errs, ValidationError{Path: path, Message: "'each:' is only valid on split steps"})
	}

	hasPrompt := strings.TrimSpace(s.Prompt) != ""
	if !gateOnly && hasPrompt && s.Agent == "" {
		errs = append(errs, ValidationError{Path: path, Message: "agent is required when prompt is set"})
	}

	if len(s.Retry) > 0 {
		hasExit := false
		exitIdx := -1
		for i, r := range s.Retry {
			if r.Exit > 0 {
				hasExit = true
				exitIdx = i
			}
		}
		if !hasExit {
			errs = append(errs, ValidationError{Path: path + ".retry", Message: fmt.Sprintf("step %q: retry must have an 'exit:' entry", s.Name)})
		} else {
			for j := exitIdx + 1; j < len(s.Retry); j++ {
				if ValidateWarn != nil {
					ValidateWarn(path+".retry", "entries after exit: are dead code")
				}
				break
			}
		}
		var lastAttempt int
		for _, r := range s.Retry {
			if r.Attempt > 0 {
				if r.Attempt <= lastAttempt {
					errs = append(errs, ValidationError{Path: path + ".retry", Message: fmt.Sprintf("step %q: retry attempt entries must be in ascending order", s.Name)})
					break
				}
				lastAttempt = r.Attempt
			}
		}
	}

	if s.Agent == "pass" {
		if s.Guard.MaxTurns > 0 || s.Guard.MaxBudget > 0 || s.Guard.MaxTokens > 0 || s.Guard.MaxTime != "" || s.Guard.NoWrite != nil {
			if ValidateWarn != nil {
				ValidateWarn(path+".guard", "guard has no effect when agent is 'pass'")
			}
		}
	}

	if s.Parallel && s.Type != "split" && len(s.Steps) == 0 {
		errs = append(errs, ValidationError{Path: path, Message: "parallel: true requires type: split with each: or a non-empty steps: group"})
	}

	if len(s.Steps) > 0 && len(s.Each) > 0 {
		errs = append(errs, ValidationError{Path: path, Message: "steps: and each: cannot be set on the same step"})
	}

	if len(s.Steps) > 0 && s.Type != "split" && strings.TrimSpace(s.Agent) != "" {
		errs = append(errs, ValidationError{Path: path, Message: "has both 'agent' and 'steps'. Hint: split into two steps or use type: split with nested steps"})
	}
	if len(s.Steps) > 0 && s.Type != "split" && strings.TrimSpace(s.Agent) == "" && strings.TrimSpace(s.Prompt) != "" {
		errs = append(errs, ValidationError{Path: path, Message: "has both 'prompt' and 'steps'. Hint: split into two steps or use type: split with nested steps"})
	}

	if s.Workflow != "" && s.Agent != "" {
		errs = append(errs, ValidationError{Path: path, Message: "workflow: and agent: cannot both be set on the same step"})
	}
	if len(s.With) > 0 && s.Workflow == "" {
		errs = append(errs, ValidationError{Path: path, Message: "with: requires workflow:"})
	}

	h := strings.TrimSpace(s.HITL)
	if h != "" && !validHitl[h] {
		errs = append(errs, ValidationError{Path: path + ".hitl", Message: "hitl must be true, before_gate, or after_gate"})
	}
	if s.Agent == "pass" && h != "" {
		if ValidateWarn != nil {
			ValidateWarn(path+".hitl", "hitl has no effect when agent is pass")
		}
	}

	for gi, g := range s.Gate {
		gp := fmt.Sprintf("%s.gate[%d]", path, gi)
		if !validGateTypes[g.Type] {
			errs = append(errs, ValidationError{Path: gp, Message: fmt.Sprintf("unknown gate type %q", g.Type)})
			continue
		}
		if g.Type == "validate" {
			if strings.TrimSpace(g.Arg) == "" {
				errs = append(errs, ValidationError{Path: gp, Message: "validate: requires a workflow path"})
			}
			if a, ok := g.With["agent"]; ok && strings.TrimSpace(a) == "" {
				if ValidateWarn != nil {
					ValidateWarn(gp+".with", "validate with: contains agent key with empty value")
				}
			}
		}
		switch g.Type {
		case "touched", "untouched":
			if g.Arg == "" {
				errs = append(errs, ValidationError{Path: gp, Message: fmt.Sprintf("%q requires an argument", g.Type)})
			}
		case "coverage":
			if g.Arg == "" || !isNumeric(g.Arg) {
				errs = append(errs, ValidationError{Path: gp, Message: "\"coverage\" requires a numeric threshold"})
			}
		case "bash":
			if g.Arg == "" {
				errs = append(errs, ValidationError{Path: gp, Message: "\"bash\" requires a command"})
			}
		}
	}

	if s.Session.Mode == "from" && s.Session.Target != "" {
		if s.Session.Target == s.Name {
			errs = append(errs, ValidationError{Path: path + ".session", Message: "session cannot reference itself"})
		} else if !stepNameExistsInWorkflow(wf, s.Session.Target) {
			errs = append(errs, ValidationError{Path: path + ".session", Message: fmt.Sprintf("session from: target step %q not found in workflow", s.Session.Target)})
		}
	}

	if s.Guard.MaxTime != "" {
		if _, err := time.ParseDuration(s.Guard.MaxTime); err != nil {
			errs = append(errs, ValidationError{Path: path + ".guard.max_time", Message: fmt.Sprintf("invalid duration %q", s.Guard.MaxTime)})
		}
	}

	fullPath := scopePath
	if fullPath != "" {
		fullPath += "/"
	}
	fullPath += s.Name

	subSeen := make(map[string]bool)
	for i := range s.Each {
		errs = append(errs, validateStep(&s.Each[i], path+fmt.Sprintf(".each[%d]", i), fullPath, subSeen, stepNamesByPath, wf)...)
	}
	for i := range s.Steps {
		errs = append(errs, validateStep(&s.Steps[i], path+fmt.Sprintf(".steps[%d]", i), fullPath, subSeen, stepNamesByPath, wf)...)
	}

	for _, m := range stepsRefRegex.FindAllStringSubmatch(s.Prompt, -1) {
		if len(m) != 3 {
			continue
		}
		refName := strings.TrimSpace(m[1])
		candidates := findStepPathsByName(stepNamesByPath, refName)
		if len(candidates) == 0 {
			if ValidateWarn != nil {
				ValidateWarn(path, fmt.Sprintf("prompt references unknown step %q in {steps.%s.%s}", refName, refName, m[2]))
			}
		} else if len(candidates) > 1 {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("ambiguous step reference %q — use fully-qualified path", refName)})
		}
	}

	return errs
}

func stepNameExistsInWorkflow(wf *Workflow, short string) bool {
	for _, p := range collectStepNodes(wf.Steps, "") {
		if p.step.Name == short {
			return true
		}
	}
	return false
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

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// FindStepByName returns the first step with the given name in a depth-first walk.
func FindStepByName(steps []Step, name string) *Step {
	for i := range steps {
		if steps[i].Name == name {
			return &steps[i]
		}
		if found := FindStepByName(steps[i].Each, name); found != nil {
			return found
		}
		if found := FindStepByName(steps[i].Steps, name); found != nil {
			return found
		}
	}
	return nil
}
