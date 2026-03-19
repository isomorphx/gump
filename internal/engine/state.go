package engine

import "time"

// StepStatus represents the state of a step in the workflow; transitions are driven by the engine.
// We keep pending/running/pass/fatal/skipped so step 6 can add retry transitions and reporting can show progress.
type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepPass    StepStatus = "pass"
	StepFatal   StepStatus = "fatal"
	StepSkipped StepStatus = "skipped"
)

// StepExecution records one step run for tracing and reporting; written to the engine journal and optionally to cook.
// StepPath is the fully-qualified path so we can correlate retries with the same logical step and reuse its session when session: reuse-on-retry.
// OutputMode is "diff", "plan", "artifact", or "" (validation-only). SessionID is set for resolveTargetedSession and reuse-on-retry.
// ValidateError and ValidateDiff are set on validation failure so retry (step 6) can inject {error} and {diff}.
// Attempt and RetryStrategy record which try this was and which strategy (same/escalate/replan) applied for reporting.
type StepExecution struct {
	StepPath      string // fully-qualified path, e.g. "implement/task-1/dev"
	StepName      string
	OutputMode    string // "diff", "plan", "artifact", "" (validation pure)
	TaskName      string
	Attempt       int
	RetryStrategy string // "same", "escalate", "replan", or "" (first run)
	Status        StepStatus
	Agent         string
	SessionID     string // from agent result, for session: reuse: <target> and reuse-on-retry
	CommitHash    string
	StartedAt     time.Time
	FinishedAt    time.Time
	ValidateError string // aggregated stderr of failed validators
	ValidateDiff  string // worktree diff at fail time for retry context
}
