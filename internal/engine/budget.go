package engine

import (
	"fmt"
	"sync"

	"github.com/isomorphx/pudding/internal/ledger"
)

// BudgetExceededError wraps ledger emission details so the engine can record budget_exceeded before circuit_breaker.
type BudgetExceededError struct {
	Event ledger.BudgetExceeded
	Msg   string
}

func (e *BudgetExceededError) Error() string { return e.Msg }

// BudgetTracker enforces cook- and step-level max_budget after each agent run (no pre-flight prediction).
type BudgetTracker struct {
	CookBudget   float64
	StepBudgets  map[string]float64
	CookSpent    float64
	StepSpent    map[string]float64
	mu           sync.Mutex
}

func NewBudgetTracker(cookBudget float64) *BudgetTracker {
	return &BudgetTracker{
		CookBudget:  cookBudget,
		StepBudgets: make(map[string]float64),
		StepSpent:   make(map[string]float64),
	}
}

func (bt *BudgetTracker) SetStepBudget(step string, budget float64) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	if bt.StepBudgets == nil {
		bt.StepBudgets = make(map[string]float64)
	}
	bt.StepBudgets[step] = budget
}

func (bt *BudgetTracker) AddCost(step string, costUSD float64) error {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.CookSpent += costUSD
	if bt.StepSpent == nil {
		bt.StepSpent = make(map[string]float64)
	}
	bt.StepSpent[step] += costUSD
	if bt.CookBudget > 0 && bt.CookSpent > bt.CookBudget {
		return &BudgetExceededError{
			Event: ledger.BudgetExceeded{Step: "", Scope: "cook", MaxUSD: bt.CookBudget, SpentUSD: bt.CookSpent},
			Msg:   fmt.Sprintf("cook budget exceeded: spent $%.2f of $%.2f max", bt.CookSpent, bt.CookBudget),
		}
	}
	if lim := bt.StepBudgets[step]; lim > 0 && bt.StepSpent[step] > lim {
		return &BudgetExceededError{
			Event: ledger.BudgetExceeded{Step: step, Scope: "step", MaxUSD: lim, SpentUSD: bt.StepSpent[step]},
			Msg:   fmt.Sprintf("step '%s' budget exceeded: spent $%.2f of $%.2f max", step, bt.StepSpent[step], lim),
		}
	}
	return nil
}

// WarningIfUnavailable returns a stderr line when cost is zero so operators know limits are not enforced.
func (bt *BudgetTracker) WarningIfUnavailable(costUSD float64) string {
	if costUSD != 0 {
		return ""
	}
	return "warning: budget tracking unavailable — provider did not report cost"
}
