package validate

import (
	"fmt"
	"strings"

	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/workflow"
)

const schemaReviewArg = "review"

// GateStateFieldName maps gate entry i to the state sub-key under <step>.gate.* (e.g. compile, bash_1).
func GateStateFieldName(gates []workflow.GateEntry, idx int) string {
	if idx < 0 || idx >= len(gates) {
		return fmt.Sprintf("unknown_%d", idx)
	}
	g := gates[idx]
	if g.Arg == "" {
		for j := 0; j < idx; j++ {
			if gates[j].Type == g.Type && gates[j].Arg == "" {
				return fmt.Sprintf("%s_%d", g.Type, idx)
			}
		}
		return g.Type
	}
	return fmt.Sprintf("%s_%d", g.Type, idx)
}

// ApplyGateResultsToState writes <step>.gate.<name> and optional .error for every gate result (R3 §4.2).
func ApplyGateResultsToState(st *state.State, stepPath string, gates []workflow.GateEntry, vr *ValidationResult) {
	if st == nil || vr == nil {
		return
	}
	for i, r := range vr.Results {
		if i >= len(gates) {
			break
		}
		name := GateStateFieldName(gates, i)
		prefix := stepPath + ".gate." + name
		passLike := r.Pass || r.Skipped
		if passLike {
			st.Set(prefix, "true")
		} else {
			st.Set(prefix, "false")
		}
		if !passLike && strings.TrimSpace(r.Stderr) != "" {
			msg := r.Stderr
			if len(msg) > 4000 {
				msg = msg[:4000]
			}
			st.Set(prefix+".error", msg)
		}
		g := gates[i]
		if g.Type == "schema" && strings.TrimSpace(g.Arg) == schemaReviewArg {
			if c := strings.TrimSpace(st.Get(stepPath + ".comments")); c != "" {
				st.Set(prefix+".comments", c)
			}
		}
	}
}

// GateTemplateMapsFromState rebuilds GateResults and GateMeta from persisted <step>.gate.* keys (retry GET templates).
func GateTemplateMapsFromState(st *state.State, stepPath string, gates []workflow.GateEntry) (results map[string]string, meta map[string]map[string]string) {
	if st == nil || len(gates) == 0 {
		return nil, nil
	}
	for i := range gates {
		name := GateStateFieldName(gates, i)
		prefix := stepPath + ".gate." + name
		v := st.Get(prefix)
		comments := st.Get(prefix + ".comments")
		errStr := st.Get(prefix + ".error")
		if v != "" {
			if results == nil {
				results = make(map[string]string)
			}
			results[name] = v
		}
		if v != "" || comments != "" || errStr != "" {
			if meta == nil {
				meta = make(map[string]map[string]string)
			}
			meta[name] = map[string]string{
				"pass":     v,
				"comments": comments,
				"error":    errStr,
			}
		}
	}
	return results, meta
}
