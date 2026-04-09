package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/isomorphx/gump/internal/agent"
	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/ledger"
	"github.com/isomorphx/gump/internal/plan"
	"github.com/isomorphx/gump/internal/run"
	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/template"
	"github.com/isomorphx/gump/internal/workflow"
)

// ErrCookAborted is returned when the user aborts during HITL (Ctrl+C).
var ErrCookAborted = errors.New("aborted")

// Engine runs the recipe step-by-step; behavior is inferred from fields (no type:) so recipes stay declarative. Flat State holds outputs for v0.0.4 `{step.field}` templates.
// AgentsCLI is set by the CLI so cook_started can record agent versions; stepCompletedCount, retryTriggeredCount, totalCostUSD, agentsUsed are accumulated for cook_completed and index.
type Engine struct {
	Run                  *run.Run
	Workflow             *workflow.Workflow
	Resolver             agent.AdapterResolver
	Config               *config.Config
	SpecContent          string
	Steps                []StepExecution
	State                *state.State
	AgentsCLI            map[string]string
	CookAgentOverride    string // CLI --agent overrides step agent when set
	FromStep             string // when set (replay), skip steps until we reach this path
	ResumePassedSteps    map[string]bool
	ResumePreviousStatus string
	replayOriginalCookID string // for replay_started event
	replayRestoredCommit string
	stepCompletedCount   int
	retryTriggeredCount  int
	totalCostUSD         float64
	globalTokens         int
	agentsUsed           map[string]struct{}
	cookRunStartedAt     time.Time
	lastStep             *lastStepOutcome
	replayReachedStart   bool // true once we've reached FromStep in executeSteps
	stepRunIndex         int  // 1-based index of current step for [N/total] header (Feature 12)
	stepTotalEstimate    int  // total steps when known (e.g. after group_started with task_count)
	globalStepAttempts   map[string]int
	budgetTracker        *BudgetTracker
	budgetWarnOnce       bool
	// pendingRestartFrom delivers review/gate failure context to the first attempt after restart_from.
	pendingRestartFrom map[string]*ErrorContext
	restartCycle       int
	lastFailureSource  string
	// PauseAfterStep is set by CLI --pause-after to force HITL on that step name (in-memory only).
	PauseAfterStep string
	// forceSessionReuse is enabled for retry same policies (step/group) so retries
	// resume the previous session even when step session mode is fresh.
	forceSessionReuse bool
}

// New builds an engine bound to an existing run dir, worktree, and ledger.
func New(runPtr *run.Run, wf *workflow.Workflow, resolver agent.AdapterResolver, cfg *config.Config, specContent string) *Engine {
	st := runPtr.State
	if st == nil {
		st = state.New()
		runPtr.State = st
	}
	return &Engine{
		Run: runPtr, Workflow: wf, Resolver: resolver, Config: cfg, SpecContent: specContent,
		Steps: nil, State: st,
		agentsUsed:         make(map[string]struct{}),
		globalStepAttempts: make(map[string]int),
		budgetTracker:      NewBudgetTracker(wf.MaxBudget),
		pendingRestartFrom: make(map[string]*ErrorContext),
	}
}

