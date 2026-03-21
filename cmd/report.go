package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/isomorphx/pudding/internal/config"
	"github.com/isomorphx/pudding/internal/ledger"
	"github.com/spf13/cobra"
)

var (
	reportLastN int
)

var reportCmd = &cobra.Command{
	Use:   "report [cook-id]",
	Short: "Show metrics for a cook or aggregate over recent cooks",
	Long:  "With no args or --last 1: show the latest cook. With cook-id: show that cook. With --last N: aggregate metrics over the last N cooks.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runReport,
}

func init() {
	reportCmd.Flags().IntVar(&reportLastN, "last", 0, "Aggregate over the last N cooks (default 1 if no cook-id)")
	rootCmd.AddCommand(reportCmd)
}

// normalizeManifestEvent maps pre-v4 ledger field names so report stays compatible with older manifests (M5 parsing contract).
func normalizeManifestEvent(ev map[string]interface{}) {
	t, _ := ev["type"].(string)
	switch t {
	case "validation_started":
		ev["type"] = "gate_started"
		if v, ok := ev["validators"]; ok {
			if _, has := ev["checks"]; !has {
				ev["checks"] = v
			}
		}
	case "validation_passed":
		ev["type"] = "gate_passed"
	case "validation_failed":
		ev["type"] = "gate_failed"
	}
	if t2, _ := ev["type"].(string); t2 == "step_started" {
		if task, ok := ev["task"].(string); ok {
			if _, has := ev["item"]; !has {
				ev["item"] = task
			}
		}
	}
}

func runReport(cmd *cobra.Command, args []string) error {
	_, _, err := config.Load()
	if err != nil {
		return err
	}
	projectRoot := config.ProjectRoot()
	puddingDir := filepath.Join(projectRoot, ".pudding", "cooks")
	if st, err := os.Stat(filepath.Dir(puddingDir)); err != nil || !st.IsDir() {
		return fmt.Errorf("no .pudding/cooks directory — run a cook first")
	}

	var cookIDs []string
	if len(args) == 1 {
		cookIDs = []string{args[0]}
	} else {
		entries, err := ledger.ReadIndex(projectRoot)
		if err != nil {
			return err
		}
		n := reportLastN
		if n <= 0 {
			n = 1
		}
		// Index is append-only; last N are the last N entries.
		from := len(entries) - n
		if from < 0 {
			from = 0
		}
		for i := len(entries) - 1; i >= from && len(cookIDs) < n; i-- {
			cookIDs = append([]string{entries[i].CookID}, cookIDs...)
		}
		if len(cookIDs) == 0 {
			return fmt.Errorf("no cooks found — run pudding cook first")
		}
	}

	if len(cookIDs) == 1 && (reportLastN <= 0 || len(args) == 1) {
		return reportOne(puddingDir, cookIDs[0])
	}
	return reportAggregate(projectRoot, cookIDs)
}

func reportOne(cooksDir, cookID string) error {
	manifestPath := filepath.Join(cooksDir, cookID, "manifest.ndjson")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("cook %s not found or no manifest", cookID)
		}
		return err
	}
	var cookStarted, cookCompleted map[string]interface{}
	var steps []map[string]interface{}
	stepAgent := make(map[string]string)
	stepRetries := make(map[string]int)
	stepCost := make(map[string]float64)
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		normalizeManifestEvent(ev)
		t, _ := ev["type"].(string)
		switch t {
		case "cook_started":
			cookStarted = ev
		case "cook_completed":
			cookCompleted = ev
		case "step_completed":
			steps = append(steps, ev)
		case "step_started":
			if step, ok := ev["step"].(string); ok {
				if a, ok := ev["agent"].(string); ok {
					stepAgent[step] = a
				}
			}
		case "retry_triggered":
			if step, ok := ev["step"].(string); ok {
				stepRetries[step]++
			}
		case "agent_completed":
			if step, ok := ev["step"].(string); ok {
				if c, ok := toFloat64(ev["cost_usd"]); ok {
					stepCost[step] += c
				}
			}
		}
	}
	if cookStarted == nil || cookCompleted == nil {
		return fmt.Errorf("invalid manifest for cook %s", cookID)
	}
	recipe, _ := cookStarted["recipe"].(string)
	spec, _ := cookStarted["spec"].(string)
	status, _ := cookCompleted["status"].(string)
	durationMs, _ := toInt(cookCompleted["duration_ms"])
	cost, _ := toFloat64(cookCompleted["total_cost_usd"])
	stepCount, _ := toInt(cookCompleted["steps"])
	retries, _ := toInt(cookCompleted["retries"])
	agents := uniqueAgentsFromManifest(data)

	fmt.Printf("Cook %s (%s) -- %s\n", cookID, recipe, status)
	fmt.Printf("  Spec: %s\n", spec)
	fmt.Printf("  Duration: %s\n", formatDuration(durationMs))
	fmt.Printf("  Cost: $%.2f\n", cost)
	fmt.Printf("  Steps: %d executed, %d retries\n", stepCount, retries)
	fmt.Printf("  Agents: %s\n", strings.Join(agents, ", "))
	fmt.Println()
	fmt.Println("  Steps:")
	for _, s := range steps {
		step, _ := s["step"].(string)
		st, _ := s["status"].(string)
		dm, _ := toInt(s["duration_ms"])
		agent := stepAgent[step]
		retriesN := stepRetries[step]
		costStep := stepCost[step]
		line := fmt.Sprintf("    %-30s %s  %s", step, st, formatDuration(dm))
		if costStep > 0 {
			line += fmt.Sprintf("  $%.2f", costStep)
		}
		if agent != "" {
			line += "  " + agent
		} else {
			line += "  (gate)"
		}
		if retriesN > 0 {
			line += fmt.Sprintf("  (%d retries)", retriesN)
		}
		fmt.Println(line)
	}
	return nil
}

