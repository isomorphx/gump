package validate

import (
	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/diff"
	"github.com/isomorphx/gump/internal/workflow"
	"github.com/isomorphx/gump/internal/statebag"
)

// RunValidators runs every validator for the step in order and aggregates results.
// We do not short-circuit on first failure so the agent (and logs) see all failures in one go at retry time.
func RunValidators(gates []workflow.GateEntry, cfg *config.Config, worktreeDir string, dc *diff.DiffContract, stateBag *statebag.StateBag, stepPath string) *ValidationResult {
	out := &ValidationResult{Results: make([]SingleResult, 0, len(gates))}
	for _, v := range gates {
		var r *SingleResult
		switch v.Type {
		case "compile":
			r = RunCompileValidator(cfg, worktreeDir)
		case "test":
			r = RunTestValidator(cfg, worktreeDir)
		case "lint":
			r = RunLintValidator(cfg, worktreeDir)
		case "bash":
			r = RunBashValidator(v, worktreeDir, cfg)
		case "schema":
			r = RunSchemaValidatorWithArg(stepPath, stateBag, v.Arg)
		case "touched":
			r = RunTouchedValidator(v.Arg, dc)
		case "untouched":
			r = RunUntouchedValidator(v.Arg, dc)
		case "tests_found":
			r = RunTestsFoundValidator(cfg, worktreeDir)
		case "coverage":
			r = RunCoverageValidator(v.Arg, cfg, worktreeDir)
		case "validate":
			r = &SingleResult{Validator: "validate: " + v.Arg, Pass: true, Skipped: true, Stdout: "validate gate not executed in this release (R1 parser only)"}
		default:
			r = &SingleResult{Validator: v.Type, Pass: false, Stderr: "unknown validator type: " + v.Type}
		}
		out.Results = append(out.Results, *r)
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
