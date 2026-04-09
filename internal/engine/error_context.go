package engine

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