func reportAggregate(repoRoot string, cookIDs []string) error {
	entries, err := ledger.ReadIndex(repoRoot)
	if err != nil {
		return err
	}
	byID := make(map[string]ledger.IndexEntry)
	for _, e := range entries {
		byID[e.CookID] = e
	}
	var selected []ledger.IndexEntry
	for _, id := range cookIDs {
		if e, ok := byID[id]; ok {
			selected = append(selected, e)
		}
	}
	if len(selected) == 0 {
		return fmt.Errorf("no index entries for the requested cooks")
	}
	passCount := 0
	var totalDuration, totalCost float64
	for _, e := range selected {
		if e.Status == "pass" {
			passCount++
		}
		totalDuration += float64(e.DurationMs)
		totalCost += e.CostUSD
	}
	n := len(selected)
	fmt.Printf("Pudding Report -- last %d cooks\n", n)
	fmt.Printf("  Success rate: %d/%d (%d%%)\n", passCount, n, (passCount*100)/n)
	fmt.Printf("  Avg duration: %s\n", formatDuration(int(totalDuration/float64(n))))
	fmt.Printf("  Avg cost: $%.2f\n", totalCost/float64(n))

	cooksDir := filepath.Join(repoRoot, ".pudding", "cooks")

	// By recipe from index
	recipePass := make(map[string]int)
	recipeTotal := make(map[string]int)
	recipeDuration := make(map[string]int64)
	recipeCost := make(map[string]float64)
	for _, e := range selected {
		recipeTotal[e.Recipe]++
		recipeDuration[e.Recipe] += int64(e.DurationMs)
		recipeCost[e.Recipe] += e.CostUSD
		if e.Status == "pass" {
			recipePass[e.Recipe]++
		}
	}
	if len(recipeTotal) > 0 {
		fmt.Println()
		fmt.Println("  By recipe:")
		for r, t := range recipeTotal {
			p := recipePass[r]
			avgDur := formatDuration(int(recipeDuration[r] / int64(t)))
			avgCost := recipeCost[r] / float64(t)
			fmt.Printf("    %-12s %d cooks   %d%% pass   avg $%.2f   avg %s\n", r, t, (p*100)/t, avgCost, avgDur)
		}
	}

	// Parse manifests for by step×agent, top retry steps, circuit breakers
	type stepAgentStat struct{ pass, total int }
	byStepAgent := make(map[string]*stepAgentStat)
	stepRetriesSum := make(map[string]int)
	circuitBreakerCount := 0
	for _, e := range selected {
		manifestPath := filepath.Join(cooksDir, e.CookID, "manifest.ndjson")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		lastAgent := make(map[string]string)
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		for sc.Scan() {
			var ev map[string]interface{}
			if json.Unmarshal(sc.Bytes(), &ev) != nil {
				continue
			}
			normalizeManifestEvent(ev)
			t, _ := ev["type"].(string)
			switch t {
			case "step_started":
				if step, ok := ev["step"].(string); ok {
					if a, ok := ev["agent"].(string); ok {
						lastAgent[step] = a
					}
				}
			case "step_completed":
				step, _ := ev["step"].(string)
				st, _ := ev["status"].(string)
				agent := lastAgent[step]
				if agent == "" {
					agent = "(gate)"
				}
				key := step + " × " + agent
				if byStepAgent[key] == nil {
					byStepAgent[key] = &stepAgentStat{}
				}
				byStepAgent[key].total++
				if st == "pass" {
					byStepAgent[key].pass++
				}
			case "retry_triggered":
				if step, ok := ev["step"].(string); ok {
					stepRetriesSum[step]++
				}
			case "circuit_breaker":
				circuitBreakerCount++
			}
		}
	}
	if len(byStepAgent) > 0 {
		fmt.Println()
		fmt.Println("  By step × agent:")
		for key, s := range byStepAgent {
			pct := 0
			if s.total > 0 {
				pct = (s.pass * 100) / s.total
			}
			fmt.Printf("    %-35s %d%% pass   (%d/%d)\n", key, pct, s.pass, s.total)
		}
	}
	if len(stepRetriesSum) > 0 {
		fmt.Println()
		fmt.Println("  Top retry steps:")
		for step, sum := range stepRetriesSum {
			avg := float64(sum) / float64(n)
			fmt.Printf("    %-20s avg %.1f retries/cook\n", step, avg)
		}
	}
	if circuitBreakerCount > 0 {
		fmt.Printf("\n  Circuit breakers: %d\n", circuitBreakerCount)
	}
	return nil
}

func uniqueAgentsFromManifest(manifestData []byte) []string {
	seen := make(map[string]struct{})
	sc := bufio.NewScanner(strings.NewReader(string(manifestData)))
	for sc.Scan() {
		var ev map[string]interface{}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		normalizeManifestEvent(ev)
		if ev["type"] == "step_started" {
			if a, ok := ev["agent"].(string); ok && a != "" {
				seen[a] = struct{}{}
			}
		}
	}
	var out []string
	for a := range seen {
		out = append(out, a)
	}
	return out
}

func formatDuration(ms int) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	s := ms / 1000
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	s %= 60
	if s > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%dm", m)
}

func toInt(v interface{}) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	}
	return 0, false
}

func toFloat64(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	}
	return 0, false
}
