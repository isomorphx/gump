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

var validOutputValues = map[string]bool{"diff": true, "plan": true, "artifact": true}
var validStrategyTypes = map[string]bool{"same": true, "escalate": true, "replan": true}
// validSessionModes allows reuse-on-retry so recipes can express "fresh first run, resume on retry" without storing session in State Bag.
var validSessionModes = map[string]bool{"reuse": true, "fresh": true, "reuse-targeted": true, "reuse-on-retry": true}
var validValidatorTypes = map[string]bool{
	"compile": true, "test": true, "lint": true, "schema": true,
	"touched": true, "untouched": true, "tests_found": true, "coverage": true, "bash": true,
}

// stepsRefRegex matches {steps.<name>.output} or {steps.<name>.diff}
var stepsRefRegex = regexp.MustCompile(`\{steps\.([^}.]+)\.(output|diff)\}`)

// Validate runs v3 structural rules: inference (no type), output/foreach/session, state-bag refs.
func Validate(r *Recipe) []ValidationError {
	var errs []ValidationError
	if r.Name == "" {
		errs = append(errs, ValidationError{Path: "recipe", Message: "name is required"})
	}
	if len(r.Steps) == 0 {
		errs = append(errs, ValidationError{Path: "recipe", Message: "at least one step is required"})
	}
	// Collect all step names by path for foreach and ref resolution
	stepNamesByPath := make(map[string]string) // fullPath -> short name (for ambiguity check)
	allSteps := collectStepsWithPath(r.Steps, "")
	for path, name := range allSteps {
		stepNamesByPath[path] = name
	}
	seenNames := make(map[string]bool)
	for i := range r.Steps {
		e := validateStep(r, &r.Steps[i], fmt.Sprintf("steps[%d]", i), "", seenNames, stepNamesByPath)
		errs = append(errs, e...)
	}
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

func validateStep(rec *Recipe, s *Step, path string, scopePath string, seenNames map[string]bool, stepNamesByPath map[string]string) []ValidationError {
	var errs []ValidationError
	if s.Name == "" {
		errs = append(errs, ValidationError{Path: path, Message: "name is required"})
	} else {
		if seenNames[s.Name] {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("duplicate step name %q", s.Name)})
		}
		seenNames[s.Name] = true
	}

	hasAgent := s.Agent != ""
	hasSteps := len(s.Steps) > 0
	hasValidate := len(s.Validate) > 0
	hasRecipe := s.Recipe != ""
	hasForeachRecipe := s.Foreach != "" && s.Recipe != ""

	if hasAgent && hasSteps {
		errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q: cannot have both 'agent' and 'steps'", s.Name)})
	}
	// recipe: makes the step a container; it cannot also be atomic (agent/prompt/validate).
	if hasRecipe && (hasAgent || s.Prompt != "" || len(s.Validate) > 0) {
		errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q: recipe: cannot have 'agent', 'prompt', or 'validate' (container only)", s.Name)})
	}
	if !hasAgent && !hasSteps && !hasValidate && !hasForeachRecipe && !hasRecipe {
		errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q: must have 'agent', 'steps', 'validate', or 'recipe' (with optional 'foreach')", s.Name)})
	}

	if hasAgent && !hasSteps {
		if s.Output != "" && !validOutputValues[s.Output] {
			errs = append(errs, ValidationError{Path: path, Message: "output must be \"diff\", \"plan\", or \"artifact\""})
		}
	}
	if !hasAgent && !hasSteps && s.Output != "" {
		errs = append(errs, ValidationError{Path: path, Message: "output only allowed on steps with agent"})
	}
	if hasSteps && s.Output != "" {
		errs = append(errs, ValidationError{Path: path, Message: "output only allowed on steps with agent"})
	}

	if s.Foreach != "" {
		refStep := findStepByName(rec.Steps, s.Foreach, "")
		if refStep == nil {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q: foreach references unknown step %q", s.Name, s.Foreach)})
		} else if refStep.Output != "plan" {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("step %q: foreach references step %q which is not output: plan", s.Name, s.Foreach)})
		}
	}

	if s.Session.Mode != "" && !validSessionModes[s.Session.Mode] {
		errs = append(errs, ValidationError{Path: path, Message: "session must be \"reuse\", \"fresh\", \"reuse: <step-name>\", or \"reuse-on-retry\""})
	}
	if s.Session.Mode == "reuse-targeted" && s.Session.Target != "" {
		refStep := findStepByName(rec.Steps, s.Session.Target, "")
		if refStep == nil {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("session reuse target %q: step not found", s.Session.Target)})
		} else if refStep.Agent != "" && s.Agent != "" && refStep.Agent != s.Agent && ValidateWarn != nil {
			ValidateWarn(path, "session reuse target uses different agent — runtime will use fresh session")
		}
	}

	if s.Timeout != "" {
		if _, err := time.ParseDuration(s.Timeout); err != nil {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("invalid timeout %q (must be a valid Go duration, e.g. \"5m\", \"30s\")", s.Timeout)})
		}
	}
	for j, v := range s.Validate {
		p := path + ".validate[" + fmt.Sprint(j) + "]"
		if !validValidatorTypes[v.Type] {
			errs = append(errs, ValidationError{Path: p, Message: fmt.Sprintf("unknown validator %q", v.Type)})
		}
		switch v.Type {
		case "touched", "untouched":
			if v.Arg == "" {
				errs = append(errs, ValidationError{Path: p, Message: fmt.Sprintf("%q requires a glob argument", v.Type)})
			}
		case "coverage":
			if v.Arg == "" {
				errs = append(errs, ValidationError{Path: p, Message: "\"coverage\" requires a numeric threshold"})
			} else if !isNumeric(v.Arg) {
				errs = append(errs, ValidationError{Path: p, Message: "\"coverage\" requires a numeric threshold"})
			}
		case "bash":
			if v.Arg == "" {
				errs = append(errs, ValidationError{Path: p, Message: "\"bash\" requires a command"})
			}
		}
	}
	if s.Retry != nil {
		errs = append(errs, validateRetry(s.Retry, path)...)
	}

	fullPath := scopePath
	if fullPath != "" {
		fullPath += "/"
	}
	fullPath += s.Name
	subSeen := make(map[string]bool)
	for i := range s.Steps {
		errs = append(errs, validateStep(rec, &s.Steps[i], path+".steps["+fmt.Sprint(i)+"]", fullPath, subSeen, stepNamesByPath)...)
	}

	// State Bag refs in prompt: {steps.<name>.output} / {steps.<name>.diff} must reference existing step; ambiguous = error
	for _, m := range stepsRefRegex.FindAllStringSubmatch(s.Prompt, -1) {
		if len(m) != 3 {
			continue
		}
		refName := strings.TrimSpace(m[1])
		candidates := findStepPathsByName(stepNamesByPath, refName)
		if len(candidates) == 0 {
			errs = append(errs, ValidationError{Path: path, Message: fmt.Sprintf("prompt references unknown step %q in {steps.%s.%s}", refName, refName, m[2])})
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

func findStepPathsByName(stepNamesByPath map[string]string, name string) []string {
	var out []string
	for path, n := range stepNamesByPath {
		if n == name {
			out = append(out, path)
		}
	}
	return out
}

func validateRetry(r *RetryPolicy, path string) []ValidationError {
	var errs []ValidationError
	p := path + ".retry"
	if r.MaxAttempts < 1 {
		errs = append(errs, ValidationError{Path: p, Message: "max_attempts must be >= 1"})
	}
	if r.MaxAttempts > 10 {
		errs = append(errs, ValidationError{Path: p, Message: "max_attempts cannot exceed 10"})
	}
	if len(r.Strategy) == 0 {
		errs = append(errs, ValidationError{Path: p, Message: "strategy is required"})
	}
	for i, e := range r.Strategy {
		sp := p + ".strategy[" + fmt.Sprint(i) + "]"
		if !validStrategyTypes[e.Type] {
			errs = append(errs, ValidationError{Path: sp, Message: "type must be \"same\", \"escalate\", or \"replan\""})
		}
		if (e.Type == "escalate" || e.Type == "replan") && e.Agent == "" {
			errs = append(errs, ValidationError{Path: sp, Message: fmt.Sprintf("agent is required for %q", e.Type)})
		}
	}
	return errs
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
