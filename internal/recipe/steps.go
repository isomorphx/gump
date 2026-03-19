package recipe

// LeafStep describes an atomic step (no substeps) for replay resolution. PathPrefix is the group path (e.g. "implement" for step "code" under group "implement").
type LeafStep struct {
	PathPrefix string
	Name       string
}

// LeafSteps returns all atomic steps in the recipe with their path prefix so --from-step can be resolved by name against the recipe graph.
func LeafSteps(rec *Recipe) []LeafStep {
	var out []LeafStep
	walkSteps(rec.Steps, "", &out)
	return out
}

func walkSteps(steps []Step, pathPrefix string, out *[]LeafStep) {
	for _, s := range steps {
		if len(s.Steps) > 0 || s.Recipe != "" {
			prefix := s.Name
			if pathPrefix != "" {
				prefix = pathPrefix + "/" + s.Name
			}
			walkSteps(s.Steps, prefix, out)
			continue
		}
		*out = append(*out, LeafStep{PathPrefix: pathPrefix, Name: s.Name})
	}
}
