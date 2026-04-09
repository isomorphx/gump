package engine

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/isomorphx/gump/internal/ledger"
	"github.com/isomorphx/gump/internal/plan"
	"github.com/isomorphx/gump/internal/sandbox"
	"github.com/isomorphx/gump/internal/workflow"
)

// ErrorContext carries failed validation output so the next attempt gets {error} and {diff} in the prompt.
// ReviewComment is set when a review step failed so the retry prompt can surface the reviewer’s feedback.
type ErrorContext struct {
	Error         string
	Diff          string
	ReviewComment string
	Attempt       int
	Strategy      string
	// FromRestart is set when this context was stashed after a restart_from jump so attempt 1 still gets retry markdown.
	FromRestart bool
}

// RunWithRetry runs one atomic step using the legacy-shaped policy synthesized from v0.0.4 retry: until the engine is fully ported.
// runOnce runs the step once and returns (nil, _) on pass, (err, preStepCommit) on failure; preStepCommit is the worktree HEAD at start of runOnce so we can reset before the next attempt.
func (e *Engine) RunWithRetry(step *workflow.Step, scopePath string, taskContext *plan.Task, runOnce func(attempt int, agentOverride string, errorContext *ErrorContext) (err error, preStepCommit string)) error {
	err, preCommit := runOnce(1, "", nil)
	if err == nil {
		e.emitStepCompletedFromLast()
		return nil
	}
	errorContext := e.lastValidationErrorContext()
	if errorContext != nil {
		errorContext.Attempt = 1
	}
	failCountsBySource := map[string]int{}
	attempt := 1

	of := step.OnFailureCompat()
	for {
		source := e.lastFailureSource
		if source == "" {
			source = "gate_fail"
		}
		if of == nil {
			break
		}
		action := of.ActionForFailureSource(source)
		if action == nil {
			break
		}
		isConditional := of.IsConditionalForm()
		// Flat form keeps legacy behavior: retry is total attempts.
		// Conditional form interprets retry as extra retries per source.
		attemptLimit := action.Retry
		if !isConditional && attemptLimit <= 0 {
			attemptLimit = 1
		}
		if isConditional && attemptLimit < 0 {
			attemptLimit = 0
		}
		failCountsBySource[source]++
		canRetry := false
		if isConditional {
			canRetry = failCountsBySource[source] <= attemptLimit
		} else {
			canRetry = failCountsBySource[source] < attemptLimit
		}
		expanded := workflow.ExpandStrategy(action.Strategy)
		if strings.TrimSpace(action.RestartFrom) != "" && len(expanded) == 0 {
			// WHY: restart_from-only policy should jump immediately on each failure
			// instead of burning local same-step retries first.
			if canRetry {
				return &ErrRestartFrom{TargetName: strings.TrimSpace(action.RestartFrom), CurrentPath: scopePath}
			}
		}
		if !canRetry {
			if strings.TrimSpace(action.RestartFrom) != "" {
				if len(expanded) == 0 {
					// WHY: restart_from-only reached its attempt ceiling; stop instead of looping forever.
					break
				}
				return &ErrRestartFrom{TargetName: strings.TrimSpace(action.RestartFrom), CurrentPath: scopePath}
			}
			break
		}
		if len(expanded) == 0 {
			expanded = []workflow.StrategyEntryCompat{{Type: "same", Count: 1}}
		}
		idx := failCountsBySource[source] - 1
		if idx >= len(expanded) {
			idx = len(expanded) - 1
		}
		strategy := expanded[idx]
		attempt++
		strategyLabel := strategy.Type
		if strategy.Agent != "" {
			strategyLabel = strategy.Type + ": " + strategy.Agent
		}
		if e.Cook.Ledger != nil {
			_ = e.Cook.Ledger.Emit(ledger.RetryTriggered{Step: scopePath, Attempt: attempt, Strategy: strategyLabel, Scope: "step"})
			e.retryTriggeredCount++
			e.State.IncrementRunRetries()
		}
		displayMax := attemptLimit
		if isConditional {
			displayMax = attemptLimit + 1
		}
		if displayMax <= 0 {
			displayMax = 1
		}
		RetryTriggerLine(fmt.Sprintf("retry attempt %d/%d (%s)", attempt, displayMax, strategyLabel))

		if strategy.Type == "replan" {
			if err := e.Cook.ResetTo(preCommit); err != nil {
				return fmt.Errorf("worktree reset before replan: %w", err)
			}
			RetryTriggerLine(fmt.Sprintf("replan (%s) — decomposing into sub-tasks", strategy.Agent))
			if replanErr := e.ExecuteReplan(strategy.Agent, step, scopePath, errorContext, taskContext); replanErr != nil {
				if errorContext != nil {
					errorContext.Error = e.lastValidationError()
					errorContext.Diff = e.lastValidationDiff()
				}
				err = replanErr
				continue
			}
			if e.Cook.Ledger != nil {
				commit, _ := sandbox.HeadCommit(e.Cook.WorktreeDir)
				_ = e.Cook.Ledger.Emit(ledger.StepCompleted{Step: scopePath, Status: "pass", DurationMs: 0, Artifacts: map[string]string{}, Commit: commit})
				e.stepCompletedCount++
				e.printCookTotal()
			}
			return nil
		}

		if strategy.Type == "escalate" {
			if err := e.Cook.ResetTo(preCommit); err != nil {
				return fmt.Errorf("worktree reset before retry: %w", err)
			}
		}
		if errorContext != nil {
			errorContext.Attempt = attempt
			errorContext.Strategy = strategyLabel
		}

		prevForce := e.forceSessionReuse
		e.forceSessionReuse = strategy.Type == "same"
		err, preCommit = runOnce(attempt, strategy.Agent, errorContext)
		e.forceSessionReuse = prevForce
		if err == nil {
			e.emitStepCompletedFromLast()
			return nil
		}
		if errorContext != nil {
			if ctx := e.lastValidationErrorContext(); ctx != nil {
				errorContext.Error = ctx.Error
				errorContext.Diff = ctx.Diff
				errorContext.ReviewComment = ctx.ReviewComment
			}
		}
	}

	if e.Cook.Ledger != nil {
		_ = e.Cook.Ledger.Emit(ledger.CircuitBreaker{
			Step: scopePath, Scope: "step", Reason: "all attempts exhausted", TotalAttempts: attempt,
		})
	}
	e.emitStepCompletedFromLast()
	fmt.Fprintf(os.Stderr, "        ✗ FATAL: all attempts exhausted (%d attempts)\n", attempt)
	return fmt.Errorf("step %s: all attempts exhausted (%d attempts)", step.Name, attempt)
}

