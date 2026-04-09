package engine

import (
	"fmt"
	"strings"

	"github.com/isomorphx/gump/internal/workflow"
)

// executeWorkflowSequential runs only top-level steps in order (R3); composite workflows error until R5/R6.
// WHY: GET→RUN→GATE must see a single linear step path before split/parallel semantics return in R6.
func (e *Engine) executeWorkflowSequential() error {
	lastSessionByAgent := make(map[string]string)
	e.stepTotalEstimate = len(e.Workflow.Steps)
	for i := range e.Workflow.Steps {
		step := &e.Workflow.Steps[i]
		stepPath := step.Name
		if e.FromStep != "" && !e.replayReachedStart {
			if stepPath == e.FromStep {
				e.replayReachedStart = true
			} else if strings.HasPrefix(e.FromStep, stepPath+"/") {
				// FromStep refers to a nested path; flat R3 workflows do not recurse here.
			} else {
				continue
			}
		}
		if e.ResumePassedSteps != nil && e.ResumePassedSteps[stepPath] {
			continue
		}
		if e.Run != nil && e.Run.State != nil && e.Run.State.Get(stepPath+".status") == "pass" {
			continue
		}
		if err := e.checkGlobalWorkflowBounds(); err != nil {
			return err
		}
		if err := e.dispatchTopLevelStep(step, stepPath, lastSessionByAgent); err != nil {
			return err
		}
		if err := e.checkGlobalWorkflowBounds(); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) dispatchTopLevelStep(step *workflow.Step, stepPath string, lastSessionByAgent map[string]string) error {
	if len(step.Each) > 0 {
		return fmt.Errorf("split+each not yet implemented (R6)")
	}
	if len(step.Steps) > 0 {
		return fmt.Errorf("parallel groups not yet implemented (R6)")
	}
	if strings.TrimSpace(step.Workflow) != "" {
		return fmt.Errorf("workflow calls not yet implemented (R5)")
	}
	if workflow.IsGateOnlyStep(step) {
		return e.executeGateOnlyTopLevel(step, stepPath)
	}
	return e.runAtomicStep(step, stepPath, nil, lastSessionByAgent, workflow.SessionConfig{Mode: "fresh"}, "", nil)
}
