package state

import (
	"encoding/json"
	"strings"
	"sync"
)

// PrevSnapshotJSONKey is the state.json key for RotatePrev snapshots (template {prev.*}); not a flat string entry.
const PrevSnapshotJSONKey = "__gump_prev_snapshot__"

// State is the flat v0.0.4 workflow state: one string per fully-qualified key so templates and persistence stay simple.
type State struct {
	mu sync.RWMutex
	// entries keys use slashes in the step path segment (e.g. build/task-1/converge.output).
	entries map[string]string
	// prevEntries holds a virtual {prev.*} namespace per step path across retries; persisted under PrevSnapshotJSONKey.
	prevMu      sync.RWMutex
	prevEntries map[string]map[string]string
}

// New returns an empty State.
func New() *State {
	return &State{
		entries:     make(map[string]string),
		prevEntries: make(map[string]map[string]string),
	}
}

// Set writes a fully-qualified key.
func (s *State) Set(key string, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]string)
	}
	s.entries[key] = value
}

// Get returns the value for key or "".
func (s *State) Get(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.entries == nil {
		return ""
	}
	return s.entries[key]
}

// RotatePrev snapshots current step keys into the prev namespace so a retry can still resolve {prev.*}.
func (s *State) RotatePrev(stepPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		return
	}
	prefix := stepPath + "."
	if s.prevEntries == nil {
		s.prevEntries = make(map[string]map[string]string)
	}
	prev := make(map[string]string)
	for k, v := range s.entries {
		if strings.HasPrefix(k, prefix) {
			field := strings.TrimPrefix(k, prefix)
			prev[field] = v
		}
	}
	if len(prev) > 0 {
		s.prevEntries[stepPath] = prev
	}
}

// GetPrev returns a field from the last RotatePrev snapshot for stepPath.
func (s *State) GetPrev(stepPath string, field string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.prevEntries == nil {
		return ""
	}
	m := s.prevEntries[stepPath]
	if m == nil {
		return ""
	}
	return m[field]
}

// Keys returns a sorted copy of all entry keys (debug / serialization).
func (s *State) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entries) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.entries))
	for k := range s.entries {
		out = append(out, k)
	}
	// Stable order for tests and diffs.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Serialize exports string entries as JSON plus an optional PrevSnapshotJSONKey object for retry template {prev.*}.
func (s *State) Serialize() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]interface{})
	if s.entries != nil {
		for k, v := range s.entries {
			out[k] = v
		}
	}
	if len(s.prevEntries) > 0 {
		prev := make(map[string]map[string]string, len(s.prevEntries))
		for sp, m := range s.prevEntries {
			if m == nil {
				continue
			}
			cp := make(map[string]string, len(m))
			for fk, fv := range m {
				cp[fk] = fv
			}
			prev[sp] = cp
		}
		if len(prev) > 0 {
			out[PrevSnapshotJSONKey] = prev
		}
	}
	if len(out) == 0 {
		return json.MarshalIndent(map[string]string{}, "", "  ")
	}
	return json.MarshalIndent(out, "", "  ")
}

// Restore loads a State from Serialize output or migrates legacy state-bag.json shape.
func Restore(data []byte) (*State, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if _, legacy := raw["entries"]; legacy {
		return restoreLegacyStateBag(data)
	}
	st := New()
	for k, v := range raw {
		if k == PrevSnapshotJSONKey {
			var prev map[string]map[string]string
			if json.Unmarshal(v, &prev) != nil || len(prev) == 0 {
				continue
			}
			if st.prevEntries == nil {
				st.prevEntries = make(map[string]map[string]string)
			}
			for sp, fields := range prev {
				if fields == nil {
					continue
				}
				cp := make(map[string]string, len(fields))
				for fk, fv := range fields {
					cp[fk] = fv
				}
				st.prevEntries[sp] = cp
			}
			continue
		}
		var str string
		if err := json.Unmarshal(v, &str); err != nil {
			continue
		}
		st.Set(k, str)
	}
	return st, nil
}

type legacyPayload struct {
	Entries map[string]legacyEntry `json:"entries"`
	Run     map[string]string      `json:"run"`
}

