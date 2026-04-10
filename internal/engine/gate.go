package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/isomorphx/gump/internal/diff"
	"github.com/isomorphx/gump/internal/ledger"
	"github.com/isomorphx/gump/internal/plan"
	"github.com/isomorphx/gump/internal/sandbox"
	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/validate"
	"github.com/isomorphx/gump/internal/workflow"
)

func (e *Engine) runWorkflowValidateGate(v workflow.GateEntry, stepPath string, gateWT string, resolveCtx *state.ResolveContext) *validate.SingleResult {
	label := "validate:" + strings.TrimSpace(v.Arg)
	if resolveCtx == nil {
		resolveCtx = &state.ResolveContext{
			State: e.State, StepPath: stepPath, Spec: e.SpecContent,
			GateResults: map[string]string{}, GateMeta: map[string]map[string]string{},
		}
	}
	swr := &SubWorkflowRunner{ParentEngine: e}
	inputs := make(map[string]string, len(v.With))
	for k, val := range v.With {
		inputs[k] = val
	}
	ns := stepPath + ".gate." + workflow.ValidatorGateNameFromPath(v.Arg)
	pass, comments, _, err := swr.RunValidator(v.Arg, inputs, ns, gateWT, resolveCtx)
	if err != nil {
		return &validate.SingleResult{Validator: label, Pass: false, Stderr: err.Error()}
	}
	if pass {
		return &validate.SingleResult{Validator: label, Pass: true, Stdout: comments}
	}
	msg := comments
	if msg == "" {
		msg = "validator returned pass=false"
	}
	return &validate.SingleResult{Validator: label, Pass: false, Stdout: comments, Stderr: msg}
}

func (e *Engine) runOneGateEntry(v workflow.GateEntry, stepPath string, gateWT string, dc *diff.DiffContract, resolveCtx *state.ResolveContext) validate.SingleResult {
	switch v.Type {
	case "validate":
		return *e.runWorkflowValidateGate(v, stepPath, gateWT, resolveCtx)
	case "compile":
		return *validate.RunCompileValidator(e.Config, gateWT)
	case "test":
		return *validate.RunTestValidator(e.Config, gateWT)
	case "lint":
		return *validate.RunLintValidator(e.Config, gateWT)
	case "bash":
		return *validate.RunBashValidator(v, gateWT, e.Config)
	case "schema":
		return *validate.RunSchemaValidatorWithArg(stepPath, e.State, v.Arg)
	case "touched":
		return *validate.RunTouchedValidator(v.Arg, dc)
	case "untouched":
		return *validate.RunUntouchedValidator(v.Arg, dc)
	case "tests_found":
		return *validate.RunTestsFoundValidator(e.Config, gateWT)
	case "coverage":
		return *validate.RunCoverageValidator(v.Arg, e.Config, gateWT)
	default:
		return validate.SingleResult{Validator: v.Type, Pass: false, Stderr: "unknown validator type: " + v.Type}
	}
}

func (e *Engine) runAllGateValidators(gates []workflow.GateEntry, stepPath string, gateWT string, dc *diff.DiffContract, resolveCtx *state.ResolveContext) *validate.ValidationResult {
	out := &validate.ValidationResult{Results: make([]validate.SingleResult, 0, len(gates))}
	for _, v := range gates {
		r := e.runOneGateEntry(v, stepPath, gateWT, dc, resolveCtx)
		out.Results = append(out.Results, r)
	}
	allPass := true
	for i := range out.Results {
		if !out.Results[i].Pass {
			allPass = false
			break
		}
	}
	out.Pass = allPass
	return out
}

func gateCheckLabels(gate []workflow.GateEntry) []string {
	checks := make([]string, 0, len(gate))
	for _, v := range gate {
		if v.Arg != "" {
			checks = append(checks, v.Type+":"+v.Arg)
		} else {
			checks = append(checks, v.Type)
		}
	}
	return checks
}

// validationWithoutAgentOpts configures gate-only validation without an agent (FinalDiff + validators).
type validationWithoutAgentOpts struct {
	// fixedSessionLedgerMode, if non-empty, is used as StepStarted.SessionMode instead of sessionModeForLedger.
	fixedSessionLedgerMode string
	// persistStepStatusKeys sets stepPath.status and stepPath.attempt on Run.State when true (sequential top-level).
	persistStepStatusKeys bool
	// finalDiffErrLabel is a short phrase for FinalDiff errors ("gate-only" vs "validation").
	finalDiffErrLabel string
}

