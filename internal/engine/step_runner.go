package engine

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/isomorphx/gump/internal/agent"
	"github.com/isomorphx/gump/internal/brand"
	pkgcontext "github.com/isomorphx/gump/internal/context"
	"github.com/isomorphx/gump/internal/diff"
	"github.com/isomorphx/gump/internal/ledger"
	"github.com/isomorphx/gump/internal/plan"
	"github.com/isomorphx/gump/internal/sandbox"
	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/template"
	"github.com/isomorphx/gump/internal/workflow"
)

// buildRetryValidatorInvoker is used so retry validate: conditions can run nested workflows without duplicating runner wiring (R5).
func (e *Engine) buildRetryValidatorInvoker(stepPath string, resolveCtx *state.ResolveContext) ValidatorInvoker {
	return &ValidatorInvokerImpl{
		SubRunner:   &SubWorkflowRunner{ParentEngine: e},
		StepPath:    stepPath,
		WorktreeDir: e.Run.WorktreeDir,
		ResolveCtx:  resolveCtx,
	}
}

// retryEval is the retry policy object used in the atomic-step loop (live *RetryEvaluator or a test double).
type retryEval interface {
	Evaluate(attempt int, gateStates map[string]gatePassState, validator ValidatorInvoker, resolveCtx *state.ResolveContext) (*RetryDecision, error)
}

// retryEvalFactory builds retryEval for a step; tests replace it to inject mocks.
var retryEvalFactory func(step *workflow.Step, stepPath string) retryEval

// retryEvaluateHook wraps Evaluate in tests; when set, the hook decides the decision (may delegate to the real evaluator).
var retryEvaluateHook func(real retryEval, attempt int, gateStates map[string]gatePassState, validator ValidatorInvoker, resolveCtx *state.ResolveContext) (*RetryDecision, error)

func makeRetryEvalForStep(step *workflow.Step, stepPath string) retryEval {
	if retryEvalFactory != nil {
		return retryEvalFactory(step, stepPath)
	}
	return NewRetryEvaluator(step.Retry, stepPath, strings.TrimSpace(step.Agent))
}

func evaluateRetryAttempt(eval retryEval, attempt int, gateStates map[string]gatePassState, inv ValidatorInvoker, resolveCtx *state.ResolveContext) (*RetryDecision, error) {
	if retryEvaluateHook != nil {
		return retryEvaluateHook(eval, attempt, gateStates, inv, resolveCtx)
	}
	return eval.Evaluate(attempt, gateStates, inv, resolveCtx)
}

// atomicRetryApply carries R4 sticky overrides into GET/RUN for one retry attempt.
type atomicRetryApply struct {
	agent             string
	promptTemplate    string
	forceFreshSession bool
}

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

func stepLedgerType(step *workflow.Step) string {
	if step == nil {
		return ""
	}
	if t := strings.TrimSpace(step.Type); t != "" {
		return t
	}
	return engineOutputToStepType(step.OutputMode())
}

func (e *Engine) snapshotForAtomicStep(worktreeNone bool, preStepCommit, stepName, taskName string, attempt int) (*diff.DiffContract, error) {
	if worktreeNone {
		return &diff.DiffContract{
			Patch:        "",
			FilesChanged: nil,
			BaseCommit:   preStepCommit,
			HeadCommit:   preStepCommit,
		}, nil
	}
	return e.Run.Snapshot(stepName, taskName, attempt)
}

