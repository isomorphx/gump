package engine

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/template"
	"github.com/isomorphx/gump/internal/workflow"
)

// SubWorkflowRunner executes nested workflows against the parent run (same ledger/worktree) with isolated child state (R5).
type SubWorkflowRunner struct {
	ParentEngine *Engine
}

func newChildEngine(parent *Engine, childWf *workflow.Workflow, childState *state.State, ledgerPrefix string) *Engine {
	return &Engine{
		Run:                 parent.Run,
		Workflow:            childWf,
		Resolver:            parent.Resolver,
		Config:              parent.Config,
		SpecContent:         parent.SpecContent,
		State:               childState,
		AgentsCLI:           parent.AgentsCLI,
		RunAgentOverride:    parent.RunAgentOverride,
		agentsUsed:          make(map[string]struct{}),
		globalStepAttempts:  make(map[string]int),
		budgetTracker:       NewBudgetTracker(childWf.MaxBudget),
		pendingRestartFrom:  make(map[string]*ErrorContext),
		LedgerStepPrefix:    ledgerPrefix,
		SkipNoWritePost:     true,
		runStartedAt:        parent.runStartedAt,
		stepTotalEstimate:   len(childWf.Steps),
	}
}

func (parent *Engine) mergeChildTelemetry(child *Engine) {
	if parent == nil || child == nil {
		return
	}
	parent.totalCostUSD += child.totalCostUSD
	parent.globalTokens += child.globalTokens
	if parent.agentsUsed == nil {
		parent.agentsUsed = make(map[string]struct{})
	}
	for a := range child.agentsUsed {
		parent.agentsUsed[a] = struct{}{}
	}
	if parent.State != nil && child.totalCostUSD > 0 {
		parent.State.AddRunCost(child.totalCostUSD)
	}
}

// RunSubWorkflow resolves and runs a nested workflow; child metrics fold into the parent for global caps (spec R5).
func (swr *SubWorkflowRunner) RunSubWorkflow(workflowPath string, inputs map[string]string, worktreeDir string, ledgerPrefix string, resolveCtx *state.ResolveContext) (*state.State, error) {
	if swr == nil || swr.ParentEngine == nil {
		// WHY: callers must never treat a missing runner as success with a nil state bag.
		return nil, fmt.Errorf("subworkflow: parent engine not set")
	}
	parent := swr.ParentEngine
	resolved, err := workflow.Resolve(strings.TrimSpace(workflowPath), parent.Run.RepoRoot)
	if err != nil {
		return nil, err
	}
	rd := ""
	if resolved.Path != "" {
		rd = filepath.Dir(resolved.Path)
	}
	childWf, _, err := workflow.Parse(resolved.Raw, rd)
	if err != nil {
		return nil, err
	}
	if ev := workflow.Validate(childWf); len(ev) > 0 {
		return nil, ev[0]
	}
	childState := state.New()
	// WHY: nested runs inherit run.* telemetry; parent state may be absent in tests or thin harnesses.
	if parent.State != nil {
		childState.SetRunAll(parent.State.CloneRun())
	}
	for k, v := range inputs {
		childState.Set(k, template.Resolve(v, resolveCtx))
	}
	if resolveCtx != nil {
		for _, key := range workflow.CollectReferencedVars(childWf) {
			if childState.Get(key) != "" {
				continue
			}
			if v := resolveCtx.Resolve(key); v != "" {
				childState.Set(key, v)
				continue
			}
			if v := parent.State.Get(key); v != "" {
				childState.Set(key, v)
			}
		}
	}
	childEng := newChildEngine(parent, childWf, childState, ledgerPrefix)
	err = childEng.executeWorkflowSequential()
	parent.mergeChildTelemetry(childEng)
	return childState, err
}

// graftChildStateIntoParent copies each child state key under namespace+".state." on the parent (R5 retry_validator §4.3).
func graftChildStateIntoParent(parent *state.State, namespace string, child *state.State) {
	if parent == nil || child == nil || strings.TrimSpace(namespace) == "" {
		return
	}
	prefix := strings.TrimSpace(namespace) + ".state."
	for _, k := range child.Keys() {
		parent.Set(prefix+k, child.Get(k))
	}
}

// RunValidator runs a validator-shaped workflow and reads the final validate.json verdict (ADR-039).
func (swr *SubWorkflowRunner) RunValidator(workflowPath string, inputs map[string]string, namespace string, worktreeDir string, resolveCtx *state.ResolveContext) (pass bool, comments string, childState *state.State, err error) {
	ledgerPrefix := strings.ReplaceAll(namespace, ".", "/")
	childState, err = swr.RunSubWorkflow(workflowPath, inputs, worktreeDir, ledgerPrefix, resolveCtx)
	ok, _, com, _, perr := ParseValidateJSON(worktreeDir)
	if perr == nil {
		// WHY: a nested validate step returns a fatal error to the child engine on fail, but the gate still needs the JSON verdict.
		return ok, com, childState, nil
	}
	if err != nil {
		return false, "", childState, err
	}
	return false, "", childState, perr
}
