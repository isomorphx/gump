package engine

import (
	"strings"

	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/template"
	"github.com/isomorphx/gump/internal/workflow"
)

// ValidatorInvokerImpl wires retry validate: conditions to nested validator workflows (R5 / ADR-050).
type ValidatorInvokerImpl struct {
	SubRunner   *SubWorkflowRunner
	StepPath    string
	WorktreeDir string
	ResolveCtx  *state.ResolveContext
}

func (vi *ValidatorInvokerImpl) InvokeValidator(workflowPath string, inputs map[string]string) (bool, error) {
	if vi == nil || vi.SubRunner == nil {
		return false, nil
	}
	resolved := make(map[string]string, len(inputs))
	for k, v := range inputs {
		resolved[k] = template.Resolve(v, vi.ResolveCtx)
	}
	ns := vi.StepPath + ".retry_validator." + workflow.ValidatorGateNameFromPath(workflowPath)
	pass, _, child, err := vi.SubRunner.RunValidator(strings.TrimSpace(workflowPath), resolved, ns, vi.WorktreeDir, vi.ResolveCtx)
	if err != nil {
		return false, err
	}
	// WHY: spec R5 §4.3 — retry validator output is available under impl.retry_validator.<name>.* for debugging and rare template use.
	if pe := vi.SubRunner.ParentEngine; pe != nil {
		graftChildStateIntoParent(pe.State, ns, child)
	}
	return pass, nil
}