func (e *Engine) runAtomicStep(step *workflow.Step, stepPath string, taskContext *plan.Task, lastSessionByAgent map[string]string, parentSession workflow.SessionConfig, groupAgentOverride string, inheritedVars map[string]string) error {
	override0 := groupAgentOverride
	if !step.ShouldRunWithRetryLoop() {
		var errCtx *ErrorContext
		if c := e.consumePendingRestartFrom(stepPath); c != nil {
			c.FromRestart = true
			errCtx = c
		}
		err, _ := e.runAtomicStepOnce(step, stepPath, taskContext, lastSessionByAgent, parentSession, 1, override0, errCtx, nil, inheritedVars, nil)
		e.emitStepCompletedFromLast()
		return err
	}
	maxA := step.MaxAttempts()
	var errCtxStart *ErrorContext
	if c := e.consumePendingRestartFrom(stepPath); c != nil {
		c.FromRestart = true
		errCtxStart = c
	}
	err, preCommit := e.runAtomicStepOnce(step, stepPath, taskContext, lastSessionByAgent, parentSession, 1, override0, errCtxStart, nil, inheritedVars, nil)
	if err == nil {
		e.emitStepCompletedFromLast()
		return nil
	}
	errCtx := e.lastValidationErrorContext()
	if errCtx != nil {
		errCtx.Attempt = 1
	}
	eval := makeRetryEvalForStep(step, stepPath)
	// WHY: Evaluate(maxA+1) must run after the last failed attempt so exit: caps match R3 without an extra agent run.
	for attempt := 2; attempt <= maxA+1; attempt++ {
		gateStates := GateStatesFromStep(e.State, stepPath, step.Gate)
		resolveCtx := e.newTemplateCtx(stepPath, step, taskContext, errCtx, attempt, inheritedVars, nil)
		decision, evalErr := evaluateRetryAttempt(eval, attempt, gateStates, e.buildRetryValidatorInvoker(stepPath, resolveCtx), resolveCtx)
		if evalErr != nil {
			if e.Run.Ledger != nil {
				_ = e.Run.Ledger.Emit(ledger.CircuitBreaker{Step: e.ledgerStepPath(stepPath), Scope: "step", Reason: evalErr.Error(), TotalAttempts: attempt - 1})
			}
			e.emitStepCompletedFromLast()
			return fmt.Errorf("step %s: %w", step.Name, evalErr)
		}
		if decision.Action == "fatal" {
			if e.Run.Ledger != nil {
				_ = e.Run.Ledger.Emit(ledger.CircuitBreaker{Step: e.ledgerStepPath(stepPath), Scope: "step", Reason: "all attempts exhausted", TotalAttempts: maxA})
			}
			e.emitStepCompletedFromLast()
			fmt.Fprintf(os.Stderr, "        ✗ FATAL: all attempts exhausted (%d attempts)\n", maxA)
			return fmt.Errorf("step %s: all attempts exhausted (%d attempts)", step.Name, maxA)
		}
		if attempt > maxA {
			break
		}
		e.State.RotatePrev(stepPath)
		if strings.EqualFold(strings.TrimSpace(decision.Worktree), "reset") {
			if rerr := e.Run.ResetTo(preCommit); rerr != nil {
				return fmt.Errorf("retry worktree reset: %w", rerr)
			}
		}
		if e.Run.Ledger != nil {
			var mePtr *int
			if decision.MatchedEntry >= 0 {
				v := decision.MatchedEntry
				mePtr = &v
			}
			ov := ledgerOverridesFromDecision(decision)
			if ov == nil {
				ov = map[string]string{}
			}
			_ = e.Run.Ledger.Emit(ledger.RetryTriggered{Step: e.ledgerStepPath(stepPath), Attempt: attempt, Overrides: ov, MatchedEntry: mePtr})
			e.retryTriggeredCount++
			e.State.IncrementRunRetries()
		}
		RetryTriggerLine(fmt.Sprintf("retry attempt %d/%d", attempt, maxA))
		if errCtx != nil {
			errCtx.Attempt = attempt
			errCtx.Strategy = "retry"
		}
		var retryApply *atomicRetryApply
		if strings.TrimSpace(decision.Agent) != "" || strings.TrimSpace(decision.Prompt) != "" ||
			strings.EqualFold(strings.TrimSpace(decision.Session), "new") {
			retryApply = &atomicRetryApply{
				agent:             strings.TrimSpace(decision.Agent),
				promptTemplate:    strings.TrimSpace(decision.Prompt),
				forceFreshSession: strings.EqualFold(strings.TrimSpace(decision.Session), "new"),
			}
		}
		prevForce := e.forceSessionReuse
		e.forceSessionReuse = true
		err, preCommit = e.runAtomicStepOnce(step, stepPath, taskContext, lastSessionByAgent, parentSession, attempt, override0, errCtx, nil, inheritedVars, retryApply)
		e.forceSessionReuse = prevForce
		if err == nil {
			e.emitStepCompletedFromLast()
			return nil
		}
		if errCtx != nil {
			if ctx := e.lastValidationErrorContext(); ctx != nil {
				errCtx.Error = ctx.Error
				errCtx.Diff = ctx.Diff
				errCtx.ReviewComment = ctx.ReviewComment
			}
		}
	}
	if e.Run.Ledger != nil {
		_ = e.Run.Ledger.Emit(ledger.CircuitBreaker{Step: e.ledgerStepPath(stepPath), Scope: "step", Reason: "all attempts exhausted", TotalAttempts: maxA})
	}
	e.emitStepCompletedFromLast()
	fmt.Fprintf(os.Stderr, "        ✗ FATAL: all attempts exhausted (%d attempts)\n", maxA)
	return fmt.Errorf("step %s: all attempts exhausted (%d attempts)", step.Name, maxA)
}