// Execute runs the workflow; returns nil if all steps pass, or an error when a step is fatal.
// We always run the end-of-run sequence (state-bag, final-diff, run_completed, index, close ledger) so the ledger is complete even on fatal.
func (e *Engine) Execute() error {
	defer agent.RestoreAllContextFiles(e.Run.WorktreeDir)
	e.cookRunStartedAt = time.Now()
	// WHY: resume re-uses the worktree; a stale engine-step-attempt.json makes the stub pick the wrong by_attempt key.
	if e.ResumePassedSteps != nil && len(e.ResumePassedSteps) > 0 {
		_ = os.Remove(filepath.Join(e.Run.WorktreeDir, brand.StateDir(), "engine-step-attempt.json"))
	}
	if e.Run.Ledger != nil {
		_ = e.Run.Ledger.Emit(ledger.RunStarted{
			RunID:      e.Run.ID,
			Workflow:   e.Run.WorkflowName,
			Spec:       filepath.Base(e.Run.SpecPath),
			Commit:     e.Run.InitialCommit,
			Branch:     e.Run.OrigBranch,
			AgentsCLI:  e.AgentsCLI,
			MaxBudget:  e.Workflow.MaxBudget,
			MaxTimeout: e.Workflow.MaxTimeout,
			MaxTokens:  e.Workflow.MaxTokens,
		})
		if e.FromStep != "" && e.replayOriginalCookID != "" {
			_ = e.Run.Ledger.Emit(ledger.ReplayStarted{
				OriginalCookID: e.replayOriginalCookID,
				FromStep:       e.FromStep,
				RestoredCommit: e.replayRestoredCommit,
			})
		}
		if e.FromStep != "" && e.ResumePreviousStatus != "" {
			_ = e.Run.Ledger.Emit(ledger.RunResumed{
				RunID:          e.Run.ID,
				ResumedFrom:    e.FromStep,
				PreviousStatus: e.ResumePreviousStatus,
			})
		}
	}
	CookHeader(e.Run.WorkflowName, e.Run.ID, filepath.Base(e.Run.SpecPath))
	err := e.executeWorkflowSequential()
	e.finishCook(err)
	return err
}

// checkGlobalWorkflowBounds stops the run once global caps are crossed so partial workflows cannot spend without bound.
func (e *Engine) checkGlobalWorkflowBounds() error {
	wf := e.Workflow
	if wf.MaxBudget > 0 && e.totalCostUSD > wf.MaxBudget {
		if e.Run.Ledger != nil {
			_ = e.Run.Ledger.Emit(ledger.BudgetExceeded{
				Step: "", Scope: "budget", MaxUSD: wf.MaxBudget, SpentUSD: e.totalCostUSD,
			})
		}
		return fmt.Errorf("workflow max_budget exceeded (%.4f > %.4f)", e.totalCostUSD, wf.MaxBudget)
	}
	if wf.MaxTokens > 0 && e.globalTokens > wf.MaxTokens {
		if e.Run.Ledger != nil {
			_ = e.Run.Ledger.Emit(ledger.BudgetExceeded{
				Step: "", Scope: "tokens", MaxTokens: wf.MaxTokens, CurrentTokens: e.globalTokens,
			})
		}
		return fmt.Errorf("workflow max_tokens exceeded (%d > %d)", e.globalTokens, wf.MaxTokens)
	}
	if d := strings.TrimSpace(wf.MaxTimeout); d != "" {
		maxDur, err := time.ParseDuration(d)
		if err == nil && maxDur > 0 && time.Since(e.cookRunStartedAt) > maxDur {
			if e.Run.Ledger != nil {
				elapsed := time.Since(e.cookRunStartedAt).Seconds()
				_ = e.Run.Ledger.Emit(ledger.BudgetExceeded{
					Step: "", Scope: "timeout", MaxSeconds: maxDur.Seconds(), ElapsedSeconds: elapsed,
				})
			}
			return fmt.Errorf("workflow max_timeout exceeded")
		}
	}
	return nil
}

