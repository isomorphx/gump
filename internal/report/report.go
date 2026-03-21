package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StepReport is the per-step analytics row (spec §3).
type StepReport struct {
	Name         string
	Agent        string
	OutputMode   string
	Item         string
	Status       string
	Attempts     int
	SessionID    string
	SessionMode  string
	DurationMs   int
	TokensIn     int
	TokensOut    int
	CostUSD      float64
	Turns        []Turn
	TurnCounts   map[string]int
	FilesChanged []string
	LinesAdded   int
	LinesRemoved int
	GateResult   string
	TTFD         int
	Stall        StallMetrics
	ContextPct   float64
	ContextLabel string
}

// CookReport aggregates one cook run (spec §3).
type CookReport struct {
	CookID         string
	Recipe         string
	Status         string
	DurationMs     int
	TotalCostUSD   float64
	TotalTokensIn  int
	TotalTokensOut int
	MaxBudgetUSD   float64
	Steps          []StepReport
	Retries        int
	Agents         []string
	FilesChanged   int
	LinesAdded     int
	LinesRemoved   int
	TTFD           map[string]int
	StallLoops     map[string]StallMetrics
	ContextUsage   map[string]float64
	StallTotal     StallMetrics
	TurnDist       map[string]int
}

// NormalizeManifestEvent maps legacy v3 ledger keys so operators can audit old runs without a separate code path.
func NormalizeManifestEvent(ev map[string]interface{}) {
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

type stepAccum struct {
	stepPath     string
	agent        string
	outputMode   string
	item         string
	sessionMode  string
	startCount   int
	tokensIn     int
	tokensOut    int
	costUSD      float64
	durationMs   int
	status       string
	gatePassFail string // last gate pass/fail for this step
}

// BuildCookReport loads manifest, artefacts, and state bag for one cook directory.
func BuildCookReport(cookDir string) (*CookReport, error) {
	manifestPath := filepath.Join(cookDir, "manifest.ndjson")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var stateBagEntries map[string]struct {
		SessionID string `json:"session_id"`
		Files     string `json:"files"`
	}
	if sb, err := os.ReadFile(filepath.Join(cookDir, "state-bag.json")); err == nil {
		var payload struct {
			Entries map[string]struct {
				SessionID string `json:"session_id"`
				Files     string `json:"files"`
			} `json:"entries"`
		}
		if json.Unmarshal(sb, &payload) == nil {
			stateBagEntries = payload.Entries
		}
	}

	lines := bytes.Split(data, []byte("\n"))
	var cookID, recipe, cookStatus string
	var durationMs, retries, stepsN int
	var totalCost float64
	var maxBudget float64
	var finalPatchPath string
	agentsSet := map[string]struct{}{}

	stepOrder := []string{}
	stepSeen := map[string]struct{}{}
	stepStarts := map[string]int{}
	ac := map[string]*stepAccum{}
	gateOf := map[string]string{}

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		NormalizeManifestEvent(ev)
		typ, _ := ev["type"].(string)
		switch typ {
		case "cook_started":
			cookID, _ = ev["cook_id"].(string)
			recipe, _ = ev["recipe"].(string)
			if b, ok := ev["max_budget"].(float64); ok {
				maxBudget = b
			}
		case "cook_completed":
			cookStatus, _ = ev["status"].(string)
			if v, ok := ev["duration_ms"].(float64); ok {
				durationMs = int(v)
			}
			if v, ok := ev["total_cost_usd"].(float64); ok {
				totalCost = v
			}
			if v, ok := ev["steps"].(float64); ok {
				stepsN = int(v)
			}
			if v, ok := ev["retries"].(float64); ok {
				retries = int(v)
			}
			if arts, ok := ev["artifacts"].(map[string]interface{}); ok {
				if rel, ok := arts["final_diff"].(string); ok {
					finalPatchPath = rel
				}
			}
		case "step_started":
			sp, _ := ev["step"].(string)
			if sp == "" {
				continue
			}
			stepStarts[sp]++
			if _, ok := ac[sp]; !ok {
				ac[sp] = &stepAccum{stepPath: sp}
			}
			a := ac[sp]
			a.startCount++
			if ag, ok := ev["agent"].(string); ok {
				a.agent = ag
				if ag != "" {
					agentsSet[ag] = struct{}{}
				}
			}
			if om, ok := ev["output_mode"].(string); ok {
				a.outputMode = om
			}
			if it, ok := ev["item"].(string); ok {
				a.item = it
			}
			if sm, ok := ev["session_mode"].(string); ok {
				a.sessionMode = sm
			}
		case "agent_completed":
			sp, _ := ev["step"].(string)
			if sp == "" {
				continue
			}
			if _, ok := ac[sp]; !ok {
				ac[sp] = &stepAccum{stepPath: sp}
			}
			a := ac[sp]
			ti, _ := numToInt(ev["tokens_in"])
			to, _ := numToInt(ev["tokens_out"])
			a.tokensIn += ti
			a.tokensOut += to
			if c, ok := ev["cost_usd"].(float64); ok {
				a.costUSD += c
			}
		case "step_completed":
			sp, _ := ev["step"].(string)
			if sp == "" {
				continue
			}
			if _, ok := stepSeen[sp]; !ok {
				stepSeen[sp] = struct{}{}
				stepOrder = append(stepOrder, sp)
			}
			if _, ok := ac[sp]; !ok {
				ac[sp] = &stepAccum{stepPath: sp}
			}
			a := ac[sp]
			if st, ok := ev["status"].(string); ok {
				a.status = st
			}
			if dm, ok := ev["duration_ms"].(float64); ok {
				a.durationMs = int(dm)
			}
		case "gate_passed":
			sp, _ := ev["step"].(string)
			gateOf[sp] = "pass"
		case "gate_failed":
			sp, _ := ev["step"].(string)
			gateOf[sp] = "fail"
		}
	}
	_ = stepsN

	var agents []string
	for a := range agentsSet {
		agents = append(agents, a)
	}
	// Deterministic agent order
	for i := 0; i < len(agents); i++ {
		for j := i + 1; j < len(agents); j++ {
			if agents[j] < agents[i] {
				agents[i], agents[j] = agents[j], agents[i]
			}
		}
	}

	cr := &CookReport{
		CookID:         cookID,
		Recipe:         recipe,
		Status:         cookStatus,
		DurationMs:     durationMs,
		TotalCostUSD:   totalCost,
		Retries:        retries,
		MaxBudgetUSD:   maxBudget,
		Agents:         agents,
		TTFD:           map[string]int{},
		StallLoops:     map[string]StallMetrics{},
		ContextUsage:   map[string]float64{},
		TurnDist:       map[string]int{},
	}

	if finalPatchPath != "" {
		p := filepath.Join(cookDir, filepath.FromSlash(finalPatchPath))
		if b, err := os.ReadFile(p); err == nil {
			text := string(b)
			f, ins, del := PatchShortstat(text)
			cr.FilesChanged = f
			cr.LinesAdded = ins
			cr.LinesRemoved = del
		}
	}

	var totalIn, totalOut int
	for _, sp := range stepOrder {
		a := ac[sp]
		if a == nil {
			a = &stepAccum{stepPath: sp}
		}
		sr := StepReport{
			Name:        sp,
			Agent:       a.agent,
			OutputMode:  a.outputMode,
			Item:        a.item,
			Status:      a.status,
			Attempts:    stepStarts[sp],
			SessionMode: a.sessionMode,
			DurationMs:  a.durationMs,
			TokensIn:    a.tokensIn,
			TokensOut:   a.tokensOut,
			CostUSD:     a.costUSD,
			GateResult:  gateResultForStep(gateOf, sp, a.agent),
		}
		if stateBagEntries != nil {
			for k, ent := range stateBagEntries {
				if k == sp || strings.HasSuffix(k, "/"+pathBase(sp)) {
					if ent.SessionID != "" {
						sr.SessionID = ent.SessionID
					}
					break
				}
			}
		}
		// Session id fallback: last agent_completed in manifest for step
		if sr.SessionID == "" {
			sr.SessionID = sessionIDFromManifest(data, sp)
		}

		stdoutPath := findStdoutArtifact(cookDir, data, sp)
		var events []AgentEvent
		if stdoutPath != "" {
			rawOut, err := os.ReadFile(stdoutPath)
			if err == nil {
				stepStartTs := stepStartedAtFromManifest(data, sp)
				prov := ProviderForAgent(a.agent)
				events = ParseStdoutFile(rawOut, prov, stepStartTs)
				sr.Stall = ComputeStallMetrics(events)
				turns := BuildTurns(events, a.outputMode)
				sr.Turns = turns
				sr.TurnCounts = countTurnLabels(turns)
				for k, v := range sr.TurnCounts {
					cr.TurnDist[k] += v
				}
				if shouldComputeTTFD(a.outputMode, a.agent) {
					n := TTFDForDiff(turns)
					cr.TTFD[sp] = n
					sr.TTFD = n
				} else {
					sr.TTFD = -1
				}
			} else if shouldComputeTTFD(a.outputMode, a.agent) {
				cr.TTFD[sp] = 0
				sr.TTFD = 0
			} else {
				sr.TTFD = -1
			}
		} else if shouldComputeTTFD(a.outputMode, a.agent) {
			cr.TTFD[sp] = 0
			sr.TTFD = 0
		} else {
			sr.TTFD = -1
		}
		cr.StallLoops[sp] = sr.Stall
		diffPath := findDiffArtifact(cookDir, data, sp)
		if diffPath != "" {
			if b, err := os.ReadFile(diffPath); err == nil {
				_, ins, del := PatchShortstat(string(b))
				sr.LinesAdded = ins
				sr.LinesRemoved = del
				sr.FilesChanged = extractFilesFromPatch(string(b))
			}
		}
		if len(sr.FilesChanged) == 0 && stateBagEntries != nil {
			for k, ent := range stateBagEntries {
				if k == sp && ent.Files != "" {
					parts := strings.Split(ent.Files, ",")
					for _, p := range parts {
						p = strings.TrimSpace(p)
						if p != "" {
							sr.FilesChanged = append(sr.FilesChanged, p)
						}
					}
				}
			}
		}

		cw := ContextWindowForAgent(a.agent)
		var pct float64
		var label string
		if cw > 0 && sr.TokensIn > 0 {
			tokensForSession := sr.TokensIn
			if sr.SessionID != "" {
				tokensForSession = SessionTokensInByManifest(data, sr.SessionID)
			}
			pct = float64(tokensForSession) / float64(cw) * 100
			if pct > 100 {
				pct = 100
			}
			sr.ContextPct = pct
			label = fmt.Sprintf("%.0f%%", pct)
		} else if sr.TokensIn > 0 {
			label = "N/A"
		}
		sr.ContextLabel = label
		if sr.TokensIn > 0 {
			cr.ContextUsage[sp] = sr.ContextPct / 100
		}

		totalIn += sr.TokensIn
		totalOut += sr.TokensOut
		cr.Steps = append(cr.Steps, sr)
	}

	cr.TotalTokensIn = totalIn
	cr.TotalTokensOut = totalOut
	cr.StallTotal = AggregateStall(collectStalls(cr.Steps))

	return cr, nil
}

func pathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func gateResultForStep(gateOf map[string]string, stepPath, agent string) string {
	if agent == "" {
		if g, ok := gateOf[stepPath]; ok {
			return g
		}
		return "none"
	}
	if g, ok := gateOf[stepPath]; ok {
		return g
	}
	return "none"
}

func shouldComputeTTFD(outputMode, agent string) bool {
	if strings.TrimSpace(agent) == "" {
		return false
	}
	return strings.TrimSpace(outputMode) == "diff"
}

func collectStalls(steps []StepReport) []StallMetrics {
	var out []StallMetrics
	for _, s := range steps {
		out = append(out, s.Stall)
	}
	return out
}

func countTurnLabels(turns []Turn) map[string]int {
	m := map[string]int{}
	for _, t := range turns {
		if t.Label == "" {
			continue
		}
		m[t.Label]++
	}
	return m
}

func sessionIDFromManifest(manifest []byte, stepPath string) string {
	var last string
	sc := scanLines(manifest)
	for _, line := range sc {
		var ev map[string]interface{}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev["type"] != "agent_completed" {
			continue
		}
		sp, _ := ev["step"].(string)
		if sp != stepPath {
			continue
		}
		if sid, ok := ev["session_id"].(string); ok {
			last = sid
		}
	}
	return last
}