// executeValidationWithoutAgent runs gate validators on the cumulative worktree diff (no agent).
// Used for top-level gate-only steps and for nested validation-only steps inside groups / foreach.
func (e *Engine) executeValidationWithoutAgent(step *workflow.Step, stepPath string, taskContext *plan.Task, parentSession workflow.SessionConfig, opts validationWithoutAgentOpts) error {
	e.stepRunIndex++
	startedAt := time.Now()
	taskInfo := ""
	if taskContext != nil && taskContext.Name != "" {
		taskInfo = "[task " + taskContext.Name + "]"
	}
	StepHeader(e.stepRunIndex, e.stepTotalEstimate, stepPath, "validate", taskInfo, "", "")
	sessionMode := opts.fixedSessionLedgerMode
	if sessionMode == "" {
		sessionMode = sessionModeForLedger(step, parentSession)
	}
	lp := e.ledgerStepPath(stepPath)
	if e.Run.Ledger != nil {
		checks := gateCheckLabels(step.Gate)
		_ = e.Run.Ledger.Emit(ledger.StepStarted{Step: lp, Agent: "", StepType: stepLedgerType(step), Item: taskContextName(taskContext), Attempt: 1, SessionMode: sessionMode})
		_ = e.Run.Ledger.Emit(ledger.GateStarted{Step: lp, Checks: checks})
	}
	dc, err := e.Run.FinalDiff()
	if err != nil {
		tn := taskContextName(taskContext)
		e.Steps = append(e.Steps, StepExecution{StepPath: stepPath, StepName: step.Name, TaskName: tn, Attempt: 1, Status: StepFatal, StartedAt: startedAt, FinishedAt: time.Now()})
		if e.Run.Ledger != nil {
			e.emitStepCompleted(stepPath, 1, "fatal", int(time.Since(startedAt).Milliseconds()), nil, "", "", false)
			e.stepCompletedCount++
			e.printRunTotal()
		}
		return fmt.Errorf("final diff for %s step: %w", opts.finalDiffErrLabel, err)
	}
	resolveCtx := e.newTemplateCtx(stepPath, step, taskContext, nil, 1, nil, nil)
	vr := e.runAllGateValidators(step.Gate, stepPath, e.Run.WorktreeDir, dc, resolveCtx)
	validate.ApplyGateResultsToState(e.Run.State, stepPath, step.Gate, vr)
	writeValidationArtifact(e.Run.RunDir, lp, 1, vr)
	validationArtifactRel := filepath.Join("artifacts", ledger.ArtifactName(lp, 1, "validation", "json"))
	if vr.Pass {
		if e.Run.Ledger != nil {
			_ = e.Run.Ledger.Emit(ledger.GatePassed{Step: lp, Artifact: validationArtifactRel})
			commit, _ := sandbox.HeadCommit(e.Run.WorktreeDir)
			_ = e.Run.Ledger.Emit(ledger.StepCompleted{Step: lp, Status: "pass", DurationMs: int(time.Since(startedAt).Milliseconds()), Commit: commit})
			e.stepCompletedCount++
		}
		e.printRunTotal()
		tn := taskContextName(taskContext)
		e.Steps = append(e.Steps, StepExecution{StepPath: stepPath, StepName: step.Name, TaskName: tn, Attempt: 1, Status: StepPass, StartedAt: startedAt, FinishedAt: time.Now()})
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
		if opts.persistStepStatusKeys {
			e.Run.State.Set(stepPath+".status", "pass")
			e.Run.State.Set(stepPath+".attempt", "1")
		}
		return nil
	}
	var failedNames []string
	var errParts []string
	for _, r := range vr.Results {
		if !r.Pass {
			failedNames = append(failedNames, r.Validator)
			errParts = append(errParts, r.Stderr)
		}
	}
	if e.Run.Ledger != nil {
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
		_ = e.Run.Ledger.Emit(ledger.GateFailed{Step: lp, Reason: reason, Artifact: validationArtifactRel})
		commit, _ := sandbox.HeadCommit(e.Run.WorktreeDir)
		_ = e.Run.Ledger.Emit(ledger.StepCompleted{Step: lp, Status: "fatal", DurationMs: int(time.Since(startedAt).Milliseconds()), Commit: commit})
		e.stepCompletedCount++
		e.printRunTotal()
	}
	tn := taskContextName(taskContext)
	e.Steps = append(e.Steps, StepExecution{StepPath: stepPath, StepName: step.Name, TaskName: tn, Attempt: 1, Status: StepFatal, StartedAt: startedAt, FinishedAt: time.Now(), ValidateError: strings.Join(errParts, "\n---\n"), ValidateDiff: dc.Patch})
	formatValidationDetails(os.Stderr, stepPath, vr)
	fmt.Fprintf(os.Stderr, "        ✗ validation failed: %s\n", strings.Join(failedNames, ", "))
	if opts.persistStepStatusKeys {
		e.Run.State.Set(stepPath+".status", "fatal")
		e.Run.State.Set(stepPath+".attempt", "1")
	}
	return fmt.Errorf("step %s: validation failed: %s", step.Name, strings.Join(failedNames, ", "))
}

