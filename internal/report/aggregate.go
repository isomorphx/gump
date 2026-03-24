package report

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/isomorphx/gump/internal/brand"
)

// AggregateReport is the cross-run summary (spec §8).
type AggregateReport struct {
	N             int
	RecipeHeader  string
	PassCount     int
	TotalRuns     int
	AvgDurationMs int
	AvgCostUSD    float64
	TotalCostUSD  float64
	AvgRetries    float64
	GuardTriggers int
	Rows          []AggregateStepRow
	CostByRun     []struct {
		Label string
		USD   float64
	}
}

// AggregateStepRow is one line in Steps × Agents.
type AggregateStepRow struct {
	Pattern     string
	Agent       string
	SuccessPass int
	SuccessTot  int
	AvgCost     float64
	AvgRetries  float64
}

type aggKey struct {
	pattern string
	agent   string
}

type aggRow struct {
	passTot  int
	passOK   int
	costSum  float64
	retrySum float64
	retryDen int
}

// BuildAggregateReport loads manifests for the given run IDs (chronological order).
func BuildAggregateReport(repoRoot string, cookIDs []string) (*AggregateReport, error) {
	cooksDir := filepath.Join(repoRoot, brand.StateDir(), brand.RunsDir())
	type cookMeta struct {
		id       string
		recipe   string
		status   string
		duration int
		cost     float64
		retries  float64
	}
	var cooks []cookMeta
	recipes := map[string]int{}
	for _, id := range cookIDs {
		p := filepath.Join(cooksDir, id, "manifest.ndjson")
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var cg cookMeta
		cg.id = id
		for _, line := range scanLines(data) {
			var ev map[string]interface{}
			if json.Unmarshal(line, &ev) != nil {
				continue
			}
			NormalizeManifestEvent(ev)
			switch ev["type"] {
			case "cook_started":
				cg.recipe, _ = ev["recipe"].(string)
			case "run_started":
				cg.recipe, _ = ev["workflow"].(string)
			case "cook_completed":
				cg.status, _ = ev["status"].(string)
				if v, ok := ev["duration_ms"].(float64); ok {
					cg.duration = int(v)
				}
				if v, ok := ev["total_cost_usd"].(float64); ok {
					cg.cost = v
				}
				if v, ok := ev["retries"].(float64); ok {
					cg.retries = v
				}
			case "run_completed":
				cg.status, _ = ev["status"].(string)
				if v, ok := ev["duration_ms"].(float64); ok {
					cg.duration = int(v)
				}
				if v, ok := ev["total_cost_usd"].(float64); ok {
					cg.cost = v
				}
				if v, ok := ev["retries"].(float64); ok {
					cg.retries = v
				}
			}
		}
		cooks = append(cooks, cg)
		if cg.recipe != "" {
			recipes[cg.recipe]++
		}
	}
	if len(cooks) == 0 {
		return nil, fmt.Errorf("no run data")
	}
	ar := &AggregateReport{
		N:         len(cooks),
		TotalRuns: len(cooks),
	}
	if len(recipes) == 1 {
		for r := range recipes {
			ar.RecipeHeader = r
		}
	} else {
		ar.RecipeHeader = "mixed"
	}
	var pass int
	var durSum, costSum, retSum float64
	for _, c := range cooks {
		if c.status == "pass" {
			pass++
		}
		durSum += float64(c.duration)
		costSum += c.cost
		retSum += c.retries
	}
	ar.PassCount = pass
	ar.AvgDurationMs = int(durSum / float64(len(cooks)))
	ar.AvgCostUSD = costSum / float64(len(cooks))
	ar.TotalCostUSD = costSum
	ar.AvgRetries = retSum / float64(len(cooks))

	for i, c := range cooks {
		ar.CostByRun = append(ar.CostByRun, struct {
			Label string
			USD   float64
		}{fmt.Sprintf("Run %d", i+1), c.cost})
	}

	rows := map[aggKey]*aggRow{}
	for _, c := range cooks {
		dir := filepath.Join(cooksDir, c.id)
		cr, err := BuildCookReport(dir)
		if err != nil {
			continue
		}
		ar.GuardTriggers += cr.GuardTriggers
		data, _ := os.ReadFile(filepath.Join(dir, "manifest.ndjson"))
		for _, s := range cr.Steps {
			pat := normalizeStepPattern(s.Name, s.Item)
			ag := s.Agent
			if ag == "" {
				ag = "(gate)"
			}
			k := aggKey{pattern: pat, agent: ag}
			if rows[k] == nil {
				rows[k] = &aggRow{}
			}
			r := rows[k]
			r.passTot++
			if strings.EqualFold(s.Status, "pass") {
				r.passOK++
			}
			r.costSum += s.CostUSD
			r.retrySum += float64(retriesForExactStep(data, s.Name))
			r.retryDen++
		}
	}
	for k, v := range rows {
		row := AggregateStepRow{
			Pattern:     k.pattern,
			Agent:       k.agent,
			SuccessPass: v.passOK,
			SuccessTot:  v.passTot,
		}
		if v.passTot > 0 {
			row.AvgCost = v.costSum / float64(v.passTot)
		}
		if v.retryDen > 0 {
			row.AvgRetries = v.retrySum / float64(v.retryDen)
		}
		ar.Rows = append(ar.Rows, row)
	}
	for i := 0; i < len(ar.Rows); i++ {
		for j := i + 1; j < len(ar.Rows); j++ {
			if ar.Rows[j].Pattern < ar.Rows[i].Pattern || (ar.Rows[j].Pattern == ar.Rows[i].Pattern && ar.Rows[j].Agent < ar.Rows[i].Agent) {
				ar.Rows[i], ar.Rows[j] = ar.Rows[j], ar.Rows[i]
			}
		}
	}
	return ar, nil
}

