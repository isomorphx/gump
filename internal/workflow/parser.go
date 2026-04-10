package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseWarn is set by the CLI for ad-hoc warnings outside the structured Warning slice.
var ParseWarn func(msg string)

// Parse reads v0.0.4 workflow YAML. workflowDir is used to resolve prompt { file: path }; empty skips file prompts.
func Parse(yamlBytes []byte, workflowDir string) (*Workflow, []Warning, error) {
	var root map[string]interface{}
	if err := yaml.Unmarshal(yamlBytes, &root); err != nil {
		return nil, nil, fmt.Errorf("workflow YAML: %w", err)
	}
	if root == nil {
		return nil, nil, fmt.Errorf("workflow YAML: empty document")
	}

	var warnings []Warning
	warnings = append(warnings, detectDeprecated(root)...)

	name, _ := root["name"].(string)
	name = strings.TrimSpace(name)

	wf := &Workflow{Name: name}
	if v, ok := root["max_budget"]; ok && v != nil {
		f, err := toFloat64(v)
		if err != nil {
			return nil, warnings, fmt.Errorf("max_budget: %w", err)
		}
		wf.MaxBudget = f
	}
	if v, ok := root["max_timeout"]; ok && v != nil {
		wf.MaxTimeout = strings.TrimSpace(fmt.Sprint(v))
	}
	if v, ok := root["max_tokens"]; ok && v != nil {
		n, err := toInt(v)
		if err != nil {
			return nil, warnings, fmt.Errorf("max_tokens: %w", err)
		}
		wf.MaxTokens = n
	}

	rawSteps, ok := root["steps"].([]interface{})
	if !ok {
		return nil, warnings, fmt.Errorf("steps: required list")
	}
	for i, el := range rawSteps {
		sm, ok := el.(map[string]interface{})
		if !ok {
			return nil, warnings, fmt.Errorf("steps[%d]: expected mapping", i)
		}
		st, w, err := parseStep(sm, workflowDir)
		warnings = append(warnings, w...)
		if err != nil {
			return nil, warnings, fmt.Errorf("steps[%d]: %w", i, err)
		}
		wf.Steps = append(wf.Steps, st)
	}

	return wf, warnings, nil
}