type legacyEntry struct {
	Value     string `json:"output"`
	Diff      string `json:"diff"`
	Files     string `json:"files,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	StepPath  string `json:"step_path"`
	Status    string `json:"status"`
	Duration  string `json:"duration"`
	Cost      string `json:"cost"`
	Turns     string `json:"turns"`
	Retries   string `json:"retries"`
	TokensIn  string `json:"tokens_in"`
	TokensOut string `json:"tokens_out"`
	CacheRead string `json:"cache_read"`
	CacheWrite string `json:"cache_write"`
	CheckResult string `json:"check_result"`
}

// restoreLegacyStateBag flattens v0.0.3 state-bag.json so resume/replay keep working after R2.
func restoreLegacyStateBag(data []byte) (*State, error) {
	var p legacyPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	st := New()
	base := func(path string, e legacyEntry) {
		if path == "" {
			return
		}
		if e.Value != "" {
			st.Set(path+".output", e.Value)
		}
		if e.Diff != "" {
			st.Set(path+".diff", e.Diff)
		}
		if e.Files != "" {
			st.Set(path+".files", e.Files)
		}
		if e.SessionID != "" {
			st.Set(path+".session_id", e.SessionID)
		}
		if e.Status != "" {
			st.Set(path+".status", e.Status)
		}
		if e.Duration != "" {
			st.Set(path+".duration", e.Duration)
		}
		if e.Cost != "" {
			st.Set(path+".cost", e.Cost)
		}
		if e.Turns != "" {
			st.Set(path+".turns", e.Turns)
		}
		if e.Retries != "" {
			st.Set(path+".retries", e.Retries)
		}
		if e.TokensIn != "" {
			st.Set(path+".tokens_in", e.TokensIn)
		}
		if e.TokensOut != "" {
			st.Set(path+".tokens_out", e.TokensOut)
		}
		if e.CacheRead != "" {
			st.Set(path+".cache_read", e.CacheRead)
		}
		if e.CacheWrite != "" {
			st.Set(path+".cache_write", e.CacheWrite)
		}
		if e.CheckResult != "" {
			st.Set(path+".check_result", e.CheckResult)
		}
	}
	for path, e := range p.Entries {
		p := path
		if e.StepPath != "" {
			p = e.StepPath
		}
		p = strings.ReplaceAll(p, ".steps.", "/")
		p = strings.ReplaceAll(p, ".", "/")
		base(p, e)
	}
	for k, v := range p.Run {
		if v != "" {
			st.Set("run."+k, v)
		}
	}
	return st, nil
}

// SetStepOutput mirrors the former StateBag.Set: coerces empty output to diff for diff-style steps.
func (s *State) SetStepOutput(fullPath string, value string, diff string, files []string, sessionID string) {
	if value == "" && diff != "" {
		value = diff
	}
	s.Set(fullPath+".output", value)
	s.Set(fullPath+".diff", diff)
	if len(files) > 0 {
		s.Set(fullPath+".files", strings.Join(files, ", "))
	} else {
		s.Set(fullPath+".files", "")
	}
	s.Set(fullPath+".session_id", sessionID)
}

// Graft merges child flat keys under prefix/ (nested workflow boundary).
func (s *State) Graft(prefix string, child *State) {
	if child == nil {
		return
	}
	child.mu.RLock()
	defer child.mu.RUnlock()
	for k, v := range child.entries {
		s.Set(prefix+"/"+k, v)
	}
}

// ResetGroup rotates then clears all keys for steps under groupPath (group retry semantics).
func (s *State) ResetGroup(groupPath string) []string {
	prefix := strings.TrimSuffix(groupPath, "/") + "/"
	stepSet := map[string]struct{}{}
	for _, k := range s.Keys() {
		if strings.HasPrefix(k, prefix) {
			if sp := stepPathFromGroupedKey(groupPath, k); sp != "" {
				stepSet[sp] = struct{}{}
			}
		}
	}
	var moved []string
	for sp := range stepSet {
		s.RotatePrev(sp)
		for _, dk := range s.deleteKeysWithPrefix(sp + ".") {
			moved = append(moved, dk)
		}
	}
	return moved
}

func stepPathFromGroupedKey(groupPath, key string) string {
	prefix := strings.TrimSuffix(groupPath, "/") + "/"
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(key, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	dot := strings.Index(last, ".")
	if dot < 0 {
		return ""
	}
	var rel string
	if len(parts) == 1 {
		rel = last[:dot]
	} else {
		rel = strings.Join(parts[:len(parts)-1], "/") + "/" + last[:dot]
	}
	return groupPath + "/" + rel
}

func (s *State) deleteKeysWithPrefix(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var removed []string
	for k := range s.entries {
		if strings.HasPrefix(k, prefix) {
			removed = append(removed, k)
			delete(s.entries, k)
		}
	}
	return removed
}

// ClearKeyPrefix drops every entry under prefix so replay can re-execute one split task without resurrecting stale gate/session keys from the failed run.
func (s *State) ClearKeyPrefix(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries != nil {
		for k := range s.entries {
			if strings.HasPrefix(k, prefix) {
				delete(s.entries, k)
			}
		}
	}
	s.clearPrevLocked(prefix)
}

// ClearSplitSubtree wipes the planner step and all task branches so a replay from the split anchor cannot short-circuit on an old plan or half-finished tasks.
func (s *State) ClearSplitSubtree(splitStepPath string) {
	p := strings.Trim(splitStepPath, "/")
	if p == "" {
		return
	}
	slash := p + "/"
	dot := p + "."
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries != nil {
		for k := range s.entries {
			if strings.HasPrefix(k, slash) || strings.HasPrefix(k, dot) {
				delete(s.entries, k)
			}
		}
	}
	if s.prevEntries != nil {
		for sp := range s.prevEntries {
			if sp == p || strings.HasPrefix(sp, slash) {
				delete(s.prevEntries, sp)
			}
		}
	}
}

func (s *State) clearPrevLocked(prefix string) {
	if s.prevEntries == nil {
		return
	}
	for sp := range s.prevEntries {
		if strings.HasPrefix(sp, prefix) {
			delete(s.prevEntries, sp)
		}
	}
}

// DeleteStepOutputsForRestart clears live keys for paths while preserving prev for session reuse.
func (s *State) DeleteStepOutputsForRestart(paths []string) {
	for _, p := range paths {
		s.RotatePrev(p)
		s.deleteKeysWithPrefix(p + ".")
	}
}

// PrevSessionID returns the session_id from the prev snapshot (restart_from / reuse-on-retry).
func (s *State) PrevSessionID(fullPath string) string {
	return s.GetPrev(fullPath, "session_id")
}

// ClearSessionIDsForGroup blanks session_id for all steps under groupPath.
func (s *State) ClearSessionIDsForGroup(groupPath string) []string {
	prefix := strings.TrimSuffix(groupPath, "/") + "/"
	var cleared []string
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		return nil
	}
	for k, v := range s.entries {
		if !strings.HasSuffix(k, ".session_id") || v == "" {
			continue
		}
		stepPath := strings.TrimSuffix(k, ".session_id")
		if stepPath == groupPath || strings.HasPrefix(stepPath, prefix) {
			s.entries[k] = ""
			cleared = append(cleared, stepPath)
		}
	}
	if s.prevEntries != nil {
		for pk, m := range s.prevEntries {
			if pk == groupPath || strings.HasPrefix(pk, prefix) {
				if m != nil {
					delete(m, "session_id")
				}
			}
		}
	}
	return cleared
}

// UpdateStepAgentMetrics records per-step telemetry strings used in reports and templates.
func (s *State) UpdateStepAgentMetrics(fullPath string, durationMs int, costUSD float64, turns int, tokensIn, tokensOut, cacheReadTokens, cacheWriteTokens int) {
	s.Set(fullPath+".duration", formatInt(durationMs))
	s.Set(fullPath+".cost", formatCostUSD(costUSD))
	s.Set(fullPath+".turns", formatInt(turns))
	s.Set(fullPath+".tokens_in", formatInt(tokensIn))
	s.Set(fullPath+".tokens_out", formatInt(tokensOut))
	s.Set(fullPath+".cache_read", formatInt(cacheReadTokens))
	s.Set(fullPath+".cache_write", formatInt(cacheWriteTokens))
}

func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	// Avoid importing strconv in hot path? use strconv.Itoa
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [32]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func formatCostUSD(usd float64) string {
	if usd < 0.01 && usd > 0 {
		return trimFloat(usd, 4)
	}
	if usd == 0 {
		return "0.00"
	}
	return trimFloat(usd, 2)
}

func trimFloat(f float64, prec int) string {
	s := ""
	if prec == 2 {
		s = formatFloat2(f)
	} else {
		s = formatFloat4(f)
	}
	return s
}

func formatFloat2(f float64) string {
	// small helper without fmt for fewer deps — use simple approach
	n := int64(f*100 + 0.5)
	whole := n / 100
	frac := n % 100
	if frac < 0 {
		frac = -frac
	}
	return itoa(int(whole)) + "." + pad2(int(frac))
}

func formatFloat4(f float64) string {
	n := int64(f*10000 + 0.5)
	whole := n / 10000
	frac := n % 10000
	if frac < 0 {
		frac = -frac
	}
	w := itoa(int(whole))
	fs := itoa(int(frac))
	for len(fs) < 4 {
		fs = "0" + fs
	}
	return w + "." + fs
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

// SetStepCheckResult stores the aggregated validator outcome label for a step.
func (s *State) SetStepCheckResult(fullPath, checkResult string) {
	s.Set(fullPath+".check_result", checkResult)
}

// SetStepOutcome stores final status and retry count for a step.
func (s *State) SetStepOutcome(fullPath, status string, retries int) {
	s.Set(fullPath+".status", status)
	s.Set(fullPath+".retries", itoa(retries))
}

// SetRunMetric keeps legacy run.* keys for metrics-only consumers until R3 removes them entirely.
func (s *State) SetRunMetric(key, value string) {
	s.Set("run."+key, value)
}

// CloneRun copies run.* keys so a nested workflow can inherit aggregate telemetry without sharing the parent map.
func (s *State) CloneRun() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string)
	if s.entries == nil {
		return out
	}
	for k, v := range s.entries {
		if strings.HasPrefix(k, "run.") {
			out[strings.TrimPrefix(k, "run.")] = v
		}
	}
	return out
}

// SetRunAll replaces all run.* keys from a snapshot (child workflow boundary).
func (s *State) SetRunAll(run map[string]string) {
	for k, v := range run {
		s.SetRunMetric(k, v)
	}
}

// GetRunMetric reads run.* keys (telemetry / CLI); templates no longer resolve {run.*}.
func (s *State) GetRunMetric(key string) string {
	return s.Get("run." + key)
}

// AddRunCost accumulates run.cost as a formatted string.
func (s *State) AddRunCost(delta float64) {
	cur := parseFloat(s.Get("run.cost"))
	cur += delta
	s.SetRunMetric("cost", formatCostUSD(cur))
}

// IncrementRunTokensIn increments run.tokens_in.
func (s *State) IncrementRunTokensIn(delta int) {
	n := parseInt(s.Get("run.tokens_in")) + delta
	s.SetRunMetric("tokens_in", itoa(n))
}

// IncrementRunTokensOut increments run.tokens_out.
func (s *State) IncrementRunTokensOut(delta int) {
	n := parseInt(s.Get("run.tokens_out")) + delta
	s.SetRunMetric("tokens_out", itoa(n))
}

// IncrementRunRetries increments run.retries.
func (s *State) IncrementRunRetries() {
	n := parseInt(s.Get("run.retries")) + 1
	s.SetRunMetric("retries", itoa(n))
}

func parseInt(s string) int {
	n := 0
	neg := false
	for i, c := range s {
		if c == '-' && i == 0 {
			neg = true
			continue
		}
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		return -n
	}
	return n
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	neg := false
	i := 0
	if s[0] == '-' {
		neg = true
		i = 1
	}
	whole := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		whole = whole*10 + int(s[i]-'0')
		i++
	}
	frac := 0
	div := 1
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			frac = frac*10 + int(s[i]-'0')
			div *= 10
			i++
		}
	}
	v := float64(whole) + float64(frac)/float64(div)
	if neg {
		return -v
	}
	return v
}

// GetStepScoped resolves shortName.field using strict scope rules (engine / schema helpers).
func (s *State) GetStepScoped(shortName, scopePath, field string) string {
	ctx := &ResolveContext{State: s, StepPath: scopePath}
	return ctx.Resolve(shortName + "." + field)
}
