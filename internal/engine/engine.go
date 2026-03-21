package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/isomorphx/pudding/internal/agent"
	"github.com/isomorphx/pudding/internal/config"
	pkgcontext "github.com/isomorphx/pudding/internal/context"
	"github.com/isomorphx/pudding/internal/cook"
	"github.com/isomorphx/pudding/internal/diff"
	"github.com/isomorphx/pudding/internal/ledger"
	"github.com/isomorphx/pudding/internal/plan"
	"github.com/isomorphx/pudding/internal/recipe"
	"github.com/isomorphx/pudding/internal/sandbox"
	"github.com/isomorphx/pudding/internal/statebag"
	"github.com/isomorphx/pudding/internal/template"
	"github.com/isomorphx/pudding/internal/validate"
)

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

// Engine runs the recipe step-by-step; behavior is inferred from fields (no type:) so recipes stay declarative. State Bag holds outputs for {steps.<n>.output/diff}.
// AgentsCLI is set by the CLI so cook_started can record agent versions; stepCompletedCount, retryTriggeredCount, totalCostUSD, agentsUsed are accumulated for cook_completed and index.
type Engine struct {
	Cook                 *cook.Cook
	Recipe               *recipe.Recipe
	Resolver             agent.AdapterResolver
	Config               *config.Config
	SpecContent          string
	Steps                []StepExecution
	StateBag             *statebag.StateBag
	AgentsCLI            map[string]string
	CookAgentOverride    string // CLI --agent overrides step agent when set
	FromStep                string // when set (replay), skip steps until we reach this path
	replayOriginalCookID    string // for replay_started event
	replayRestoredCommit   string
	stepCompletedCount     int
	retryTriggeredCount   int
	totalCostUSD          float64
	agentsUsed            map[string]struct{}
	cookRunStartedAt      time.Time
	lastStep              *lastStepOutcome
	replayReachedStart    bool // true once we've reached FromStep in executeSteps
	stepRunIndex          int  // 1-based index of current step for [N/total] header (Feature 12)
	stepTotalEstimate     int  // total steps when known (e.g. after group_started with task_count)
}

// New builds an engine with an empty State Bag and counters for ledger/index.
func New(c *cook.Cook, rec *recipe.Recipe, r agent.AdapterResolver, cfg *config.Config, specContent string) *Engine {
	return &Engine{
		Cook: c, Recipe: rec, Resolver: r, Config: cfg, SpecContent: specContent,
		Steps: nil, StateBag: statebag.New(),
		agentsUsed: make(map[string]struct{}),
	}
}

// Run executes the recipe; returns nil if all steps pass, or an error when a step is fatal.
// We always run the end-of-cook sequence (state-bag, final-diff, cook_completed, index, close ledger) so the ledger is complete even on fatal.
func (e *Engine) Run() error {
	defer agent.RestoreAllContextFiles(e.Cook.WorktreeDir)
	e.cookRunStartedAt = time.Now()
	if e.Cook.Ledger != nil {
		_ = e.Cook.Ledger.Emit(ledger.CookStarted{
			CookID:    e.Cook.ID,
			Recipe:    e.Cook.RecipeName,
			Spec:      filepath.Base(e.Cook.SpecPath),
			Commit:    e.Cook.InitialCommit,
			Branch:    e.Cook.OrigBranch,
			AgentsCLI: e.AgentsCLI,
		})
		if e.FromStep != "" && e.replayOriginalCookID != "" {
			_ = e.Cook.Ledger.Emit(ledger.ReplayStarted{
				OriginalCookID: e.replayOriginalCookID,
				FromStep:       e.FromStep,
				RestoredCommit: e.replayRestoredCommit,
			})
		}
	}
	CookHeader(e.Cook.RecipeName, e.Cook.ID, filepath.Base(e.Cook.SpecPath))
	err := e.executeSteps(e.Recipe.Steps, nil, "", make(map[string]string), recipe.SessionConfig{Mode: "fresh"}, "")
	e.finishCook(err)
	return err
}