func parseStep(raw map[string]interface{}, workflowDir string) (Step, []Warning, error) {
	var w []Warning
	w = append(w, detectDeprecatedStep(raw)...)

	name, _ := raw["name"].(string)
	name = strings.TrimSpace(name)

	if _, ok := raw["validate"]; ok {
		return Step{}, w, fmt.Errorf("step %q: 'validate:' at step level was removed — replaced by 'gate:' (e.g. gate: [compile])", name)
	}

	getKeys := []string{"prompt", "context", "worktree", "session", "workflow"}
	runKeys := []string{"agent", "guard", "hitl"}

	var getMap, runMap map[string]interface{}
	if g, ok := raw["get"].(map[string]interface{}); ok {
		getMap = g
	}
	if r, ok := raw["run"].(map[string]interface{}); ok {
		runMap = r
	}

	if getMap != nil {
		if err := noDupBlockKeys(raw, "get", getKeys, name); err != nil {
			return Step{}, w, err
		}
		for _, k := range getKeys {
			_, atTop := raw[k]
			_, inGet := getMap[k]
			if atTop && !inGet {
				return Step{}, w, fmt.Errorf("step %q: keyword %q cannot appear at step level when 'get:' is present — put it under 'get:'", name, k)
			}
		}
	}
	if runMap != nil {
		if err := noDupBlockKeys(raw, "run", runKeys, name); err != nil {
			return Step{}, w, err
		}
		for _, k := range runKeys {
			_, atTop := raw[k]
			_, inRun := runMap[k]
			if atTop && !inRun {
				return Step{}, w, fmt.Errorf("step %q: keyword %q cannot appear at step level when 'run:' is present — put it under 'run:'", name, k)
			}
		}
	}

	lookup := func(key string) (interface{}, bool) {
		if containsStr(getKeys, key) && getMap != nil {
			v, ok := getMap[key]
			return v, ok
		}
		if containsStr(runKeys, key) && runMap != nil {
			v, ok := runMap[key]
			return v, ok
		}
		v, ok := raw[key]
		return v, ok
	}

	typeStr := ""
	if v, ok := lookup("type"); ok && v != nil {
		typeStr = strings.TrimSpace(fmt.Sprint(v))
	}

	agentStr := ""
	if v, ok := lookup("agent"); ok && v != nil {
		agentStr = strings.TrimSpace(fmt.Sprint(v))
	}
	promptVal, hasPrompt := lookup("prompt")

	step := Step{Name: name, Type: typeStr, Agent: agentStr}

	subStepsList := asStepList(raw["steps"])
	hasChildSteps := len(subStepsList) > 0
	workfRaw, hasWorkflowKey := raw["workflow"]
	workfStr := ""
	if hasWorkflowKey && workfRaw != nil {
		workfStr = strings.TrimSpace(fmt.Sprint(workfRaw))
	}
	parallelVal, hasParallel := raw["parallel"]
	parallelOn := hasParallel && toBool(parallelVal)
	_, hasEach := raw["each"]

	isWorkflowCall := hasWorkflowKey && workfStr != ""
	isParallelGroup := parallelOn && hasChildSteps && typeStr == "" && agentStr == "" && !hasPrompt && !hasEach && getMap == nil && runMap == nil && !isWorkflowCall
	isGateOnlyCandidate := typeStr == "" && agentStr == "" && !hasPrompt &&
		getMap == nil && runMap == nil &&
		!isWorkflowCall && !hasEach &&
		!hasChildSteps
	legacyNoType := typeStr == "" && !isGateOnlyCandidate && !isWorkflowCall && !isParallelGroup &&
		(agentStr != "" || hasPrompt)

	if typeStr == "" {
		if !(isGateOnlyCandidate || isWorkflowCall || isParallelGroup || legacyNoType) {
			return Step{}, w, fmt.Errorf("step %q: type is required (code, split, or validate)", name)
		}
	} else if err := validateStepType(typeStr, name); err != nil {
		return Step{}, w, err
	}

	if hasPrompt {
		prompt, err := resolvePrompt(promptVal, workflowDir)
		if err != nil {
			return Step{}, w, fmt.Errorf("step %q: %w", name, err)
		}
		step.Prompt = prompt
	}

	if v, ok := lookup("context"); ok {
		ctx, err := parseContextList(v)
		if err != nil {
			return Step{}, w, fmt.Errorf("step %q: context: %w", name, err)
		}
		step.Context = ctx
	}

	if v, ok := lookup("worktree"); ok && v != nil {
		step.Worktree = strings.TrimSpace(fmt.Sprint(v))
	}

	if v, ok := lookup("session"); ok {
		sc, err := parseSession(v)
		if err != nil {
			return Step{}, w, fmt.Errorf("step %q: %w", name, err)
		}
		step.Session = sc
	} else {
		step.Session = SessionConfig{Mode: "new"}
	}

	if getMap != nil {
		if wv, ok := getMap["workflow"]; ok && wv != nil {
			wp := strings.TrimSpace(fmt.Sprint(wv))
			if wp == "" {
				return Step{}, w, fmt.Errorf("step %q: get.workflow requires a non-empty path", name)
			}
			wm := make(map[string]string)
			for k2, v2 := range getMap {
				switch k2 {
				case "prompt", "context", "worktree", "session", "workflow":
					continue
				default:
					wm[k2] = strings.TrimSpace(fmt.Sprint(v2))
				}
			}
			step.GetWorkflow = &GetWorkflowSpec{
				Name: WorkflowRefLastSegment(wp),
				Path: wp,
				With: wm,
			}
		}
	}

	if v, ok := lookup("guard"); ok {
		g, err := parseGuard(v)
		if err != nil {
			return Step{}, w, fmt.Errorf("step %q: guard: %w", name, err)
		}
		step.Guard = g
	}

	if v, ok := lookup("hitl"); ok && v != nil {
		step.HITL, _ = parseHITLRaw(v)
	}

	if g, ok := raw["gate"]; ok {
		ge, err := parseGate(g)
		if err != nil {
			return Step{}, w, fmt.Errorf("step %q: gate: %w", name, err)
		}
		step.Gate = ge
	}

	if r, ok := raw["retry"]; ok {
		re, err := parseRetry(r)
		if err != nil {
			return Step{}, w, fmt.Errorf("step %q: retry: %w", name, err)
		}
		step.Retry = re
	}

	if v, ok := raw["parallel"]; ok {
		step.Parallel = toBool(v)
	}

	if wv, ok := raw["workflow"]; ok && wv != nil {
		step.Workflow = strings.TrimSpace(fmt.Sprint(wv))
	}
	if wm, ok := raw["with"].(map[string]interface{}); ok {
		step.With = stringifyMap(wm)
	}

	if ea, ok := raw["each"].([]interface{}); ok {
		for i, el := range ea {
			sm, ok := el.(map[string]interface{})
			if !ok {
				return Step{}, w, fmt.Errorf("step %q: each[%d]: expected mapping", name, i)
			}
			sub, wsub, err := parseStep(sm, workflowDir)
			w = append(w, wsub...)
			if err != nil {
				return Step{}, w, fmt.Errorf("step %q: each[%d]: %w", name, i, err)
			}
			step.Each = append(step.Each, sub)
		}
	}

	if len(subStepsList) > 0 {
		sub := subStepsList
		for i, sm := range sub {
			subst, wsub, err := parseStep(sm, workflowDir)
			w = append(w, wsub...)
			if err != nil {
				return Step{}, w, fmt.Errorf("step %q: steps[%d]: %w", name, i, err)
			}
			step.Steps = append(step.Steps, subst)
		}
	}

	return step, w, nil
}

