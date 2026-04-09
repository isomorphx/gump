package engine

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/isomorphx/gump/internal/agent"
	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/config"
	pkgcontext "github.com/isomorphx/gump/internal/context"
	"github.com/isomorphx/gump/internal/cook"
	"github.com/isomorphx/gump/internal/diff"
	"github.com/isomorphx/gump/internal/ledger"
	"github.com/isomorphx/gump/internal/plan"
	"github.com/isomorphx/gump/internal/workflow"
	"github.com/isomorphx/gump/internal/sandbox"
	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/template"
	"github.com/isomorphx/gump/internal/validate"
)

// ErrCookAborted is returned when the user aborts during HITL (Ctrl+C).
var ErrCookAborted = errors.New("aborted")

// lastStepOutcome holds the result of the last runAtomicStepOnce so we emit step_completed only once after the retry loop (not per attempt).
type lastStepOutcome struct {
	stepPath      string
	attempt       int
	status        string
	durationMs    int
	patch         string
	outputMode    string
	outputValue   string
	hasValidation bool
}

// Engine runs the recipe step-by-step; behavior is inferred from fields (no type:) so recipes stay declarative. Flat State holds outputs for v0.0.4 `{step.field}` templates.
// AgentsCLI is set by the CLI so cook_started can record agent versions; stepCompletedCount, retryTriggeredCount, totalCostUSD, agentsUsed are accumulated for cook_completed and index.
type Engine struct {
	Cook                 *cook.Cook
	Recipe               *workflow.Workflow
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

// New builds an engine with empty workflow State and counters for ledger/index.
func New(c *cook.Cook, rec *workflow.Workflow, r agent.AdapterResolver, cfg *config.Config, specContent string) *Engine {
	return &Engine{
		Cook: c, Recipe: rec, Resolver: r, Config: cfg, SpecContent: specContent,
		Steps: nil, State: state.New(),
		agentsUsed:         make(map[string]struct{}),
		globalStepAttempts: make(map[string]int),
		budgetTracker:      NewBudgetTracker(rec.MaxBudget),
		pendingRestartFrom: make(map[string]*ErrorContext),
	}
}

// Run executes the workflow; returns nil if all steps pass, or an error when a step is fatal.
// We always run the end-of-run sequence (state-bag, final-diff, run_completed, index, close ledger) so the ledger is complete even on fatal.
func (e *Engine) Run() error {
	defer agent.RestoreAllContextFiles(e.Cook.WorktreeDir)
	e.cookRunStartedAt = time.Now()
	// WHY: resume re-uses the worktree; a stale engine-step-attempt.json makes the stub pick the wrong by_attempt key.
	if e.ResumePassedSteps != nil && len(e.ResumePassedSteps) > 0 {
		_ = os.Remove(filepath.Join(e.Cook.WorktreeDir, brand.StateDir(), "engine-step-attempt.json"))
	}
	if e.Cook.Ledger != nil {
		_ = e.Cook.Ledger.Emit(ledger.RunStarted{
			RunID:     e.Cook.ID,
			Workflow:  e.Cook.RecipeName,
			Spec:      filepath.Base(e.Cook.SpecPath),
			Commit:    e.Cook.InitialCommit,
			Branch:    e.Cook.OrigBranch,
			AgentsCLI: e.AgentsCLI,
			MaxBudget: e.Recipe.MaxBudget,
		})
		if e.FromStep != "" && e.replayOriginalCookID != "" {
			_ = e.Cook.Ledger.Emit(ledger.ReplayStarted{
				OriginalCookID: e.replayOriginalCookID,
				FromStep:       e.FromStep,
				RestoredCommit: e.replayRestoredCommit,
			})
		}
		if e.FromStep != "" && e.ResumePreviousStatus != "" {
			_ = e.Cook.Ledger.Emit(ledger.RunResumed{
				RunID:          e.Cook.ID,
				ResumedFrom:    e.FromStep,
				PreviousStatus: e.ResumePreviousStatus,
			})
		}
	}
	CookHeader(e.Cook.RecipeName, e.Cook.ID, filepath.Base(e.Cook.SpecPath))
	err := e.executeSteps(e.Recipe.Steps, nil, "", make(map[string]string), workflow.SessionConfig{Mode: "fresh"}, "", nil)
	e.finishCook(err)
	return err
}

// executeSteps runs steps; composite (steps:) with optional foreach, atomic (agent:), or validation-only (gate:).
// groupAgentOverride is set when we're inside a group retry with strategy "escalate"; atomic steps in this subtree use that agent.
func (e *Engine) executeSteps(steps []workflow.Step, taskContext *plan.Task, pathPrefix string, lastSessionByAgent map[string]string, parentSession workflow.SessionConfig, groupAgentOverride string, inheritedVars map[string]string) error {
	groupBaseCommit, _ := PreStepCommit(e.Cook.WorktreeDir)
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
			if e.Cook.Ledger != nil {
				_ = e.Cook.Ledger.Emit(ledger.GroupStarted{Step: stepPath, Foreach: foreachRef, Parallel: step.Parallel, TaskCount: taskCount})
			}
			if taskCount > 0 {
				e.stepTotalEstimate = e.stepRunIndex + taskCount*len(subSteps)
			} else {
				e.stepTotalEstimate = e.stepRunIndex + len(subSteps)
			}
			groupStart := time.Now()
			groupAttempts := 0
			runGroupOnce := func(agentOverride string, sessionMap map[string]string) error {
				if step.Parallel {
					// So each branch gets a worktree cloned from current main state (same commit for all, deterministic merge order).
					baseCommit, err := PreStepCommit(e.Cook.WorktreeDir)
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
				preGroupCommit, _ := PreStepCommit(e.Cook.WorktreeDir)
				maxAttempts := step.MaxAttempts()
				expanded := workflow.ExpandStrategy(of.Strategy)
				if len(expanded) == 0 {
					expanded = []workflow.StrategyEntryCompat{{Type: "same", Count: 1}}
				}
				sessionMap := make(map[string]string)
				for groupAttempt := 1; groupAttempt <= maxAttempts; groupAttempt++ {
					groupAttempts++
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
							_ = e.Cook.ResetTo(preGroupCommit)
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
						keys := []string{}
						if strategy.Type == "escalate" || strategy.Type == "replan" {
							keys = e.State.ResetGroup(stepPath)
						}
						if e.Cook.Ledger != nil {
							if len(keys) > 0 {
								_ = e.Cook.Ledger.Emit(ledger.StateBagScopeReset{Group: stepPath, Keys: keys})
							}
							_ = e.Cook.Ledger.Emit(ledger.GroupRetry{Step: stepPath, Attempt: groupAttempt, Strategy: strategyLabel})
							if len(invalidated) > 0 {
								_ = e.Cook.Ledger.Emit(ledger.GroupRetrySessionsReset{
									Group:               stepPath,
									Attempt:             groupAttempt,
									Strategy:            strategy.Type,
									InvalidatedSessions: invalidated,
								})
							}
						}
						fmt.Fprintf(os.Stderr, "[%s]\tgroup-retry\tattempt %d/%d (%s)\n", stepPath, groupAttempt, maxAttempts, strategyLabel)
						if strategy.Type == "escalate" || strategy.Type == "replan" {
							sessionMap = make(map[string]string)
						}
						groupAttemptPath := filepath.Join(e.Cook.WorktreeDir, brand.StateDir(), "group-attempt")
						_ = os.MkdirAll(filepath.Dir(groupAttemptPath), 0755)
						_ = os.WriteFile(groupAttemptPath, []byte(fmt.Sprintf("%d", groupAttempt)), 0644)
					}
					prevForce := e.forceSessionReuse
					e.forceSessionReuse = strategyLabel == "same"
					err := runGroupOnce(agentOverride, sessionMap)
					e.forceSessionReuse = prevForce
					if err == nil {
						if e.Cook.Ledger != nil {
							iterations := taskCount
							if iterations == 0 {
								iterations = 1
							}
							_ = e.Cook.Ledger.Emit(ledger.GroupCompleted{
								Step: stepPath, Status: "pass", Iterations: iterations, Attempts: groupAttempts,
								DurationMs: int(time.Since(groupStart).Milliseconds()),
							})
						}
						break
					}
					if groupAttempt == maxAttempts {
						if e.Cook.Ledger != nil {
							_ = e.Cook.Ledger.Emit(ledger.CircuitBreaker{
								Step: stepPath, Scope: "group", Reason: "group retry exhausted", TotalAttempts: maxAttempts,
							})
							iterations := taskCount
							if iterations == 0 {
								iterations = 1
							}
							_ = e.Cook.Ledger.Emit(ledger.GroupCompleted{
								Step: stepPath, Status: "fatal", Iterations: iterations, Attempts: groupAttempts,
								DurationMs: int(time.Since(groupStart).Milliseconds()),
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
				if e.Cook.Ledger != nil {
					iterations := taskCount
					if iterations == 0 {
						iterations = 1
					}
					_ = e.Cook.Ledger.Emit(ledger.GroupCompleted{
						Step: stepPath, Status: "pass", Iterations: iterations, Attempts: 1,
						DurationMs: int(time.Since(groupStart).Milliseconds()),
					})
				}
			}
			continue
		}

		// Validation pure: no agent; run validators on the worktree state (cumulative diff).
		if step.Agent == "" && len(step.Gate) > 0 {
			e.stepRunIndex++
			taskInfo := ""
			if taskContext != nil && taskContext.Name != "" {
				taskInfo = "[task " + taskContext.Name + "]"
			}
			StepHeader(e.stepRunIndex, e.stepTotalEstimate, stepPath, "validate", taskInfo, "", "")
			startedAt := time.Now()
			if e.Cook.Ledger != nil {
				checks := make([]string, 0, len(step.Gate))
				for _, v := range step.Gate {
					if v.Arg != "" {
						checks = append(checks, v.Type+":"+v.Arg)
					} else {
						checks = append(checks, v.Type)
					}
				}
				_ = e.Cook.Ledger.Emit(ledger.StepStarted{Step: stepPath, Agent: "", OutputMode: "", Item: taskContextName(taskContext), Attempt: 1, SessionMode: sessionModeForLedger(&step, parentSession)})
				_ = e.Cook.Ledger.Emit(ledger.GateStarted{Step: stepPath, Checks: checks})
			}
			dc, err := e.Cook.FinalDiff()
			if err != nil {
				e.Steps = append(e.Steps, StepExecution{StepPath: stepPath, StepName: step.Name, TaskName: taskContextName(taskContext), Attempt: 1, Status: StepFatal, StartedAt: startedAt, FinishedAt: time.Now()})
				if e.Cook.Ledger != nil {
					e.emitStepCompleted(stepPath, 1, "fatal", int(time.Since(startedAt).Milliseconds()), nil, "", "", false)
					e.stepCompletedCount++
					e.printCookTotal()
				}
				return fmt.Errorf("final diff for validation step: %w", err)
			}
			vr := validate.RunValidators(step.Gate, e.Config, e.Cook.WorktreeDir, dc, e.State, stepPath)
			writeValidationArtifact(e.Cook.CookDir, stepPath, 1, vr)
			validationArtifactRel := filepath.Join("artifacts", ledger.ArtifactName(stepPath, 1, "validation", "json"))
			if vr.Pass {
				if e.Cook.Ledger != nil {
					_ = e.Cook.Ledger.Emit(ledger.GatePassed{Step: stepPath, Artifact: validationArtifactRel})
					artifacts := map[string]string{"validation": validationArtifactRel}
					commit, _ := sandbox.HeadCommit(e.Cook.WorktreeDir)
					_ = e.Cook.Ledger.Emit(ledger.StepCompleted{Step: stepPath, Status: "pass", DurationMs: int(time.Since(startedAt).Milliseconds()), Artifacts: artifacts, Commit: commit})
					e.stepCompletedCount++
				}
				e.printCookTotal()
				e.Steps = append(e.Steps, StepExecution{StepPath: stepPath, StepName: step.Name, TaskName: taskContextName(taskContext), Attempt: 1, Status: StepPass, StartedAt: startedAt, FinishedAt: time.Now()})
				_, nSkipped := countValidationPassedSkipped(vr)
				var passed, skipped []string
				for _, r := range vr.Results {
					if r.Skipped {
						skipped = append(skipped, r.Validator)
					} else if r.Pass {
						passed = append(passed, r.Validator)
					}
				}
				ValidationSummaryLine(passed, skipped)
				fmt.Fprintf(os.Stderr, "[%s]\tpass\t%s\n", stepPath, formatValidationPassSummary(len(passed), nSkipped))
				if nSkipped > 0 {
					formatValidationDetails(os.Stderr, stepPath, vr)
				}
				continue
			}
			var failedNames []string
			var errParts []string
			for _, r := range vr.Results {
				if !r.Pass {
					failedNames = append(failedNames, r.Validator)
					errParts = append(errParts, r.Stderr)
				}
			}
			if e.Cook.Ledger != nil {
				reason := ""
				for _, r := range vr.Results {
					if !r.Pass && r.Stderr != "" {
						firstLine := strings.TrimSpace(strings.Split(r.Stderr, "\n")[0])
						if len(firstLine) > 200 {
							firstLine = firstLine[:200]
						}
						reason = firstLine
						break
					}
				}
				_ = e.Cook.Ledger.Emit(ledger.GateFailed{Step: stepPath, Reason: reason, Artifact: validationArtifactRel})
				artifacts := map[string]string{"validation": validationArtifactRel}
				commit, _ := sandbox.HeadCommit(e.Cook.WorktreeDir)
				_ = e.Cook.Ledger.Emit(ledger.StepCompleted{Step: stepPath, Status: "fatal", DurationMs: int(time.Since(startedAt).Milliseconds()), Artifacts: artifacts, Commit: commit})
				e.stepCompletedCount++
				e.printCookTotal()
			}
			exec := StepExecution{StepPath: stepPath, StepName: step.Name, TaskName: taskContextName(taskContext), Attempt: 1, Status: StepFatal, StartedAt: startedAt, FinishedAt: time.Now(), ValidateError: strings.Join(errParts, "\n---\n"), ValidateDiff: dc.Patch}
			e.Steps = append(e.Steps, exec)
			formatValidationDetails(os.Stderr, stepPath, vr)
			fmt.Fprintf(os.Stderr, "        ✗ validation failed: %s\n", strings.Join(failedNames, ", "))
			return fmt.Errorf("step %s: validation failed: %s", step.Name, strings.Join(failedNames, ", "))
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
					if e.Cook.Ledger != nil {
						_ = e.Cook.Ledger.Emit(ledger.CircuitBreaker{
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
				commit, okSnap, err := CommitBeforeLatestStepSnapshot(e.Cook.WorktreeDir, rf.TargetName)
				if err != nil {
					return err
				}
				if !okSnap {
					commit = groupBaseCommit
				}
				if err := e.Cook.ResetTo(commit); err != nil {
					return err
				}
				if err := gitCleanFD(e.Cook.WorktreeDir); err != nil {
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

func taskContextName(t *plan.Task) string {
	if t == nil {
		return ""
	}
	return t.Name
}

// sessionModeForLedger records the effective session strategy for white-glass tracing; v4 defaults to fresh when unset.
func sessionModeForLedger(step *workflow.Step, parentSession workflow.SessionConfig) string {
	eff := workflow.ResolveSessionForEngine(step, parentSession)
	return eff.Mode
}

func (e *Engine) runAtomicStep(step *workflow.Step, stepPath string, taskContext *plan.Task, lastSessionByAgent map[string]string, parentSession workflow.SessionConfig, groupAgentOverride string, inheritedVars map[string]string) error {
	if step.ShouldRunWithRetryLoop() {
		return e.RunWithRetry(step, stepPath, taskContext, func(attempt int, agentOverride string, errorContext *ErrorContext) (err error, preStepCommit string) {
			override := agentOverride
			if override == "" && groupAgentOverride != "" {
				override = groupAgentOverride
			}
			if attempt == 1 && errorContext == nil {
				if c := e.consumePendingRestartFrom(stepPath); c != nil {
					c.FromRestart = true
					errorContext = c
				}
			}
			return e.runAtomicStepOnce(step, stepPath, taskContext, lastSessionByAgent, parentSession, attempt, override, errorContext, nil, inheritedVars)
		})
	}
	override := groupAgentOverride
	var errCtx *ErrorContext
	if c := e.consumePendingRestartFrom(stepPath); c != nil {
		c.FromRestart = true
		errCtx = c
	}
	err, _ := e.runAtomicStepOnce(step, stepPath, taskContext, lastSessionByAgent, parentSession, 1, override, errCtx, nil, inheritedVars)
	e.emitStepCompletedFromLast()
	return err
}

// runAtomicStepOnce runs one attempt of an atomic step; returns (err, preStepCommit) so retry can reset. preStepCommit is captured at start.
func (e *Engine) runAtomicStepOnce(step *workflow.Step, stepPath string, taskContext *plan.Task, lastSessionByAgent map[string]string, parentSession workflow.SessionConfig, attempt int, agentOverride string, errorContext *ErrorContext, extraVars map[string]string, inheritedVars map[string]string) (err error, preStepCommit string) {
	e.lastFailureSource = ""
	preStepCommit, _ = PreStepCommit(e.Cook.WorktreeDir)
	maxGlobal := step.MaxAttempts()
	if maxGlobal < 1 {
		maxGlobal = 1
	}
	cur := e.globalStepAttempts[stepPath]
	if cur >= maxGlobal {
		if e.Cook.Ledger != nil {
			_ = e.Cook.Ledger.Emit(ledger.CircuitBreaker{
				Step: stepPath, Scope: "step", Reason: "global step attempts exhausted before agent run", TotalAttempts: cur,
			})
		}
		fmt.Fprintf(os.Stderr, "        ✗ FATAL: global step attempts exhausted for %s (%d/%d)\n", stepPath, cur, maxGlobal)
		return fmt.Errorf("step %s: all %d attempts exhausted", step.Name, maxGlobal), preStepCommit
	}
	e.globalStepAttempts[stepPath]++
	e.writeEngineStepAttemptMarker(step.Name, e.globalStepAttempts[stepPath])
	if attempt > 1 {
		e.State.RotatePrev(stepPath)
	}
	taskName := taskContextName(taskContext)
	tctx := e.newTemplateCtx(stepPath, taskContext, errorContext, attempt, inheritedVars, extraVars)
	outputMode := step.OutputMode()
	prompt := step.EffectivePromptAt(attempt)
	resolvedPrompt := template.Resolve(prompt, tctx)

	effectiveSession := workflow.ResolveSessionForEngine(step, parentSession)
	sessionReuse := attempt == 1 && (effectiveSession.Mode == "reuse" || effectiveSession.Mode == "reuse-targeted")

	var taskFiles []string
	if taskContext != nil && len(taskContext.Files) > 0 {
		taskFiles = taskContext.Files
	}
	agentToUse := step.EffectiveAgentAt(attempt, step.Agent)
	if e.CookAgentOverride != "" {
		agentToUse = e.CookAgentOverride
	}
	if agentOverride != "" {
		agentToUse = agentOverride
	}
	agentToUse = strings.TrimSpace(agentToUse)
	if agentToUse != "pass" && agentToUse != "" {
		agentToUse = strings.TrimSpace(template.Resolve(agentToUse, tctx))
	}
	if agentToUse == "pass" || strings.TrimSpace(agentToUse) == "pass" {
		agentToUse = "pass"
	}
	var adapter agent.AgentAdapter
	if agentToUse != "pass" {
		var adaptErr error
		adapter, adaptErr = e.Resolver.AdapterFor(agentToUse)
		if adaptErr != nil {
			return adaptErr, preStepCommit
		}
	}
	agentForCtx := agentToUse
	if agentForCtx == "pass" {
		agentForCtx = "claude-sonnet"
	}
	contextFile := agent.ContextFileForAgent(agentForCtx)
	agent.RemoveOtherContextFiles(e.Cook.WorktreeDir, contextFile)
	if err := PrepareOutputDir(e.Cook.WorktreeDir); err != nil {
		return fmt.Errorf("prepare output dir: %w", err), preStepCommit
	}
	var retryCtx *pkgcontext.RetryContext
	if errorContext != nil && step.OnFailureCompat() != nil && (attempt > 1 || errorContext.FromRestart) {
		ra := attempt
		maxA := step.MaxAttempts()
		if errorContext.FromRestart && attempt == 1 {
			// WHY: after restart_from the stub and templates see a follow-up attempt, not a blind first run.
			ra = 2
			if maxA < 2 {
				maxA = 2
			}
		}
		promptOverridden := false
		for _, r := range step.Retry {
			if r.Exit > 0 {
				continue
			}
			if r.Attempt > 0 && ra >= r.Attempt && strings.TrimSpace(r.Prompt) != "" {
				promptOverridden = true
				break
			}
		}
		escFrom, escTo := "", ""
		if agentOverride != "" {
			escTo = agentOverride
			escFrom = step.Agent
		}
		retryCtx = &pkgcontext.RetryContext{
			Attempt:            ra,
			MaxAttempts:        maxA,
			Diff:               errorContext.Diff,
			Error:              errorContext.Error,
			IsPromptOverridden: promptOverridden,
			EscalatedFrom:      escFrom,
			EscalatedTo:        escTo,
			ReviewComment:      errorContext.ReviewComment,
		}
	}
	var buildOpts *pkgcontext.BuildOptions
	if sessionReuse {
		buildOpts = &pkgcontext.BuildOptions{SessionReuse: true}
	}
	if err := pkgcontext.Build(engineOutputToStepType(outputMode), resolvedPrompt, step.Context, agentForCtx, e.Config, e.Cook.WorktreeDir, e.SpecContent, taskFiles, contextFile, retryCtx, buildOpts); err != nil {
		return fmt.Errorf("write context: %w", err), preStepCommit
	}

	timeout := time.Duration(0)
	if step.Guard.MaxTime != "" {
		var parseErr error
		timeout, parseErr = time.ParseDuration(step.Guard.MaxTime)
		if parseErr != nil {
			return fmt.Errorf("step %s: invalid guard max_time %q: %w", step.Name, step.Guard.MaxTime, parseErr), preStepCommit
		}
	}

	sessionID := ""
	if e.forceSessionReuse {
		if sid := e.lastSessionIDForStep(stepPath); sid != "" {
			sessionID = sid
		}
		if sessionID == "" && lastSessionByAgent != nil {
			if sid, ok := lastSessionByAgent[agent.AgentPrefix(agentToUse)]; ok {
				sessionID = sid
			}
		}
		if sessionID == "" {
			sessionID = e.State.Get(stepPath + ".session_id")
		}
	}
	if e.forceSessionReuse && sessionID != "" {
		// retry same policy: force session reuse for this step/group attempt.
	} else {
	switch effectiveSession.Mode {
	case "fresh":
		sessionID = ""
	case "reuse-on-retry":
		if attempt > 1 {
			sessionID = e.lastSessionIDForStep(stepPath)
		} else if sid := e.State.PrevSessionID(stepPath); sid != "" {
			// WHY: after restart_from, the prior session id lives in prev so attempt 1 can resume the review thread.
			sessionID = sid
		}
	case "reuse":
		if attempt == 1 && lastSessionByAgent != nil {
			if sid, ok := lastSessionByAgent[agent.AgentPrefix(agentToUse)]; ok {
				sessionID = sid
			}
			if sessionID == "" {
				// WHY: resume runs skip previously completed steps, so the in-memory map is empty.
				sessionID = e.State.Get(stepPath + ".session_id")
			}
		}
	case "reuse-targeted":
		if attempt == 1 {
			sessionID = e.resolveTargetedSession(effectiveSession.Target, agentToUse)
			if sessionID == "" {
				fmt.Fprintf(os.Stderr, "[%s] session: reuse: %s — target step not found or different agent, using fresh session\n", brand.Lower(), effectiveSession.Target)
			}
		}
	}
	}

	promptForAgent := resolvedPrompt
	if outputMode == "plan" {
		promptForAgent += "\n[" + brand.Upper() + ":plan]"
	} else if outputMode == "artifact" {
		promptForAgent += "\n[" + brand.Upper() + ":artifact]"
	} else if outputMode == "review" {
		promptForAgent += "\n[" + brand.Upper() + ":review]"
	}
	promptForAgent += "\n[" + brand.Upper() + ":step:" + step.Name + "]"
	if timeout > 0 {
		promptForAgent += "\n[" + brand.Upper() + ":timeout]"
	}

	strategyLabel := ""
	if errorContext != nil {
		strategyLabel = errorContext.Strategy
		if errorContext.FromRestart && strategyLabel == "" {
			strategyLabel = "restart_from"
		}
	}
	exec := StepExecution{
		StepPath:      stepPath,
		StepName:      step.Name,
		OutputMode:    outputMode,
		TaskName:      taskName,
		Attempt:       attempt,
		RetryStrategy: strategyLabel,
		Status:        StepRunning,
		Agent:         agentToUse,
		StartedAt:     time.Now(),
	}
	if e.Cook.Ledger != nil {
		_ = e.Cook.Ledger.Emit(ledger.StepStarted{
			Step: stepPath, Agent: agentToUse, OutputMode: outputMode, Item: taskName, Attempt: attempt,
			SessionMode: sessionModeForLedger(step, parentSession),
		})
		if agentToUse != "" {
			e.agentsUsed[agentToUse] = struct{}{}
		}
	}
	e.stepRunIndex++
	taskInfo := ""
	if taskContext != nil && taskContext.Name != "" {
		taskInfo = "[task " + taskContext.Name + "]"
	}
	retryInfo := ""
	if step.OnFailureCompat() != nil && (attempt > 1 || (errorContext != nil && errorContext.FromRestart)) {
		shAttempt := attempt
		shMax := step.MaxAttempts()
		if errorContext != nil && errorContext.FromRestart && attempt == 1 {
			shAttempt = 2
			if shMax < 2 {
				shMax = 2
			}
		}
		retryInfo = fmt.Sprintf("[retry %d/%d (%s)]", shAttempt, shMax, strategyLabel)
	}
	sessionInfo := ""
	if sessionID != "" {
		sessionInfo = "[session: reuse]"
	}
	StepHeader(e.stepRunIndex, e.stepTotalEstimate, stepPath, agentToUse, taskInfo, retryInfo, sessionInfo)

	maxTurns := step.Guard.MaxTurns
	var proc *agent.Process
	var result *agent.RunResult
	var guardRuntime *GuardRuntime
	guardTriggered := false
	guardName := ""
	guardReason := ""
	if agentToUse != "pass" {
		promptHash := sha256.Sum256([]byte(promptForAgent))
		ctx := context.Background()
		if sessionID != "" {
			var err error
			proc, err = adapter.Resume(ctx, agent.ResumeRequest{
				Worktree:  e.Cook.WorktreeDir,
				Prompt:    promptForAgent,
				AgentName: agentToUse,
				SessionID: sessionID,
				Timeout:   timeout,
				MaxTurns:  maxTurns,
			})
			if err != nil {
				exec.Status = StepFatal
				exec.FinishedAt = time.Now()
				e.Steps = append(e.Steps, exec)
				e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
				fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, err)
				return fmt.Errorf("step %s: %w", step.Name, err), preStepCommit
			}
		} else {
			var err error
			proc, err = adapter.Launch(ctx, agent.LaunchRequest{
				Worktree:  e.Cook.WorktreeDir,
				Prompt:    promptForAgent,
				AgentName: agentToUse,
				Timeout:   timeout,
				MaxTurns:  maxTurns,
			})
			if err != nil {
				exec.Status = StepFatal
				exec.FinishedAt = time.Now()
				e.Steps = append(e.Steps, exec)
				e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
				fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, err)
				return fmt.Errorf("step %s: %w", step.Name, err), preStepCommit
			}
		}
		sessionSource := ""
		if effectiveSession.Mode == "reuse-on-retry" && sessionID != "" {
			sessionSource = "reuse-on-retry"
		}
		if e.Cook.Ledger != nil {
			_ = e.Cook.Ledger.Emit(ledger.AgentLaunched{
				Step: stepPath, CLI: adapter.LastLaunchCLI(), Worktree: e.Cook.WorktreeDir,
				Agent: agentToUse, SessionID: sessionID, SessionSource: sessionSource,
				PromptHash: hex.EncodeToString(promptHash[:]),
			})
		}
		guardRuntime = NewGuardRuntime(step)
		for ev := range adapter.Stream(proc) {
			inDelta, outDelta, turnDelta := extractPartialUsage(ev.Raw)
			proc.AddPartialMetrics(agent.RunResult{
				InputTokens:  inDelta,
				OutputTokens: outDelta,
				NumTurns:     turnDelta,
			})
			if g, r, ok := guardRuntime.CheckEvent(ev.Raw); ok {
				guardTriggered = true
				guardName = g
				guardReason = r
				agent.Terminate(proc)
			}
			formatStreamEventToTerminal(ev, agentToUse)
		}
		var err error
		result, err = adapter.Wait(proc)
		if err != nil {
			exec.Status = StepFatal
			exec.FinishedAt = time.Now()
			e.Steps = append(e.Steps, exec)
			e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
			fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, err)
			return fmt.Errorf("step %s: %w", step.Name, err), preStepCommit
		}
	} else {
		result = &agent.RunResult{Result: "[agent pass]"}
	}
	if guardRuntime != nil {
		guardRuntime.AddCost(result.CostUSD)
		if !guardTriggered {
			if g, r, ok := guardRuntime.CheckBudget(); ok {
				guardTriggered = true
				guardName = g
				guardReason = r
			}
		}
	}
	runUsageApplied := false
	applyRunUsage := func(cost float64, tokensIn int, tokensOut int, turns int, cacheRead int, cacheCreate int, durationMs int) {
		if runUsageApplied {
			return
		}
		runUsageApplied = true
		e.totalCostUSD += cost
		e.State.AddRunCost(cost)
		e.State.IncrementRunTokensIn(tokensIn)
		e.State.IncrementRunTokensOut(tokensOut)
		e.State.UpdateStepAgentMetrics(stepPath, durationMs, cost, turns, tokensIn, tokensOut, cacheRead, cacheCreate)
		e.State.Set(stepPath+".agent", agentToUse)
	}
	if guardRuntime != nil {
		guardRuntime.AddCost(result.CostUSD)
		if !guardTriggered {
			if g, r, ok := guardRuntime.CheckBudget(); ok {
				guardTriggered = true
				guardName = g
				guardReason = r
			}
		}
	}
	if guardTriggered {
		partial := proc.PartialMetrics()
		_ = e.Cook.ResetTo(preStepCommit)
		applyRunUsage(partial.CostUSD, partial.InputTokens, partial.OutputTokens, partial.NumTurns, partial.CacheReadTokens, partial.CacheCreationTokens, int(time.Since(exec.StartedAt).Milliseconds()))
		exec.Status = StepFatal
		exec.ValidateError = fmt.Sprintf("guard %s triggered: %s", guardName, guardReason)
		exec.ValidateDiff = ""
		exec.FinishedAt = time.Now()
		interruptActiveTurn()
		e.Steps = append(e.Steps, exec)
		if e.Cook.Ledger != nil {
			_ = e.Cook.Ledger.Emit(ledger.GuardTriggered{Step: stepPath, Guard: guardName, Reason: guardReason})
			_ = e.Cook.Ledger.Emit(ledger.AgentKilled{
				Step:         stepPath,
				Reason:       "guard:" + guardName,
				DurationMs:   int(time.Since(exec.StartedAt).Milliseconds()),
				InputTokens:  partial.InputTokens,
				OutputTokens: partial.OutputTokens,
				CostUSD:      partial.CostUSD,
				TurnsPartial: partial.NumTurns,
			})
		}
		e.setLastStepOutcome(stepPath, attempt, "guard_failed", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
		e.lastFailureSource = "guard_fail"
		return fmt.Errorf("step %s: guard %s triggered: %s", step.Name, guardName, guardReason), preStepCommit
	}
	isTimeoutError := result.IsError && (result.ExitCode == -1 || (proc != nil && proc.TimedOut))
	if isTimeoutError && proc != nil {
		partial := proc.PartialMetrics()
		if partial.InputTokens == 0 && partial.OutputTokens == 0 && partial.NumTurns == 0 && partial.CostUSD == 0 {
			partial = *result
		}
		applyRunUsage(partial.CostUSD, partial.InputTokens, partial.OutputTokens, partial.NumTurns, partial.CacheReadTokens, partial.CacheCreationTokens, int(time.Since(exec.StartedAt).Milliseconds()))
		if e.Cook.Ledger != nil {
			_ = e.Cook.Ledger.Emit(ledger.AgentKilled{
				Step:         stepPath,
				Reason:       "timeout",
				DurationMs:   int(time.Since(exec.StartedAt).Milliseconds()),
				InputTokens:  partial.InputTokens,
				OutputTokens: partial.OutputTokens,
				CostUSD:      partial.CostUSD,
				TurnsPartial: partial.NumTurns,
			})
		}
		exec.Status = StepFatal
		exec.FinishedAt = time.Now()
		interruptActiveTurn()
		e.Steps = append(e.Steps, exec)
		e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
		fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: timeout\n", stepPath)
		return fmt.Errorf("step %s: timeout", step.Name), preStepCommit
	}
	if e.budgetTracker != nil {
		if w := e.budgetTracker.WarningIfUnavailable(result.CostUSD); w != "" && !e.budgetWarnOnce {
			e.budgetWarnOnce = true
			fmt.Fprintln(os.Stderr, w)
		}
		if err := e.budgetTracker.AddCost(stepPath, result.CostUSD); err != nil {
			var be *BudgetExceededError
			if errors.As(err, &be) && e.Cook.Ledger != nil {
				_ = e.Cook.Ledger.Emit(be.Event)
			}
			exec.Status = StepFatal
			exec.FinishedAt = time.Now()
			e.Steps = append(e.Steps, exec)
			e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
			fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, err)
			if e.Cook.Ledger != nil {
				_ = e.Cook.Ledger.Emit(ledger.CircuitBreaker{Step: stepPath, Scope: "step", Reason: err.Error(), TotalAttempts: attempt})
			}
			return fmt.Errorf("step %s: %w", step.Name, err), preStepCommit
		}
	}
	// WHY: apply agent usage exactly once per attempt so normal/timeout/guard
	// paths cannot double-count run cost/tokens.
	applyRunUsage(result.CostUSD, result.InputTokens, result.OutputTokens, result.NumTurns, result.CacheReadTokens, result.CacheCreationTokens, result.DurationMs)
	AgentResultText(result.Result)
	ctxStr := ""
	if info := agent.LookupModel(agentToUse); info != nil && info.ContextWindow > 0 {
		total := result.InputTokens + result.OutputTokens
		pct := float64(total) / float64(info.ContextWindow) * 100
		ctxStr = fmt.Sprintf(" | %dk/%dk ctx (%.0f%%)", (total+500)/1000, info.ContextWindow/1000, pct)
		if pct > 80 {
			ctxStr += " ⚠ context nearly full"
		}
	} else if result.InputTokens > 0 || result.OutputTokens > 0 {
		ctxStr = fmt.Sprintf(" | %dk tokens", (result.InputTokens+result.OutputTokens+500)/1000)
	}
	AgentSummaryLine(step.Name, float64(result.DurationMs)/1000, result.CostUSD, ctxStr, result.NumTurns)
	agentArtifacts := make(map[string]string)
	if e.Cook.Ledger != nil && proc != nil && !isTimeoutError {
		stdoutName := ledger.ArtifactName(stepPath, attempt, "stdout", "log")
		stderrName := ledger.ArtifactName(stepPath, attempt, "stderr", "log")
		if b, _ := os.ReadFile(proc.StdoutFile); len(b) > 0 {
			if rel, _ := e.Cook.Ledger.WriteArtifact(stdoutName, b); rel != "" {
				agentArtifacts["stdout"] = rel
			}
		}
		if b, _ := os.ReadFile(proc.StderrFile); len(b) > 0 {
			if rel, _ := e.Cook.Ledger.WriteArtifact(stderrName, b); rel != "" {
				agentArtifacts["stderr"] = rel
			}
		}
		_ = e.Cook.Ledger.Emit(ledger.AgentCompleted{
			Step: stepPath, ExitCode: result.ExitCode, DurationMs: result.DurationMs,
			TokensIn: result.InputTokens, TokensOut: result.OutputTokens, CostUSD: result.CostUSD,
			SessionID: result.SessionID, IsError: result.IsError, Artifacts: agentArtifacts,
		})
	}
	if result.IsError {
		exec.Status = StepFatal
		exec.FinishedAt = time.Now()
		e.Steps = append(e.Steps, exec)
		e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
		e.lastFailureSource = "gate_fail"
		fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: agent error\n", stepPath)
		return fmt.Errorf("step %s: agent reported error", step.Name), preStepCommit
	}

	if result.SessionID != "" && lastSessionByAgent != nil {
		lastSessionByAgent[agent.AgentPrefix(agentToUse)] = result.SessionID
	}
	exec.SessionID = result.SessionID

	// Extract output from .gump/out/ before snapshot (dir is gitignored).
	var outputValue string
	switch outputMode {
	case "plan":
		tasks, raw, err := ExtractPlanOutput(e.Cook.WorktreeDir)
		if err != nil {
			exec.Status = StepFatal
			exec.FinishedAt = time.Now()
			e.Steps = append(e.Steps, exec)
			e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
			fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, err)
			return fmt.Errorf("plan step failed: %w", err), preStepCommit
		}
		outputValue = raw
		_ = tasks
	case "artifact":
		text, err := ExtractArtifactOutput(e.Cook.WorktreeDir)
		if err != nil {
			exec.Status = StepFatal
			exec.FinishedAt = time.Now()
			e.Steps = append(e.Steps, exec)
			e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
			fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, err)
			return fmt.Errorf("artifact step failed: %w", err), preStepCommit
		}
		outputValue = text
	case "review":
		pass, comment, raw, err := ParseReviewJSON(e.Cook.WorktreeDir)
		if err != nil {
			dc, snapErr := e.Cook.Snapshot(step.Name, taskName, attempt)
			if snapErr != nil {
				exec.Status = StepFatal
				exec.FinishedAt = time.Now()
				e.Steps = append(e.Steps, exec)
				e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
				fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, snapErr)
				return fmt.Errorf("snapshot after invalid review.json: %w", snapErr), preStepCommit
			}
			errMsg := err.Error()
			exec.ValidateError = errMsg
			exec.ValidateDiff = dc.Patch
			exec.Status = StepFatal
			exec.FinishedAt = time.Now()
			e.Steps = append(e.Steps, exec)
			e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, raw, true)
			e.lastFailureSource = "review_fail"
			fmt.Fprintf(os.Stderr, "[%s]\tFAIL\t%s\n", stepPath, errMsg)
			RetryValidationFailed(errMsg, "")
			if raw != "" {
				filesForBag := dc.FilesChanged
				if list, err2 := gitDiffNameOnly(e.Cook.WorktreeDir, dc.BaseCommit, dc.HeadCommit); err2 == nil {
					filesForBag = list
				}
				e.State.SetStepOutput(stepPath, raw, dc.Patch, filesForBag, result.SessionID)
			}
			if e.globalStepAttempts[stepPath] > step.MaxAttempts() {
				if e.Cook.Ledger != nil {
					_ = e.Cook.Ledger.Emit(ledger.CircuitBreaker{Step: stepPath, Scope: "step", Reason: "budget exhausted after invalid review.json", TotalAttempts: attempt})
				}
				return fmt.Errorf("step %s: all attempts exhausted", step.Name), preStepCommit
			}
			return fmt.Errorf("step %s: review step failed: %w", step.Name, err), preStepCommit
		}
		outputValue = raw
		if !pass {
			dc, snapErr := e.Cook.Snapshot(step.Name, taskName, attempt)
			if snapErr != nil {
				exec.Status = StepFatal
				exec.FinishedAt = time.Now()
				e.Steps = append(e.Steps, exec)
				e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
				fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, snapErr)
				return fmt.Errorf("snapshot after failed review: %w", snapErr), preStepCommit
			}
			filesForBag := dc.FilesChanged
			if list, err2 := gitDiffNameOnly(e.Cook.WorktreeDir, dc.BaseCommit, dc.HeadCommit); err2 == nil {
				filesForBag = list
			}
			e.State.SetStepOutput(stepPath, outputValue, dc.Patch, filesForBag, result.SessionID)
			errMsg := fmt.Sprintf("review did not pass: %s", comment)
			exec.ValidateError = errMsg
			exec.ValidateDiff = dc.Patch
			exec.ReviewComment = comment
			exec.Status = StepFatal
			exec.FinishedAt = time.Now()
			e.Steps = append(e.Steps, exec)
			e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, outputValue, true)
			e.lastFailureSource = "review_fail"
			fmt.Fprintf(os.Stderr, "[%s]\tFAIL\t%s\n", stepPath, errMsg)
			RetryValidationFailed("review did not pass", "")
			if e.globalStepAttempts[stepPath] > step.MaxAttempts() {
				if e.Cook.Ledger != nil {
					_ = e.Cook.Ledger.Emit(ledger.CircuitBreaker{Step: stepPath, Scope: "step", Reason: "budget exhausted after failed review", TotalAttempts: attempt})
				}
				return fmt.Errorf("step %s: all attempts exhausted", step.Name), preStepCommit
			}
			return fmt.Errorf("step %s: review did not pass", step.Name), preStepCommit
		}
	}

	dc, err := e.Cook.Snapshot(step.Name, taskName, attempt)
	if err != nil {
		exec.Status = StepFatal
		exec.FinishedAt = time.Now()
		e.Steps = append(e.Steps, exec)
		e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, outputValue, false)
		fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, err)
		return fmt.Errorf("snapshot after step %s: %w", step.Name, err), preStepCommit
	}

	filesForBag := dc.FilesChanged
	if list, err := gitDiffNameOnly(e.Cook.WorktreeDir, dc.BaseCommit, dc.HeadCommit); err == nil {
		filesForBag = list
	}
	e.State.SetStepOutput(stepPath, outputValue, dc.Patch, filesForBag, result.SessionID)
	if e.Cook.Ledger != nil {
		filesJoined := strings.Join(filesForBag, ", ")
		keyOut := stepPath + ".output"
		artifactRel := ""
		if len(outputValue) >= 1024 {
			name := ledger.ArtifactName(stepPath, attempt, "output", "json")
			if outputMode == "artifact" {
				name = ledger.ArtifactName(stepPath, attempt, "output", "txt")
			}
			if rel, _ := e.Cook.Ledger.WriteArtifact(name, []byte(outputValue)); rel != "" {
				artifactRel = rel
			}
		}
		_ = e.Cook.Ledger.Emit(ledger.StateBagUpdated{Key: keyOut, Artifact: artifactRel})
		keyFiles := stepPath + ".files"
		artifactFiles := ""
		if len(filesJoined) >= 1024 {
			name := ledger.ArtifactName(stepPath, attempt, "files", "txt")
			if rel, _ := e.Cook.Ledger.WriteArtifact(name, []byte(filesJoined)); rel != "" {
				artifactFiles = rel
			}
		}
		_ = e.Cook.Ledger.Emit(ledger.StateBagUpdated{Key: keyFiles, Artifact: artifactFiles})
		// WHY: session threads must be auditable alongside output and file lists without a new event type.
		_ = e.Cook.Ledger.Emit(ledger.StateBagUpdated{Key: stepPath + ".session_id", Artifact: ""})
	}

	if strings.TrimSpace(step.HITL) == "before_gate" && len(step.Gate) > 0 {
		if err := e.hitlPauseAfterSuccess(step, stepPath, outputMode, dc.FilesChanged); err != nil {
			return err, preStepCommit
		}
	}

	// Apply task.files blast radius policy (v0.0.4: workflow-level blast_radius removed; always enforce when task.files is set).
	blastMode := "enforce"
	if blastMode != "off" && taskContext != nil && len(taskContext.Files) > 0 {
		repoFiles := filterRepoFilesOnly(e.Cook.WorktreeDir, dc.FilesChanged)
		violators, errMsg := checkBlastRadius(repoFiles, taskContext.Files)
		if len(violators) > 0 {
			if blastMode == "warn" {
				warnMsg := blastRadiusWarningMessage(violators, taskContext.Files)
				if e.Cook.Ledger != nil {
					_ = e.Cook.Ledger.Emit(ledger.BlastRadiusWarning{Step: stepPath, Violators: violators, Allowed: taskContext.Files})
				}
				fmt.Fprintln(os.Stderr, warnMsg)
			} else if blastMode == "enforce" {
				if e.Cook.Ledger != nil {
					_ = e.Cook.Ledger.Emit(ledger.GateFailed{Step: stepPath, Reason: errMsg, Artifact: ""})
					e.emitStepCompleted(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, outputValue, true)
					e.stepCompletedCount++
					e.printCookTotal()
				}
				exec.ValidateError = errMsg
				exec.ValidateDiff = dc.Patch
				exec.Status = StepFatal
				exec.FinishedAt = time.Now()
				e.Steps = append(e.Steps, exec)
				e.State.SetStepCheckResult(stepPath, "fail")
				e.State.SetRunMetric("status", "fail")
				e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, outputValue, true)
				e.lastFailureSource = "gate_fail"
				fmt.Fprintf(os.Stderr, "[%s]\tFAIL\t%s\n", stepPath, errMsg)
				return fmt.Errorf("step %s: %s", step.Name, errMsg), preStepCommit
			}
		}
	}

	if len(step.Gate) > 0 {
		if e.Cook.Ledger != nil {
			checks := make([]string, 0, len(step.Gate))
			for _, v := range step.Gate {
				if v.Arg != "" {
					checks = append(checks, v.Type+":"+v.Arg)
				} else {
					checks = append(checks, v.Type)
				}
			}
			_ = e.Cook.Ledger.Emit(ledger.GateStarted{Step: stepPath, Checks: checks})
		}
		vr := validate.RunValidators(step.Gate, e.Config, e.Cook.WorktreeDir, dc, e.State, stepPath)
		writeValidationArtifact(e.Cook.CookDir, stepPath, attempt, vr)
		validationArtifactRel := filepath.Join("artifacts", ledger.ArtifactName(stepPath, attempt, "validation", "json"))
		if vr.Pass {
			if e.Cook.Ledger != nil {
				_ = e.Cook.Ledger.Emit(ledger.GatePassed{Step: stepPath, Artifact: validationArtifactRel})
			}
			exec.Status = StepPass
			exec.CommitHash = dc.HeadCommit
			exec.FinishedAt = time.Now()
			e.Steps = append(e.Steps, exec)
			e.State.SetStepCheckResult(stepPath, "pass")
			e.State.SetRunMetric("status", "pass")
			e.setLastStepOutcome(stepPath, attempt, "pass", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, outputValue, true)
			detail := buildValidationPassDetail(outputMode, outputValue, dc, vr)
			fmt.Fprintf(os.Stderr, "[%s]\tpass\t%s\n", stepPath, detail)
			_, nSkipped := countValidationPassedSkipped(vr)
			if nSkipped > 0 {
				formatValidationDetails(os.Stderr, stepPath, vr)
			}
			if err := e.hitlPauseAfterSuccess(step, stepPath, outputMode, dc.FilesChanged); err != nil {
				return err, ""
			}
			return nil, ""
		}
		var failedNames []string
		var errParts []string
		for _, r := range vr.Results {
			if !r.Pass {
				failedNames = append(failedNames, r.Validator)
				errParts = append(errParts, r.Stderr)
			}
		}
		if e.Cook.Ledger != nil {
			reason := ""
			for _, r := range vr.Results {
				if !r.Pass && r.Stderr != "" {
					firstLine := strings.TrimSpace(strings.Split(r.Stderr, "\n")[0])
					if len(firstLine) > 200 {
						firstLine = firstLine[:200]
					}
					reason = firstLine
					break
				}
			}
			_ = e.Cook.Ledger.Emit(ledger.GateFailed{Step: stepPath, Reason: reason, Artifact: validationArtifactRel})
		}
		exec.ValidateError = strings.Join(errParts, "\n---\n")
		exec.ValidateDiff = dc.Patch
		exec.Status = StepFatal
		exec.FinishedAt = time.Now()
		e.Steps = append(e.Steps, exec)
		e.State.SetStepCheckResult(stepPath, "fail")
		e.State.SetRunMetric("status", "fail")
		e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, outputValue, true)
		e.lastFailureSource = "gate_fail"
		formatValidationDetails(os.Stderr, stepPath, vr)
		RetryValidationFailed("validation failed: "+strings.Join(failedNames, ", "), "")
		if e.globalStepAttempts[stepPath] > step.MaxAttempts() {
			if e.Cook.Ledger != nil {
				_ = e.Cook.Ledger.Emit(ledger.CircuitBreaker{Step: stepPath, Scope: "step", Reason: "budget exhausted after failed gate", TotalAttempts: attempt})
			}
			return fmt.Errorf("step %s: all attempts exhausted", step.Name), preStepCommit
		}
		return fmt.Errorf("step %s: validation failed: %s", step.Name, strings.Join(failedNames, ", ")), preStepCommit
	}

	// No gate checks: treat as "none" for {steps.<n>.check_result}.
	e.State.SetStepCheckResult(stepPath, "none")
	exec.Status = StepPass
	exec.CommitHash = dc.HeadCommit
	exec.FinishedAt = time.Now()
	e.Steps = append(e.Steps, exec)
	e.setLastStepOutcome(stepPath, attempt, "pass", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, outputValue, false)

	detail := ""
	if outputMode == "plan" && outputValue != "" {
		if tasks, err := plan.ParsePlanOutput([]byte(outputValue)); err == nil && len(tasks) > 0 {
			ContextLine("plan", len(tasks), 0)
			detail = fmt.Sprintf("%d tasks planned", len(tasks))
		}
	} else if outputMode == "review" && outputValue != "" {
		detail = "review.json recorded"
	} else if outputMode != "plan" && dc != nil {
		n := len(dc.FilesChanged)
		ContextLine("diff", 0, n)
		if n == 0 {
			detail = "no changes"
		} else {
			detail = fmt.Sprintf("%d file(s) changed", n)
		}
	}
	if detail != "" {
		fmt.Fprintf(os.Stderr, "[%s]\tpass\t%s\n", stepPath, detail)
	}
	if err := e.hitlPauseAfterSuccess(step, stepPath, outputMode, dc.FilesChanged); err != nil {
		return err, ""
	}
	return nil, ""
}

