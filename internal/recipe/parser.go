package recipe

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseWarn is called for deprecated fields (kept for compatibility with older migrations).
var ParseWarn func(msg string)

// Parse turns YAML into a Recipe using YAML schema v4.
// recipeDir is the directory of the recipe file so prompt: file: <path> can be resolved relative to the recipe and keep long prompts out of YAML; empty for built-in recipes.
func Parse(yamlBytes []byte, recipeDir string) (*Recipe, error) {
	raw := struct {
		Name        string          `yaml:"name"`
		Description string          `yaml:"description"`
		MaxBudget   float64         `yaml:"max_budget"`
		Steps       []rawStep       `yaml:"steps"`
		Review      *[]interface{}  `yaml:"review"` // legacy root-level review (M1 rejects)
	}{}
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		if strings.Contains(err.Error(), "mapping values are not allowed") {
			return nil, fmt.Errorf("recipe YAML: %w — for targeted session, use \"session:\\n  reuse: <step-name>\" or \"session: { reuse: <step-name> }\", not \"session: reuse: <step-name>\" on one line", err)
		}
		return nil, fmt.Errorf("recipe YAML: %w", err)
	}
	if raw.Review != nil {
		return nil, fmt.Errorf("recipe has root-level 'review:' block which is no longer supported.\nHint: add a gate step at the end of your steps list instead.")
	}
	r := &Recipe{
		Name:        raw.Name,
		Description: raw.Description,
		MaxBudget:   raw.MaxBudget,
		Steps:       make([]Step, 0, len(raw.Steps)),
	}
	for i := range raw.Steps {
		step, err := parseStep(&raw.Steps[i], fmt.Sprintf("steps[%d]", i), recipeDir)
		if err != nil {
			return nil, err
		}
		r.Steps = append(r.Steps, *step)
	}
	return r, nil
}

type rawStep struct {
	Name       string        `yaml:"name"`
	Type       string        `yaml:"type"`
	Agent      string        `yaml:"agent"`
	Prompt     interface{}   `yaml:"prompt"` // string (inline) or map with "file": path relative to recipe dir
	Output     string        `yaml:"output"`
	Gate       *[]interface{} `yaml:"gate"`
	Validate   *[]interface{} `yaml:"validate"` // legacy (M1 rejects)
	Retry      *rawRetry     `yaml:"retry"`     // legacy (M1 rejects)
	OnFailure  *rawOnFailure `yaml:"on_failure"`
	Steps      []rawStep     `yaml:"steps"`
	Recipe     string        `yaml:"recipe"`
	Parallel   bool          `yaml:"parallel"`
	Foreach    string        `yaml:"foreach"`
	Session    interface{}   `yaml:"session"` // string "reuse"/"fresh" etc, or map reuse: step-name
	Context    []ContextSource `yaml:"context"`
	Timeout    string        `yaml:"timeout"`
	MaxBudget  float64       `yaml:"max_budget"`
	HITL       bool          `yaml:"hitl"`
	MaxTurns   int           `yaml:"max_turns"`
}

type rawRetry struct {
	MaxAttempts int           `yaml:"max_attempts"`
	Strategy    []interface{} `yaml:"strategy"`
}

type rawOnFailure struct {
	Retry       *int           `yaml:"retry"`
	Strategy    []interface{} `yaml:"strategy"`
	RestartFrom string        `yaml:"restart_from"`
}

