package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/isomorphx/gump/internal/agent"
	pkgcontext "github.com/isomorphx/gump/internal/context"
	"github.com/isomorphx/gump/internal/ledger"
	"github.com/isomorphx/gump/internal/plan"
	"github.com/isomorphx/gump/internal/workflow"
	"github.com/isomorphx/gump/internal/template"
)

// ExecuteReplan runs the replan agent to produce a new plan, then runs each sub-task with the original step agent (no retry on sub-tasks).
func (e *Engine) ExecuteReplan(replanAgent string, step *workflow.Step, scopePath string, errorContext *ErrorContext, task *plan.Task) error {
	originalPrompt := step.Prompt
	if step.OutputMode() == "plan" && originalPrompt == "" {
		originalPrompt = "Analyze the following specification and produce a plan.\n\n{spec}"
	}
	originalResolved := template.Resolve(originalPrompt, e.newTemplateCtx(scopePath, nil, nil, 1, nil, nil))
	itemName, itemDesc, itemFiles := "", originalResolved, ""
	if task != nil {
		itemName = task.Name
		itemDesc = task.Description
		if itemDesc == "" {
			itemDesc = originalResolved
		}
		itemFiles = strings.Join(task.Files, ", ")
	}
	diffStr := ""
	errStr := ""
	if errorContext != nil {
		diffStr = errorContext.Diff
		errStr = errorContext.Error
	}

	contextFile := agent.ContextFileForAgent(replanAgent)
	agent.RemoveOtherContextFiles(e.Cook.WorktreeDir, contextFile)
	if err := PrepareOutputDir(e.Cook.WorktreeDir); err != nil {
		return fmt.Errorf("prepare output dir for replan: %w", err)
	}
	maxErr, maxDiff := 2000, 3000
	if e.Config != nil {
		if e.Config.ErrorContextMaxErrorChars > 0 {
			maxErr = e.Config.ErrorContextMaxErrorChars
		}
		if e.Config.ErrorContextMaxDiffChars > 0 {
			maxDiff = e.Config.ErrorContextMaxDiffChars
		}
	}
	replanBody, err := pkgcontext.BuildReplan(e.Cook.WorktreeDir, itemName, itemDesc, itemFiles, diffStr, errStr, contextFile, maxErr, maxDiff)
	if err != nil {
		return fmt.Errorf("write replan context: %w", err)
	}

	adapter, err := e.Resolver.AdapterFor(replanAgent)
	if err != nil {
		return err
	}
	promptForAgent := replanBody + "\n[PUDDING:plan]"
	ctx := context.Background()
	proc, err := adapter.Launch(ctx, agent.LaunchRequest{
		Worktree:  e.Cook.WorktreeDir,
		Prompt:    promptForAgent,
		AgentName: replanAgent,
		Timeout:   0,
		MaxTurns:  0,
	})
	if err != nil {
		return fmt.Errorf("replan agent launch: %w", err)
	}
	for ev := range adapter.Stream(proc) {
		formatStreamEventToTerminal(ev, replanAgent)
	}
	result, err := adapter.Wait(proc)
	if err != nil {
		return fmt.Errorf("replan agent wait: %w", err)
	}
	if result.IsError {
		return fmt.Errorf("replan agent reported error")
	}

	tasks, raw, err := ExtractPlanOutput(e.Cook.WorktreeDir)
	if err != nil {
		return fmt.Errorf("replan did not produce valid plan: %w", err)
	}
	if err := plan.ValidatePlanSchema(tasks); err != nil {
		return fmt.Errorf("replan plan schema: %w", err)
	}
	if e.Cook.Ledger != nil {
		name := ledger.SanitizeStepPath(scopePath) + "-replan-output.json"
		if rel, _ := e.Cook.Ledger.WriteArtifact(name, []byte(raw)); rel != "" {
			_ = e.Cook.Ledger.Emit(ledger.ReplanTriggered{Step: scopePath, Agent: replanAgent, Artifact: rel})
		}
	}

	// Snapshot the plan (commit plan.json output).
	taskName := "-"
	if _, err := e.Cook.Snapshot(step.Name+"/replan", taskName, 1); err != nil {
		return fmt.Errorf("snapshot replan: %w", err)
	}
	e.State.SetStepOutput(scopePath+"/replan", raw, "", nil, "")

	// Run each sub-task with the original step agent; no retry on sub-tasks.
	sessionMap := make(map[string]string)
	for _, task := range tasks {
		// Parent prefix in name so State Bag, artifacts and logs show e.g. [green/replan-task-fix-add] not [replan-task-fix-add].
		ephemeralName := step.Name + "/replan-task-" + task.Name
		subPath := scopePath + "/replan-task-" + task.Name
		ephemeral := workflow.Step{
			Name:   ephemeralName,
			Type:   "code",
			Agent:  step.Agent,
			Prompt: replanSubTaskPrompt,
			Gate:   step.Gate,
		}
		extraVars := map[string]string{"original_prompt": originalResolved}
		if err := e.runAtomicStepWithVars(&ephemeral, subPath, &task, sessionMap, workflow.SessionConfig{Mode: "fresh"}, extraVars); err != nil {
			return err
		}
	}
	return nil
}

// replanSubTaskPrompt is the prompt template for each replan sub-task (spec 5.2). "Implementation (replan sub-task)" is used so the stub can use root scenario files only.
const replanSubTaskPrompt = `## Your Task: Implementation (replan sub-task)

This is a sub-task from a re-planning phase. Focus only on this specific sub-task.

Sub-task: {task.name}
Description: {task.description}
Files to modify: {task.files}

Original context:
{original_prompt}`