// executeSteps runs steps; composite (steps:) with optional foreach, atomic (agent:), or validation-only (gate:).
// groupAgentOverride is set when we're inside a group retry with strategy "escalate"; atomic steps in this subtree use that agent.
func (e *Engine) executeSteps(steps []workflow.Step, taskContext *plan.Task, pathPrefix string, lastSessionByAgent map[string]string, parentSession workflow.SessionConfig, groupAgentOverride string, inheritedVars map[string]string) error {
	groupBaseCommit, _ := PreStepCommit(e.Run.WorktreeDir)
	for i := 0; i < len(steps); i++ {
		step := steps[i]
		stepPath := pathPrefix
		if stepPath != "" {
			stepPath += "/"
		}
		stepPath += step.Name

		// Replay: skip steps until we reach FromStep; do not skip a group that contains FromStep (e.g. implement when FromStep is implement/task-2/code).
		if e.FromStep != "" {
			if !e.replayReachedStart {
				if stepPath == e.FromStep {
					e.replayReachedStart = true
				} else if strings.HasPrefix(e.FromStep, stepPath+"/") {
					// FromStep is inside this composite; recurse will hit it.
				} else {
					continue
				}
			}
		}
		if e.ResumePassedSteps != nil && e.ResumePassedSteps[stepPath] {
			continue
		}

		// Composite: parallel group, split+each, or nested workflow call (plan iteration uses the split step name as state-bag scope).
		foreachRef := ""
		if step.Type == "split" && len(step.Each) > 0 {
			foreachRef = step.Name
		}
		isComposite := len(step.Steps) > 0 || step.Workflow != "" || (step.Type == "split" && len(step.Each) > 0)
		if isComposite {
			if step.Workflow != "" && foreachRef == "" && len(step.Steps) == 0 && len(step.Each) == 0 {
				childRec, childVars, err := e.resolveWorkflow(&step, stepPath, taskContext, inheritedVars)
				if err != nil {
					return err
				}
				parentSB := e.State
				childSB := state.New()
				childSB.SetRunAll(parentSB.CloneRun())
				e.State = childSB
				err = e.executeSteps(childRec.Steps, taskContext, "", make(map[string]string), workflow.SessionConfig{Mode: "fresh"}, "", childVars)
				e.State = parentSB
				if err != nil {
					return err
				}
				e.State.Graft(stepPath, childSB)
				continue
			}
			var planTasks []plan.Task
			taskCount := 0
			if foreachRef != "" {
				planShortName := step.Name
				planRaw := e.State.GetStepScoped(planShortName, stepPath, "output")
				if planRaw == "" && strings.TrimSpace(step.Agent) != "" && strings.TrimSpace(step.Prompt) != "" {
					if err := e.runAtomicStep(&step, stepPath, taskContext, lastSessionByAgent, parentSession, groupAgentOverride, inheritedVars); err != nil {
						return err
					}
					planRaw = e.State.GetStepScoped(planShortName, stepPath, "output")
				}
				if planRaw == "" {
					return fmt.Errorf("split step %q has no plan output in State Bag (set agent: and prompt: on the split to run a planner, or pre-seed output)", step.Name)
				}
				var err error
				planTasks, err = plan.ParsePlanOutput([]byte(planRaw))
				if err != nil {
					return fmt.Errorf("foreach plan from State Bag: %w", err)
				}
				taskCount = len(planTasks)
			}
			subSteps, err := e.resolveSubSteps(&step)
			if err != nil {
				return err
			}
			if e.Run.Ledger != nil {
				_ = e.Run.Ledger.Emit(ledger.GroupStarted{Step: stepPath, Foreach: foreachRef, Parallel: step.Parallel, TaskCount: taskCount})
			}
			if taskCount > 0 {
				e.stepTotalEstimate = e.stepRunIndex + taskCount*len(subSteps)
			} else {
				e.stepTotalEstimate = e.stepRunIndex + len(subSteps)
			}
			runGroupOnce := func(agentOverride string, sessionMap map[string]string) error {
				if step.Parallel {
					// So each branch gets a worktree cloned from current main state (same commit for all, deterministic merge order).
					baseCommit, err := PreStepCommit(e.Run.WorktreeDir)
					if err != nil {
						return err
					}
					return RunParallelGroup(e, &step, stepPath, subSteps, planTasks, baseCommit, sessionMap, step.Session, agentOverride, inheritedVars)
				}
				if planTasks != nil {
					for _, task := range planTasks {
						taskPrefix := step.Name + "/" + task.Name
						if pathPrefix != "" {
							taskPrefix = pathPrefix + "/" + taskPrefix
						}
						taskSession := make(map[string]string)
						if step.Workflow != "" && len(step.Steps) == 0 && len(step.Each) == 0 {
							childRec, childVars, werr := e.resolveWorkflow(&step, taskPrefix, &task, inheritedVars)
							if werr != nil {
								return werr
							}
							if werr = e.executeSteps(childRec.Steps, &task, taskPrefix, taskSession, step.Session, agentOverride, childVars); werr != nil {
								return werr
							}
						} else {
							if err := e.executeSteps(subSteps, &task, taskPrefix, taskSession, step.Session, agentOverride, inheritedVars); err != nil {
								return err
							}
						}
					}
					return nil
				}
				return e.executeSteps(subSteps, nil, stepPath, sessionMap, step.Session, agentOverride, inheritedVars)
			}
			if of := step.OnFailureCompat(); of != nil && step.MaxAttempts() > 1 {
				preGroupCommit, _ := PreStepCommit(e.Run.WorktreeDir)
				maxAttempts := step.MaxAttempts()
				expanded := workflow.ExpandStrategy(of.Strategy)
				if len(expanded) == 0 {
					expanded = []workflow.StrategyEntryCompat{{Type: "same", Count: 1}}
				}
				sessionMap := make(map[string]string)
				for groupAttempt := 1; groupAttempt <= maxAttempts; groupAttempt++ {
					var agentOverride string
					var strategyLabel string
					if groupAttempt > 1 {
						retryIndex := groupAttempt - 2
						idx := retryIndex
						if idx >= len(expanded) {
							idx = len(expanded) - 1
						}
						strategy := expanded[idx]
						strategyLabel = strategy.Type
						if strategy.Agent != "" {
							strategyLabel = strategy.Type + ": " + strategy.Agent
							agentOverride = strategy.Agent
						}
						if strategy.Type == "escalate" || strategy.Type == "replan" {
							_ = e.Run.ResetTo(preGroupCommit)
						}
						// WHY: group retry re-runs the subtree; global step attempt counters must not block this fresh pass (unlike restart_from, which preserves the target budget).
						prefix := stepPath + "/"
						for k := range e.globalStepAttempts {
							if k == stepPath || strings.HasPrefix(k, prefix) {
								e.globalStepAttempts[k] = 0
							}
						}
						invalidated := []string{}
						if strategy.Type == "escalate" || strategy.Type == "replan" {
							invalidated = e.State.ClearSessionIDsForGroup(stepPath)
							e.clearStepSessionsForGroup(stepPath)
						}
						if strategy.Type == "escalate" || strategy.Type == "replan" {
							e.State.ResetGroup(stepPath)
						}
						if e.Run.Ledger != nil && len(invalidated) > 0 {
							_ = e.Run.Ledger.Emit(ledger.GroupRetrySessionsReset{
								Group:               stepPath,
								Attempt:             groupAttempt,
								Strategy:            strategy.Type,
								InvalidatedSessions: invalidated,
							})
						}
						fmt.Fprintf(os.Stderr, "[%s]\tgroup-retry\tattempt %d/%d (%s)\n", stepPath, groupAttempt, maxAttempts, strategyLabel)
						if strategy.Type == "escalate" || strategy.Type == "replan" {
							sessionMap = make(map[string]string)
						}
						groupAttemptPath := filepath.Join(e.Run.WorktreeDir, brand.StateDir(), "group-attempt")
						_ = os.MkdirAll(filepath.Dir(groupAttemptPath), 0755)
						_ = os.WriteFile(groupAttemptPath, []byte(fmt.Sprintf("%d", groupAttempt)), 0644)
					}
					prevForce := e.forceSessionReuse
					e.forceSessionReuse = strategyLabel == "same"
					err := runGroupOnce(agentOverride, sessionMap)
					e.forceSessionReuse = prevForce
					if err == nil {
						break
					}
					if groupAttempt == maxAttempts {
						if e.Run.Ledger != nil {
							_ = e.Run.Ledger.Emit(ledger.CircuitBreaker{
								Step: stepPath, Scope: "group", Reason: "group retry exhausted", TotalAttempts: maxAttempts,
							})
						}
						fmt.Fprintf(os.Stderr, "[%s]\tFATAL\tgroup retry exhausted after %d attempts\n", stepPath, maxAttempts)
						return err
					}
				}
			} else {
				if err := runGroupOnce("", lastSessionByAgent); err != nil {
					return err
				}
			}
			continue
		}

		// Validation pure: no agent; run validators on the worktree state (cumulative diff).
		if step.Agent == "" && len(step.Gate) > 0 {
			if err := e.executeValidationWithoutAgent(&step, stepPath, taskContext, parentSession, validationWithoutAgentOpts{
				finalDiffErrLabel: "validation",
			}); err != nil {
				return err
			}
			continue
		}

		// Atomic step with agent. groupAgentOverride applies when we're in a group retry with strategy "escalate".
		if err := e.runAtomicStep(&step, stepPath, taskContext, lastSessionByAgent, parentSession, groupAgentOverride, inheritedVars); err != nil {
			if rf, ok := IsRestartFrom(err); ok {
				targetIdx := findStepIndexByName(steps, rf.TargetName)
				if targetIdx < 0 || targetIdx > i {
					return fmt.Errorf("restart_from: step %q not found or invalid in scope", rf.TargetName)
				}
				targetPath := joinStepPath(pathPrefix, steps[targetIdx].Name)
				targetStep := &steps[targetIdx]
				maxTarget := targetStep.MaxAttempts()
				if maxTarget < 1 {
					maxTarget = 1
				}
				if e.globalStepAttempts[targetPath] >= maxTarget {
					if e.Run.Ledger != nil {
						_ = e.Run.Ledger.Emit(ledger.CircuitBreaker{
							Step: targetPath, Scope: "step", Reason: "restart_from blocked: target global step attempts exhausted", TotalAttempts: e.globalStepAttempts[targetPath],
						})
					}
					fmt.Fprintf(os.Stderr, "        ✗ FATAL: restart_from blocked — target %q exhausted global attempts (%d/%d)\n", rf.TargetName, e.globalStepAttempts[targetPath], maxTarget)
					return fmt.Errorf("step %s: restart_from blocked: target %q exhausted global step attempts (%d/%d)", step.Name, rf.TargetName, e.globalStepAttempts[targetPath], maxTarget)
				}
				if ctx := e.lastValidationErrorContext(); ctx != nil {
					c := *ctx
					e.pendingRestartFrom[targetPath] = &c
				}
				commit, okSnap, err := CommitBeforeLatestStepSnapshot(e.Run.WorktreeDir, rf.TargetName)
				if err != nil {
					return err
				}
				if !okSnap {
					commit = groupBaseCommit
				}
				if err := e.Run.ResetTo(commit); err != nil {
					return err
				}
				if err := gitCleanFD(e.Run.WorktreeDir); err != nil {
					return err
				}
				var paths []string
				for j := targetIdx; j <= i; j++ {
					paths = append(paths, joinStepPath(pathPrefix, steps[j].Name))
				}
				e.State.DeleteStepOutputsForRestart(paths)
				// WHY: do not reset the target step's attempt counter — restarts must not grant a fresh retry budget on that step.
				for j := targetIdx + 1; j < i; j++ {
					e.globalStepAttempts[joinStepPath(pathPrefix, steps[j].Name)] = 0
				}
				e.restartCycle++
				if err := e.writeRestartCycleMarker(); err != nil {
					return err
				}
				i = targetIdx - 1
				continue
			}
			return err
		}
	}
	return nil
}

