package workflow

// detectDeprecated scans the root workflow map for fields removed in v0.0.4 so authors get actionable migration hints without hard failures.
func detectDeprecated(m map[string]interface{}) []Warning {
	var w []Warning
	if _, ok := m["inputs"]; ok {
		w = append(w, Warning{Message: "'inputs:' is removed in v0.0.4 — workflow inputs are deduced from variables (ADR-047)"})
	}
	if _, ok := m["review"]; ok {
		w = append(w, Warning{Message: "'review:' at workflow level is removed — use a validate step or gate validator (ADR-039)"})
	}
	if _, ok := m["description"]; ok {
		w = append(w, Warning{Message: "'description:' is ignored in v0.0.4"})
	}
	return w
}

// detectDeprecatedStep scans a raw step map for removed step-level keywords.
func detectDeprecatedStep(m map[string]interface{}) []Warning {
	var w []Warning
	keys := []struct {
		key string
		msg string
	}{
		{"output", "'output:' is removed — use 'type:' to declare step type (ADR-036)"},
		{"on_failure", "'on_failure:' is removed — use 'retry:' list (ADR-037)"},
		{"strategy", "'strategy:' is removed — use 'retry:' list with overrides (ADR-037)"},
		{"restart_from", "'restart_from:' is removed — retry relaunches from GET (ADR-037)"},
		{"replan", "'replan:' is removed — use a sub-workflow in retry (ADR-037)"},
		{"foreach", "'foreach:' is removed — use 'type: split' with 'each:' (ADR-043)"},
		{"recipe", "'recipe:' is removed — use 'workflow:' (ADR-043)"},
	}
	for _, k := range keys {
		if _, ok := m[k.key]; ok {
			w = append(w, Warning{Message: k.msg})
		}
	}
	return w
}
