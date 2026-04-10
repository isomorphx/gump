package workflow

// This file ties CLI --from-step to engine paths when split+each introduces dynamic task segments that never appear in static leaf walks.

import (
	"fmt"
	"strings"
)

// leafFullPath is the engine step path for a static atomic step (no split task segment).
func leafFullPath(l LeafStep) string {
	if l.PathPrefix == "" {
		return l.Name
	}
	return l.PathPrefix + "/" + l.Name
}

// StepAtPath returns the step at engine path segments (e.g. "reviews" or "grp/code"), or nil.
func StepAtPath(steps []Step, path string) *Step {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	return stepAtPathParts(steps, parts, 0)
}

func stepAtPathParts(steps []Step, parts []string, idx int) *Step {
	if idx >= len(parts) {
		return nil
	}
	want := parts[idx]
	for i := range steps {
		if steps[i].Name != want {
			continue
		}
		cur := &steps[i]
		if idx == len(parts)-1 {
			return cur
		}
		if len(cur.Steps) > 0 {
			return stepAtPathParts(cur.Steps, parts, idx+1)
		}
		// split+each has no static children under the split in the YAML tree.
		return nil
	}
	return nil
}

// MatchSplitEachQualifiedPath reports whether path looks like <splitPath>/<task>/<eachStep> for this workflow.
func MatchSplitEachQualifiedPath(wf *Workflow, path string) bool {
	if wf == nil || path == "" {
		return false
	}
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return false
	}
	splitPath := strings.Join(parts[:len(parts)-2], "/")
	eachLeaf := parts[len(parts)-1]
	st := StepAtPath(wf.Steps, splitPath)
	if st == nil || st.Type != "split" || len(st.Each) == 0 {
		return false
	}
	for i := range st.Each {
		if st.Each[i].Name == eachLeaf {
			return true
		}
	}
	return false
}

// SplitTaskStatePrefix returns the state/ledger prefix for all steps of one task (…/<task>/).
func SplitTaskStatePrefix(qualifiedStep string) string {
	parts := strings.Split(qualifiedStep, "/")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], "/") + "/"
}

// IsSplitCompositePath reports whether path is exactly a split step with each (replay anchor for whole split).
func IsSplitCompositePath(wf *Workflow, path string) bool {
	if wf == nil {
		return false
	}
	st := StepAtPath(wf.Steps, strings.Trim(path, "/"))
	return st != nil && st.Type == "split" && len(st.Each) > 0
}

func shortNameUsedInAnySplitEach(steps []Step, name string) bool {
	for i := range steps {
		s := &steps[i]
		if s.Type == "split" && len(s.Each) > 0 {
			for j := range s.Each {
				if s.Each[j].Name == name {
					return true
				}
			}
		}
		if len(s.Steps) > 0 && shortNameUsedInAnySplitEach(s.Steps, name) {
			return true
		}
	}
	return false
}

// ResolveEngineStepPath maps CLI --from-step to a single engine path (replay / resume metadata).
func ResolveEngineStepPath(fromStep string, rec *Workflow) (string, error) {
	fromStep = strings.TrimSpace(fromStep)
	if fromStep == "" {
		return "", fmt.Errorf("--from-step is required for replay")
	}
	if rec == nil {
		return "", fmt.Errorf("workflow is nil")
	}
	if MatchSplitEachQualifiedPath(rec, fromStep) {
		return fromStep, nil
	}
	if IsSplitCompositePath(rec, fromStep) {
		return strings.Trim(fromStep, "/"), nil
	}
	leaves := LeafSteps(rec)
	if strings.Contains(fromStep, "/") {
		for _, l := range leaves {
			if leafFullPath(l) == fromStep {
				return fromStep, nil
			}
		}
		return "", fmt.Errorf("step %q not found in workflow %q", fromStep, rec.Name)
	}
	if shortNameUsedInAnySplitEach(rec.Steps, fromStep) {
		return "", fmt.Errorf("ambiguous step reference — use qualified path like '<split>/<task>/%s'", fromStep)
	}
	var withName []LeafStep
	for _, l := range leaves {
		if l.Name == fromStep {
			withName = append(withName, l)
		}
	}
	if len(withName) == 1 {
		return leafFullPath(withName[0]), nil
	}
	if len(withName) > 1 {
		var paths []string
		for _, l := range withName {
			paths = append(paths, leafFullPath(l))
		}
		return "", fmt.Errorf("step %q is ambiguous in workflow %q. Use full path: %s", fromStep, rec.Name, strings.Join(paths, ", "))
	}
	return "", fmt.Errorf("step %q not found in workflow %q", fromStep, rec.Name)
}
