package state

import (
	"strconv"
	"strings"
)

// ResolveContext carries everything the template layer needs so v0.0.4 resolution stays deterministic (no shadow maps, no implicit proximity).
type ResolveContext struct {
	State       *State
	StepPath    string
	Spec        string
	Task        *TaskVars
	Attempt     int
	Error       string
	Diff        string
	GateResults map[string]string
	GateMeta    map[string]map[string]string
	// Extra carries subworkflow `with:` keys and other caller-defined placeholders until the engine is fully on v0.0.4.
	Extra map[string]string
}

// TaskVars is the each-scope task surface exposed to templates as {task.*}.
type TaskVars struct {
	Name        string
	Description string
	Files       string
}

// Resolve returns a template variable value; empty means unknown so the template layer can drop lone placeholder lines.
func (ctx *ResolveContext) Resolve(varName string) string {
	if ctx == nil {
		return ""
	}
	switch varName {
	case "spec":
		return ctx.Spec
	case "attempt":
		if ctx.Attempt <= 0 {
			return ""
		}
		return strconv.Itoa(ctx.Attempt)
	case "error":
		return ctx.Error
	case "diff":
		return ctx.Diff
	case "output":
		if ctx.State == nil {
			return ""
		}
		return ctx.State.Get(ctx.StepPath + ".output")
	}
	if strings.HasPrefix(varName, "task.") {
		if ctx.Task == nil {
			return ""
		}
		switch varName {
		case "task.name":
			return ctx.Task.Name
		case "task.description":
			return ctx.Task.Description
		case "task.files":
			return ctx.Task.Files
		}
		return ""
	}
	if strings.HasPrefix(varName, "prev.") {
		if ctx.Attempt <= 1 || ctx.State == nil {
			return ""
		}
		field := strings.TrimPrefix(varName, "prev.")
		return ctx.State.GetPrev(ctx.StepPath, field)
	}
	if strings.HasPrefix(varName, "gate.") {
		return ctx.resolveGate(strings.TrimPrefix(varName, "gate."))
	}
	// v0.0.3 spellings removed so workflows fail closed in templates and dry-run can flag migrations.
	if strings.HasPrefix(varName, "steps.") || strings.HasPrefix(varName, "item.") || strings.HasPrefix(varName, "run.") {
		return ""
	}
	if ctx.Extra != nil {
		if v, ok := ctx.Extra[varName]; ok {
			return v
		}
	}
	// WHY: sub-workflow inputs from gate with: / RunSubWorkflow live as flat keys (e.g. agent, diff); templates must see them without step. prefixes (R5 / ADR-047).
	if ctx.State != nil && !strings.Contains(varName, ".") {
		if v := strings.TrimSpace(ctx.State.Get(varName)); v != "" {
			return v
		}
	}
	return ctx.resolveQualifiedStepVar(varName)
}

func (ctx *ResolveContext) resolveGate(rest string) string {
	if rest == "" {
		return ""
	}
	segs := strings.Split(rest, ".")
	last := segs[len(segs)-1]
	if last == "pass" || last == "comments" || last == "error" {
		if len(segs) < 2 {
			return ""
		}
		name := strings.Join(segs[:len(segs)-1], ".")
		if ctx.GateMeta != nil && ctx.GateMeta[name] != nil {
			return ctx.GateMeta[name][last]
		}
		return ""
	}
	if ctx.GateResults != nil {
		return ctx.GateResults[rest]
	}
	return ""
}

func (ctx *ResolveContext) resolveQualifiedStepVar(varName string) string {
	if ctx.State == nil {
		return ""
	}
	dot := strings.IndexByte(varName, '.')
	if dot < 0 {
		return ""
	}
	first, field := varName[:dot], varName[dot+1:]
	if first == "" || field == "" {
		return ""
	}
	// Explicit qualified path uses slashes in the step segment; lookup the key verbatim in the flat state.
	if strings.Contains(first, "/") {
		return ctx.State.Get(varName)
	}
	if p := eachScopePrefix(ctx.StepPath); p != "" {
		if v := ctx.State.Get(p + first + "." + field); v != "" {
			return v
		}
	}
	return ctx.State.Get(first + "." + field)
}

func eachScopePrefix(stepPath string) string {
	i := strings.LastIndex(stepPath, "/")
	if i < 0 {
		return ""
	}
	return stepPath[:i+1]
}
