package validate

import "time"

// ValidationResult holds the aggregate outcome of all validators for a step.
// We aggregate so the retry (step 6) can show one combined {error} instead of losing context after the first failure.
type ValidationResult struct {
	Pass    bool
	Results []SingleResult
}

// SingleResult is one validator run: shell exit code and captured output for diagnostics and {error}.
// When Skipped is true, the validator was not run (e.g. tool not installed); Pass is also true so the step does not fail.
type SingleResult struct {
	Validator string
	Pass      bool
	Skipped   bool // true when validator was skipped (optional tool not installed)
	ExitCode  int
	Stdout    string
	Stderr    string
	Duration  time.Duration
}