func (e *Engine) lastValidationErrorContext() *ErrorContext {
	for i := len(e.Steps) - 1; i >= 0; i-- {
		s := &e.Steps[i]
		if s.ValidateError != "" || s.ValidateDiff != "" || s.ReviewComment != "" {
			return &ErrorContext{Error: s.ValidateError, Diff: s.ValidateDiff, ReviewComment: s.ReviewComment}
		}
	}
	return nil
}

func (e *Engine) lastValidationError() string {
	for i := len(e.Steps) - 1; i >= 0; i-- {
		if e.Steps[i].ValidateError != "" {
			return e.Steps[i].ValidateError
		}
	}
	return ""
}

func (e *Engine) lastValidationDiff() string {
	for i := len(e.Steps) - 1; i >= 0; i-- {
		if e.Steps[i].ValidateDiff != "" {
			return e.Steps[i].ValidateDiff
		}
	}
	return ""
}

// PreStepCommit returns the current worktree HEAD so a future retry can reset to it.
func PreStepCommit(worktreeDir string) (string, error) {
	return sandbox.HeadCommit(worktreeDir)
}

// IsRestartFrom reports whether err requests a restart_from sibling jump.
func IsRestartFrom(err error) (*ErrRestartFrom, bool) {
	var rf *ErrRestartFrom
	if errors.As(err, &rf) {
		return rf, true
	}
	return nil, false
}
