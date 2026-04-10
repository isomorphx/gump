package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/isomorphx/gump/internal/run"
	"github.com/isomorphx/gump/internal/state"
)

type StepDetail struct {
	Step        string
	Agent       string
	Output      string
	SessionMode string
	Status      string
	Attempts    []StepAttempt
	StateBag    map[string]string
	Files       []string
}

type StepAttempt struct {
	Attempt   int
	Status    string
	Duration  string
	CostUSD   float64
	Turns     int
	TokensIn  int
	TokensOut int
	Error     string
}

func BuildStepDetail(runDir, stepQuery string) (*StepDetail, error) {
	manifestPath := filepath.Join(runDir, "manifest.ndjson")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	lines := detailScanLines(manifest)
	target, err := resolveStepPath(lines, stepQuery)
	if err != nil {
		return nil, err
	}

	detail := &StepDetail{
		Step:     target,
		Attempts: []StepAttempt{},
		StateBag: map[string]string{},
	}
	curr := -1
	for _, line := range lines {
		var ev map[string]interface{}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		NormalizeManifestEvent(ev)
		typ, _ := ev["type"].(string)
		step, _ := ev["step"].(string)
		if step != target {
			continue
		}
		switch typ {
		case "step_started":
			a := StepAttempt{Attempt: intOrDefault(ev["attempt"], len(detail.Attempts)+1)}
			detail.Attempts = append(detail.Attempts, a)
			curr = len(detail.Attempts) - 1
			if ag, _ := ev["agent"].(string); ag != "" {
				detail.Agent = ag
			}
			st, _ := ev["step_type"].(string)
			om, _ := ev["output_mode"].(string)
			if m := ledgerStepTypeToReportOutputMode(st); m != "" {
				detail.Output = m
			} else if om != "" {
				detail.Output = om
			}
			if sm, _ := ev["session_mode"].(string); sm != "" {
				detail.SessionMode = sm
			}
		case "agent_completed":
			if curr < 0 || curr >= len(detail.Attempts) {
				continue
			}
			at := &detail.Attempts[curr]
			at.Duration = formatDuration(intOrDefault(ev["duration_ms"], 0))
			at.CostUSD = floatOrDefault(ev["cost_usd"], 0)
			at.TokensIn = intOrDefault(ev["tokens_in"], 0)
			at.TokensOut = intOrDefault(ev["tokens_out"], 0)
			if sid, _ := ev["session_id"].(string); sid != "" {
				detail.StateBag["session_id"] = sid
			}
		case "gate_failed":
			if curr >= 0 && curr < len(detail.Attempts) {
				at := &detail.Attempts[curr]
				at.Status = "gate_fail"
				at.Error = trunc200(stringOrDefault(ev["reason"], ""))
			}
		case "guard_triggered":
			if curr >= 0 && curr < len(detail.Attempts) {
				at := &detail.Attempts[curr]
				at.Status = "guard_fail"
				at.Error = trunc200(stringOrDefault(ev["reason"], ""))
			}
		case "step_completed":
			detail.Status = stringOrDefault(ev["status"], "")
			if curr >= 0 && curr < len(detail.Attempts) {
				at := &detail.Attempts[curr]
				if detail.Status == "pass" {
					at.Status = "pass"
				} else if at.Status == "" {
					at.Status = detail.Status
				}
			}
			if arts, ok := ev["artifacts"].(map[string]interface{}); ok {
				if rel, _ := arts["diff"].(string); rel != "" {
					diffPath := filepath.Join(runDir, filepath.FromSlash(rel))
					if b, err := os.ReadFile(diffPath); err == nil {
						detail.Files = extractFilesFromPatch(string(b))
					}
				}
			}
		}
	}

	if b, err := run.ReadStateFile(runDir); err == nil {
		if st, err := state.Restore(b); err == nil && st != nil {
			prefix := target + "."
			for _, k := range st.Keys() {
				if strings.HasPrefix(k, prefix) {
					field := strings.TrimPrefix(k, prefix)
					detail.StateBag[field] = st.Get(k)
				}
			}
		}
	}

	if len(detail.Attempts) == 0 {
		return nil, fmt.Errorf("no attempts found for step %q", stepQuery)
	}
	return detail, nil
}

func RenderStepDetail(detail *StepDetail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Step: %s\n", detail.Step)
	fmt.Fprintf(&b, "%s\n", strings.Repeat("─", 57))
	fmt.Fprintf(&b, "Agent:     %s\n", fallback(detail.Agent, ""))
	fmt.Fprintf(&b, "Output:    %s\n", fallback(detail.Output, ""))
	fmt.Fprintf(&b, "Session:   %s\n", fallback(detail.SessionMode, ""))
	fmt.Fprintf(&b, "Status:    %s\n\n", fallback(detail.Status, ""))

	fmt.Fprintf(&b, "Attempts:\n")
	for i, a := range detail.Attempts {
		st := fallback(a.Status, "unknown")
		fmt.Fprintf(&b, "  %d. %-11s %7s  $%.2f  %d/%d tok\n", i+1, st, fallback(a.Duration, "0s"), a.CostUSD, a.TokensIn, a.TokensOut)
		if a.Error != "" {
			fmt.Fprintf(&b, "     error: %q\n", a.Error)
		}
	}
	fmt.Fprintf(&b, "\nState Bag:\n")
	keys := make([]string, 0, len(detail.StateBag))
	for k := range detail.StateBag {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := detail.StateBag[k]
		if k == "output" {
			v = trunc200(v)
		}
		fmt.Fprintf(&b, "  %-10s %s\n", k+":", v)
	}

	fmt.Fprintf(&b, "\nFiles Changed:\n")
	if len(detail.Files) == 0 {
		fmt.Fprintf(&b, "  (none)\n")
	} else {
		for _, f := range detail.Files {
			fmt.Fprintf(&b, "  %s\n", f)
		}
	}
	return b.String()
}

func resolveStepPath(lines [][]byte, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("--detail requires a step name")
	}
	seen := map[string]struct{}{}
	var steps []string
	for _, line := range lines {
		var ev map[string]interface{}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if t, _ := ev["type"].(string); t != "step_completed" {
			continue
		}
		step, _ := ev["step"].(string)
		if step == "" {
			continue
		}
		if _, ok := seen[step]; ok {
			continue
		}
		seen[step] = struct{}{}
		steps = append(steps, step)
	}
	var matches []string
	for _, s := range steps {
		if s == query || detailPathBase(s) == query || strings.HasSuffix(s, "/"+query) {
			matches = append(matches, s)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("step %q not found", query)
	}
	sort.Strings(matches)
	return "", fmt.Errorf("step %q is ambiguous; matches: %s", query, strings.Join(matches, ", "))
}

func detailScanLines(b []byte) [][]byte {
	lines := bytes.Split(b, []byte("\n"))
	out := make([][]byte, 0, len(lines))
	for _, l := range lines {
		l = bytes.TrimSpace(l)
		if len(l) > 0 {
			out = append(out, l)
		}
	}
	return out
}

func detailPathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func intOrDefault(v interface{}, def int) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		if n, err := strconv.Atoi(x); err == nil {
			return n
		}
	}
	return def
}

func floatOrDefault(v interface{}, def float64) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		if n, err := strconv.ParseFloat(x, 64); err == nil {
			return n
		}
	}
	return def
}

func stringOrDefault(v interface{}, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

func stringify(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func trunc200(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 200 {
		return s
	}
	return s[:200] + "..."
}

func fallback(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}