func asStepList(v interface{}) []map[string]interface{} {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	var out []map[string]interface{}
	for _, el := range arr {
		if sm, ok := el.(map[string]interface{}); ok {
			out = append(out, sm)
		}
	}
	return out
}

func noDupBlockKeys(raw map[string]interface{}, block string, keys []string, stepName string) error {
	bm, ok := raw[block].(map[string]interface{})
	if !ok {
		return nil
	}
	n := strings.TrimSpace(stepName)
	for _, k := range keys {
		if _, inBlock := bm[k]; !inBlock {
			continue
		}
		if _, atTop := raw[k]; atTop {
			return fmt.Errorf("step %q: keyword %q cannot appear both in %q block and at step level", n, k, block)
		}
	}
	return nil
}

func containsStr(slice []string, s string) bool {
	for _, x := range slice {
		if x == s {
			return true
		}
	}
	return false
}

func validateStepType(t, stepName string) error {
	switch t {
	case "code", "split", "validate", "artifact":
		return nil
	case "search", "scrape", "surf", "use", "doc":
		return fmt.Errorf("step %q: type %q is not yet supported", stepName, t)
	default:
		return fmt.Errorf("step %q: unknown type %q", stepName, t)
	}
}

func resolvePrompt(promptRaw interface{}, workflowDir string) (string, error) {
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
			return "", fmt.Errorf("prompt file must have a non-empty 'file' path")
		}
		if workflowDir == "" {
			return "", fmt.Errorf("prompt file not found: %s", filePath)
		}
		absPath := filepath.Join(workflowDir, filepath.FromSlash(filePath))
		content, err := os.ReadFile(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("prompt file not found: %s", filePath)
			}
			return "", err
		}
		return string(content), nil
	default:
		return "", fmt.Errorf("prompt must be a string or { file: <path> }")
	}
}

func parseSession(v interface{}) (SessionConfig, error) {
	if v == nil {
		return SessionConfig{Mode: "new"}, nil
	}
	switch x := v.(type) {
	case string:
		s := strings.TrimSpace(x)
		if s == "" || s == "new" {
			return SessionConfig{Mode: "new"}, nil
		}
		return SessionConfig{}, fmt.Errorf("invalid session value, expected 'new' or 'from: <step>'")
	case map[string]interface{}:
		if t, ok := x["from"]; ok {
			return SessionConfig{Mode: "from", Target: strings.TrimSpace(fmt.Sprint(t))}, nil
		}
		return SessionConfig{}, fmt.Errorf("invalid session value, expected 'new' or 'from: <step>'")
	default:
		return SessionConfig{}, fmt.Errorf("invalid session value, expected 'new' or 'from: <step>'")
	}
}