func parseStep(raw *rawStep, pathPrefix string, recipeDir string) (*Step, error) {
	if raw.Type != "" {
		return nil, fmt.Errorf("step %q uses 'type:' which is no longer needed. Pudding infers the step type from the fields present.\nHint: remove the 'type:' field. Use 'output: plan' for plan steps, 'foreach:' for iteration.", raw.Name)
	}
	prompt, err := resolvePrompt(raw.Prompt, pathPrefix+".prompt", recipeDir)
	if err != nil {
		return nil, err
	}

	// Detect v3 obsolete fields and guide migration to v4.
	if raw.Validate != nil && raw.Retry != nil {
		return nil, fmt.Errorf("step %q uses legacy v3 fields: validate: has been replaced by gate:.\nHint: replace validate: + retry: with gate: + on_failure: { retry: N, strategy: [...] }", raw.Name)
	}
	if raw.Validate != nil {
		return nil, fmt.Errorf("step %q uses 'validate:' which has been replaced by 'gate:'.\nHint: rename 'validate:' to 'gate:' and move 'retry:' inside 'on_failure:'.", raw.Name)
	}
	if raw.Retry != nil {
		return nil, fmt.Errorf("step %q uses legacy 'retry:' which has been replaced by 'on_failure:'.", raw.Name)
	}

	gateValidators, err := parseValidatorsErr(derefInterfaceSlice(raw.Gate), pathPrefix+".gate")
	if err != nil {
		return nil, err
	}

	// Parse on_failure (v4). This is normalised to the legacy RetryPolicy so the runtime can keep using it.
	var onFailure *OnFailure
	var retry *RetryPolicy
	if raw.OnFailure != nil {
		strategy, err := parseStrategy(raw.OnFailure.Strategy, pathPrefix+".on_failure.strategy")
		if err != nil {
			return nil, err
		}
		on := &OnFailure{
			Retry:       0,
			Strategy:    strategy,
			RestartFrom: raw.OnFailure.RestartFrom,
		}
		if raw.OnFailure.Retry != nil {
			on.Retry = *raw.OnFailure.Retry
		}
		onFailure = on
		if on.Retry > 0 {
			retry = &RetryPolicy{MaxAttempts: on.Retry, Strategy: ExpandStrategy(on.Strategy)}
			// WHY: engine expects strategy entries with Count expanded when max_attempts>1.
			// We expand here to preserve old runtime semantics while keeping v4 YAML clean.
		}
	}

	subSteps := make([]Step, 0, len(raw.Steps))
	for i := range raw.Steps {
		s, err := parseStep(&raw.Steps[i], pathPrefix+fmt.Sprintf(".steps[%d]", i), recipeDir)
		if err != nil {
			return nil, err
		}
		subSteps = append(subSteps, *s)
	}
	session := parseSession(raw.Session)
	output := strings.TrimSpace(raw.Output)
	if output == "" && raw.Agent != "" {
		output = "diff"
	}
	return &Step{
		Name:      raw.Name,
		Agent:     raw.Agent,
		Prompt:    prompt,
		Output:    output,
		Steps:     subSteps,
		Recipe:    raw.Recipe,
		Parallel:  raw.Parallel,
		Foreach:   strings.TrimSpace(raw.Foreach),
		Session:   session,
		Context:   raw.Context,
		Timeout:   raw.Timeout,
		MaxBudget: raw.MaxBudget,
		HITL:      raw.HITL,
		MaxTurns:  raw.MaxTurns,

		Gate:      gateValidators,
		OnFailure: onFailure,

		// Legacy normalisation for the current runtime.
		Validate: gateValidators,
		Retry:    retry,
	}, nil
}

func derefInterfaceSlice(p *[]interface{}) []interface{} {
	if p == nil {
		return nil
	}
	return *p
}

// resolvePrompt turns YAML prompt (string or {file: path}) into one string so downstream only ever sees inline content; external files keep recipes readable.
func resolvePrompt(promptRaw interface{}, pathCtx string, recipeDir string) (string, error) {
	if promptRaw == nil {
		return "", nil
	}
	switch v := promptRaw.(type) {
	case string:
		return v, nil
	case map[string]interface{}:
		filePath, _ := v["file"].(string)
		filePath = strings.TrimSpace(filePath)
		if filePath == "" {
			return "", fmt.Errorf("%s: prompt file must have a non-empty 'file' path", pathCtx)
		}
		if recipeDir == "" {
			return "", fmt.Errorf("prompt file not found: %s", filePath)
		}
		absPath := filepath.Join(recipeDir, filepath.FromSlash(filePath))
		content, err := os.ReadFile(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("prompt file not found: %s", filePath)
			}
			return "", fmt.Errorf("%s: %w", pathCtx, err)
		}
		return string(content), nil
	default:
		return "", fmt.Errorf("%s: prompt must be a string or { file: <path> }", pathCtx)
	}
}

