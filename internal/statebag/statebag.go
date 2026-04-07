package statebag

import (
	"encoding/json"
	"fmt"
	"log"
	"path"
	"strconv"
	"strings"
	"sync"
)

// Entry holds the output and diff for one step, keyed by fully-qualified path.
// We store StepPath so Get can resolve by short name and scope (same group, parent, root).
type Entry struct {
	Value     string `json:"output"`
	Diff      string `json:"diff"`
	Files     string `json:"files,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	StepPath  string `json:"step_path"`

	// Metrics captured during execution (Part B).
	// Stored as strings because the template engine expects string interpolation.
	Status      string `json:"status"`
	Duration    string `json:"duration"`
	Cost        string `json:"cost"`
	Turns       string `json:"turns"`
	Retries     string `json:"retries"`
	TokensIn    string `json:"tokens_in"`
	TokensOut   string `json:"tokens_out"`
	CacheRead   string `json:"cache_read"`
	CacheWrite  string `json:"cache_write"`
	CheckResult string `json:"check_result"`
}

// StateBag stores step outputs keyed by fully-qualified path so prompts can reference {steps.<name>.output} and {steps.<name>.diff} without a global "plan" variable.
// Concurrent access is safe.
type StateBag struct {
	mu      sync.RWMutex
	entries map[string]Entry
	// prev holds entries moved by ResetGroup so retry can resolve "previous" vs "current" (step 6).
	prev map[string]Entry
	// run holds run-level metrics for templates (run.* placeholders).
	run map[string]string

	// Numeric accumulators used to safely update run.cost/tokens across parallel steps.
	runCostUSD   float64
	runTokensIn  int
	runTokensOut int
	runRetries   int
}

// New returns an empty StateBag.
func New() *StateBag {
	return &StateBag{
		entries: make(map[string]Entry),
		prev:    make(map[string]Entry),
		run: map[string]string{
			"cost":       "",
			"duration":   "",
			"tokens_in":  "",
			"tokens_out": "",
			"retries":    "",
			"status":     "",
		},
	}
}

// Set records output, diff, changed files, and optional session id for a step at fullPath.
func (sb *StateBag) Set(fullPath string, value string, diff string, files []string, sessionID string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.entries == nil {
		sb.entries = make(map[string]Entry)
	}
	// WHY: agent metrics might be written before output extraction; preserve metrics on output overwrite.
	existing := sb.entries[fullPath]
	existing.StepPath = fullPath

	// WHY: v4 unifie {steps.<n>.diff} et {steps.<n>.output}. In "diff" output-mode
	// the engine historically stored the patch in Diff while Value stayed empty.
	// Copying diff → output for empty Value keeps templates consistent without
	// needing to change runtime snapshots/ledger formats here.
	if value == "" && diff != "" {
		value = diff
	}
	filesStr := ""
	if len(files) > 0 {
		filesStr = strings.Join(files, ", ")
	}
	existing.Value = value
	existing.Diff = diff
	existing.Files = filesStr
	existing.SessionID = sessionID
	sb.entries[fullPath] = existing
}

// Get resolves {steps.<shortName>.output} or {steps.<shortName>.diff} using scope proximity:
// current entries first (precedence over prev), then parent-by-parent scope chain. First match wins; multiple matches at same scope panic.
func (sb *StateBag) Get(shortName string, scopePath string, field string) string {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	e := sb.resolveByScope(shortName, scopePath)
	if e == nil {
		return ""
	}
	if field == "diff" {
		return e.Diff
	}
	if field == "files" {
		return e.Files
	}
	if field == "session_id" {
		return e.SessionID
	}
	switch field {
	case "status":
		return e.Status
	case "duration":
		return e.Duration
	case "cost":
		return e.Cost
	case "turns":
		return e.Turns
	case "retries":
		return e.Retries
	case "tokens_in":
		return e.TokensIn
	case "tokens_out":
		return e.TokensOut
	case "cache_read":
		return e.CacheRead
	case "cache_write":
		return e.CacheWrite
	case "check_result":
		return e.CheckResult
	}
	return e.Value
}

// buildScopeChain returns scopePath and each parent prefix down to "" (e.g. "a/b/c" -> ["a/b/c", "a/b", "a", ""]).
func buildScopeChain(scopePath string) []string {
	if scopePath == "" {
		return []string{""}
	}
	parts := strings.Split(scopePath, "/")
	out := make([]string, 0, len(parts)+1)
	for i := len(parts); i >= 0; i-- {
		out = append(out, strings.Join(parts[:i], "/"))
	}
	return out
}

// resolveInSource finds the entry for shortName in a single source map by walking the scope chain (closest scope first). Returns nil if none; panics if ambiguous.
func resolveInSource(source map[string]Entry, shortName string, scopePath string) *Entry {
	shortNameSlash := strings.ReplaceAll(strings.ReplaceAll(shortName, ".steps.", "/"), ".", "/")
	var candidates []Entry
	for _, e := range source {
		if path.Base(e.StepPath) != shortName && e.StepPath != shortName && e.StepPath != shortNameSlash && path.Base(e.StepPath) != path.Base(shortNameSlash) {
			continue
		}
		candidates = append(candidates, e)
	}
	if len(candidates) == 0 {
		return nil
	}
	for _, scope := range buildScopeChain(scopePath) {
		var atScope []Entry
		for _, e := range candidates {
			inScope := (scope == "" && !strings.Contains(e.StepPath, "/")) || (scope != "" && (e.StepPath == scope || strings.HasPrefix(e.StepPath, scope+"/")))
			if inScope {
				atScope = append(atScope, e)
			}
		}
		if len(atScope) == 1 {
			return &atScope[0]
		}
		if len(atScope) > 1 {
			log.Printf("statebag: ambiguous reference '%s' in scope '%s', returning empty (use fully-qualified path)", shortName, scopePath)
			return nil
		}
	}
	if len(candidates) == 1 {
		return &candidates[0]
	}
	log.Printf("statebag: ambiguous reference '%s' in scope '%s', returning empty (use fully-qualified path)", shortName, scopePath)
	return nil
}

// resolveByScope picks the entry for shortName closest to scopePath from current entries only. After ResetGroup, keys moved to prev are not visible so {steps.code.output} resolves to "" (retry semantics).
func (sb *StateBag) resolveByScope(shortName string, scopePath string) *Entry {
	return resolveInSource(sb.entries, shortName, scopePath)
}

// Graft merges a child state bag into this bag under prefix.
func (sb *StateBag) Graft(prefix string, child *StateBag) {
	if child == nil {
		return
	}
	child.mu.RLock()
	childEntries := make(map[string]Entry, len(child.entries))
	for k, v := range child.entries {
		childEntries[k] = v
	}
	childRun := make(map[string]string, len(child.run))
	for k, v := range child.run {
		childRun[k] = v
	}
	child.mu.RUnlock()

	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.entries == nil {
		sb.entries = make(map[string]Entry)
	}
	for k, v := range childEntries {
		target := prefix + ".steps." + strings.ReplaceAll(k, "/", ".")
		if strings.HasPrefix(k, "run.") {
			target = prefix + "." + k
		}
		v.StepPath = target
		sb.entries[target] = v
	}
	for k, v := range childRun {
		if k != "" {
			sb.run[k] = v
		}
	}
}

func (sb *StateBag) CloneRun() map[string]string {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	out := make(map[string]string, len(sb.run))
	for k, v := range sb.run {
		out[k] = v
	}
	return out
}

func (sb *StateBag) SetRunAll(run map[string]string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.run == nil {
		sb.run = map[string]string{}
	}
	for k, v := range run {
		sb.run[k] = v
	}
}

// ResetGroup moves entries under groupPath from "current" to "prev" for retry semantics (step 6).
// Returns the list of keys moved, for the ledger event state_bag_scope_reset.
func (sb *StateBag) ResetGroup(groupPath string) []string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.prev == nil {
		sb.prev = make(map[string]Entry)
	}
	prefix := strings.TrimSuffix(groupPath, "/") + "/"
	var moved []string
	for k, e := range sb.entries {
		if k == groupPath || strings.HasPrefix(k, prefix) {
			sb.prev[k] = e
			delete(sb.entries, k)
			moved = append(moved, k)
		}
	}
	return moved
}

// DeleteStepOutputsForRestart removes current entries for paths; session ids are copied into prev so reuse-on-retry can resume after restart_from.
func (sb *StateBag) DeleteStepOutputsForRestart(paths []string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.prev == nil {
		sb.prev = make(map[string]Entry)
	}
	for _, p := range paths {
		if e, ok := sb.entries[p]; ok {
			// WHY: restart_from is logically a retry; keep prior metrics in prev so templates don't see them.
			sb.prev[p] = e
			delete(sb.entries, p)
		}
	}
}

// PrevSessionID returns the session id stored in prev for a full step path (reuse-on-retry after restart_from).
func (sb *StateBag) PrevSessionID(fullPath string) string {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	if sb.prev == nil {
		return ""
	}
	return sb.prev[fullPath].SessionID
}

// ClearSessionIDsForGroup invalidates session ids for all entries under groupPath.
// Returns the list of affected step paths.
func (sb *StateBag) ClearSessionIDsForGroup(groupPath string) []string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	prefix := strings.TrimSuffix(groupPath, "/") + "/"
	var cleared []string
	for k, e := range sb.entries {
		if k == groupPath || strings.HasPrefix(k, prefix) {
			if e.SessionID != "" {
				e.SessionID = ""
				sb.entries[k] = e
				cleared = append(cleared, k)
			}
		}
	}
	for k, e := range sb.prev {
		if k == groupPath || strings.HasPrefix(k, prefix) {
			if e.SessionID != "" {
				e.SessionID = ""
				sb.prev[k] = e
				cleared = append(cleared, k)
			}
		}
	}
	return cleared
}

func formatCostUSDString(usd float64) string {
	// WHY: keep JSON-stored cost stable for tests/templates by using rounded decimals.
	if usd < 0.01 && usd > 0 {
		return fmt.Sprintf("%.4f", usd)
	}
	if usd == 0 {
		return "0.00"
	}
	return fmt.Sprintf("%.2f", usd)
}

// SetRunMetric updates run-level metrics for run.* templates.
func (sb *StateBag) SetRunMetric(key, value string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.run == nil {
		sb.run = map[string]string{}
	}
	sb.run[key] = value
}

func (sb *StateBag) GetRunMetric(key string) string {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	if sb.run == nil {
		return ""
	}
	return sb.run[key]
}

// AddRunCost increments run.cost by the given delta.
func (sb *StateBag) AddRunCost(deltaCostUSD float64) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.run == nil {
		sb.run = map[string]string{}
	}
	sb.runCostUSD += deltaCostUSD
	sb.run["cost"] = formatCostUSDString(sb.runCostUSD)
}

func (sb *StateBag) IncrementRunTokensIn(delta int) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.run == nil {
		sb.run = map[string]string{}
	}
	sb.runTokensIn += delta
	sb.run["tokens_in"] = fmt.Sprintf("%d", sb.runTokensIn)
}

func (sb *StateBag) IncrementRunTokensOut(delta int) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.run == nil {
		sb.run = map[string]string{}
	}
	sb.runTokensOut += delta
	sb.run["tokens_out"] = fmt.Sprintf("%d", sb.runTokensOut)
}

// IncrementRunRetries increments run.retries by 1 (retry_triggered event).
func (sb *StateBag) IncrementRunRetries() {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.run == nil {
		sb.run = map[string]string{}
	}
	sb.runRetries++
	sb.run["retries"] = fmt.Sprintf("%d", sb.runRetries)
}

// UpdateStepAgentMetrics stores agent completion metrics at fullPath.
func (sb *StateBag) UpdateStepAgentMetrics(fullPath string, durationMs int, costUSD float64, turns int, tokensIn, tokensOut, cacheReadTokens, cacheWriteTokens int) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.entries == nil {
		sb.entries = make(map[string]Entry)
	}
	e := sb.entries[fullPath]
	e.StepPath = fullPath
	e.Duration = fmt.Sprintf("%d", durationMs)
	e.Cost = formatCostUSDString(costUSD)
	e.Turns = fmt.Sprintf("%d", turns)
	e.TokensIn = fmt.Sprintf("%d", tokensIn)
	e.TokensOut = fmt.Sprintf("%d", tokensOut)
	e.CacheRead = fmt.Sprintf("%d", cacheReadTokens)
	e.CacheWrite = fmt.Sprintf("%d", cacheWriteTokens)
	sb.entries[fullPath] = e
}

// SetStepCheckResult stores validator result at fullPath.
func (sb *StateBag) SetStepCheckResult(fullPath, checkResult string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.entries == nil {
		sb.entries = make(map[string]Entry)
	}
	e := sb.entries[fullPath]
	e.StepPath = fullPath
	e.CheckResult = checkResult
	sb.entries[fullPath] = e
}

// SetStepOutcome stores final step status and retries at fullPath.
func (sb *StateBag) SetStepOutcome(fullPath, status string, retries int) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.entries == nil {
		sb.entries = make(map[string]Entry)
	}
	e := sb.entries[fullPath]
	e.StepPath = fullPath
	e.Status = status
	e.Retries = fmt.Sprintf("%d", retries)
	sb.entries[fullPath] = e
}

// Serialize exports the State Bag to JSON for persistence in state-bag.json.
func (sb *StateBag) Serialize() ([]byte, error) {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	payload := struct {
		Entries map[string]Entry  `json:"entries"`
		Run     map[string]string `json:"run"`
	}{
		Entries: sb.entries,
		Run:     sb.run,
	}
	return json.MarshalIndent(payload, "", "  ")
}

// Restore reconstructs a StateBag from JSON (for replay).
func Restore(data []byte) (*StateBag, error) {
	var payload struct {
		Entries map[string]Entry  `json:"entries"`
		Run     map[string]string `json:"run"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.Entries == nil {
		payload.Entries = make(map[string]Entry)
	}
	sb := &StateBag{entries: payload.Entries, prev: make(map[string]Entry)}
	sb.run = payload.Run
	if sb.run == nil {
		sb.run = map[string]string{
			"cost":       "",
			"duration":   "",
			"tokens_in":  "",
			"tokens_out": "",
			"retries":    "",
			"status":     "",
		}
	}

	if v := sb.run["cost"]; v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			sb.runCostUSD = f
		}
	}
	if v := sb.run["tokens_in"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			sb.runTokensIn = n
		}
	}
	if v := sb.run["tokens_out"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			sb.runTokensOut = n
		}
	}
	if v := sb.run["retries"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			sb.runRetries = n
		}
	}
	return sb, nil
}
