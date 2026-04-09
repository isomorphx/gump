package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/isomorphx/gump/internal/agent"
	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/cook"
	"github.com/isomorphx/gump/internal/workflow"
	"github.com/isomorphx/gump/internal/state"
)

func findResumableCook(repoRoot, runID string) (string, string, error) {
	runsDir := filepath.Join(repoRoot, brand.StateDir(), brand.RunsDir())
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", "", fmt.Errorf("list runs: %w", err)
	}
	if runID != "" {
		dir := filepath.Join(runsDir, runID)
		st, err := cook.ReadStatus(dir)
		if err != nil {
			return "", "", fmt.Errorf("run %s: %w", runID, err)
		}
		if st.Status == "pass" {
			return "", "", fmt.Errorf("run already completed successfully")
		}
		if st.Status != "fatal" && st.Status != "aborted" {
			return "", "", fmt.Errorf("run %s is not resumable (status=%s)", runID, st.Status)
		}
		return dir, st.Status, nil
	}
	type cand struct {
		dir    string
		status string
		mtime  int64
	}
	var cands []cand
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(runsDir, e.Name())
		st, err := cook.ReadStatus(dir)
		if err != nil {
			continue
		}
		if st.Status != "fatal" && st.Status != "aborted" {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, "status.json"))
		if err != nil {
			continue
		}
		cands = append(cands, cand{dir: dir, status: st.Status, mtime: info.ModTime().UnixNano()})
	}
	if len(cands) == 0 {
		var latestStatus string
		var latestM int64
		var haveLatest bool
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(runsDir, e.Name())
			st, err := cook.ReadStatus(dir)
			if err != nil {
				continue
			}
			info, err := os.Stat(filepath.Join(dir, "status.json"))
			if err != nil {
				continue
			}
			mt := info.ModTime().UnixNano()
			if !haveLatest || mt > latestM {
				latestStatus, latestM = st.Status, mt
				haveLatest = true
			}
		}
		if latestStatus == "pass" {
			return "", "", fmt.Errorf("run already completed successfully")
		}
		return "", "", fmt.Errorf("no fatal/aborted run found to resume")
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mtime > cands[j].mtime })
	return cands[0].dir, cands[0].status, nil
}

func parseManifestForResume(manifestPath string) (fatalStep string, passed map[string]bool, err error) {
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", nil, err
	}
	passed = map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		typ, _ := ev["type"].(string)
		if typ == "step_completed" {
			step, _ := ev["step"].(string)
			status, _ := ev["status"].(string)
			if status == "pass" {
				passed[step] = true
			} else if status == "fatal" || status == "fail" || status == "guard_failed" {
				fatalStep = step
			}
		}
		if typ == "circuit_breaker" && fatalStep == "" {
			step, _ := ev["step"].(string)
			if step != "" {
				fatalStep = step
			}
		}
	}
	if fatalStep == "" {
		return "", passed, fmt.Errorf("cannot determine fatal step from manifest")
	}
	return fatalStep, passed, nil
}

// RunResume resumes a failed run in-place (worktree preserved).
func RunResume(repoRoot, runID string, resolver agent.AdapterResolver, cfg *config.Config, agentsCLI map[string]string) (*cook.Cook, int, error) {
	cookDir, previousStatus, err := findResumableCook(repoRoot, runID)
	if err != nil {
		return nil, 0, err
	}
	stateJSONPath := filepath.Join(cookDir, "state.json")
	legacyStatePath := filepath.Join(cookDir, "state-bag.json")
	manifestPath := filepath.Join(cookDir, "manifest.ndjson")
	workflowPath := filepath.Join(cookDir, "workflow-snapshot.yaml")
	ctxPath := filepath.Join(cookDir, "context-snapshot.json")
	if _, err := os.Stat(stateJSONPath); err != nil {
		if _, err2 := os.Stat(legacyStatePath); err2 != nil {
			return nil, 0, fmt.Errorf("resume precondition failed: state.json (or legacy state-bag.json) missing. Fix: re-run from scratch or clean stale runs with `gump gc --keep-last 1`")
		}
	}
	for _, p := range []string{manifestPath, workflowPath, ctxPath} {
		if _, err := os.Stat(p); err != nil {
			if strings.HasSuffix(p, "manifest.ndjson") {
				return nil, 0, fmt.Errorf("resume precondition failed: manifest.ndjson missing. Fix: re-run from scratch or clean stale runs with `gump gc --keep-last 1`")
			}
			return nil, 0, fmt.Errorf("resume precondition failed: missing %s", filepath.Base(p))
		}
	}
	var ctx struct {
		Spec string `json:"spec"`
	}
	ctxData, _ := os.ReadFile(ctxPath)
	_ = json.Unmarshal(ctxData, &ctx)
	stateData, err := cook.ReadStateFile(cookDir)
	if err != nil {
		return nil, 0, fmt.Errorf("read state: %w", err)
	}
	sb, err := state.Restore(stateData)
	if err != nil {
		return nil, 0, fmt.Errorf("restore state: %w", err)
	}
	workflowRaw, err := os.ReadFile(workflowPath)
	if err != nil {
		return nil, 0, fmt.Errorf("read workflow snapshot: %w", err)
	}
	rec, _, err := workflow.Parse(workflowRaw, "")
	if err != nil {
		return nil, 0, fmt.Errorf("parse workflow snapshot: %w", err)
	}
	if errs := workflow.Validate(rec); len(errs) > 0 {
		return nil, 0, errs[0]
	}
	fatalStep, passed, err := parseManifestForResume(manifestPath)
	if err != nil {
		return nil, 0, err
	}
	cookID := filepath.Base(cookDir)
	wt := filepath.Join(repoRoot, brand.StateDir(), "worktrees", brand.WorktreeDirPrefix()+cookID)
	if _, err := os.Stat(wt); err != nil {
		return nil, 0, fmt.Errorf("resume precondition failed: worktree missing (%s). Fix: clear stale runs with `gump gc --keep-last 1`", wt)
	}
	specPath := filepath.Join(repoRoot, ctx.Spec)
	c, err := cook.NewCookForReplay(rec, specPath, repoRoot, workflowRaw, "", wt)
	if err != nil {
		return nil, 0, err
	}
	specContent, err := os.ReadFile(specPath)
	if err != nil {
		return nil, 0, err
	}
	eng := New(c, rec, resolver, cfg, string(specContent))
	eng.AgentsCLI = agentsCLI
	eng.FromStep = fatalStep
	eng.ResumePassedSteps = passed
	eng.ResumePreviousStatus = previousStatus
	eng.State = sb
	if err := eng.Run(); err != nil {
		return c, 0, err
	}
	return c, len(eng.Steps), nil
}
