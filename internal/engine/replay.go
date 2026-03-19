package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/isomorphx/pudding/internal/agent"
	"github.com/isomorphx/pudding/internal/config"
	"github.com/isomorphx/pudding/internal/cook"
	"github.com/isomorphx/pudding/internal/ledger"
	"github.com/isomorphx/pudding/internal/recipe"
	"github.com/isomorphx/pudding/internal/statebag"
)

// FindLastFatalCook returns the cook dir of the most recent cook with status "fatal", or the cook dir for cookID if cookID != "" (status not checked).
func FindLastFatalCook(repoRoot string, cookID string) (string, error) {
	cooksDir := filepath.Join(repoRoot, ".pudding", "cooks")
	entries, err := os.ReadDir(cooksDir)
	if err != nil {
		return "", fmt.Errorf("list cooks: %w", err)
	}
	if cookID != "" {
		dir := filepath.Join(cooksDir, cookID)
		if _, err := os.Stat(dir); err != nil {
			return "", fmt.Errorf("cook %s: %w", cookID, err)
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
		st, err := cook.ReadStatus(dir)
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
		return "", fmt.Errorf("no fatal cook found — run a cook first and let it fail, or use --cook <uuid>")
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mtime > candidates[j].mtime })
	return candidates[0].dir, nil
}

// fullPath returns the engine step path for a leaf (PathPrefix/Name or Name).
func leafFullPath(l recipe.LeafStep) string {
	if l.PathPrefix == "" {
		return l.Name
	}
	return l.PathPrefix + "/" + l.Name
}

// ResolveFromStep resolves --from-step (short name or full path) against the recipe only. The manifest is not used for resolution (it may not contain the from-step if that step failed). Returns the full step path for the engine. The manifest is only used later for RestoreCommitBeforeStep.
func ResolveFromStep(fromStep string, rec *recipe.Recipe) (string, error) {
	fromStep = strings.TrimSpace(fromStep)
	if fromStep == "" {
		return "", fmt.Errorf("--from-step is required for replay")
	}
	recipeName := rec.Name
	leaves := recipe.LeafSteps(rec)
	// Full path (contains "/"): must match a leaf path in the recipe.
	if strings.Contains(fromStep, "/") {
		for _, l := range leaves {
			if leafFullPath(l) == fromStep {
				return fromStep, nil
			}
		}
		return "", fmt.Errorf("step %q not found in recipe %q", fromStep, recipeName)
	}
	// Short name: resolve against recipe step names only.
	var withName []recipe.LeafStep
	for _, l := range leaves {
		if l.Name == fromStep {
			withName = append(withName, l)
		}
	}
	if len(withName) == 0 {
		return "", fmt.Errorf("step %q not found in recipe %q", fromStep, recipeName)
	}
	if len(withName) > 1 {
		var paths []string
		for _, l := range withName {
			paths = append(paths, leafFullPath(l))
		}
		return "", fmt.Errorf("step %q is ambiguous in recipe %q. Use full path: %s", fromStep, recipeName, strings.Join(paths, ", "))
	}
	return leafFullPath(withName[0]), nil
}

// RunReplay finds the fatal cook, restores state bag, reuses the original cook's worktree (HITL), and runs the engine from fromStep. Returns the new cook and steps count so the CLI can write status.
func RunReplay(repoRoot, specPath, fromStep, cookID string, rec *recipe.Recipe, recipeRaw []byte, resolver agent.AdapterResolver, cfg *config.Config, agentsCLI map[string]string) (*cook.Cook, int, error) {
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
	originalWorktree := filepath.Join(repoRoot, ".pudding", "worktrees", "cook-"+info.OriginalCookID)
	stateBagPath := filepath.Join(cookDir, "state-bag.json")
	stateBagData, err := os.ReadFile(stateBagPath)
	if err != nil {
		return nil, 0, fmt.Errorf("read state-bag: %w", err)
	}
	sb, err := statebag.Restore(stateBagData)
	if err != nil {
		return nil, 0, fmt.Errorf("restore state-bag: %w", err)
	}
	specContent, err := os.ReadFile(specPath)
	if err != nil {
		return nil, 0, err
	}
	c, err := cook.NewCookForReplay(rec, specPath, repoRoot, recipeRaw, restoreCommit, originalWorktree)
	if err != nil {
		return nil, 0, err
	}
	eng := New(c, rec, resolver, cfg, string(specContent))
	eng.AgentsCLI = agentsCLI
	eng.FromStep = resolvedStep
	eng.StateBag = sb
	eng.replayOriginalCookID = info.OriginalCookID
	eng.replayRestoredCommit = restoreCommit
	if err := eng.Run(); err != nil {
		return c, 0, err
	}
	return c, len(eng.Steps), nil
}