// executeGateOnlyTopLevel runs gate validators on the current worktree without an agent (R3 sequential).
func (e *Engine) executeGateOnlyTopLevel(step *workflow.Step, stepPath string) error {
	return e.executeValidationWithoutAgent(step, stepPath, nil, workflow.SessionConfig{}, validationWithoutAgentOpts{
		fixedSessionLedgerMode: "new",
		persistStepStatusKeys:  true,
		finalDiffErrLabel:      "gate-only",
	})
}

// runStepGateAfterAgent runs validators when the step defines gates, after a successful agent run and diff snapshot.
// retPre is the pre-step commit to return to the caller when non-empty (HITL paths).
func (e *Engine) runStepGateAfterAgent(step *workflow.Step, stepPath string, attempt int, exec *StepExecution, gateWT string, dc *diff.DiffContract, outputMode, outputValue string, preStepCommit string, guardPrelude string) (err error, retPre string) {
	lp := e.ledgerStepPath(stepPath)
	if e.Run.Ledger != nil {
		_ = e.Run.Ledger.Emit(ledger.GateStarted{Step: lp, Checks: gateCheckLabels(step.Gate)})
	}
	resolveCtx := e.newTemplateCtx(stepPath, step, nil, nil, attempt, nil, nil)
	vr := e.runAllGateValidators(step.Gate, stepPath, gateWT, dc, resolveCtx)
	validate.ApplyGateResultsToState(e.State, stepPath, step.Gate, vr)
	writeValidationArtifact(e.Run.RunDir, lp, attempt, vr)
	validationArtifactRel := filepath.Join("artifacts", ledger.ArtifactName(lp, attempt, "validation", "json"))
	if vr.Pass {
		if e.Run.Ledger != nil {
			_ = e.Run.Ledger.Emit(ledger.GatePassed{Step: lp, Artifact: validationArtifactRel})
		}
		exec.Status = StepPass
		exec.CommitHash = dc.HeadCommit
		exec.FinishedAt = time.Now()
		e.Steps = append(e.Steps, *exec)
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
	if e.Run.Ledger != nil {
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
		_ = e.Run.Ledger.Emit(ledger.GateFailed{Step: lp, Reason: reason, Artifact: validationArtifactRel})
	}
	msg := strings.Join(errParts, "\n---\n")
	if strings.TrimSpace(guardPrelude) != "" {
		if msg != "" {
			msg = guardPrelude + "\n---\n" + msg
		} else {
			msg = guardPrelude
		}
	}
	exec.ValidateError = msg
	exec.ValidateDiff = dc.Patch
	exec.Status = StepFatal
	exec.FinishedAt = time.Now()
	e.Steps = append(e.Steps, *exec)
	e.State.SetStepCheckResult(stepPath, "fail")
	e.State.SetRunMetric("status", "fail")
	e.setLastStepOutcome(stepPath, attempt, "fatal", int(time.Since(exec.StartedAt).Milliseconds()), dc, outputMode, outputValue, true)
	e.lastFailureSource = "gate_fail"
	formatValidationDetails(os.Stderr, stepPath, vr)
	RetryValidationFailed("validation failed: "+strings.Join(failedNames, ", "), "")
	if strings.TrimSpace(step.HITL) == "after_gate" && attempt < step.MaxAttempts() {
		if err := e.hitlPauseStep(stepPath, "after_gate", outputMode, dc.FilesChanged); err != nil {
			return err, preStepCommit
		}
	}
	if e.globalStepAttempts[stepPath] > step.MaxAttempts() {
		if e.Run.Ledger != nil {
			_ = e.Run.Ledger.Emit(ledger.CircuitBreaker{Step: stepPath, Scope: "step", Reason: "budget exhausted after failed gate", TotalAttempts: attempt})
		}
		return fmt.Errorf("step %s: all attempts exhausted", step.Name), preStepCommit
	}
	return fmt.Errorf("step %s: validation failed: %s", step.Name, strings.Join(failedNames, ", ")), preStepCommit
}

// writeValidationArtifact persists validation result under the step’s artifact dir so the ledger can reference it.
func writeValidationArtifact(runDir, stepPath string, attempt int, vr *validate.ValidationResult) {
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
	dir := filepath.Join(runDir, "artifacts")
	_ = os.MkdirAll(dir, 0755)
	_ = os.WriteFile(filepath.Join(dir, name), data, 0644)
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
	if outputMode == "validate" && outputValue != "" {
		parts = append(parts, "validate "+outputValue)
	}
	if outputMode != "plan" && outputMode != "validate" && dc != nil {
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
