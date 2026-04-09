package engine

import (
	"github.com/isomorphx/gump/internal/workflow"
)

// executeWorkflowSequential runs top-level steps in order (R6: same dispatcher as nested executeSteps).
func (e *Engine) executeWorkflowSequential() error {
	lastSessionByAgent := make(map[string]string)
	e.stepTotalEstimate = len(e.Workflow.Steps)
	err := e.executeSteps(e.Workflow.Steps, nil, "", lastSessionByAgent, workflow.SessionConfig{Mode: "fresh"}, "", nil)
	if err != nil {
		return err
	}
	return e.checkGlobalWorkflowBounds()
}
