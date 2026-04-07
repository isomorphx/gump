package validate

import (
	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/diff"
	"github.com/isomorphx/gump/internal/recipe"
	"github.com/isomorphx/gump/internal/statebag"
)

// RunValidators runs every validator for the step in order and aggregates results.
// We do not short-circuit on first failure so the agent (and logs) see all failures in one go at retry time.
func RunValidators(validators []recipe.Validator, cfg *config.Config, worktreeDir string, dc *diff.DiffContract, stateBag *statebag.StateBag, stepPath string) *ValidationResult {
	out := &ValidationResult{Results: make([]SingleResult, 0, len(validators))}
	for _, v := range validators {
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
