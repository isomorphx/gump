package engine

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/isomorphx/pudding/internal/diff"
	"github.com/isomorphx/pudding/internal/ledger"
	"github.com/isomorphx/pudding/internal/plan"
	"github.com/isomorphx/pudding/internal/recipe"
	"github.com/isomorphx/pudding/internal/sandbox"
)

// parallelResult holds the outcome of one parallel unit (sub-step or task) for barrier and merge.
type parallelResult struct {
	StepName    string
	WorktreeDir string
	BranchName  string
	Err         error
	Diff        *diff.DiffContract // set when output was diff and step passed
	OutputMode  string
	Steps       []StepExecution
	StepCount   int
	RetryCount  int
	CostUSD     float64
	AgentsUsed  map[string]struct{}
}

// RunParallelGroup runs sub-steps or tasks in parallel worktrees, then merges diff outputs or fails on conflict.
func RunParallelGroup(e *Engine, step *recipe.Step, stepPath string, subSteps []recipe.Step, planTasks []plan.Task, baseCommit string, lastSessionByAgent map[string]string, parentSession recipe.SessionConfig, groupAgentOverride string) error {
	repoRoot := e.Cook.RepoRoot
	cookID := e.Cook.ID
	units := buildParallelUnits(step, stepPath, subSteps, planTasks)
	results := make([]parallelResult, len(units))
	var wg sync.WaitGroup
	for i := range units {
		u := &units[i]
		wtDir := filepath.Join(repoRoot, ".pudding", "worktrees", "cook-"+cookID, "parallel-"+u.Name)
		brName := "pudding/cook-" + cookID + "-parallel-" + u.Name
		if err := sandbox.CreateWorktreeAtCommit(repoRoot, wtDir, brName, baseCommit); err != nil {
			return fmt.Errorf("parallel worktree %s: %w", u.Name, err)
		}
		c := e.Cook.CloneForWorktree(wtDir, brName)
		subEng := New(c, e.Recipe, e.Resolver, e.Config, e.SpecContent)
		subEng.StateBag = e.StateBag
		subEng.AgentsCLI = e.AgentsCLI
		subEng.CookAgentOverride = e.CookAgentOverride
		subEng.Cook.Ledger = e.Cook.Ledger
		wg.Add(1)
		go func(idx int, eng *Engine, unit *parallelUnit) {
			defer wg.Done()
			defer func() {
				_ = sandbox.RemoveWorktree(repoRoot, eng.Cook.WorktreeDir, eng.Cook.BranchName)
			}()
			sessionMap := make(map[string]string)
			err := eng.executeSteps(unit.Steps, unit.Task, unit.PathPrefix, sessionMap, parentSession, groupAgentOverride)
			res := parallelResult{
				StepName:    unit.Name,
				WorktreeDir: eng.Cook.WorktreeDir,
				BranchName:  eng.Cook.BranchName,
				Err:         err,
				OutputMode:  unit.OutputMode,
			}
			if err == nil {
				if unit.OutputMode == "diff" {
					// Exclude .pudding and provider context files so only repo code is merged.
					dc, _ := sandbox.FinalDiffExclude(eng.Cook.WorktreeDir, baseCommit, sandbox.ParallelMergeExcludes)
					res.Diff = dc
				}
				res.Steps = append([]StepExecution(nil), eng.Steps...)
				res.StepCount = eng.stepCompletedCount
				res.RetryCount = eng.retryTriggeredCount
				res.CostUSD = eng.totalCostUSD
				res.AgentsUsed = eng.agentsUsed
			}
			results[idx] = res
		}(i, subEng, u)
	}
	wg.Wait()
	for _, r := range results {
		if r.Err != nil {
			return r.Err
		}
	}
	for _, r := range results {
		e.stepCompletedCount += r.StepCount
		e.retryTriggeredCount += r.RetryCount
		e.totalCostUSD += r.CostUSD
		for k := range r.AgentsUsed {
			e.agentsUsed[k] = struct{}{}
		}
		e.Steps = append(e.Steps, r.Steps...)
	}
	diffsWithOutput := filterDiffResults(results)
	if len(diffsWithOutput) == 0 {
		return nil
	}
	return mergeParallelDiffs(e, stepPath, step.Name, baseCommit, diffsWithOutput)
}

