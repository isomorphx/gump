package validate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/isomorphx/gump/internal/plan"
	"github.com/isomorphx/gump/internal/statebag"
)

const planSchemaArg = "plan"
const reviewSchemaArg = "review"

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

// RunReviewSchemaValidator checks review step output in the State Bag (pass boolean + comment string).
func RunReviewSchemaValidator(stepPath string, stateBag *statebag.StateBag) *SingleResult {
	stepName := stepPath
	if idx := strings.LastIndex(stepPath, "/"); idx >= 0 {
		stepName = stepPath[idx+1:]
	}
	raw := stateBag.Get(stepName, stepPath, "output")
	if raw == "" {
		return &SingleResult{
			Validator: "schema: review",
			Pass:      false,
			Stderr:    fmt.Sprintf("schema: no review output found for step %q", stepName),
		}
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return &SingleResult{Validator: "schema: review", Pass: false, Stderr: "schema: " + err.Error()}
	}
	_, hasPass := m["pass"]
	cv, hasComment := m["comment"]
	if !hasPass || !hasComment {
		return &SingleResult{Validator: "schema: review", Pass: false, Stderr: "schema: review output must include pass and comment"}
	}
	if _, ok := cv.(string); !ok {
		return &SingleResult{Validator: "schema: review", Pass: false, Stderr: "schema: comment must be a string"}
	}
	return &SingleResult{Validator: "schema: review", Pass: true}
}

// RunSchemaValidatorWithArg runs schema validation; Arg is "plan" (default), or "review" for review steps.
func RunSchemaValidatorWithArg(stepPath string, stateBag *statebag.StateBag, arg string) *SingleResult {
	if arg == "" {
		arg = planSchemaArg
	}
	switch arg {
	case planSchemaArg:
		return RunSchemaValidator(stepPath, stateBag)
	case reviewSchemaArg:
		return RunReviewSchemaValidator(stepPath, stateBag)
	default:
		return &SingleResult{
			Validator: "schema: " + arg,
			Pass:      false,
			Stderr:    fmt.Sprintf("schema: unknown schema %q, supported: plan, review", arg),
		}
	}
}
