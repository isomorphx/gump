package engine

import (
	"fmt"
	"os"
	"strings"

	"github.com/isomorphx/gump/internal/agent"
	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/plan"
	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/validate"
	"github.com/isomorphx/gump/internal/workflow"
)

func taskContextName(t *plan.Task) string {
	if t == nil {
		return ""
	}
	return t.Name
}

// sessionModeForLedger records session as new / from:<step> for ledger consumers (v0.0.4 vocabulary).
func sessionModeForLedger(step *workflow.Step, parentSession workflow.SessionConfig) string {
	eff := workflow.ResolveSessionForEngine(step, parentSession)
	switch eff.Mode {
	case "fresh":
		return "new"
	case "reuse-targeted":
		if eff.Target != "" {
			return "from:" + eff.Target
		}
		return "from"
	default:
		return eff.Mode
	}
}
func (e *Engine) resolveTargetedSession(target string, currentAgent string) string {
	sid := e.State.Get(target + ".session_id")
	targetAgent := strings.TrimSpace(e.State.Get(target + ".agent"))
	if targetAgent == "" {
		for i := len(e.Steps) - 1; i >= 0; i-- {
			if e.Steps[i].StepName == target && e.Steps[i].Agent != "" {
				targetAgent = e.Steps[i].Agent
				break
			}
		}
	}
	if sid != "" && targetAgent != "" && agent.AgentPrefix(targetAgent) != agent.AgentPrefix(currentAgent) {
		fmt.Fprintf(os.Stderr, "[%s] session: different agent/provider (reference step %q used %s, current %s), using new session\n", brand.Lower(), target, targetAgent, currentAgent)
		return ""
	}
	if sid != "" {
		return sid
	}
	for i := len(e.Steps) - 1; i >= 0; i-- {
		if e.Steps[i].StepName == target && e.Steps[i].SessionID != "" {
			prevA := e.Steps[i].Agent
			if prevA != "" && agent.AgentPrefix(prevA) != agent.AgentPrefix(currentAgent) {
				fmt.Fprintf(os.Stderr, "[%s] session: different agent/provider (reference step %q used %s, current %s), using new session\n", brand.Lower(), target, prevA, currentAgent)
				return ""
			}
			return e.Steps[i].SessionID
		}
	}
	return ""
}

// lastSessionIDForStep returns the SessionID from the last agent_completed for this step path so retries can resume the same agent session instead of starting fresh.
func (e *Engine) lastSessionIDForStep(stepPath string) string {
	for i := len(e.Steps) - 1; i >= 0; i-- {
		if e.Steps[i].StepPath == stepPath && e.Steps[i].SessionID != "" {
			return e.Steps[i].SessionID
		}
	}
	return ""
}
func (e *Engine) buildVars(taskContext *plan.Task, errorContext *ErrorContext, extraVars map[string]string, inheritedVars map[string]string) map[string]string {
	vars := map[string]string{
		"spec":  e.SpecContent,
		"error": "",
		"diff":  "",
	}
	for k, v := range inheritedVars {
		vars[k] = v
	}
	if taskContext != nil {
		vars["task.name"] = taskContext.Name
		vars["task.description"] = taskContext.Description
		vars["task.files"] = strings.Join(taskContext.Files, ", ")
		vars["item.name"] = taskContext.Name
		vars["item.description"] = taskContext.Description
		vars["item.files"] = strings.Join(taskContext.Files, ", ")
	}
	if errorContext != nil {
		vars["error"] = errorContext.Error
		vars["diff"] = errorContext.Diff
	}
	for k, v := range extraVars {
		vars[k] = v
	}
	return vars
}

func engineOutputToStepType(om string) string {
	switch om {
	case "diff":
		return "code"
	case "plan":
		return "split"
	case "validate":
		return "validate"
	case "review":
		return "validate"
	default:
		return om
	}
}

func (e *Engine) newTemplateCtx(stepPath string, step *workflow.Step, taskContext *plan.Task, errCtx *ErrorContext, attempt int, inheritedVars, extraVars map[string]string) *state.ResolveContext {
	ex := map[string]string{}
	for k, v := range inheritedVars {
		if k != "spec" {
			ex[k] = v
		}
	}
	for k, v := range extraVars {
		if k != "spec" {
			ex[k] = v
		}
	}
	ctx := &state.ResolveContext{
		State:    e.State,
		StepPath: stepPath,
		Spec:     e.SpecContent,
		Attempt:  attempt,
		Extra:    ex,
	}
	if attempt > 1 && step != nil && len(step.Gate) > 0 {
		gr, gm := validate.GateTemplateMapsFromState(e.State, stepPath, step.Gate)
		ctx.GateResults = gr
		ctx.GateMeta = gm
	}
	if ctx.GateResults == nil {
		ctx.GateResults = map[string]string{}
	}
	if ctx.GateMeta == nil {
		ctx.GateMeta = map[string]map[string]string{}
	}
	if taskContext != nil {
		ctx.Task = &state.TaskVars{
			Name: taskContext.Name, Description: taskContext.Description,
			Files: strings.Join(taskContext.Files, ", "),
		}
	}
	if errCtx != nil {
		ctx.Error = errCtx.Error
		ctx.Diff = errCtx.Diff
	}
	return ctx
}
