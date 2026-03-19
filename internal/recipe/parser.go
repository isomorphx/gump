package recipe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseWarn is called for deprecated fields (type, review). Set by cmd/cook so e2e can assert on stderr.
var ParseWarn func(msg string)

// Parse turns YAML into a Recipe (v3: no type/review; output, session config, foreach).
// recipeDir is the directory of the recipe file so prompt: file: <path> can be resolved relative to the recipe and keep long prompts out of YAML; empty for built-in recipes.
func Parse(yamlBytes []byte, recipeDir string) (*Recipe, error) {
	raw := struct {
		Name        string          `yaml:"name"`
		Description string          `yaml:"description"`
		Steps       []rawStep       `yaml:"steps"`
		Review      []interface{}   `yaml:"review"`
	}{}
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		if strings.Contains(err.Error(), "mapping values are not allowed") {
			return nil, fmt.Errorf("recipe YAML: %w — for targeted session, use \"session:\\n  reuse: <step-name>\" or \"session: { reuse: <step-name> }\", not \"session: reuse: <step-name>\" on one line", err)
		}
		return nil, fmt.Errorf("recipe YAML: %w", err)
	}
	if len(raw.Review) > 0 && ParseWarn != nil {
		ParseWarn("field 'review' is deprecated — add a validation step at the end of 'steps:' instead")
	}
	r := &Recipe{
		Name:        raw.Name,
		Description: raw.Description,
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
	Name     string        `yaml:"name"`
	Type     string        `yaml:"type"`
	Agent    string        `yaml:"agent"`
	Prompt   interface{}   `yaml:"prompt"` // string (inline) or map with "file": path relative to recipe dir
	Output   string        `yaml:"output"`
	Validate []interface{} `yaml:"validate"`
	Retry    *struct {
		MaxAttempts int           `yaml:"max_attempts"`
		Strategy    []interface{} `yaml:"strategy"`
	} `yaml:"retry"`
	Steps    []rawStep     `yaml:"steps"`
	Recipe   string        `yaml:"recipe"`
	Parallel bool          `yaml:"parallel"`
	Foreach  string        `yaml:"foreach"`
	Session  interface{}   `yaml:"session"` // string "reuse"/"fresh" or map reuse: step-name
	Context  []ContextSource `yaml:"context"`
	Timeout  string        `yaml:"timeout"`
	MaxTurns int           `yaml:"max_turns"`
}

func parseStep(raw *rawStep, pathPrefix string, recipeDir string) (*Step, error) {
	if raw.Type != "" && ParseWarn != nil {
		ParseWarn("field 'type' is deprecated and ignored — step behavior is inferred from declared fields")
	}
	prompt, err := resolvePrompt(raw.Prompt, pathPrefix+".prompt", recipeDir)
	if err != nil {
		return nil, err
	}
	validate, err := parseValidatorsErr(raw.Validate, pathPrefix+".validate")
	if err != nil {
		return nil, err
	}
	retry, err := parseRetry(raw.Retry, pathPrefix)
	if err != nil {
		return nil, err
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
		Name:     raw.Name,
		Agent:    raw.Agent,
		Prompt:   prompt,
		Output:   output,
		Validate: validate,
		Retry:    retry,
		Steps:    subSteps,
		Recipe:   raw.Recipe,
		Parallel: raw.Parallel,
		Foreach:   strings.TrimSpace(raw.Foreach),
		Session:  session,
		Context:  raw.Context,
		Timeout:  raw.Timeout,
		MaxTurns: raw.MaxTurns,
	}, nil
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
		return SessionConfig{Mode: s}
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

// parseStrategy supports: "same", "escalate: claude-sonnet", and map form same: 3, escalate: claude-sonnet.
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
				agent := strings.TrimSpace(s[idx+1:])
				out = append(out, StrategyEntry{Type: typ, Agent: agent, Count: 1})
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
