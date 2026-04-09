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

// FindLastFatalCook returns the run dir of the most recent run with status "fatal", or the run dir for cookID if cookID != "" (status not checked).
func FindLastFatalCook(repoRoot string, cookID string) (string, error) {
	cooksDir := filepath.Join(repoRoot, brand.StateDir(), brand.RunsDir())
	entries, err := os.ReadDir(cooksDir)
	if err != nil {
		return "", fmt.Errorf("list cooks: %w", err)
	}
	if cookID != "" {
		dir := filepath.Join(cooksDir, cookID)
		if _, err := os.Stat(dir); err != nil {
			return "", fmt.Errorf("run %s: %w", cookID, err)
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
		dir := filepath.Join(cooksDir, e.Name())
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
		return "", fmt.Errorf("no fatal run found — execute a run first and let it fail, or use --cook <uuid>")
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mtime > candidates[j].mtime })
	return candidates[0].dir, nil
}

// ResolveFromStep resolves --from-step (short name or full path) against the recipe only. The manifest is not used for resolution (it may not contain the from-step if that step failed). Returns the full step path for the engine. The manifest is only used later for RestoreCommitBeforeStep.
func ResolveFromStep(fromStep string, rec *workflow.Workflow) (string, error) {
	return workflow.ResolveEngineStepPath(fromStep, rec)
}

// RunReplay finds the fatal run, restores state bag, reuses the original run worktree (HITL), and runs the engine from fromStep. Returns the new run and steps count so the CLI can write status.
func RunReplay(repoRoot, specPath, fromStep, cookID string, rec *workflow.Workflow, recipeRaw []byte, resolver agent.AdapterResolver, cfg *config.Config, agentsCLI map[string]string) (*run.Run, int, error) {
	cookDir, err := FindLastFatalCook(repoRoot, cookID)
	if err != nil {
		return nil, 0, err
	}
	info, err := ledger.ReadReplayInfo(cookDir)
	if err != nil {
		return nil, 0, fmt.Errorf("read replay info: %w", err)
	}
	resolvedStep, err := ResolveFromStep(fromStep, rec)
	if err != nil {
		return nil, 0, err
	}
	restoreCommit := info.RestoreCommitBeforeStep(resolvedStep, info.InitialCommit)
	originalWorktree := filepath.Join(repoRoot, brand.StateDir(), "worktrees", brand.WorktreeDirPrefix()+info.OriginalCookID)
	stateBagData, err := run.ReadStateFile(cookDir)
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
	c, err := run.NewRunForReplay(rec, specPath, repoRoot, recipeRaw, restoreCommit, originalWorktree, cfg)
	if err != nil {
		return nil, 0, err
	}
	c.State = sb
	eng := New(c, rec, resolver, cfg, string(specContent))
	eng.AgentsCLI = agentsCLI
	eng.FromStep = resolvedStep
	eng.replayOriginalCookID = info.OriginalCookID
	eng.replayRestoredCommit = restoreCommit
	if err := eng.Execute(); err != nil {
		return c, 0, err
	}
	return c, len(eng.Steps), nil
}