type parallelUnit struct {
	Name        string
	PathPrefix  string
	Steps       []recipe.Step
	Task        *plan.Task
	OutputMode  string
}

func buildParallelUnits(step *recipe.Step, stepPath string, subSteps []recipe.Step, planTasks []plan.Task) []parallelUnit {
	if len(planTasks) > 0 {
		units := make([]parallelUnit, len(planTasks))
		for i, task := range planTasks {
			taskPrefix := step.Name + "/" + task.Name
			if stepPath != "" {
				taskPrefix = stepPath + "/" + task.Name
			}
			units[i] = parallelUnit{
				Name:       task.Name,
				PathPrefix: taskPrefix,
				Steps:      subSteps,
				Task:       &planTasks[i],
				OutputMode: inferOutputMode(subSteps),
			}
		}
		return units
	}
	units := make([]parallelUnit, len(subSteps))
	for i, s := range subSteps {
		pathPrefix := stepPath + "/" + s.Name
		out := s.Output
		if out == "" && s.Agent != "" {
			out = "diff"
		}
		units[i] = parallelUnit{
			Name:       s.Name,
			PathPrefix: pathPrefix,
			Steps:      []recipe.Step{s},
			Task:       nil,
			OutputMode: out,
		}
	}
	return units
}

func inferOutputMode(steps []recipe.Step) string {
	for _, s := range steps {
		if s.Output != "" {
			return s.Output
		}
		if s.Agent != "" {
			return "diff"
		}
	}
	return "diff"
}

func filterDiffResults(results []parallelResult) []parallelResult {
	var out []parallelResult
	for _, r := range results {
		if r.Diff != nil && r.Diff.Patch != "" {
			out = append(out, r)
		}
	}
	return out
}

// mergeParallelDiffs detects file conflicts, then applies patches in declaration order and snapshots the main worktree.
func mergeParallelDiffs(e *Engine, stepPath, stepName, baseCommit string, results []parallelResult) error {
	mainDir := e.Cook.WorktreeDir
	type stepDiff struct {
		stepName string
		files    []string
		patch    string
	}
	var stepDiffs []stepDiff
	for _, r := range results {
		stepDiffs = append(stepDiffs, stepDiff{r.StepName, r.Diff.FilesChanged, r.Diff.Patch})
	}
	// Conflict detection: any two steps touching the same file → circuit breaker.
	for i := 0; i < len(stepDiffs); i++ {
		for j := i + 1; j < len(stepDiffs); j++ {
			common := fileIntersection(stepDiffs[i].files, stepDiffs[j].files)
			if len(common) > 0 {
				reason := fmt.Sprintf("merge conflict: steps %q and %q both modify: %v. Hint: ensure the plan decomposes tasks with disjoint blast radii.", stepDiffs[i].stepName, stepDiffs[j].stepName, common)
				if e.Cook.Ledger != nil {
					_ = e.Cook.Ledger.Emit(ledger.CircuitBreaker{Step: stepPath, Scope: "group", Reason: reason, TotalAttempts: 1})
				}
				return fmt.Errorf("Fan-out merge failed: steps %q and %q both modify:\n  - %s\nHint: ensure the plan decomposes tasks with disjoint blast radii.", stepDiffs[i].stepName, stepDiffs[j].stepName, joinPaths(common))
			}
		}
	}
	for _, sd := range stepDiffs {
		if err := sandbox.ApplyPatch(mainDir, []byte(sd.patch)); err != nil {
			reason := fmt.Sprintf("merge apply failed: %v", err)
			if e.Cook.Ledger != nil {
				_ = e.Cook.Ledger.Emit(ledger.CircuitBreaker{Step: stepPath, Scope: "group", Reason: reason, TotalAttempts: 1})
			}
			return fmt.Errorf("Fan-out merge apply: %w", err)
		}
	}
	_, err := e.Cook.Snapshot(stepName, "", 1)
	return err
}

func fileIntersection(a, b []string) []string {
	set := make(map[string]bool)
	for _, f := range a {
		set[f] = true
	}
	var out []string
	for _, f := range b {
		if set[f] {
			out = append(out, f)
		}
	}
	return out
}

func joinPaths(files []string) string {
	s := ""
	for i, f := range files {
		if i > 0 {
			s += "\n  - "
		}
		s += f
	}
	return s
}