func parseSession(v interface{}) SessionConfig {
	if v == nil {
		return SessionConfig{Mode: "fresh"}
	}
	switch x := v.(type) {
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return SessionConfig{Mode: "fresh"}
		}
		switch s {
		case "fresh", "reuse", "reuse-on-retry":
			return SessionConfig{Mode: s}
		default:
			// v4: `session: reuse: <step>` string form.
			if strings.HasPrefix(s, "reuse:") {
				target := strings.TrimSpace(strings.TrimPrefix(s, "reuse:"))
				return SessionConfig{Mode: "reuse-targeted", Target: target}
			}
			return SessionConfig{Mode: s}
		}
	case map[string]interface{}:
		for k, val := range x {
			if strings.TrimSpace(k) == "reuse" && val != nil {
				target := strings.TrimSpace(fmt.Sprint(val))
				return SessionConfig{Mode: "reuse-targeted", Target: target}
			}
		}
	}
	return SessionConfig{Mode: "fresh"}
}

func parseValidatorsErr(list []interface{}, ctx string) ([]Validator, error) {
	if len(list) == 0 {
		return nil, nil
	}
	out := make([]Validator, 0, len(list))
	for i, item := range list {
		switch v := item.(type) {
		case string:
			// "schema: plan" or "compile"
			if idx := strings.IndexByte(v, ':'); idx >= 0 {
				out = append(out, Validator{Type: strings.TrimSpace(v[:idx]), Arg: strings.TrimSpace(v[idx+1:])})
			} else {
				out = append(out, Validator{Type: strings.TrimSpace(v)})
			}
		case map[string]interface{}:
			for k, val := range v {
				arg := ""
				if val != nil {
					arg = fmt.Sprint(val)
				}
				out = append(out, Validator{Type: strings.TrimSpace(k), Arg: strings.TrimSpace(arg)})
				break
			}
		default:
			return nil, fmt.Errorf("%s[%d]: expected string or map for validator", ctx, i)
		}
	}
	return out, nil
}

func parseRetry(r *struct {
	MaxAttempts int           `yaml:"max_attempts"`
	Strategy    []interface{} `yaml:"strategy"`
}, pathPrefix string) (*RetryPolicy, error) {
	if r == nil {
		return nil, nil
	}
	strategy, err := parseStrategy(r.Strategy, pathPrefix+".retry.strategy")
	if err != nil {
		return nil, err
	}
	return &RetryPolicy{
		MaxAttempts: r.MaxAttempts,
		Strategy:    strategy,
	}, nil
}

// parseStrategy supports v3/v4 forms:
// - "same" or "same: N" (N repeats)
// - "escalate: <agent>" / "replan: <agent>"
// - map form: same: 3, escalate: <agent>
func parseStrategy(list []interface{}, ctx string) ([]StrategyEntry, error) {
	if len(list) == 0 {
		return nil, nil
	}
	var out []StrategyEntry
	for i, item := range list {
		switch v := item.(type) {
		case string:
			s := strings.TrimSpace(v)
			if idx := strings.IndexByte(s, ':'); idx >= 0 {
				typ := strings.TrimSpace(s[:idx])
				rest := strings.TrimSpace(s[idx+1:])
				if typ == "same" {
					n, err := strconv.Atoi(rest)
					if err != nil || n <= 0 {
						return nil, fmt.Errorf("%s[%d]: same: <count> must be a positive integer", ctx, i)
					}
					out = append(out, StrategyEntry{Type: typ, Count: n})
				} else {
					out = append(out, StrategyEntry{Type: typ, Agent: rest, Count: 1})
				}
			} else {
				out = append(out, StrategyEntry{Type: s, Count: 1})
			}
		case map[string]interface{}:
			for k, val := range v {
				typ := strings.TrimSpace(k)
				agent := ""
				count := 1
				switch n := val.(type) {
				case string:
					agent = n
				case int:
					count = n
				case float64:
					count = int(n)
				}
				out = append(out, StrategyEntry{Type: typ, Agent: agent, Count: count})
				break
			}
		default:
			return nil, fmt.Errorf("%s[%d]: expected string or map", ctx, i)
		}
	}
	return out, nil
}

// ExpandStrategy returns the full strategy slice with Count expanded (e.g. same: 3 → 3 entries).
func ExpandStrategy(entries []StrategyEntry) []StrategyEntry {
	var out []StrategyEntry
	for _, e := range entries {
		n := e.Count
		if n <= 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			out = append(out, StrategyEntry{Type: e.Type, Agent: e.Agent, Count: 1})
		}
	}
	return out
}