// runAtomicStepOnce runs one attempt of an atomic step; returns (err, preStepCommit) so retry can reset. preStepCommit is captured at start.
func (e *Engine) runAtomicStepOnce(step *workflow.Step, stepPath string, taskContext *plan.Task, lastSessionByAgent map[string]string, parentSession workflow.SessionConfig, attempt int, agentOverride string, errorContext *ErrorContext, extraVars map[string]string, inheritedVars map[string]string, retry *atomicRetryApply) (err error, preStepCommit string) {
	var guardContinueGate bool
	var guardFailPrefix string
	e.lastFailureSource = ""
	preStepCommit, _ = PreStepCommit(e.Run.WorktreeDir)
	maxGlobal := step.MaxAttempts()
	if maxGlobal < 1 {
		maxGlobal = 1
	}
	cur := e.globalStepAttempts[stepPath]
	if cur >= maxGlobal {
		if e.Run.Ledger != nil {
			_ = e.Run.Ledger.Emit(ledger.CircuitBreaker{
				Step: e.ledgerStepPath(stepPath), Scope: "step", Reason: "global step attempts exhausted before agent run", TotalAttempts: cur,
			})
		}
		fmt.Fprintf(os.Stderr, "        ✗ FATAL: global step attempts exhausted for %s (%d/%d)\n", stepPath, cur, maxGlobal)
		return fmt.Errorf("step %s: all %d attempts exhausted", step.Name, maxGlobal), preStepCommit
	}
	e.globalStepAttempts[stepPath]++
	e.writeEngineStepAttemptMarker(step.Name, e.globalStepAttempts[stepPath])
	agentWT := e.Run.WorktreeDir
	gateWT := e.Run.WorktreeDir
	worktreeNone := strings.EqualFold(strings.TrimSpace(step.Worktree), "none")
	var cleanupNone func()
	defer func() {
		if cleanupNone != nil {
			cleanupNone()
		}
	}()
	if worktreeNone {
		if t := strings.TrimSpace(step.Type); t != "validate" && t != "split" {
			return fmt.Errorf("step %s: worktree \"none\" is only supported for type validate or split", step.Name), preStepCommit
		}
		tmp, err := os.MkdirTemp("", "gump-wt-none-*")
		if err != nil {
			return fmt.Errorf("worktree none: %w", err), preStepCommit
		}
		agentWT = tmp
		cleanupNone = func() { _ = os.RemoveAll(tmp) }
		_ = os.MkdirAll(filepath.Join(agentWT, brand.StateDir(), "out"), 0755)
		_ = os.MkdirAll(filepath.Join(agentWT, brand.StateDir(), "artefacts"), 0755)
	}
	taskName := taskContextName(taskContext)
	tctx := e.newTemplateCtx(stepPath, step, taskContext, errorContext, attempt, inheritedVars, extraVars)
	if step.GetWorkflow != nil {
		gw := step.GetWorkflow
		inputs := make(map[string]string, len(gw.With))
		for k, v := range gw.With {
			inputs[k] = v
		}
		swr := &SubWorkflowRunner{ParentEngine: e}
		ledgerP := stepPath + "/" + gw.Name
		childState, gerr := swr.RunSubWorkflow(gw.Path, inputs, agentWT, ledgerP, tctx)
		if gerr != nil {
			return fmt.Errorf("get workflow %q: %w", gw.Path, gerr), preStepCommit
		}
		var lastStepName string
		if resolved, rerr := workflow.Resolve(gw.Path, e.Run.RepoRoot); rerr == nil {
			rd := filepath.Dir(resolved.Path)
			if cw, _, perr := workflow.Parse(resolved.Raw, rd); perr == nil && len(cw.Steps) > 0 {
				lastStepName = cw.Steps[len(cw.Steps)-1].Name
			}
		}
		lo := ""
		if lastStepName != "" {
			lo = childState.Get(lastStepName + ".output")
		}
		e.State.Set(gw.Name+".output", lo)
		for _, k := range childState.Keys() {
			e.State.Set(gw.Name+".state."+k, childState.Get(k))
		}
	}
	outputMode := step.OutputMode()
	promptSrc := step.Prompt
	if retry != nil && strings.TrimSpace(retry.promptTemplate) != "" {
		promptSrc = retry.promptTemplate
	}
	resolvedPrompt := template.Resolve(promptSrc, tctx)

	effectiveSession := workflow.ResolveSessionForEngine(step, parentSession)
	sessionReuse := attempt == 1 && (effectiveSession.Mode == "reuse" || effectiveSession.Mode == "reuse-targeted")

	var taskFiles []string
	if taskContext != nil && len(taskContext.Files) > 0 {
		taskFiles = taskContext.Files
	}
	agentToUse := strings.TrimSpace(step.Agent)
	if e.CookAgentOverride != "" {
		agentToUse = strings.TrimSpace(e.CookAgentOverride)
	}
	if strings.TrimSpace(agentOverride) != "" {
		agentToUse = strings.TrimSpace(agentOverride)
	}
	if retry != nil && strings.TrimSpace(retry.agent) != "" {
		agentToUse = strings.TrimSpace(retry.agent)
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
	agent.RemoveOtherContextFiles(agentWT, contextFile)
	if err := PrepareOutputDir(agentWT); err != nil {
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
		promptOverridden := retry != nil && strings.TrimSpace(retry.promptTemplate) != ""
		escFrom, escTo := "", ""
		if retry != nil {
			if r := strings.TrimSpace(retry.agent); r != "" && r != strings.TrimSpace(step.Agent) {
				escTo = r
				escFrom = step.Agent
			}
		} else if agentOverride != "" {
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
	if err := pkgcontext.Build(engineOutputToStepType(outputMode), resolvedPrompt, step.Context, agentForCtx, e.Config, agentWT, e.SpecContent, taskFiles, contextFile, retryCtx, buildOpts); err != nil {
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
	// WHY: sticky session:new must not be undone by reuse-on-retry resolution in the same attempt.
	if retry == nil || !retry.forceFreshSession {
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
					sessionID = e.resolveTargetedSession(stepPath, effectiveSession.Target, agentToUse)
					if sessionID == "" {
						fmt.Fprintf(os.Stderr, "[%s] session: reuse: %s — target step not found or different agent, using fresh session\n", brand.Lower(), effectiveSession.Target)
					}
				}
			}
		}
	}

	if e.forceSessionReuse && attempt > 1 {
		prevAgent := strings.TrimSpace(e.State.Get(stepPath + ".agent"))
		currAgent := strings.TrimSpace(agentToUse)
		if prevAgent != "" && prevAgent != currAgent {
			fmt.Fprintf(os.Stderr, "[%s] session: retry changed agent (%s → %s), starting new session\n", brand.Lower(), prevAgent, currAgent)
			sessionID = ""
		}
	}

	promptForAgent := resolvedPrompt
	if outputMode == "plan" {
		promptForAgent += "\n[" + brand.Upper() + ":plan]"
	} else if outputMode == "artifact" {
		promptForAgent += "\n[" + brand.Upper() + ":artifact]"
	} else if outputMode == "validate" {
		promptForAgent += "\n[" + brand.Upper() + ":validate]"
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
	if e.Run.Ledger != nil {
		_ = e.Run.Ledger.Emit(ledger.StepStarted{
			Step: e.ledgerStepPath(stepPath), Agent: agentToUse, StepType: stepLedgerType(step), Item: taskName, Attempt: attempt,
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
				Worktree:  agentWT,
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
				Worktree:  agentWT,
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
		if e.Run.Ledger != nil {
			_ = e.Run.Ledger.Emit(ledger.AgentLaunched{
				Step: e.ledgerStepPath(stepPath), CLI: adapter.LastLaunchCLI(), Worktree: agentWT,
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
			if guardRuntime != nil {
				guardRuntime.AddTokens(inDelta, outDelta)
				if g, r, ok := guardRuntime.CheckMaxTokens(); ok {
					guardTriggered = true
					guardName = g
					guardReason = r
					agent.Terminate(proc)
				}
				if g, r, ok := guardRuntime.CheckMaxTime(); ok {
					guardTriggered = true
					guardName = g
					guardReason = r
					agent.Terminate(proc)
				}
				if g, r, ok := guardRuntime.CheckEvent(ev.Raw); ok {
					guardTriggered = true
					guardName = g
					guardReason = r
					agent.Terminate(proc)
				}
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
		if guardRuntime != nil && !guardTriggered {
			guardRuntime.SyncTokensFromResult(result.InputTokens, result.OutputTokens)
			if g, r, ok := guardRuntime.CheckMaxTokens(); ok {
				guardTriggered = true
				guardName = g
				guardReason = r
			}
			if g, r, ok := guardRuntime.CheckMaxTime(); ok {
				guardTriggered = true
				guardName = g
				guardReason = r
			}
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
		e.globalTokens += tokensIn + tokensOut
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
		_ = e.Run.ResetTo(preStepCommit)
		applyRunUsage(partial.CostUSD, partial.InputTokens, partial.OutputTokens, partial.NumTurns, partial.CacheReadTokens, partial.CacheCreationTokens, int(time.Since(exec.StartedAt).Milliseconds()))
		guardContinueGate = true
		guardFailPrefix = fmt.Sprintf("guard %s triggered: %s", guardName, guardReason)
		interruptActiveTurn()
		if e.Run.Ledger != nil {
			_ = e.Run.Ledger.Emit(ledger.GuardTriggered{Step: e.ledgerStepPath(stepPath), Guard: guardName, Reason: guardReason})
			_ = e.Run.Ledger.Emit(ledger.AgentKilled{
				Step:         e.ledgerStepPath(stepPath),
				Reason:       "guard:" + guardName,
				DurationMs:   int(time.Since(exec.StartedAt).Milliseconds()),
				InputTokens:  partial.InputTokens,
				OutputTokens: partial.OutputTokens,
				CostUSD:      partial.CostUSD,
				TurnsPartial: partial.NumTurns,
			})
		}
		e.lastFailureSource = "guard_fail"
	}
	isTimeoutError := result.IsError && (result.ExitCode == -1 || (proc != nil && proc.TimedOut))
	if !guardContinueGate && isTimeoutError && proc != nil {
		partial := proc.PartialMetrics()
		if partial.InputTokens == 0 && partial.OutputTokens == 0 && partial.NumTurns == 0 && partial.CostUSD == 0 {
			partial = *result
		}
		applyRunUsage(partial.CostUSD, partial.InputTokens, partial.OutputTokens, partial.NumTurns, partial.CacheReadTokens, partial.CacheCreationTokens, int(time.Since(exec.StartedAt).Milliseconds()))
		if e.Run.Ledger != nil {
			_ = e.Run.Ledger.Emit(ledger.AgentKilled{
				Step:         e.ledgerStepPath(stepPath),
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
	if !guardContinueGate && e.budgetTracker != nil {
		if w := e.budgetTracker.WarningIfUnavailable(result.CostUSD); w != "" && !e.budgetWarnOnce {
			e.budgetWarnOnce = true
			fmt.Fprintln(os.Stderr, w)
		}
		if err := e.budgetTracker.AddCost(stepPath, result.CostUSD); err != nil {
			var be *BudgetExceededError
			if errors.As(err, &be) && e.Run.Ledger != nil {
				_ = e.Run.Ledger.Emit(be.Event)
			}
			exec.Status = StepFatal
			exec.FinishedAt = time.Now()
			e.Steps = append(e.Steps, exec)
			e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
			fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, err)
			if e.Run.Ledger != nil {
				_ = e.Run.Ledger.Emit(ledger.CircuitBreaker{Step: e.ledgerStepPath(stepPath), Scope: "step", Reason: err.Error(), TotalAttempts: attempt})
			}
			return fmt.Errorf("step %s: %w", step.Name, err), preStepCommit
		}
	}
	// WHY: apply agent usage exactly once per attempt so normal/timeout/guard
	// paths cannot double-count run cost/tokens.
	if !guardContinueGate {
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
		if e.Run.Ledger != nil && proc != nil && !isTimeoutError {
			stdoutName := ledger.ArtifactName(e.ledgerStepPath(stepPath), attempt, "stdout", "log")
			stderrName := ledger.ArtifactName(e.ledgerStepPath(stepPath), attempt, "stderr", "log")
			if b, _ := os.ReadFile(proc.StdoutFile); len(b) > 0 {
				if rel, _ := e.Run.Ledger.WriteArtifact(stdoutName, b); rel != "" {
					agentArtifacts["stdout"] = rel
				}
			}
			if b, _ := os.ReadFile(proc.StderrFile); len(b) > 0 {
				if rel, _ := e.Run.Ledger.WriteArtifact(stderrName, b); rel != "" {
					agentArtifacts["stderr"] = rel
				}
			}
			_ = e.Run.Ledger.Emit(ledger.AgentCompleted{
				Step: e.ledgerStepPath(stepPath), ExitCode: result.ExitCode, DurationMs: result.DurationMs,
				TokensIn: result.InputTokens, TokensOut: result.OutputTokens, CostUSD: result.CostUSD,
				SessionID: result.SessionID, IsError: result.IsError, Artifacts: agentArtifacts,
			})
		}
	}
	if !guardContinueGate && !result.IsError && agentToUse != "pass" {
		if err := e.maybeCheckNoWritePostStep(step, guardRuntime, preStepCommit, agentWT, gateWT); err != nil {
			exec.Status = StepFatal
			exec.FinishedAt = time.Now()
			e.Steps = append(e.Steps, exec)
			e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
			e.lastFailureSource = "guard_fail"
			fmt.Fprintf(os.Stderr, "[%s]\tfail\t%v\n", stepPath, err)
			if e.Run.Ledger != nil {
				_ = e.Run.Ledger.Emit(ledger.GuardTriggered{Step: e.ledgerStepPath(stepPath), Guard: "no_write", Reason: err.Error()})
			}
			return fmt.Errorf("step %s: %w", step.Name, err), preStepCommit
		}
	}
	if !guardContinueGate && result.IsError {
		exec.Status = StepFatal
		exec.FinishedAt = time.Now()
		e.Steps = append(e.Steps, exec)
		e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
		e.lastFailureSource = "gate_fail"
		fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: agent error\n", stepPath)
		return fmt.Errorf("step %s: agent reported error", step.Name), preStepCommit
	}

	if !guardContinueGate && result.SessionID != "" && lastSessionByAgent != nil {
		lastSessionByAgent[agent.AgentPrefix(agentToUse)] = result.SessionID
	}
	if !guardContinueGate {
		exec.SessionID = result.SessionID
	}

	// Extract output from .gump/out/ before snapshot (dir is gitignored).
	var outputValue string
	if guardContinueGate {
		outputValue = ""
	} else {
		switch outputMode {
		case "plan":
			tasks, raw, err := ExtractPlanOutput(agentWT)
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
			text, err := ExtractArtifactOutput(agentWT)
			if err != nil {
				exec.Status = StepFatal
				exec.FinishedAt = time.Now()
				e.Steps = append(e.Steps, exec)
				e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
				fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, err)
				return fmt.Errorf("artifact step failed: %w", err), preStepCommit
			}
			outputValue = text
		case "validate":
			pass, boolStr, comments, raw, err := ParseValidateJSON(agentWT)
			if err != nil {
				dc, snapErr := e.snapshotForAtomicStep(worktreeNone, preStepCommit, step.Name, taskName, attempt)
				if snapErr != nil {
					exec.Status = StepFatal
					exec.FinishedAt = time.Now()
					e.Steps = append(e.Steps, exec)
					e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
					fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, snapErr)
					return fmt.Errorf("snapshot after invalid validate.json: %w", snapErr), preStepCommit
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
					if list, err2 := gitDiffNameOnly(gateWT, dc.BaseCommit, dc.HeadCommit); err2 == nil {
						filesForBag = list
					}
					e.State.Set(stepPath+".comments", "")
					e.State.SetStepOutput(stepPath, "", dc.Patch, filesForBag, result.SessionID)
				}
				if e.globalStepAttempts[stepPath] > step.MaxAttempts() {
					if e.Run.Ledger != nil {
						_ = e.Run.Ledger.Emit(ledger.CircuitBreaker{Step: e.ledgerStepPath(stepPath), Scope: "step", Reason: "budget exhausted after invalid validate.json", TotalAttempts: attempt})
					}
					return fmt.Errorf("step %s: all attempts exhausted", step.Name), preStepCommit
				}
				return fmt.Errorf("step %s: validate step failed: %w", step.Name, err), preStepCommit
			}
			e.State.Set(stepPath+".comments", comments)
			outputValue = boolStr
			if !pass {
				dc, snapErr := e.snapshotForAtomicStep(worktreeNone, preStepCommit, step.Name, taskName, attempt)
				if snapErr != nil {
					exec.Status = StepFatal
					exec.FinishedAt = time.Now()
					e.Steps = append(e.Steps, exec)
					e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
					fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, snapErr)
					return fmt.Errorf("snapshot after failed validate: %w", snapErr), preStepCommit
				}
				filesForBag := dc.FilesChanged
				if list, err2 := gitDiffNameOnly(gateWT, dc.BaseCommit, dc.HeadCommit); err2 == nil {
					filesForBag = list
				}
				e.State.SetStepOutput(stepPath, boolStr, dc.Patch, filesForBag, result.SessionID)
				errMsg := fmt.Sprintf("validate did not pass: %s", comments)
				exec.ValidateError = errMsg
				exec.ValidateDiff = dc.Patch
				exec.ReviewComment = comments
				exec.Status = StepFatal
				exec.FinishedAt = time.Now()
				e.Steps = append(e.Steps, exec)
				e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, outputValue, true)
				e.lastFailureSource = "review_fail"
				fmt.Fprintf(os.Stderr, "[%s]\tFAIL\t%s\n", stepPath, errMsg)
				RetryValidationFailed("validate did not pass", "")
				if e.globalStepAttempts[stepPath] > step.MaxAttempts() {
					if e.Run.Ledger != nil {
						_ = e.Run.Ledger.Emit(ledger.CircuitBreaker{Step: e.ledgerStepPath(stepPath), Scope: "step", Reason: "budget exhausted after failed validate", TotalAttempts: attempt})
					}
					return fmt.Errorf("step %s: all attempts exhausted", step.Name), preStepCommit
				}
				return fmt.Errorf("step %s: validate did not pass", step.Name), preStepCommit
			}
		}
	}

	dc, err := e.snapshotForAtomicStep(worktreeNone, preStepCommit, step.Name, taskName, attempt)
	if err != nil {
		exec.Status = StepFatal
		exec.FinishedAt = time.Now()
		e.Steps = append(e.Steps, exec)
		e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, outputValue, false)
		fmt.Fprintf(os.Stderr, "[%s]\tfatal\tFATAL: %v\n", stepPath, err)
		return fmt.Errorf("snapshot after step %s: %w", step.Name, err), preStepCommit
	}

	filesForBag := dc.FilesChanged
	if list, err := gitDiffNameOnly(gateWT, dc.BaseCommit, dc.HeadCommit); err == nil {
		filesForBag = list
	}
	e.State.SetStepOutput(stepPath, outputValue, dc.Patch, filesForBag, result.SessionID)
	if e.Run.Ledger != nil {
		filesJoined := strings.Join(filesForBag, ", ")
		keyOut := stepPath + ".output"
		artifactRel := ""
		if len(outputValue) >= 1024 {
			name := ledger.ArtifactName(e.ledgerStepPath(stepPath), attempt, "output", "json")
			if outputMode == "artifact" {
				name = ledger.ArtifactName(e.ledgerStepPath(stepPath), attempt, "output", "txt")
			}
			if rel, _ := e.Run.Ledger.WriteArtifact(name, []byte(outputValue)); rel != "" {
				artifactRel = rel
			}
		}
		_ = e.Run.Ledger.Emit(ledger.StateBagUpdated{Key: keyOut, Artifact: artifactRel})
		keyFiles := stepPath + ".files"
		artifactFiles := ""
		if len(filesJoined) >= 1024 {
			name := ledger.ArtifactName(e.ledgerStepPath(stepPath), attempt, "files", "txt")
			if rel, _ := e.Run.Ledger.WriteArtifact(name, []byte(filesJoined)); rel != "" {
				artifactFiles = rel
			}
		}
		_ = e.Run.Ledger.Emit(ledger.StateBagUpdated{Key: keyFiles, Artifact: artifactFiles})
		// WHY: session threads must be auditable alongside output and file lists without a new event type.
		_ = e.Run.Ledger.Emit(ledger.StateBagUpdated{Key: stepPath + ".session_id", Artifact: ""})
	}

	if e.needsHITLBeforeGate(step) && len(step.Gate) > 0 {
		if err := e.hitlPauseStep(stepPath, "before_gate", outputMode, dc.FilesChanged); err != nil {
			return err, preStepCommit
		}
	}

	// Apply task.files blast radius policy (v0.0.4: workflow-level blast_radius removed; always enforce when task.files is set).
	blastMode := "enforce"
	if blastMode != "off" && taskContext != nil && len(taskContext.Files) > 0 {
		repoFiles := filterRepoFilesOnly(gateWT, dc.FilesChanged)
		violators, errMsg := checkBlastRadius(repoFiles, taskContext.Files)
		if len(violators) > 0 {
			if blastMode == "warn" {
				warnMsg := blastRadiusWarningMessage(violators, taskContext.Files)
				if e.Run.Ledger != nil {
					_ = e.Run.Ledger.Emit(ledger.BlastRadiusWarning{Step: e.ledgerStepPath(stepPath), Violators: violators, Allowed: taskContext.Files})
				}
				fmt.Fprintln(os.Stderr, warnMsg)
			} else if blastMode == "enforce" {
				if e.Run.Ledger != nil {
					_ = e.Run.Ledger.Emit(ledger.GateFailed{Step: e.ledgerStepPath(stepPath), Reason: errMsg, Artifact: ""})
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

	if guardContinueGate && len(step.Gate) == 0 {
		exec.ValidateError = guardFailPrefix
		exec.Status = StepFatal
		exec.FinishedAt = time.Now()
		e.Steps = append(e.Steps, exec)
		e.setLastStepOutcome(stepPath, attempt, "guard_failed", int(time.Since(exec.StartedAt).Milliseconds()), nil, outputMode, "", false)
		return fmt.Errorf("step %s: %s", step.Name, guardFailPrefix), preStepCommit
	}
	if len(step.Gate) > 0 {
		err, retPre := e.runStepGateAfterAgent(step, stepPath, attempt, &exec, gateWT, dc, outputMode, outputValue, preStepCommit, guardFailPrefix)
		if err != nil {
			return err, retPre
		}
		return nil, ""
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
	} else if outputMode == "validate" && outputValue != "" {
		detail = "validate.json → " + outputValue
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

// runAtomicStepWithVars runs one atomic step with optional extra template vars. No retry loop.
func (e *Engine) runAtomicStepWithVars(step *workflow.Step, stepPath string, taskContext *plan.Task, lastSessionByAgent map[string]string, parentSession workflow.SessionConfig, extraVars map[string]string) error {
	err, _ := e.runAtomicStepOnce(step, stepPath, taskContext, lastSessionByAgent, parentSession, 1, "", nil, extraVars, nil, nil)
	e.emitStepCompletedFromLast()
	return err
}

func (e *Engine) needsHITLBeforeGate(step *workflow.Step) bool {
	if strings.TrimSpace(step.HITL) == "before_gate" {
		return true
	}
	return e.PauseAfterStep != "" && step.Name == e.PauseAfterStep
}

func (e *Engine) hitlPauseStep(stepPath, position, outputMode string, filesChanged []string) error {
	if strings.TrimSpace(os.Getenv("GUMP_E2E_AUTO_HITL_CONTINUE")) == "1" {
		if e.Run.Ledger != nil {
			_ = e.Run.Ledger.Emit(ledger.HITLPaused{Step: e.ledgerStepPath(stepPath), Position: position})
			_ = e.Run.Ledger.Emit(ledger.HITLResumed{Step: e.ledgerStepPath(stepPath), Action: "continue"})
		}
		return nil
	}
	mode := outputMode
	if mode == "" {
		mode = "diff"
	}
	fstr := strings.Join(filterRepoFilesOnly(e.Run.WorktreeDir, filesChanged), ", ")
	fmt.Fprintf(os.Stderr, "[%s] HITL pause (%s) on step '%s'\n\nResult:\n  Mode:    %s\n  Files:   %s\n\n  Worktree: %s\n  Press Enter to continue, Ctrl+C to abort.\n", brand.Lower(), position, stepPath, mode, fstr, e.Run.WorktreeDir)
	if e.Run.Ledger != nil {
		_ = e.Run.Ledger.Emit(ledger.HITLPaused{Step: e.ledgerStepPath(stepPath), Position: position})
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
		if e.Run.Ledger != nil {
			_ = e.Run.Ledger.Emit(ledger.HITLResumed{Step: e.ledgerStepPath(stepPath), Action: "abort"})
		}
		return ErrCookAborted
	case err := <-readCh:
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}
	if e.Run.Ledger != nil {
		_ = e.Run.Ledger.Emit(ledger.HITLResumed{Step: e.ledgerStepPath(stepPath), Action: "continue"})
	}
	return nil
}

func (e *Engine) hitlPauseAfterSuccess(step *workflow.Step, stepPath string, outputMode string, filesChanged []string) error {
	if strings.TrimSpace(step.HITL) != "after_gate" {
		return nil
	}
	return e.hitlPauseStep(stepPath, "after_gate", outputMode, filesChanged)
}

func (e *Engine) writeEngineStepAttemptMarker(stepName string, n int) {
	p := filepath.Join(e.Run.WorktreeDir, brand.StateDir(), "engine-step-attempt.json")
	_ = os.MkdirAll(filepath.Dir(p), 0755)
	_ = os.WriteFile(p, []byte(fmt.Sprintf(`{"step":%q,"n":%d}`, stepName, n)), 0644)
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
	if e.Run.Ledger == nil || e.lastStep == nil {
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
	e.State.Set(o.stepPath+".attempt", strconv.Itoa(o.attempt))
	e.State.SetStepCheckResult(o.stepPath, checkResult)
	if !e.cookRunStartedAt.IsZero() {
		e.State.SetRunMetric("duration", fmt.Sprintf("%d", int(time.Since(e.cookRunStartedAt).Milliseconds())))
	}
	e.State.SetRunMetric("status", stepStatus)

	artifacts := make(map[string]string)
	lp := e.ledgerStepPath(o.stepPath)
	if o.patch != "" && o.outputMode == "diff" {
		name := ledger.ArtifactName(lp, o.attempt, "diff", "patch")
		if rel, _ := e.Run.Ledger.WriteArtifact(name, []byte(o.patch)); rel != "" {
			artifacts["diff"] = rel
		}
	}
	if (o.outputMode == "plan" || o.outputMode == "artifact") && o.outputValue != "" {
		ext := "json"
		if o.outputMode == "artifact" {
			ext = "txt"
		}
		name := ledger.ArtifactName(lp, o.attempt, "output", ext)
		if rel, _ := e.Run.Ledger.WriteArtifact(name, []byte(o.outputValue)); rel != "" {
			artifacts["output"] = rel
		}
	}
	if o.hasValidation {
		artifacts["validation"] = filepath.Join("artifacts", ledger.ArtifactName(lp, o.attempt, "validation", "json"))
	}
	commit, _ := sandbox.HeadCommit(e.Run.WorktreeDir)
	_ = e.Run.Ledger.Emit(ledger.StepCompleted{Step: lp, Status: o.status, DurationMs: o.durationMs, Commit: commit})
	e.stepCompletedCount++
	e.printCookTotal()
}

// emitStepCompleted writes step artifacts (diff, output) then emits step_completed; used only for validation-only steps (no retry loop).
func (e *Engine) emitStepCompleted(stepPath string, attempt int, status string, durationMs int, dc *diff.DiffContract, outputMode, outputValue string, hasValidation bool) {
	if e.Run.Ledger == nil {
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
	lp := e.ledgerStepPath(stepPath)
	if dc != nil && dc.Patch != "" && outputMode == "diff" {
		name := ledger.ArtifactName(lp, attempt, "diff", "patch")
		if rel, _ := e.Run.Ledger.WriteArtifact(name, []byte(dc.Patch)); rel != "" {
			artifacts["diff"] = rel
		}
	}
	if (outputMode == "plan" || outputMode == "artifact") && outputValue != "" {
		ext := "json"
		if outputMode == "artifact" {
			ext = "txt"
		}
		name := ledger.ArtifactName(lp, attempt, "output", ext)
		if rel, _ := e.Run.Ledger.WriteArtifact(name, []byte(outputValue)); rel != "" {
			artifacts["output"] = rel
		}
	}
	if hasValidation {
		artifacts["validation"] = filepath.Join("artifacts", ledger.ArtifactName(lp, attempt, "validation", "json"))
	}
	commit, _ := sandbox.HeadCommit(e.Run.WorktreeDir)
	_ = e.Run.Ledger.Emit(ledger.StepCompleted{Step: lp, Status: status, DurationMs: durationMs, Commit: commit})
	e.stepCompletedCount++
	e.printCookTotal()
}
