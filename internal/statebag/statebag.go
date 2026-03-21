package statebag

import (
	"encoding/json"
	"path"
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
}

// StateBag stores step outputs keyed by fully-qualified path so prompts can reference {steps.<name>.output} and {steps.<name>.diff} without a global "plan" variable.
// Concurrent access is safe.
type StateBag struct {
	mu      sync.RWMutex
	entries map[string]Entry
	// prev holds entries moved by ResetGroup so retry can resolve "previous" vs "current" (step 6).
	prev map[string]Entry
}

// New returns an empty StateBag.
func New() *StateBag {
	return &StateBag{entries: make(map[string]Entry), prev: make(map[string]Entry)}
}

// Set records output, diff, changed files, and optional session id for a step at fullPath.
func (sb *StateBag) Set(fullPath string, value string, diff string, files []string, sessionID string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.entries == nil {
		sb.entries = make(map[string]Entry)
	}
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
	sb.entries[fullPath] = Entry{Value: value, Diff: diff, Files: filesStr, SessionID: sessionID, StepPath: fullPath}
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
	var candidates []Entry
	for _, e := range source {
		if path.Base(e.StepPath) != shortName && e.StepPath != shortName {
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
			panic("statebag: ambiguous step reference " + shortName + " — use fully-qualified path")
		}
	}
	if len(candidates) == 1 {
		return &candidates[0]
	}
	panic("statebag: ambiguous step reference " + shortName + " — use fully-qualified path")
}

// resolveByScope picks the entry for shortName closest to scopePath from current entries only. After ResetGroup, keys moved to prev are not visible so {steps.code.output} resolves to "" (retry semantics).
func (sb *StateBag) resolveByScope(shortName string, scopePath string) *Entry {
	return resolveInSource(sb.entries, shortName, scopePath)
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
			if e.SessionID != "" {
				prev := sb.prev[p]
				prev.SessionID = e.SessionID
				prev.StepPath = p
				sb.prev[p] = prev
			}
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

// Serialize exports the State Bag to JSON for persistence in state-bag.json.
func (sb *StateBag) Serialize() ([]byte, error) {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	payload := struct {
		Entries map[string]Entry `json:"entries"`
	}{Entries: sb.entries}
	return json.MarshalIndent(payload, "", "  ")
}

// Restore reconstructs a StateBag from JSON (for replay).
func Restore(data []byte) (*StateBag, error) {
	var payload struct {
		Entries map[string]Entry `json:"entries"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.Entries == nil {
		payload.Entries = make(map[string]Entry)
	}
	return &StateBag{entries: payload.Entries, prev: make(map[string]Entry)}, nil
}
