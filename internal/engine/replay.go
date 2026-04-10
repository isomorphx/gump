package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/isomorphx/gump/internal/agent"
	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/ledger"
	"github.com/isomorphx/gump/internal/run"
	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/workflow"
)

// FindLastFatalRun returns the run dir of the most recent run with status "fatal", or the run dir for runID if runID != "" (status not checked).
func FindLastFatalRun(repoRoot string, runID string) (string, error) {
	runsDir := filepath.Join(repoRoot, brand.StateDir(), brand.RunsDir())
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("list runs: %w", err)
	}
	if runID != "" {
		dir := filepath.Join(runsDir, runID)
		if _, err := os.Stat(dir); err != nil {
			return "", fmt.Errorf("run %s: %w", runID, err)
		}
		return dir, nil
	}
	type cand struct {
		dir   string
		mtime int64
	}
	var candidates []cand
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(runsDir, e.Name())
		st, err := run.ReadStatus(dir)
		if err != nil || st.Status != "fatal" {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, "status.json"))
		if err != nil {
			continue
		}
		candidates = append(candidates, cand{dir: dir, mtime: info.ModTime().UnixNano()})
	}
	if len(candidates) == 0 {
		dep := "--" + "co" + "ok" + " <uuid>"
		return "", fmt.Errorf("no fatal run found — execute a run first and let it fail, or use %s", dep)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mtime > candidates[j].mtime })
	return candidates[0].dir, nil
}

// ResolveFromStep resolves --from-step (short name or full path) against the workflow only. The manifest is not used for resolution (it may not contain the from-step if that step failed). Returns the full step path for the engine. The manifest is only used later for RestoreCommitBeforeStep.
func ResolveFromStep(fromStep string, rec *workflow.Workflow) (string, error) {
	return workflow.ResolveEngineStepPath(fromStep, rec)
}

// RunReplay finds the fatal run, restores state bag, reuses the original run worktree (HITL), and runs the engine from fromStep. Returns the new run and steps count so the CLI can write status.
func RunReplay(repoRoot, specPath, fromStep, runID string, rec *workflow.Workflow, workflowRaw []byte, resolver agent.AdapterResolver, cfg *config.Config, agentsCLI map[string]string) (*run.Run, int, error) {
	runDir, err := FindLastFatalRun(repoRoot, runID)
	if err != nil {
		return nil, 0, err
	}
	info, err := ledger.ReadReplayInfo(runDir)
	if err != nil {
		return nil, 0, fmt.Errorf("read replay info: %w", err)
	}
	resolvedStep, err := ResolveFromStep(fromStep, rec)
	if err != nil {
		return nil, 0, err
	}
	restoreCommit := info.RestoreCommitBeforeStep(resolvedStep, info.InitialCommit)
	originalWorktree := filepath.Join(repoRoot, brand.StateDir(), "worktrees", brand.WorktreeDirPrefix()+info.OriginalRunID)
	stateBagData, err := run.ReadStateFile(runDir)
	if err != nil {
		return nil, 0, fmt.Errorf("read state: %w", err)
	}
	sb, err := state.Restore(stateBagData)
	if err != nil {
		return nil, 0, fmt.Errorf("restore state: %w", err)
	}
	if workflow.MatchSplitEachQualifiedPath(rec, resolvedStep) {
		sb.ClearKeyPrefix(workflow.SplitTaskStatePrefix(resolvedStep))
	} else if workflow.IsSplitCompositePath(rec, resolvedStep) {
		// WHY: replay from the split anchor must re-run GET/RUN/GATE and every task, not reuse a stale plan or per-task keys.
		sb.ClearSplitSubtree(resolvedStep)
	}
	specContent, err := os.ReadFile(specPath)
	if err != nil {
		return nil, 0, err
	}
	c, err := run.NewRunForReplay(rec, specPath, repoRoot, workflowRaw, restoreCommit, originalWorktree, cfg)
	if err != nil {
		return nil, 0, err
	}
	c.State = sb
	eng := New(c, rec, resolver, cfg, string(specContent))
	eng.AgentsCLI = agentsCLI
	eng.FromStep = resolvedStep
	eng.replayOriginalRunID = info.OriginalRunID
	eng.replayRestoredCommit = restoreCommit
	if err := eng.Execute(); err != nil {
		return c, 0, err
	}
	return c, len(eng.Steps), nil
}