func parseGuard(v interface{}) (Guard, error) {
	if v == nil {
		return Guard{}, nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return Guard{}, fmt.Errorf("expected mapping")
	}
	var g Guard
	_, hasTimeout := m["timeout"]
	_, hasMaxTime := m["max_time"]
	if hasTimeout && hasMaxTime {
		return Guard{}, fmt.Errorf("'timeout' and 'max_time' are aliases, use one")
	}
	if v, ok := m["max_turns"]; ok {
		n, err := toInt(v)
		if err != nil {
			return g, fmt.Errorf("max_turns: %w", err)
		}
		g.MaxTurns = n
	}
	if v, ok := m["max_budget"]; ok {
		f, err := toFloat64(v)
		if err != nil {
			return g, fmt.Errorf("max_budget: %w", err)
		}
		g.MaxBudget = f
	}
	if v, ok := m["max_tokens"]; ok {
		n, err := toInt(v)
		if err != nil {
			return g, fmt.Errorf("max_tokens: %w", err)
		}
		g.MaxTokens = n
	}
	if v, ok := m["max_time"]; ok {
		g.MaxTime = strings.TrimSpace(fmt.Sprint(v))
	}
	if v, ok := m["timeout"]; ok {
		g.MaxTime = strings.TrimSpace(fmt.Sprint(v))
	}
	if v, ok := m["no_write"]; ok {
		b := toBool(v)
		g.NoWrite = &b
	}
	return g, nil
}

var gateArgKeys = map[string]bool{
	"touched": true, "untouched": true, "coverage": true, "bash": true, "validate": true,
}

func parseGate(v interface{}) ([]GateEntry, error) {
	list, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("expected list")
	}
	var out []GateEntry
	for i, el := range list {
		switch t := el.(type) {
		case string:
			s := strings.TrimSpace(t)
			if s == "" {
				return nil, fmt.Errorf("[%d]: empty string", i)
			}
			if idx := strings.IndexByte(s, ':'); idx >= 0 {
				out = append(out, GateEntry{Type: strings.TrimSpace(s[:idx]), Arg: strings.TrimSpace(s[idx+1:])})
			} else {
				out = append(out, GateEntry{Type: s})
			}
		case map[string]interface{}:
			if len(t) == 0 {
				return nil, fmt.Errorf("[%d]: empty map", i)
			}
			if vv, ok := t["validate"]; ok {
				arg := strings.TrimSpace(fmt.Sprint(vv))
				if arg == "" || arg == "<nil>" {
					return nil, fmt.Errorf("validate: path required")
				}
				with := make(map[string]string)
				for k2, v2 := range t {
					if k2 == "validate" {
						continue
					}
					with[k2] = strings.TrimSpace(fmt.Sprint(v2))
				}
				out = append(out, GateEntry{Type: "validate", Arg: arg, With: with})
				continue
			}
			handled := false
			for _, ks := range []string{"touched", "untouched", "coverage", "bash"} {
				if val, ok := t[ks]; ok {
					if !gateArgKeys[ks] {
						return nil, fmt.Errorf("unknown entry %q", ks)
					}
					out = append(out, GateEntry{Type: ks, Arg: strings.TrimSpace(fmt.Sprint(val))})
					handled = true
					break
				}
			}
			if !handled {
				for k := range t {
					return nil, fmt.Errorf("unknown entry %q", strings.TrimSpace(k))
				}
			}
		default:
			return nil, fmt.Errorf("[%d]: expected string or map", i)
		}
	}
	return out, nil
}

