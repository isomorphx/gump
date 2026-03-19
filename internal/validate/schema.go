package validate

import (
	"fmt"
	"strings"

	"github.com/isomorphx/pudding/internal/plan"
	"github.com/isomorphx/pudding/internal/statebag"
)

const planSchemaArg = "plan"

// RunSchemaValidator ensures the plan in the State Bag is valid JSON and passes schema checks so foreach can rely on it.
func RunSchemaValidator(stepPath string, stateBag *statebag.StateBag) *SingleResult {
	// stepPath is the full path of the step that produced the plan (e.g. "decompose"); we read that step's output.
	stepName := stepPath
	if idx := strings.LastIndex(stepPath, "/"); idx >= 0 {
		stepName = stepPath[idx+1:]
	}
	raw := stateBag.Get(stepName, stepPath, "output")
	if raw == "" {
		return &SingleResult{
			Validator: "schema: plan",
			Pass:      false,
			Stderr:    fmt.Sprintf("schema: no plan output found for step %q", stepName),
		}
	}
	tasks, err := plan.ParsePlanOutput([]byte(raw))
	if err != nil {
		return &SingleResult{Validator: "schema: plan", Pass: false, Stderr: "schema: " + err.Error()}
	}
	if err := plan.ValidatePlanSchema(tasks); err != nil {
		return &SingleResult{Validator: "schema: plan", Pass: false, Stderr: "schema: " + err.Error()}
	}
	return &SingleResult{Validator: "schema: plan", Pass: true}
}

// RunSchemaValidatorWithArg runs schema validation; Arg must be "plan" or empty for step 5.
func RunSchemaValidatorWithArg(stepPath string, stateBag *statebag.StateBag, arg string) *SingleResult {
	if arg == "" {
		arg = planSchemaArg
	}
	if arg != planSchemaArg {
		return &SingleResult{
			Validator: "schema: " + arg,
			Pass:      false,
			Stderr:    fmt.Sprintf("schema: unknown schema %q, only 'plan' is supported", arg),
		}
	}
	return RunSchemaValidator(stepPath, stateBag)
}
