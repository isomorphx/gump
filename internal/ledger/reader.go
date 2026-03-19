package ledger

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// StatusSnapshot is the in-memory view of an in-progress cook for pudding status.
type StatusSnapshot struct {
	CookDir       string
	CookID        string
	Recipe        string
	Spec          string
	StartedAt     time.Time
	LastEventAt   time.Time
	TotalCostUSD  float64
	CompletedSteps []CompletedStepRow
	CurrentStep   string
	CurrentAgent  string
	CurrentTask   string
	CurrentAttempt int
	AgentRunningSince time.Time
}

// CompletedStepRow is one line in the "Completed steps" section.
type CompletedStepRow struct {
	Step     string
	Duration time.Duration
	CostUSD  float64
	Agent    string
	Extra    string // e.g. "(4 tasks)"
}

// FindInProgressCook lists .pudding/cooks/*/manifest.ndjson and returns the cook dir of the one in progress (last event not cook_completed), most recent first. Returns "" if none.
func FindInProgressCook(repoRoot string) string {
	cooksDir := filepath.Join(repoRoot, ".pudding", "cooks")
	entries, err := os.ReadDir(cooksDir)
	if err != nil {
		return ""
	}
	type cand struct {
		dir string
		ts  time.Time
	}
	var candidates []cand
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		manifestPath := filepath.Join(cooksDir, e.Name(), manifestName)
		f, err := os.Open(manifestPath)
		if err != nil {
			continue
		}
		var lastLine string
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			lastLine = strings.TrimSpace(sc.Text())
		}
		_ = f.Close()
		if lastLine == "" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(lastLine), &ev) != nil {
			continue
		}
		if ev["type"] == "cook_completed" {
			continue
		}
		ts, _ := ev["ts"].(string)
		t, _ := time.Parse(time.RFC3339Nano, ts)
		if t.IsZero() {
			t = time.Now()
		}
		candidates = append(candidates, cand{dir: filepath.Join(cooksDir, e.Name()), ts: t})
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ts.After(candidates[j].ts) })
	return candidates[0].dir
}

// ReadStatus reads a manifest.ndjson and builds StatusSnapshot for the given cook dir (in progress).
func ReadStatus(cookDir string) (*StatusSnapshot, error) {
	manifestPath := filepath.Join(cookDir, manifestName)
	f, err := os.Open(manifestPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	snap := &StatusSnapshot{CookDir: cookDir}
	var lastTs time.Time
	var lastStepAgent string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		ts, _ := ev["ts"].(string)
		t, _ := time.Parse(time.RFC3339Nano, ts)
		if !t.IsZero() {
			lastTs = t
		}
		switch ev["type"] {
		case "cook_started":
			snap.CookID, _ = ev["cook_id"].(string)
			snap.Recipe, _ = ev["recipe"].(string)
			snap.Spec, _ = ev["spec"].(string)
			snap.StartedAt = t
		case "step_started":
			lastStepAgent, _ = ev["agent"].(string)
			snap.CurrentStep, _ = ev["step"].(string)
			snap.CurrentAgent, _ = ev["agent"].(string)
			snap.CurrentTask, _ = ev["task"].(string)
			if a, ok := ev["attempt"].(float64); ok {
				snap.CurrentAttempt = int(a)
			}
			snap.AgentRunningSince = t
		case "step_completed":
			step, _ := ev["step"].(string)
			durMs, _ := ev["duration_ms"].(float64)
			snap.CompletedSteps = append(snap.CompletedSteps, CompletedStepRow{
				Step:     step,
				Duration: time.Duration(durMs) * time.Millisecond,
				Agent:    lastStepAgent,
			})
		case "agent_completed":
			cost, _ := ev["cost_usd"].(float64)
			snap.TotalCostUSD += cost
			if len(snap.CompletedSteps) > 0 {
				idx := len(snap.CompletedSteps) - 1
				snap.CompletedSteps[idx].CostUSD = cost
				if snap.CompletedSteps[idx].Agent == "" {
					snap.CompletedSteps[idx].Agent, _ = ev["agent"].(string)
				}
			}
		case "cook_completed":
			if c, ok := ev["total_cost_usd"].(float64); ok {
				snap.TotalCostUSD = c
			}
		case "group_started":
			if tc, ok := ev["task_count"].(float64); ok && tc > 0 {
				if len(snap.CompletedSteps) > 0 {
					snap.CompletedSteps[len(snap.CompletedSteps)-1].Extra = " (" + fmt.Sprint(int(tc)) + " tasks)"
				}
			}
		}
	}
	snap.LastEventAt = lastTs
	// Agent for completed steps: we don't have it from step_completed; keep from agent_completed when we merge. We already set CostUSD and Agent on last step when we see agent_completed. For earlier steps we'd need to track step->agent. Leave Agent empty for older steps for now.
	return snap, sc.Err()
}

// ReplayInfo is used to start a replay: step paths seen and ordered step_completed for restore-commit resolution.
type ReplayInfo struct {
	InitialCommit    string   // from cook_started
	StepPathsSeen   []string // unique step paths from step_started/step_completed for --from-step resolution
	OriginalCookID  string
	StepCompletedOrder []struct{ Step, Commit string } // in manifest order; used to get commit before a given step
}

// ReadReplayInfo reads the manifest in cookDir and returns ReplayInfo. RestoreCommit for a given step is computed via RestoreCommitBeforeStep.
func ReadReplayInfo(cookDir string) (*ReplayInfo, error) {
	manifestPath := filepath.Join(cookDir, manifestName)
	f, err := os.Open(manifestPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info := &ReplayInfo{}
	stepSet := make(map[string]struct{})
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev["type"] {
		case "cook_started":
			info.InitialCommit, _ = ev["commit"].(string)
			info.OriginalCookID, _ = ev["cook_id"].(string)
		case "step_started", "step_completed":
			step, _ := ev["step"].(string)
			if step != "" {
				stepSet[step] = struct{}{}
			}
			if ev["type"] == "step_completed" {
				c, _ := ev["commit"].(string)
				info.StepCompletedOrder = append(info.StepCompletedOrder, struct{ Step, Commit string }{Step: step, Commit: c})
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for s := range stepSet {
		info.StepPathsSeen = append(info.StepPathsSeen, s)
	}
	sort.Strings(info.StepPathsSeen)
	return info, nil
}

// RestoreCommitBeforeStep returns the commit of the last step_completed before the given step path (for replay worktree state). If step is first or not found, returns initialCommit.
func (r *ReplayInfo) RestoreCommitBeforeStep(resolvedStep string, initialCommit string) string {
	var last string
	if initialCommit != "" {
		last = initialCommit
	}
	for _, e := range r.StepCompletedOrder {
		if e.Step == resolvedStep {
			break
		}
		if e.Commit != "" {
			last = e.Commit
		}
	}
	return last
}