func stepStartedAtFromManifest(manifest []byte, stepPath string) time.Time {
	sc := scanLines(manifest)
	for _, line := range sc {
		var ev map[string]interface{}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev["type"] != "step_started" {
			continue
		}
		sp, _ := ev["step"].(string)
		if sp != stepPath {
			continue
		}
		ts, _ := ev["ts"].(string)
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			return t
		}
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", ts); err == nil {
			return t
		}
	}
	return time.Time{}
}

func findStdoutArtifact(cookDir string, manifest []byte, stepPath string) string {
	var last string
	sc := scanLines(manifest)
	for _, line := range sc {
		var ev map[string]interface{}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev["type"] != "agent_completed" {
			continue
		}
		sp, _ := ev["step"].(string)
		if sp != stepPath {
			continue
		}
		arts, _ := ev["artifacts"].(map[string]interface{})
		if arts == nil {
			continue
		}
		rel, _ := arts["stdout"].(string)
		if rel != "" {
			last = filepath.Join(cookDir, filepath.FromSlash(rel))
		}
	}
	return last
}

func findDiffArtifact(cookDir string, manifest []byte, stepPath string) string {
	var last string
	sc := scanLines(manifest)
	for _, line := range sc {
		var ev map[string]interface{}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev["type"] != "step_completed" {
			continue
		}
		sp, _ := ev["step"].(string)
		if sp != stepPath {
			continue
		}
		arts, _ := ev["artifacts"].(map[string]interface{})
		if arts == nil {
			continue
		}
		rel, _ := arts["diff"].(string)
		if rel != "" {
			last = filepath.Join(cookDir, filepath.FromSlash(rel))
		}
	}
	return last
}

func extractFilesFromPatch(patch string) []string {
	var files []string
	lines := strings.Split(patch, "\n")
	const pfx = "diff --git a/"
	for _, ln := range lines {
		if !strings.HasPrefix(ln, pfx) {
			continue
		}
		rest := strings.TrimPrefix(ln, pfx)
		if idx := strings.Index(rest, " "); idx > 0 {
			f := rest[:idx]
			f = strings.TrimPrefix(f, "a/")
			files = append(files, f)
		}
	}
	return files
}
