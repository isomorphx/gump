package engine

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/isomorphx/pudding/internal/ledger"
	"github.com/isomorphx/pudding/internal/plan"
	"github.com/isomorphx/pudding/internal/recipe"
	"github.com/isomorphx/pudding/internal/sandbox"
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

// RunWithRetry runs one atomic step with the recipe on_failure policy.
// runOnce runs the step once and returns (nil, _) on pass, (err, preStepCommit) on failure; preStepCommit is the worktree HEAD at start of runOnce so we can reset before the next attempt.
func (e *Engine) RunWithRetry(step recipe.Step, scopePath string, taskContext *plan.Task, runOnce func(attempt int, agentOverride string, errorContext *ErrorContext) (err error, preStepCommit string)) error {
	expanded := step.ExpandedOnFailureStrategy()
	hasRestartOnly := step.OnFailure != nil && strings.TrimSpace(step.OnFailure.RestartFrom) != "" && len(expanded) == 0
	if len(expanded) == 0 && !hasRestartOnly {
		expanded = []recipe.StrategyEntry{{Type: "same", Count: 1}}
	}
	maxAttempts := step.MaxAttempts()
	if maxAttempts <= 1 {
		maxAttempts = 1
	}
	maxRetries := maxAttempts - 1
	if hasRestartOnly {
		// WHY: on_failure with restart_from but no strategy triggers restart on first gate failure (no same/escalate retries).
		maxRetries = 0
	}

	err, preCommit := runOnce(1, "", nil)
	if err == nil {
		e.emitStepCompletedFromLast()
		return nil
	}
	errorContext := e.lastValidationErrorContext()
	if errorContext != nil {
		errorContext.Attempt = 1
	}

	for retryIndex := 0; retryIndex < maxRetries; retryIndex++ {
		idx := retryIndex
		if len(expanded) > 0 {
			if idx >= len(expanded) {
				idx = len(expanded) - 1
			}
		} else {
			idx = 0
		}
		var strategy recipe.StrategyEntry
		if len(expanded) > 0 {
			strategy = expanded[idx]
		} else {
			strategy = recipe.StrategyEntry{Type: "same", Count: 1}
		}
		attempt := retryIndex + 2
		strategyLabel := strategy.Type
		if strategy.Agent != "" {
			strategyLabel = strategy.Type + ": " + strategy.Agent
		}
		if e.Cook.Ledger != nil {
			_ = e.Cook.Ledger.Emit(ledger.RetryTriggered{Step: scopePath, Attempt: attempt, Strategy: strategyLabel, Scope: "step"})
			e.retryTriggeredCount++
		}
		RetryTriggerLine(fmt.Sprintf("retry attempt %d/%d (%s)", attempt, maxAttempts, strategyLabel))

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

		if err := e.Cook.ResetTo(preCommit); err != nil {
			return fmt.Errorf("worktree reset before retry: %w", err)
		}
		if errorContext != nil {
			errorContext.Attempt = attempt
			errorContext.Strategy = strategyLabel
		}

		err, preCommit = runOnce(attempt, strategy.Agent, errorContext)
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

	if step.OnFailure != nil && strings.TrimSpace(step.OnFailure.RestartFrom) != "" {
		return &ErrRestartFrom{TargetName: strings.TrimSpace(step.OnFailure.RestartFrom), CurrentPath: scopePath}
	}

	if e.Cook.Ledger != nil {
		_ = e.Cook.Ledger.Emit(ledger.CircuitBreaker{
			Step: scopePath, Scope: "step", Reason: "all attempts exhausted", TotalAttempts: maxAttempts,
		})
	}
	e.emitStepCompletedFromLast()
	fmt.Fprintf(os.Stderr, "        ✗ FATAL: all %d attempts exhausted\n", maxAttempts)
	return fmt.Errorf("step %s: all %d attempts exhausted", step.Name, maxAttempts)
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
