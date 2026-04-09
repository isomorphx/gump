package workflow

import "strings"

// LeafStep is one atomic unit in the step graph for replay / from-step resolution.
type LeafStep struct {
	PathPrefix string
	Name       string
}

// LeafSteps lists atomic steps in depth-first order (matches the pre-R3 shape for the execution engine).
func LeafSteps(wf *Workflow) []LeafStep {
	if wf == nil {
		return nil
	}
	var out []LeafStep
	walkLeaves(wf.Steps, "", &out)
	return out
}

func walkLeaves(steps []Step, pathPrefix string, out *[]LeafStep) {
	for i := range steps {
		s := &steps[i]
		if isCompositeStep(s) {
			prefix := s.Name
			if pathPrefix != "" {
				prefix = pathPrefix + "/" + s.Name
			}
			if s.Type == "split" && len(s.Each) > 0 {
				walkLeaves(s.Each, prefix, out)
			} else {
				walkLeaves(s.Steps, prefix, out)
			}
			continue
		}
		*out = append(*out, LeafStep{PathPrefix: pathPrefix, Name: s.Name})
	}
}

func isCompositeStep(s *Step) bool {
	if strings.TrimSpace(s.Workflow) != "" {
		return true
	}
	if len(s.Steps) > 0 {
		return true
	}
	if s.Type == "split" && len(s.Each) > 0 {
		return true
	}
	return false
}