// executeSteps runs steps; composite (steps:) with optional foreach, atomic (agent:), or validation-only (validate:).
// groupAgentOverride is set when we're inside a group retry with strategy "escalate"; atomic steps in this subtree use that agent.
func (e *Engine) executeSteps(steps []recipe.Step, taskContext *plan.Task, pathPrefix string, lastSessionByAgent map[string]string, parentSession recipe.SessionConfig, groupAgentOverride string) error {
	for _, step := range steps {
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

		// Composite: has sub-steps or recipe reference (with optional foreach over a plan step's output).
		if len(step.Steps) > 0 || step.Recipe != "" {
			var planTasks []plan.Task
			taskCount := 0
			if step.Foreach != "" {
				planRaw := e.StateBag.Get(step.Foreach, stepPath, "output")
				if planRaw == "" {
					return fmt.Errorf("foreach references step %q but no plan output found in State Bag", step.Foreach)
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
				_ = e.Cook.Ledger.Emit(ledger.GroupStarted{Step: stepPath, Foreach: step.Foreach, Parallel: step.Parallel, TaskCount: taskCount})
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
					return RunParallelGroup(e, &step, stepPath, subSteps, planTasks, baseCommit, sessionMap, step.Session, agentOverride)
				}
				if planTasks != nil {
					for _, task := range planTasks {
						taskPrefix := step.Name + "/" + task.Name
						if pathPrefix != "" {
							taskPrefix = pathPrefix + "/" + taskPrefix
						}
						taskSession := make(map[string]string)
						if err := e.executeSteps(subSteps, &task, taskPrefix, taskSession, step.Session, agentOverride); err != nil {
							return err
						}
					}
					return nil
				}
				return e.executeSteps(subSteps, nil, stepPath, sessionMap, step.Session, agentOverride)
			}
			if step.Retry != nil && step.Retry.MaxAttempts > 1 {
				preGroupCommit, _ := PreStepCommit(e.Cook.WorktreeDir)
				maxAttempts := step.Retry.MaxAttempts
				expanded := recipe.ExpandStrategy(step.Retry.Strategy)
				if len(expanded) == 0 {
					expanded = []recipe.StrategyEntry{{Type: "same", Count: 1}}
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
						_ = e.Cook.ResetTo(preGroupCommit)
						keys := e.StateBag.ResetGroup(stepPath)
						if e.Cook.Ledger != nil {
							_ = e.Cook.Ledger.Emit(ledger.StateBagScopeReset{Group: stepPath, Keys: keys})
							_ = e.Cook.Ledger.Emit(ledger.GroupRetry{Step: stepPath, Attempt: groupAttempt, Strategy: strategyLabel})
						}
						fmt.Fprintf(os.Stderr, "[%s]\tgroup-retry\tattempt %d/%d (%s)\n", stepPath, groupAttempt, maxAttempts, strategyLabel)
						sessionMap = make(map[string]string)
						groupAttemptPath := filepath.Join(e.Cook.WorktreeDir, ".pudding", "group-attempt")
						_ = os.MkdirAll(filepath.Dir(groupAttemptPath), 0755)
						_ = os.WriteFile(groupAttemptPath, []byte(fmt.Sprintf("%d", groupAttempt)), 0644)
					}
					err := runGroupOnce(agentOverride, sessionMap)
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
		if step.Agent == "" && len(step.Validate) > 0 {
			e.stepRunIndex++
			taskInfo := ""
			if taskContext != nil && taskContext.Name != "" {
				taskInfo = "[task " + taskContext.Name + "]"
			}
			StepHeader(e.stepRunIndex, e.stepTotalEstimate, stepPath, "validate", taskInfo, "", "")
			startedAt := time.Now()
			if e.Cook.Ledger != nil {
				validators := make([]string, 0, len(step.Validate))
				for _, v := range step.Validate {
					if v.Arg != "" {
						validators = append(validators, v.Type+":"+v.Arg)
					} else {
						validators = append(validators, v.Type)
					}
				}
				_ = e.Cook.Ledger.Emit(ledger.StepStarted{Step: stepPath, Agent: "", OutputMode: "", Task: taskContextName(taskContext), Attempt: 1})
				_ = e.Cook.Ledger.Emit(ledger.ValidationStarted{Step: stepPath, Validators: validators})
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
			vr := validate.RunValidators(step.Validate, e.Config, e.Cook.WorktreeDir, dc, e.StateBag, stepPath)
			writeValidationArtifact(e.Cook.CookDir, stepPath, 1, vr)
			validationArtifactRel := filepath.Join("artifacts", ledger.ArtifactName(stepPath, 1, "validation", "json"))
			if vr.Pass {
				if e.Cook.Ledger != nil {
					_ = e.Cook.Ledger.Emit(ledger.ValidationPassed{Step: stepPath, Artifact: validationArtifactRel})
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
				_ = e.Cook.Ledger.Emit(ledger.ValidationFailed{Step: stepPath, Reason: reason, Artifact: validationArtifactRel})
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
		if err := e.runAtomicStep(&step, stepPath, taskContext, lastSessionByAgent, parentSession, groupAgentOverride); err != nil {
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

func (e *Engine) runAtomicStep(step *recipe.Step, stepPath string, taskContext *plan.Task, lastSessionByAgent map[string]string, parentSession recipe.SessionConfig, groupAgentOverride string) error {
	if step.Retry != nil && step.Retry.MaxAttempts > 1 {
		return e.RunWithRetry(*step, stepPath, taskContext, func(attempt int, agentOverride string, errorContext *ErrorContext) (err error, preStepCommit string) {
			override := agentOverride
			if override == "" && groupAgentOverride != "" {
				override = groupAgentOverride
			}
			return e.runAtomicStepOnce(step, stepPath, taskContext, lastSessionByAgent, parentSession, attempt, override, errorContext, nil)
		})
	}
	override := groupAgentOverride
	err, _ := e.runAtomicStepOnce(step, stepPath, taskContext, lastSessionByAgent, parentSession, 1, override, nil, nil)
	e.emitStepCompletedFromLast()
	return err
}

// runAtomicStepOnce runs one attempt of an atomic step; returns (err, preStepCommit) so retry can reset. preStepCommit is captured at start.
func (e *Engine) runAtomicStepOnce(step *recipe.Step, stepPath string, taskContext *plan.Task, lastSessionByAgent map[string]string, parentSession recipe.SessionConfig, attempt int, agentOverride string, errorContext *ErrorContext, extraVars map[string]string) (err error, preStepCommit string) {
	preStepCommit, _ = PreStepCommit(e.Cook.WorktreeDir)
	taskName := taskContextName(taskContext)
	vars := e.buildVars(taskContext, errorContext, extraVars)
	outputMode := step.Output
	if outputMode == "" {
		outputMode = "diff"
	}
	prompt := step.Prompt
	resolvedPrompt := template.Resolve(prompt, vars, e.StateBag, stepPath)

	effectiveSession := step.Session
	if effectiveSession.Mode == "" {
		effectiveSession = parentSession
	}
	sessionReuse := attempt == 1 && (effectiveSession.Mode == "reuse" || effectiveSession.Mode == "reuse-targeted")

	var taskFiles []string
	if taskContext != nil && len(taskContext.Files) > 0 {
		taskFiles = taskContext.Files
	}
	agentToUse := step.Agent
	if e.CookAgentOverride != "" {
		agentToUse = e.CookAgentOverride
	}
	if agentOverride != "" {
		agentToUse = agentOverride
	}
	adapter, err := e.Resolver.AdapterFor(agentToUse)
	if err != nil {
		return err, preStepCommit
	}
	contextFile := agent.ContextFileForAgent(agentToUse)
	agent.RemoveOtherContextFiles(e.Cook.WorktreeDir, contextFile)
	if err := PrepareOutputDir(e.Cook.WorktreeDir); err != nil {
		return fmt.Errorf("prepare output dir: %w", err), preStepCommit
	}
	var retrySection *pkgcontext.RetrySection
	if attempt > 1 && errorContext != nil && step.Retry != nil {
		retrySection = &pkgcontext.RetrySection{
			Attempt:        attempt,
			MaxAttempts:    step.Retry.MaxAttempts,
			Diff:           errorContext.Diff,
			Error:          errorContext.Error,
			ReviewComment:  errorContext.ReviewComment,
			Remaining:      step.Retry.MaxAttempts - attempt,
			EscalateTo:     agentOverride,
			EscalateFrom:   step.Agent,
		}
		if agentOverride == "" {
			retrySection.EscalateTo = ""
			retrySection.EscalateFrom = ""
		}
	}
	var buildOpts *pkgcontext.BuildOptions
	if sessionReuse {
		buildOpts = &pkgcontext.BuildOptions{SessionReuse: true}
	}
	if err := pkgcontext.Build(outputMode, resolvedPrompt, step.Context, e.Cook.WorktreeDir, e.Config, taskFiles, vars, contextFile, retrySection, buildOpts); err != nil {
		return fmt.Errorf("write context: %w", err), preStepCommit
	}

	timeout := time.Duration(0)
	if step.Timeout != "" {
		var parseErr error
		timeout, parseErr = time.ParseDuration(step.Timeout)
		if parseErr != nil {
			return fmt.Errorf("step %s: invalid timeout %q: %w", step.Name, step.Timeout, parseErr), preStepCommit
		}
	}

	sessionID := ""
	switch effectiveSession.Mode {
	case "fresh":
		sessionID = ""
	case "reuse-on-retry":
		// So the agent gets a fresh context on first try but keeps conversation state on retries (e.g. after validation failure).
		if attempt > 1 {
			sessionID = e.lastSessionIDForStep(stepPath)
		}
	case "reuse":
		if attempt == 1 && lastSessionByAgent != nil {
			if sid, ok := lastSessionByAgent[agent.AgentPrefix(step.Agent)]; ok {
				sessionID = sid
			}
		}
	case "reuse-targeted":
		if attempt == 1 {
			sessionID = e.resolveTargetedSession(effectiveSession.Target, step.Agent)
			if sessionID == "" {
				fmt.Fprintf(os.Stderr, "[pudding] session: reuse: %s — target step not found or different agent, using fresh session\n", effectiveSession.Target)
			}
		}
	}

	promptForAgent := resolvedPrompt
	if outputMode == "plan" {
		promptForAgent += "\n[PUDDING:plan]"
	} else if outputMode == "artifact" {
		promptForAgent += "\n[PUDDING:artifact]"
	} else if outputMode == "review" {
		promptForAgent += "\n[PUDDING:review]"
	}
	promptForAgent += "\n[PUDDING:step:" + step.Name + "]"
	if timeout > 0 {
		promptForAgent += "\n[PUDDING:timeout]"
	}

	strategyLabel := ""
	if errorContext != nil {
		strategyLabel = errorContext.Strategy
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
			Step: stepPath, Agent: agentToUse, OutputMode: outputMode, Task: taskName, Attempt: attempt,
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
	if attempt > 1 && step.Retry != nil {
		retryInfo = fmt.Sprintf("[retry %d/%d (%s)]", attempt, step.Retry.MaxAttempts, strategyLabel)
	}
	sessionInfo := ""
	if sessionID != "" {
		sessionInfo = "[session: reuse]"
	}
	StepHeader(e.stepRunIndex, e.stepTotalEstimate, stepPath, agentToUse, taskInfo, retryInfo, sessionInfo)

	promptHash := sha256.Sum256([]byte(promptForAgent))
	ctx := context.Background()
	var proc *agent.Process
	maxTurns := step.MaxTurns
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
	for ev := range adapter.Stream(proc) {
		formatStreamEventToTerminal(ev, agentToUse)
	}
	result, err := adapter.Wait(proc)
	if err != nil {
		exec.Status = StepFatal
		exec.FinishedAt = time.Now()
		e.Steps = append(e.Steps, exec)
		e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
		fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, err)
		return fmt.Errorf("step %s: %w", step.Name, err), preStepCommit
	}
	e.totalCostUSD += result.CostUSD
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
	if e.Cook.Ledger != nil && proc != nil {
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
		fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: agent error\n", stepPath)
		return fmt.Errorf("step %s: agent reported error", step.Name), preStepCommit
	}

	if result.SessionID != "" && lastSessionByAgent != nil {
		lastSessionByAgent[agent.AgentPrefix(agentToUse)] = result.SessionID
	}
	exec.SessionID = result.SessionID

	// Extract output from .pudding/out/ before snapshot (dir is gitignored).
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
		rev, err := ExtractReviewOutput(e.Cook.WorktreeDir)
		if err != nil {
			exec.Status = StepFatal
			exec.FinishedAt = time.Now()
			e.Steps = append(e.Steps, exec)
			e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
			fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, err)
			return fmt.Errorf("review step failed: %w", err), preStepCommit
		}
		outputValue = rev.Raw
		if !rev.Pass {
			dc, snapErr := e.Cook.Snapshot(step.Name, taskName, attempt)
			if snapErr != nil {
				exec.Status = StepFatal
				exec.FinishedAt = time.Now()
				e.Steps = append(e.Steps, exec)
				e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
				fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, snapErr)
				return fmt.Errorf("snapshot after failed review: %w", snapErr), preStepCommit
			}
			errMsg := fmt.Sprintf("review did not pass: %s", rev.Comment)
			exec.ValidateError = errMsg
			exec.ValidateDiff = dc.Patch
			exec.ReviewComment = rev.Comment
			exec.Status = StepFatal
			exec.FinishedAt = time.Now()
			e.Steps = append(e.Steps, exec)
			e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, outputValue, true)
			fmt.Fprintf(os.Stderr, "[%s]\tFAIL\t%s\n", stepPath, errMsg)
			RetryValidationFailed("review did not pass", "")
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

	e.StateBag.Set(stepPath, outputValue, dc.Patch, dc.FilesChanged)
	if e.Cook.Ledger != nil {
		key := stepPath + ".output"
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
		_ = e.Cook.Ledger.Emit(ledger.StateBagUpdated{Key: key, Artifact: artifactRel})
	}

	// Enforce task.files blast radius so agents don't modify files outside the planned scope (retry gets {error} injected).
	if taskContext != nil && len(taskContext.Files) > 0 {
		repoFiles := filterRepoFilesOnly(e.Cook.WorktreeDir, dc.FilesChanged)
		violators, errMsg := checkBlastRadius(repoFiles, taskContext.Files)
		if len(violators) > 0 {
			if e.Cook.Ledger != nil {
				_ = e.Cook.Ledger.Emit(ledger.ValidationFailed{Step: stepPath, Reason: errMsg, Artifact: ""})
				e.emitStepCompleted(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, outputValue, true)
				e.stepCompletedCount++
				e.printCookTotal()
			}
			exec.ValidateError = errMsg
			exec.ValidateDiff = dc.Patch
			exec.Status = StepFatal
			exec.FinishedAt = time.Now()
			e.Steps = append(e.Steps, exec)
			e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, outputValue, true)
			fmt.Fprintf(os.Stderr, "[%s]\tFAIL\t%s\n", stepPath, errMsg)
			return fmt.Errorf("step %s: %s", step.Name, errMsg), preStepCommit
		}
	}

	if len(step.Validate) > 0 {
		if e.Cook.Ledger != nil {
			validators := make([]string, 0, len(step.Validate))
			for _, v := range step.Validate {
				if v.Arg != "" {
					validators = append(validators, v.Type+":"+v.Arg)
				} else {
					validators = append(validators, v.Type)
				}
			}
			_ = e.Cook.Ledger.Emit(ledger.ValidationStarted{Step: stepPath, Validators: validators})
		}
		vr := validate.RunValidators(step.Validate, e.Config, e.Cook.WorktreeDir, dc, e.StateBag, stepPath)
		writeValidationArtifact(e.Cook.CookDir, stepPath, attempt, vr)
		validationArtifactRel := filepath.Join("artifacts", ledger.ArtifactName(stepPath, attempt, "validation", "json"))
		if vr.Pass {
			if e.Cook.Ledger != nil {
				_ = e.Cook.Ledger.Emit(ledger.ValidationPassed{Step: stepPath, Artifact: validationArtifactRel})
			}
			exec.Status = StepPass
			exec.CommitHash = dc.HeadCommit
			exec.FinishedAt = time.Now()
			e.Steps = append(e.Steps, exec)
			e.setLastStepOutcome(stepPath, attempt, "pass", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, outputValue, true)
			detail := buildValidationPassDetail(outputMode, outputValue, dc, vr)
			fmt.Fprintf(os.Stderr, "[%s]\tpass\t%s\n", stepPath, detail)
			_, nSkipped := countValidationPassedSkipped(vr)
			if nSkipped > 0 {
				formatValidationDetails(os.Stderr, stepPath, vr)
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
			_ = e.Cook.Ledger.Emit(ledger.ValidationFailed{Step: stepPath, Reason: reason, Artifact: validationArtifactRel})
		}
		exec.ValidateError = strings.Join(errParts, "\n---\n")
		exec.ValidateDiff = dc.Patch
		exec.Status = StepFatal
		exec.FinishedAt = time.Now()
		e.Steps = append(e.Steps, exec)
		e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, outputValue, true)
		formatValidationDetails(os.Stderr, stepPath, vr)
		RetryValidationFailed("validation failed: "+strings.Join(failedNames, ", "), "")
		return fmt.Errorf("step %s: validation failed: %s", step.Name, strings.Join(failedNames, ", ")), preStepCommit
	}

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
	return nil, ""
}

// runAtomicStepWithVars runs one atomic step with optional extra template vars (e.g. for replan sub-tasks). No retry.
func (e *Engine) runAtomicStepWithVars(step *recipe.Step, stepPath string, taskContext *plan.Task, lastSessionByAgent map[string]string, parentSession recipe.SessionConfig, extraVars map[string]string) error {
	err, _ := e.runAtomicStepOnce(step, stepPath, taskContext, lastSessionByAgent, parentSession, 1, "", nil, extraVars)
	e.emitStepCompletedFromLast()
	return err
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
		if strings.HasPrefix(norm, ".pudding/") {
			continue
		}
		switch norm {
		case "AGENTS.md", "CLAUDE.md", "GEMINI.md", "QWEN.md":
			continue
		}
		if strings.HasSuffix(norm, ".stub") {
			continue
		}
		// Provider context backups live next to CLAUDE.md; they are Pudding metadata, not task edits.
		if strings.HasPrefix(norm, ".pudding-original-") {
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

// buildVars builds template variables; errorContext and extraVars are optional (retry and replan sub-tasks).
func (e *Engine) buildVars(taskContext *plan.Task, errorContext *ErrorContext, extraVars map[string]string) map[string]string {
	vars := map[string]string{
		"spec":  e.SpecContent,
		"error": "",
		"diff":  "",
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

func (e *Engine) resolveSubSteps(step *recipe.Step) ([]recipe.Step, error) {
	if len(step.Steps) > 0 {
		return step.Steps, nil
	}
	if step.Recipe == "" {
		return nil, fmt.Errorf("foreach has no steps or recipe")
	}
	resolved, err := recipe.Resolve(step.Recipe, e.Cook.RepoRoot)
	if err != nil {
		return nil, err
	}
	recipeDir := ""
	if resolved.Path != "" {
		recipeDir = filepath.Dir(resolved.Path)
	}
	parsed, err := recipe.Parse(resolved.Raw, recipeDir)
	if err != nil {
		return nil, err
	}
	if errs := recipe.Validate(parsed); len(errs) > 0 {
		return nil, errs[0]
	}
	return parsed.Steps, nil
}

// formatStreamEventToTerminal prints a single line for assistant text, [tool] name, or [result]; best-effort, no error on parse failure.
func formatStreamEventToTerminal(ev agent.StreamEvent, agentName string) {
	switch ev.Type {
	case "system", "rate_limit_event", "result":
		return
	case "assistant":
		formatAssistantToTerminal(ev.Raw, agentName)
	case "user":
		formatUserToTerminal(ev.Raw, agentName)
	default:
		// raw or unknown: skip so we don't flood stderr
	}
}

func formatAssistantToTerminal(raw []byte, agentName string) {
	prefix := agentName
	if i := strings.Index(agentName, "-"); i > 0 {
		prefix = agentName[:i]
	}
	switch prefix {
	case "codex":
		formatCodexAssistantToTerminal(raw)
		return
	case "gemini":
		formatGeminiAssistantToTerminal(raw)
		return
	case "qwen":
		formatQwenAssistantToTerminal(raw)
		return
	case "opencode":
		// OpenCode uses file-backed stdout; Stream returns closed channel so no real-time lines.
		return
	}
	var msg struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Name string `json:"name"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &msg) != nil {
		return
	}
	for _, c := range msg.Message.Content {
		if c.Type == "text" && c.Text != "" {
			fmt.Fprintf(os.Stderr, "░░░░░ %s\n", TruncateStreamMessage(strings.TrimSpace(c.Text), streamTruncMessage))
		}
		if c.Type == "tool_use" && c.Name != "" {
			fmt.Fprintf(os.Stderr, "░░░░░ %s\n", c.Name)
		}
	}
}

func formatQwenAssistantToTerminal(raw []byte) {
	var msg struct {
		Message *struct {
			Content []struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Name  string `json:"name"`
				Input *struct {
					FilePath string `json:"file_path"`
				} `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &msg) != nil || msg.Message == nil {
		return
	}
	for _, c := range msg.Message.Content {
		if c.Type == "text" && c.Text != "" {
			fmt.Fprintf(os.Stderr, "░░░░░ %s\n", TruncateStreamMessage(strings.TrimSpace(c.Text), streamTruncMessage))
		}
		if c.Type == "tool_use" && c.Name != "" {
			extra := ""
			if c.Input != nil && c.Input.FilePath != "" {
				extra = " " + c.Input.FilePath
			}
			fmt.Fprintf(os.Stderr, "░░░░░ %s%s\n", c.Name, extra)
		}
	}
}

func formatCodexAssistantToTerminal(raw []byte) {
	var ev struct {
		Type string `json:"type"`
		Item *struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Command string `json:"command"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &ev) != nil || ev.Item == nil {
		return
	}
	if ev.Item.Type == "agent_message" && ev.Item.Text != "" {
		fmt.Fprintf(os.Stderr, "░░░░░ %s\n", TruncateStreamMessage(strings.TrimSpace(ev.Item.Text), streamTruncMessage))
	}
	if ev.Item.Type == "command_execution" && ev.Item.Command != "" {
		fmt.Fprintf(os.Stderr, "░░░░░ shell: %s\n", TruncateStreamShell(ev.Item.Command, streamTruncShell))
	}
}

func formatGeminiAssistantToTerminal(raw []byte) {
	var ev struct {
		Type     string `json:"type"`
		Content  string `json:"content"`
		ToolName string `json:"tool_name"`
		Params *struct {
			FilePath string `json:"file_path"`
		} `json:"parameters"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return
	}
	if ev.Content != "" {
		fmt.Fprintf(os.Stderr, "░░░░░ %s\n", TruncateStreamMessage(strings.TrimSpace(ev.Content), streamTruncMessage))
	}
	if ev.ToolName != "" {
		extra := ""
		if ev.Params != nil && ev.Params.FilePath != "" {
			extra = " " + ev.Params.FilePath
		}
		fmt.Fprintf(os.Stderr, "░░░░░ %s%s\n", ev.ToolName, extra)
	}
}

func formatUserToTerminal(raw []byte, agentName string) {
	prefix := agentName
	if i := strings.Index(agentName, "-"); i > 0 {
		prefix = agentName[:i]
	}
	if prefix == "qwen" {
		var msg struct {
			Message *struct {
				Content []struct {
					Type    string `json:"type"`
					IsError bool   `json:"is_error"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &msg) != nil || msg.Message == nil {
			return
		}
		for _, c := range msg.Message.Content {
			if c.Type == "tool_result" {
				if Verbose {
					if c.IsError {
						fmt.Fprintf(os.Stderr, "░░░░░ result: error\n")
					} else {
						fmt.Fprintf(os.Stderr, "░░░░░ result: ok\n")
					}
				}
				return
			}
		}
		return
	}
	if prefix == "opencode" {
		return
	}
	if prefix == "codex" {
		var ev struct {
			Type string `json:"type"`
			Item *struct {
				Type     string `json:"type"`
				ExitCode *int   `json:"exit_code"`
			} `json:"item"`
		}
		if json.Unmarshal(raw, &ev) != nil || ev.Item == nil {
			return
		}
		if Verbose {
			if ev.Item.Type == "command_execution" {
				exit := "?"
				if ev.Item.ExitCode != nil {
					exit = fmt.Sprintf("%d", *ev.Item.ExitCode)
				}
				fmt.Fprintf(os.Stderr, "░░░░░ result: exit:%s\n", exit)
			}
			if ev.Item.Type == "file_change" {
				fmt.Fprintf(os.Stderr, "░░░░░ result: file_change\n")
			}
		}
		return
	}
	if prefix == "gemini" {
		var ev struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		}
		if json.Unmarshal(raw, &ev) != nil {
			return
		}
		fmt.Fprintf(os.Stderr, "[result] %s\n", ev.Status)
		return
	}
	var body struct {
		ToolUseResult *struct {
			Type     string `json:"type"`
			FilePath string `json:"filePath"`
		} `json:"tool_use_result"`
	}
	if json.Unmarshal(raw, &body) != nil || body.ToolUseResult == nil {
		return
	}
	t := body.ToolUseResult.Type
	if t == "" {
		t = "result"
	}
	fmt.Fprintf(os.Stderr, "[result] %s %s\n", t, body.ToolUseResult.FilePath)
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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
	fmt.Fprintf(w, "[pudding] %s done %s | %s%s | %d %s\n", stepName, durStr, costStr, ctxStr, numTurns, turnStr)
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
		status = "fatal"
	}
	durationMs := int(time.Since(e.cookRunStartedAt).Milliseconds())

	stateJSON, _ := e.StateBag.Serialize()
	_ = os.WriteFile(filepath.Join(cookDir, "state-bag.json"), stateJSON, 0644)

	var finalDiffPatch []byte
	dc, err := e.Cook.FinalDiff()
	if err == nil && dc != nil {
		finalDiffPatch = []byte(dc.Patch)
	}
	finalDiffRel, _ := e.Cook.Ledger.WriteArtifact("final-diff.patch", finalDiffPatch)

	artifacts := map[string]string{"state_bag": "state-bag.json"}
	if finalDiffRel != "" {
		artifacts["final_diff"] = finalDiffRel
	}
	_ = e.Cook.Ledger.Emit(ledger.CookCompleted{
		Status:       status,
		DurationMs:   durationMs,
		TotalCostUSD: e.totalCostUSD,
		Steps:        e.stepCompletedCount,
		Retries:      e.retryTriggeredCount,
		Artifacts:    artifacts,
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