func retriesForExactStep(manifest []byte, stepPath string) int {
	var n int
	for _, line := range scanLines(manifest) {
		var ev map[string]interface{}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		NormalizeManifestEvent(ev)
		if ev["type"] != "retry_triggered" {
			continue
		}
		st, _ := ev["step"].(string)
		if st == stepPath {
			n++
		}
	}
	return n
}

func normalizeStepPattern(stepPath, item string) string {
	if item == "" {
		return stepPath
	}
	parts := strings.Split(stepPath, "/")
	for i, p := range parts {
		if p == item {
			parts[i] = "*"
		}
	}
	return strings.Join(parts, "/")
}

// RenderAggregateReport renders §8 TUI.
func RenderAggregateReport(ar *AggregateReport, o RenderOpts) string {
	var b strings.Builder
	h := o.hBar()
	fmt.Fprintf(&b, "%s%s%s\n", o.topLeft(), h, o.topRight())
	nStr := fmt.Sprintf("%d", ar.N)
	fmt.Fprintf(&b, "%s  Gump Report — Last %s runs%*s%s\n", o.vBar(), nStr, boxWidth-22-len(nStr), "", o.vBar())
	fmt.Fprintf(&b, "%s  Recipe: %-*s%s\n", o.vBar(), boxWidth-10, ar.RecipeHeader, o.vBar())
	fmt.Fprintf(&b, "%s%s%s\n", o.botLeft(), h, o.botRight())

	pct := 0
	if ar.TotalRuns > 0 {
		pct = (ar.PassCount * 100) / ar.TotalRuns
	}
	fmt.Fprintf(&b, "\nSummary\n")
	fmt.Fprintf(&b, "%s\n", h)
	fmt.Fprintf(&b, "  Success rate   %d/%d (%d%%)\n", ar.PassCount, ar.TotalRuns, pct)
	fmt.Fprintf(&b, "  Avg duration   %s\n", formatDuration(ar.AvgDurationMs))
	fmt.Fprintf(&b, "  Avg cost       $%.2f\n", ar.AvgCostUSD)
	fmt.Fprintf(&b, "  Total cost     $%.2f\n", ar.TotalCostUSD)
	fmt.Fprintf(&b, "  Avg retries    %.1f\n", ar.AvgRetries)
	fmt.Fprintf(&b, "  Guard triggers: %d\n", ar.GuardTriggers)

	fmt.Fprintf(&b, "\nSteps × Agents\n")
	fmt.Fprintf(&b, "%s\n", h)
	fmt.Fprintf(&b, "  Step              Agent           Success   Avg Cost   Avg Retries\n")
	for _, r := range ar.Rows {
		fmt.Fprintf(&b, "  %-17s %-15s %d/%-6d $%-8.2f %.1f\n",
			trimW(r.Pattern, 17), trimW(r.Agent, 15), r.SuccessPass, r.SuccessTot, r.AvgCost, r.AvgRetries)
	}

	fmt.Fprintf(&b, "\nCost Trend\n")
	fmt.Fprintf(&b, "%s\n", h)
	maxUSD := 0.0
	for _, c := range ar.CostByRun {
		if c.USD > maxUSD {
			maxUSD = c.USD
		}
	}
	if maxUSD <= 0 {
		maxUSD = 1
	}
	for _, c := range ar.CostByRun {
		w := int(math.Ceil(c.USD / maxUSD * 20))
		if w > 20 {
			w = 20
		}
		bar := strings.Repeat(o.barFull(), w)
		fmt.Fprintf(&b, "  %-8s $%-7.2f  %s\n", c.Label, c.USD, bar)
	}
	return b.String()
}
