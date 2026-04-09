package workflow

import (
	"path/filepath"
	"regexp"
	"strings"
)

var tplVarRe = regexp.MustCompile(`\{([a-zA-Z0-9_./-]+)\}`)

// CollectReferencedVars returns unique `{name}` placeholders from workflow text fields (ADR-047 scanning).
func CollectReferencedVars(wf *Workflow) []string {
	if wf == nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	add := func(s string) {
		for _, m := range tplVarRe.FindAllStringSubmatch(s, -1) {
			if len(m) < 2 {
				continue
			}
			k := m[1]
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, k)
		}
	}
	var walk func(steps []Step)
	walk = func(steps []Step) {
		for i := range steps {
			s := &steps[i]
			add(s.Prompt)
			add(s.Agent)
			for _, g := range s.Gate {
				add(g.Arg)
				for _, v := range g.With {
					add(v)
				}
			}
			for _, r := range s.Retry {
				add(r.Prompt)
				add(r.Not)
				add(r.Validate)
				add(r.Agent)
				for _, v := range r.With {
					add(v)
				}
			}
			for _, v := range s.With {
				add(v)
			}
			if s.GetWorkflow != nil {
				for _, v := range s.GetWorkflow.With {
					add(v)
				}
			}
			walk(s.Steps)
			walk(s.Each)
		}
	}
	walk(wf.Steps)
	return out
}

// ValidatorGateNameFromPath derives the stable gate.* template key from a validate: workflow path (spec R5 §4.2).
func ValidatorGateNameFromPath(path string) string {
	p := strings.TrimSpace(path)
	p = strings.TrimSuffix(p, ".yaml")
	p = strings.TrimSuffix(p, ".yml")
	base := filepath.ToSlash(p)
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, "-"); i > 0 {
		return base[i+1:]
	}
	return base
}

// WorkflowRefLastSegment is the final path segment of a workflow reference (used for get.workflow namespaces).
func WorkflowRefLastSegment(path string) string {
	p := strings.TrimSpace(path)
	p = strings.TrimSuffix(p, ".yaml")
	p = strings.TrimSuffix(p, ".yml")
	base := filepath.ToSlash(p)
	if i := strings.LastIndex(base, "/"); i >= 0 {
		return base[i+1:]
	}
	return base
}
