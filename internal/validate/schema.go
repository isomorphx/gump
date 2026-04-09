package validate

import (
	"fmt"

	"github.com/isomorphx/gump/internal/plan"
	"github.com/isomorphx/gump/internal/state"
)

const planSchemaArg = "plan"
const reviewSchemaArg = "review"

// RunSchemaValidator ensures the plan in the State Bag is valid JSON and passes schema checks so foreach can rely on it.
func RunSchemaValidator(stepPath string, st *state.State) *SingleResult {
	raw := ""
	if st != nil {
		raw = st.Get(stepPath + ".output")
	}
	if raw == "" {
		return &SingleResult{
			Validator: "schema: plan",
			Pass:      false,
			Stderr:    fmt.Sprintf("schema: no plan output found for step %q", stepPath),
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

// RunReviewSchemaValidator checks validate step output in the State Bag (output "true"/"false" and optional .comments).
func RunReviewSchemaValidator(stepPath string, st *state.State) *SingleResult {
	raw := ""
	if st != nil {
		raw = st.Get(stepPath + ".output")
	}
	if raw != "true" && raw != "false" {
		return &SingleResult{
			Validator: "schema: review",
			Pass:      false,
			Stderr:    fmt.Sprintf("schema: no validate output (true/false) found for step %q", stepPath),
		}
	}
	if raw == "false" {
		comments := ""
		if st != nil {
			comments = st.Get(stepPath + ".comments")
		}
		if comments == "" {
			return &SingleResult{
				Validator: "schema: review",
				Pass:      false,
				Stderr:    "schema: validate output is false but .comments is empty",
			}
		}
		return &SingleResult{Validator: "schema: review", Pass: false, Stderr: comments}
	}
	return &SingleResult{Validator: "schema: review", Pass: true}
}

// RunSchemaValidatorWithArg runs schema validation; Arg is "plan" (default), or "review" for validate-type steps.
func RunSchemaValidatorWithArg(stepPath string, st *state.State, arg string) *SingleResult {
	if arg == "" {
		arg = planSchemaArg
	}
	switch arg {
	case planSchemaArg:
		return RunSchemaValidator(stepPath, st)
	case reviewSchemaArg:
		return RunReviewSchemaValidator(stepPath, st)
	default:
		return &SingleResult{
			Validator: "schema: " + arg,
			Pass:      false,
			Stderr:    fmt.Sprintf("schema: unknown schema %q, supported: plan, review", arg),
		}
	}
}