func parseRetry(v interface{}) ([]RetryEntry, error) {
	list, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("expected list")
	}
	var out []RetryEntry
	for i, el := range list {
		m, ok := el.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("[%d]: expected mapping", i)
		}
		var cond int
		var re RetryEntry
		if x, ok := m["attempt"]; ok {
			cond++
			n, err := toInt(x)
			if err != nil {
				return nil, fmt.Errorf("[%d] attempt: %w", i, err)
			}
			re.Attempt = n
		}
		if x, ok := m["not"]; ok {
			cond++
			re.Not = strings.TrimSpace(fmt.Sprint(x))
		}
		if x, ok := m["validate"]; ok {
			cond++
			re.Validate = strings.TrimSpace(fmt.Sprint(x))
		}
		if x, ok := m["exit"]; ok {
			cond++
			n, err := toInt(x)
			if err != nil {
				return nil, fmt.Errorf("[%d] exit: %w", i, err)
			}
			re.Exit = n
		}
		if cond != 1 {
			return nil, fmt.Errorf("[%d]: retry entry must have exactly one condition (attempt, not, validate, or exit)", i)
		}

		allowed := map[string]bool{
			"attempt": true, "not": true, "validate": true, "exit": true,
			"with": true, "agent": true, "session": true, "worktree": true, "prompt": true,
		}
		for k := range m {
			if !allowed[k] {
				return nil, fmt.Errorf("[%d]: retry entry: unknown key %q", i, k)
			}
		}

		if wm, ok := m["with"].(map[string]interface{}); ok {
			re.With = stringifyMap(wm)
		}
		if x, ok := m["agent"]; ok && re.Exit == 0 {
			re.Agent = strings.TrimSpace(fmt.Sprint(x))
		}
		if x, ok := m["session"]; ok && re.Exit == 0 {
			re.Session = strings.TrimSpace(fmt.Sprint(x))
		}
		if x, ok := m["worktree"]; ok && re.Exit == 0 {
			re.Worktree = strings.TrimSpace(fmt.Sprint(x))
		}
		if x, ok := m["prompt"]; ok && re.Exit == 0 {
			switch p := x.(type) {
			case string:
				re.Prompt = p
			default:
				re.Prompt = strings.TrimSpace(fmt.Sprint(x))
			}
		}

		out = append(out, re)
	}
	return out, nil
}

func parseContextList(v interface{}) ([]ContextSource, error) {
	list, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("expected list")
	}
	var out []ContextSource
	for i, el := range list {
		m, ok := el.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("[%d]: expected mapping", i)
		}
		if fp, ok := m["file"]; ok {
			out = append(out, ContextSource{Type: "file", Value: strings.TrimSpace(fmt.Sprint(fp))})
			continue
		}
		if bp, ok := m["bash"]; ok {
			out = append(out, ContextSource{Type: "bash", Value: strings.TrimSpace(fmt.Sprint(bp))})
			continue
		}
		return nil, fmt.Errorf("[%d]: need file: or bash:", i)
	}
	if len(list) == 0 {
		return nil, nil
	}
	return out, nil
}

func parseHITLRaw(v interface{}) (string, error) {
	switch x := v.(type) {
	case bool:
		if x {
			return "before_gate", nil
		}
		return "", nil
	case string:
		s := strings.TrimSpace(x)
		switch s {
		case "true", "before_gate", "after_gate":
			if s == "true" {
				return "before_gate", nil
			}
			return s, nil
		default:
			return s, nil
		}
	default:
		return strings.TrimSpace(fmt.Sprint(v)), nil
	}
}

func stringifyMap(m map[string]interface{}) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = strings.TrimSpace(fmt.Sprint(v))
	}
	return out
}

func toFloat64(v interface{}) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(x), 64)
	default:
		return 0, fmt.Errorf("expected number")
	}
}

func toInt(v interface{}) (int, error) {
	switch x := v.(type) {
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case float64:
		return int(x), nil
	case string:
		return strconv.Atoi(strings.TrimSpace(x))
	default:
		return 0, fmt.Errorf("expected integer")
	}
}

func toBool(v interface{}) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(strings.TrimSpace(x), "true") || strings.TrimSpace(x) == "1"
	default:
		return false
	}
}