func (e *Engine) clearStepSessionsForGroup(groupPath string) {
	prefix := strings.TrimSuffix(groupPath, "/") + "/"
	for i := range e.Steps {
		sp := e.Steps[i].StepPath
		if sp == groupPath || strings.HasPrefix(sp, prefix) {
			e.Steps[i].SessionID = ""
		}
	}
}

// goModuleRootBinaryName returns the default `go build` / `go test` binary basename at module root
// (last path segment of the module path, e.g. example.com/smoketest -> smoketest). Such files are not
// source edits and must not trigger blast-radius violations when validators run `go test`.
func goModuleRootBinaryName(worktreeDir string) string {
	data, err := os.ReadFile(filepath.Join(worktreeDir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "module ") {
			mod := strings.TrimSpace(strings.TrimPrefix(line, "module"))
			mod = strings.Trim(mod, `"`)
			if mod == "" {
				return ""
			}
			if i := strings.LastIndex(mod, "/"); i >= 0 {
				return mod[i+1:]
			}
			return mod
		}
	}
	return ""
}

func filterRepoFilesOnly(worktreeDir string, files []string) []string {
	goBin := ""
	if worktreeDir != "" {
		goBin = goModuleRootBinaryName(worktreeDir)
	}
	var out []string
	for _, f := range files {
		norm := filepath.ToSlash(f)
		if strings.HasPrefix(norm, brand.StateDir()+"/") {
			continue
		}
		switch norm {
		case "AGENTS.md", "CLAUDE.md", "GEMINI.md", "QWEN.md":
			continue
		}
		if strings.HasSuffix(norm, ".stub") {
			continue
		}
		// Provider context backups live next to CLAUDE.md; they are Gump metadata, not task edits.
		if strings.HasPrefix(norm, brand.StateDir()+"-original-") {
			continue
		}
		// Go build/test produces a binary named after the module path's last segment (see `go help build`).
		if goBin != "" && (norm == goBin || norm == goBin+".exe" || strings.HasPrefix(norm, goBin+"/") || strings.HasPrefix(norm, goBin+".exe/")) {
			continue
		}
		// Legacy e2e module name (testproject) before go.mod-based detection.
		if norm == "testproject" || strings.HasPrefix(norm, "testproject/") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// checkBlastRadius returns files that are not allowed by any pattern (task.files) and the error message for validation failure.
func checkBlastRadius(filesChanged, allowedPatterns []string) (violators []string, errMsg string) {
	for _, f := range filesChanged {
		norm := filepath.ToSlash(f)
		matched := false
		for _, pat := range allowedPatterns {
			ok, _ := path.Match(filepath.ToSlash(pat), norm)
			if ok {
				matched = true
				break
			}
		}
		if !matched {
			violators = append(violators, f)
		}
	}
	if len(violators) == 0 {
		return nil, ""
	}
	var b strings.Builder
	b.WriteString("blast radius violation: files modified outside task.files scope:\n")
	for _, v := range violators {
		b.WriteString(fmt.Sprintf("  - %s (not in: %v)\n", v, allowedPatterns))
	}
	b.WriteString("Allowed: " + strings.Join(allowedPatterns, ", "))
	return violators, b.String()
}

func blastRadiusWarningMessage(violators, allowedPatterns []string) string {
	var b strings.Builder
	b.WriteString("⚠ blast radius warning: files modified outside task.files scope:\n")
	for _, v := range violators {
		b.WriteString(fmt.Sprintf("  - %s (not in allowed list)\n", v))
	}
	b.WriteString("Allowed: " + strings.Join(allowedPatterns, ", "))
	return b.String()
}

func (e *Engine) writeRestartCycleMarker() error {
	p := filepath.Join(e.Run.WorktreeDir, brand.StateDir(), "restart-cycle")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(fmt.Sprintf("%d", e.restartCycle)), 0644)
}

func (e *Engine) consumePendingRestartFrom(stepPath string) *ErrorContext {
	if e.pendingRestartFrom == nil {
		return nil
	}
	ctx := e.pendingRestartFrom[stepPath]
	delete(e.pendingRestartFrom, stepPath)
	if ctx == nil {
		return nil
	}
	c := *ctx
	return &c
}

func extractPartialUsage(raw []byte) (tokensIn int, tokensOut int, turns int) {
	var base map[string]interface{}
	if json.Unmarshal(raw, &base) != nil {
		return 0, 0, 0
	}
	if t, _ := base["type"].(string); t == "assistant" {
		turns = 1
	}
	if msg, ok := base["message"].(map[string]interface{}); ok {
		if usage, ok := msg["usage"].(map[string]interface{}); ok {
			tokensIn += int(numFromAny(usage["input_tokens"]))
			tokensOut += int(numFromAny(usage["output_tokens"]))
		}
	}
	if usage, ok := base["usage"].(map[string]interface{}); ok {
		tokensIn += int(numFromAny(usage["input_tokens"]))
		tokensOut += int(numFromAny(usage["output_tokens"]))
	}
	if item, ok := base["item"].(map[string]interface{}); ok {
		if usage, ok := item["usage"].(map[string]interface{}); ok {
			tokensIn += int(numFromAny(usage["input_tokens"]))
			tokensOut += int(numFromAny(usage["output_tokens"]))
		}
	}
	return tokensIn, tokensOut, turns
}

// buildVars builds template variables; errorContext and extraVars are optional (retry and replan sub-tasks).

func (e *Engine) resolveSubSteps(step *workflow.Step) ([]workflow.Step, error) {
	if len(step.Steps) > 0 {
		return step.Steps, nil
	}
	if step.Type == "split" && len(step.Each) > 0 {
		return step.Each, nil
	}
	workflowName := strings.TrimSpace(step.Workflow)
	if workflowName == "" {
		return nil, fmt.Errorf("orchestration step has no child steps or workflow reference")
	}
	resolved, err := workflow.Resolve(workflowName, e.Run.RepoRoot)
	if err != nil {
		return nil, err
	}
	recipeDir := ""
	if resolved.Path != "" {
		recipeDir = filepath.Dir(resolved.Path)
	}
	parsed, _, err := workflow.Parse(resolved.Raw, recipeDir)
	if err != nil {
		return nil, err
	}
	if errs := workflow.Validate(parsed); len(errs) > 0 {
		return nil, errs[0]
	}
	return parsed.Steps, nil
}

func (e *Engine) resolveWorkflow(step *workflow.Step, stepPath string, taskContext *plan.Task, inheritedVars map[string]string) (*workflow.Workflow, map[string]string, error) {
	workflowName := strings.TrimSpace(step.Workflow)
	if workflowName == "" {
		return nil, nil, fmt.Errorf("workflow: name is empty")
	}
	resolved, err := workflow.Resolve(workflowName, e.Run.RepoRoot)
	if err != nil {
		return nil, nil, err
	}
	recipeDir := ""
	if resolved.Path != "" {
		recipeDir = filepath.Dir(resolved.Path)
	}
	parsed, _, err := workflow.Parse(resolved.Raw, recipeDir)
	if err != nil {
		return nil, nil, err
	}
	if errs := workflow.Validate(parsed); len(errs) > 0 {
		return nil, nil, errs[0]
	}
	tctx := e.newTemplateCtx(stepPath, step, taskContext, nil, 1, inheritedVars, nil)
	out := map[string]string{}
	for k, raw := range step.With {
		out[k] = template.Resolve(raw, tctx)
	}
	return parsed, out, nil
}

// formatStreamEventToTerminal prints a single line for assistant text, [tool] name, or [result]; best-effort, no error on parse failure.
func formatStreamEventToTerminal(ev agent.StreamEvent, agentName string) {
	handleTurnEvent(ev, agentName)
}

// formatAgentSummary prints the post-agent line per spec: step name, duration, cost, context % when known, turns; warns if context > 80%.
func formatAgentSummary(w io.Writer, stepName, stepPath string, durationSec, costUSD float64, inputTokens, outputTokens, numTurns int, agentName string) {
	durStr := fmt.Sprintf("%.0fs", durationSec)
	if durationSec >= 60 {
		durStr = fmt.Sprintf("%.0fm%02.0fs", durationSec/60, durationSec-60*float64(int(durationSec/60)))
	}
	costStr := fmt.Sprintf("$%.2f", costUSD)
	if costUSD < 0.01 && costUSD > 0 {
		costStr = fmt.Sprintf("$%.4f", costUSD)
	} else if costUSD == 0 {
		costStr = "$0.00"
	}
	ctxStr := ""
	if info := agent.LookupModel(agentName); info != nil && info.ContextWindow > 0 {
		total := inputTokens + outputTokens
		pct := float64(total) / float64(info.ContextWindow) * 100
		ctxStr = fmt.Sprintf(" | %dk/%dk ctx (%.0f%%)", (total+500)/1000, info.ContextWindow/1000, pct)
		if pct > 80 {
			ctxStr += " ⚠ context nearly full"
		}
	} else if inputTokens > 0 || outputTokens > 0 {
		ctxStr = fmt.Sprintf(" | %dk tokens", (inputTokens+outputTokens+500)/1000)
	}
	turnStr := "turns"
	if numTurns == 1 {
		turnStr = "turn"
	}
	fmt.Fprintf(w, "[%s] %s done %s | %s%s | %d %s\n", brand.Lower(), stepName, durStr, costStr, ctxStr, numTurns, turnStr)
}

// printCookTotal prints running cost and step count after each step (Feature 11).
func (e *Engine) printCookTotal() {
	CookTotalLine(e.totalCostUSD, e.stepCompletedCount, e.stepTotalEstimate)
}

// finishCook persists state-bag and final-diff, emits cook_completed, appends to the index, and closes the ledger so the run is fully traceable.
// Always prints the cook footer (Feature 12).
func (e *Engine) finishCook(runErr error) {
	duration := time.Since(e.cookRunStartedAt)
	var fatalStep, errMsg, worktree string
	if runErr != nil {
		for i := len(e.Steps) - 1; i >= 0; i-- {
			if e.Steps[i].Status == StepFatal {
				fatalStep = e.Steps[i].StepPath
				break
			}
		}
		errMsg = runErr.Error()
		worktree = e.Run.WorktreeDir
	}
	CookFooter(runErr == nil, e.totalCostUSD, e.stepCompletedCount, e.stepTotalEstimate, e.retryTriggeredCount, duration, fatalStep, errMsg, worktree)

	if e.Run.Ledger == nil {
		return
	}
	cookDir := e.Run.RunDir
	status := "pass"
	if runErr != nil {
		if errors.Is(runErr, ErrCookAborted) {
			status = "aborted"
		} else {
			status = "fatal"
		}
	}
	durationMs := int(time.Since(e.cookRunStartedAt).Milliseconds())

	stateJSON, _ := e.State.Serialize()
	_ = os.WriteFile(filepath.Join(cookDir, "state.json"), stateJSON, 0644)

	var finalDiffPatch []byte
	dc, err := e.Run.FinalDiff()
	if err == nil && dc != nil {
		finalDiffPatch = []byte(dc.Patch)
	}
	finalDiffRel, _ := e.Run.Ledger.WriteArtifact("final-diff.patch", finalDiffPatch)

	artifacts := map[string]string{"state": "state.json"}
	if finalDiffRel != "" {
		artifacts["final_diff"] = finalDiffRel
	}
	_ = e.Run.Ledger.Emit(ledger.RunCompleted{
		RunID:      e.Run.ID,
		Status:     status,
		DurationMs: durationMs,
		TotalCost:  e.totalCostUSD,
		Artifacts:  artifacts,
	})

	agentsList := make([]string, 0, len(e.agentsUsed))
	for a := range e.agentsUsed {
		agentsList = append(agentsList, a)
	}
	_ = ledger.AppendIndex(e.Run.RepoRoot, ledger.IndexEntry{
		CookID:     e.Run.ID,
		Timestamp:  e.cookRunStartedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		Recipe:     e.Run.WorkflowName,
		Spec:       filepath.Base(e.Run.SpecPath),
		Status:     status,
		DurationMs: durationMs,
		CostUSD:    e.totalCostUSD,
		Steps:      e.stepCompletedCount,
		Retries:    e.retryTriggeredCount,
		Agents:     agentsList,
	})
	_ = e.Run.Ledger.Close()
}