// runAtomicStepWithVars runs one atomic step with optional extra template vars (e.g. for replan sub-tasks). No retry.
func (e *Engine) runAtomicStepWithVars(step *workflow.Step, stepPath string, taskContext *plan.Task, lastSessionByAgent map[string]string, parentSession workflow.SessionConfig, extraVars map[string]string) error {
	err, _ := e.runAtomicStepOnce(step, stepPath, taskContext, lastSessionByAgent, parentSession, 1, "", nil, extraVars, nil)
	e.emitStepCompletedFromLast()
	return err
}

func (e *Engine) hitlPauseAfterSuccess(step *workflow.Step, stepPath string, outputMode string, filesChanged []string) error {
	need := strings.TrimSpace(step.HITL) == "after_gate"
	if e.PauseAfterStep != "" && step.Name == e.PauseAfterStep {
		need = true
	}
	if !need {
		return nil
	}
	mode := outputMode
	if mode == "" {
		mode = "diff"
	}
	fstr := strings.Join(filterRepoFilesOnly(e.Cook.WorktreeDir, filesChanged), ", ")
	fmt.Fprintf(os.Stderr, "[%s] HITL pause after step '%s'\n\nResult:\n  Mode:    %s\n  Status:  pass\n  Files:   %s\n\n  Review the results in the worktree: %s\n  Press Enter to continue, Ctrl+C to abort.\n", brand.Lower(), stepPath, mode, fstr, e.Cook.WorktreeDir)
	if e.Cook.Ledger != nil {
		_ = e.Cook.Ledger.Emit(ledger.HITLPaused{Step: stepPath})
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	readCh := make(chan error, 1)
	go func() {
		_, err := bufio.NewReader(os.Stdin).ReadString('\n')
		readCh <- err
	}()
	select {
	case <-sigCh:
		if e.Cook.Ledger != nil {
			_ = e.Cook.Ledger.Emit(ledger.HITLResumed{Step: stepPath, Action: "abort"})
		}
		return ErrCookAborted
	case err := <-readCh:
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}
	if e.Cook.Ledger != nil {
		_ = e.Cook.Ledger.Emit(ledger.HITLResumed{Step: stepPath, Action: "continue"})
	}
	return nil
}

func (e *Engine) writeEngineStepAttemptMarker(stepName string, n int) {
	p := filepath.Join(e.Cook.WorktreeDir, brand.StateDir(), "engine-step-attempt.json")
	_ = os.MkdirAll(filepath.Dir(p), 0755)
	_ = os.WriteFile(p, []byte(fmt.Sprintf(`{"step":%q,"n":%d}`, stepName, n)), 0644)
}

// writeValidationArtifact persists validation result under the step’s artifact dir.
// writeValidationArtifact persists validation result so the ledger can reference it; attempt is required so the filename matches the event (artifact-before-event invariant).
func writeValidationArtifact(cookDir, stepPath string, attempt int, vr *validate.ValidationResult) {
	type resultRow struct {
		Validator  string `json:"validator"`
		Pass       bool   `json:"pass"`
		Skipped    bool   `json:"skipped"`
		ExitCode   int    `json:"exit_code"`
		Stdout     string `json:"stdout"`
		Stderr     string `json:"stderr"`
		DurationMs int64  `json:"duration_ms"`
	}
	rows := make([]resultRow, 0, len(vr.Results))
	for _, r := range vr.Results {
		rows = append(rows, resultRow{r.Validator, r.Pass, r.Skipped, r.ExitCode, r.Stdout, r.Stderr, r.Duration.Milliseconds()})
	}
	payload := map[string]interface{}{"pass": vr.Pass, "results": rows}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	name := ledger.ArtifactName(stepPath, attempt, "validation", "json")
	dir := filepath.Join(cookDir, "artifacts")
	_ = os.MkdirAll(dir, 0755)
	_ = os.WriteFile(filepath.Join(dir, name), data, 0644)
}

// setLastStepOutcome records the outcome of this attempt so the caller can emit step_completed once after the retry loop.
func (e *Engine) setLastStepOutcome(stepPath string, attempt int, status string, durationMs int, dc *diff.DiffContract, outputMode, outputValue string, hasValidation bool) {
	patch := ""
	if dc != nil {
		patch = dc.Patch
	}
	e.lastStep = &lastStepOutcome{
		stepPath: stepPath, attempt: attempt, status: status, durationMs: durationMs,
		patch: patch, outputMode: outputMode, outputValue: outputValue, hasValidation: hasValidation,
	}
}

// emitStepCompletedFromLast writes artifacts from lastStep then emits one step_completed and increments stepCompletedCount (called once per step after retry loop).
func (e *Engine) emitStepCompletedFromLast() {
	if e.Cook.Ledger == nil || e.lastStep == nil {
		return
	}
	o := e.lastStep
	e.lastStep = nil

	retries := o.attempt - 1
	stepStatus := "fatal"
	checkResult := "none"
	if o.status == "pass" {
		stepStatus = "pass"
		checkResult = "pass"
	} else if o.status == "guard_failed" {
		stepStatus = "guard_failed"
		checkResult = "fail"
	} else if o.hasValidation {
		stepStatus = "fail"
		checkResult = "fail"
	}
	e.State.SetStepOutcome(o.stepPath, stepStatus, retries)
	e.State.SetStepCheckResult(o.stepPath, checkResult)
	if !e.cookRunStartedAt.IsZero() {
		e.State.SetRunMetric("duration", fmt.Sprintf("%d", int(time.Since(e.cookRunStartedAt).Milliseconds())))
	}
	e.State.SetRunMetric("status", stepStatus)

	artifacts := make(map[string]string)
	if o.patch != "" && o.outputMode == "diff" {
		name := ledger.ArtifactName(o.stepPath, o.attempt, "diff", "patch")
		if rel, _ := e.Cook.Ledger.WriteArtifact(name, []byte(o.patch)); rel != "" {
			artifacts["diff"] = rel
		}
	}
	if (o.outputMode == "plan" || o.outputMode == "artifact") && o.outputValue != "" {
		ext := "json"
		if o.outputMode == "artifact" {
			ext = "txt"
		}
		name := ledger.ArtifactName(o.stepPath, o.attempt, "output", ext)
		if rel, _ := e.Cook.Ledger.WriteArtifact(name, []byte(o.outputValue)); rel != "" {
			artifacts["output"] = rel
		}
	}
	if o.hasValidation {
		artifacts["validation"] = filepath.Join("artifacts", ledger.ArtifactName(o.stepPath, o.attempt, "validation", "json"))
	}
	commit, _ := sandbox.HeadCommit(e.Cook.WorktreeDir)
	_ = e.Cook.Ledger.Emit(ledger.StepCompleted{Step: o.stepPath, Status: o.status, DurationMs: o.durationMs, Artifacts: artifacts, Commit: commit})
	e.stepCompletedCount++
	e.printCookTotal()
}

// emitStepCompleted writes step artifacts (diff, output) then emits step_completed; used only for validation-only steps (no retry loop).
func (e *Engine) emitStepCompleted(stepPath string, attempt int, status string, durationMs int, dc *diff.DiffContract, outputMode, outputValue string, hasValidation bool) {
	if e.Cook.Ledger == nil {
		return
	}

	retries := attempt - 1
	stepStatus := "fatal"
	checkResult := "none"
	if status == "pass" {
		stepStatus = "pass"
		checkResult = "pass"
	} else if status == "guard_failed" {
		stepStatus = "guard_failed"
		checkResult = "fail"
	} else if hasValidation {
		stepStatus = "fail"
		checkResult = "fail"
	}
	e.State.SetStepOutcome(stepPath, stepStatus, retries)
	e.State.SetStepCheckResult(stepPath, checkResult)
	if !e.cookRunStartedAt.IsZero() {
		e.State.SetRunMetric("duration", fmt.Sprintf("%d", int(time.Since(e.cookRunStartedAt).Milliseconds())))
	}
	e.State.SetRunMetric("status", stepStatus)

	artifacts := make(map[string]string)
	if dc != nil && dc.Patch != "" && outputMode == "diff" {
		name := ledger.ArtifactName(stepPath, attempt, "diff", "patch")
		if rel, _ := e.Cook.Ledger.WriteArtifact(name, []byte(dc.Patch)); rel != "" {
			artifacts["diff"] = rel
		}
	}
	if (outputMode == "plan" || outputMode == "artifact") && outputValue != "" {
		ext := "json"
		if outputMode == "artifact" {
			ext = "txt"
		}
		name := ledger.ArtifactName(stepPath, attempt, "output", ext)
		if rel, _ := e.Cook.Ledger.WriteArtifact(name, []byte(outputValue)); rel != "" {
			artifacts["output"] = rel
		}
	}
	if hasValidation {
		artifacts["validation"] = filepath.Join("artifacts", ledger.ArtifactName(stepPath, attempt, "validation", "json"))
	}
	commit, _ := sandbox.HeadCommit(e.Cook.WorktreeDir)
	_ = e.Cook.Ledger.Emit(ledger.StepCompleted{Step: stepPath, Status: status, DurationMs: durationMs, Artifacts: artifacts, Commit: commit})
	e.stepCompletedCount++
	e.printCookTotal()
}

func countValidationPassedSkipped(vr *validate.ValidationResult) (passed, skipped int) {
	for _, r := range vr.Results {
		if r.Skipped {
			skipped++
		} else if r.Pass {
			passed++
		}
	}
	return passed, skipped
}

func formatValidationPassSummary(passed, skipped int) string {
	if skipped == 0 {
		if passed == 1 {
			return "1 validator passed"
		}
		return fmt.Sprintf("%d validators passed", passed)
	}
	if passed == 1 && skipped == 1 {
		return "1 validator passed, 1 skipped"
	}
	return fmt.Sprintf("%d validators passed, %d skipped", passed, skipped)
}

func buildValidationPassDetail(outputMode, outputValue string, dc *diff.DiffContract, vr *validate.ValidationResult) string {
	var parts []string
	if outputMode == "plan" && outputValue != "" {
		if tasks, err := plan.ParsePlanOutput([]byte(outputValue)); err == nil && len(tasks) > 0 {
			parts = append(parts, fmt.Sprintf("%d tasks planned", len(tasks)))
		}
	}
	if outputMode == "review" && outputValue != "" {
		parts = append(parts, "review passed")
	}
	if outputMode != "plan" && outputMode != "review" && dc != nil {
		n := len(dc.FilesChanged)
		if n == 0 {
			parts = append(parts, "no changes")
		} else if n == 1 {
			parts = append(parts, "1 file changed")
		} else {
			parts = append(parts, fmt.Sprintf("%d files changed", n))
		}
	}
	if vr != nil {
		passed, skipped := countValidationPassedSkipped(vr)
		if passed > 0 || skipped > 0 {
			parts = append(parts, formatValidationPassSummary(passed, skipped))
		}
	}
	if len(parts) == 0 {
		return "ok"
	}
	return strings.Join(parts, ", ")
}

// formatValidationDetails writes per-validator lines (✓ / ⊘ / ✗) for validation results.
func formatValidationDetails(w io.Writer, stepPath string, vr *validate.ValidationResult) {
	fmt.Fprintf(w, "[%s] validation details:\n", stepPath)
	for _, r := range vr.Results {
		if r.Skipped {
			skipDetail := "skipped"
			if idx := strings.Index(r.Stdout, "'"); idx >= 0 {
				if end := strings.Index(r.Stdout[idx+1:], "'"); end >= 0 {
					skipDetail = r.Stdout[idx+1:idx+1+end] + " not installed"
				}
			}
			baseName := r.Validator
			if s := " (skipped)"; strings.HasSuffix(baseName, s) {
				baseName = baseName[:len(baseName)-len(s)]
			}
			fmt.Fprintf(w, "  ⊘ %s (skipped — %s)\n", baseName, skipDetail)
		} else if r.Pass {
			fmt.Fprintf(w, "  ✓ %s (exit %d, %s)\n", r.Validator, r.ExitCode, r.Duration)
		} else {
			fmt.Fprintf(w, "  ✗ %s (exit %d, %s)\n", r.Validator, r.ExitCode, r.Duration)
			if r.Stderr != "" {
				fmt.Fprintf(w, "    --- stderr ---\n    %s\n", strings.ReplaceAll(r.Stderr, "\n", "\n    "))
			}
		}
	}
}

// resolveTargetedSession returns the session ID of the last executed step named target, only if its agent matches currentAgent (cross-provider resume would be invalid).
func (e *Engine) resolveTargetedSession(target string, currentAgent string) string {
	// Prefer current state bag (already cleared on reset paths) before historical e.Steps.
	if sid := e.State.Get(target + ".session_id"); sid != "" {
		return sid
	}
	for i := len(e.Steps) - 1; i >= 0; i-- {
		if e.Steps[i].StepName == target && e.Steps[i].SessionID != "" {
			if e.Steps[i].Agent != currentAgent {
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
	p := filepath.Join(e.Cook.WorktreeDir, brand.StateDir(), "restart-cycle")
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
	case "review":
		return "validate"
	default:
		return om
	}
}

func (e *Engine) newTemplateCtx(stepPath string, taskContext *plan.Task, errCtx *ErrorContext, attempt int, inheritedVars, extraVars map[string]string) *state.ResolveContext {
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
		State:       e.State,
		StepPath:    stepPath,
		Spec:        e.SpecContent,
		Attempt:     attempt,
		Extra:       ex,
		GateResults: map[string]string{},
		GateMeta:    map[string]map[string]string{},
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
	resolved, err := workflow.Resolve(workflowName, e.Cook.RepoRoot)
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
	resolved, err := workflow.Resolve(workflowName, e.Cook.RepoRoot)
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
	tctx := e.newTemplateCtx(stepPath, taskContext, nil, 1, inheritedVars, nil)
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
		worktree = e.Cook.WorktreeDir
	}
	CookFooter(runErr == nil, e.totalCostUSD, e.stepCompletedCount, e.stepTotalEstimate, e.retryTriggeredCount, duration, fatalStep, errMsg, worktree)

	if e.Cook.Ledger == nil {
		return
	}
	cookDir := e.Cook.CookDir
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
	dc, err := e.Cook.FinalDiff()
	if err == nil && dc != nil {
		finalDiffPatch = []byte(dc.Patch)
	}
	finalDiffRel, _ := e.Cook.Ledger.WriteArtifact("final-diff.patch", finalDiffPatch)

	artifacts := map[string]string{"state": "state.json"}
	if finalDiffRel != "" {
		artifacts["final_diff"] = finalDiffRel
	}
	_ = e.Cook.Ledger.Emit(ledger.RunCompleted{
		RunID:      e.Cook.ID,
		Status:     status,
		DurationMs: durationMs,
		TotalCost:  e.totalCostUSD,
		Artifacts:  artifacts,
	})

	agentsList := make([]string, 0, len(e.agentsUsed))
	for a := range e.agentsUsed {
		agentsList = append(agentsList, a)
	}
	_ = ledger.AppendIndex(e.Cook.RepoRoot, ledger.IndexEntry{
		CookID:     e.Cook.ID,
		Timestamp:  e.cookRunStartedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		Recipe:     e.Cook.RecipeName,
		Spec:       filepath.Base(e.Cook.SpecPath),
		Status:     status,
		DurationMs: durationMs,
		CostUSD:    e.totalCostUSD,
		Steps:      e.stepCompletedCount,
		Retries:    e.retryTriggeredCount,
		Agents:     agentsList,
	})
	_ = e.Cook.Ledger.Close()
}
