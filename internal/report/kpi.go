package report

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// StallMetrics captures stdout-derived stall signals (spec §6).
type StallMetrics struct {
	ToolErrorCount      int
	CorrectionLoops     int
	FatalLoops          int
	RepeatedActionLoops int
}

// TTFDForDiff returns the 1-based turn index of the first coding turn, or total turns if none.
func TTFDForDiff(turns []Turn) int {
	if len(turns) == 0 {
		return 0
	}
	for _, t := range turns {
		if t.Label == "coding" {
			return t.Number
		}
	}
	return len(turns)
}

// AggregateStall sums stall metrics (tool errors are already cook-level in our model).
func AggregateStall(m []StallMetrics) StallMetrics {
	var out StallMetrics
	for _, x := range m {
		out.ToolErrorCount += x.ToolErrorCount
		out.CorrectionLoops += x.CorrectionLoops
		out.FatalLoops += x.FatalLoops
		out.RepeatedActionLoops += x.RepeatedActionLoops
	}
	return out
}

// ComputeStallMetrics derives stall KPIs from the classified stdout stream (Claude + Codex).
func ComputeStallMetrics(events []AgentEvent) StallMetrics {
	var sm StallMetrics
	for _, e := range events {
		if isToolError(e) {
			sm.ToolErrorCount++
		}
	}
	sm.FatalLoops = countFatalFailureRuns(events)
	sm.RepeatedActionLoops = countRepeatedSuccessRuns(events)
	sm.CorrectionLoops = countCorrectionLoops(events)
	return sm
}

func isToolError(e AgentEvent) bool {
	raw := extractJSONObject(e.Raw)
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	if e.SemanticLabel == "user" {
		if errVal, ok := m["is_error"].(bool); ok && errVal {
			return true
		}
	}
	if e.SemanticLabel == "shell" || e.SemanticLabel == "shell/test" {
		if ec, ok := exitCodeFromJSON(m); ok && ec != 0 {
			return true
		}
	}
	return false
}

func exitCodeFromJSON(m map[string]interface{}) (int, bool) {
	if v, ok := m["exit_code"]; ok {
		return numToIntAny(v)
	}
	return 0, false
}

func numToIntAny(v interface{}) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case json.Number:
		i, err := x.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

// countFatalFailureRuns counts runs of 3+ consecutive failures on the same action.
// Interleaved assistant lines do not break the streak—only the ordered sequence of tool failures matters.
func countFatalFailureRuns(events []AgentEvent) int {
	var failureKeys []string
	for i := 0; i < len(events); i++ {
		if !isToolError(events[i]) {
			continue
		}
		var k string
		if i > 0 && isAssistantToolEvent(events[i-1]) {
			k = identityKey(events[i-1])
		} else {
			k = identityKey(events[i])
		}
		failureKeys = append(failureKeys, k)
	}
	return countConsecutiveSameKeyEpisodes(failureKeys, 3)
}

// countRepeatedSuccessRuns counts runs of 3+ consecutive successful invocations of the same action.
// Includes Claude (assistant → user success) and Codex standalone shell lines that are not errors.
func countRepeatedSuccessRuns(events []AgentEvent) int {
	return countConsecutiveSameKeyEpisodes(successInvocationKeysInOrder(events), 3)
}

// successInvocationKeysInOrder walks the stream in order and records one identity key per successful tool outcome.
func successInvocationKeysInOrder(events []AgentEvent) []string {
	var keys []string
	for i := 0; i < len(events); i++ {
		if i < len(events)-1 && isAssistantToolEvent(events[i]) && isUserSuccessEvent(events[i+1]) {
			keys = append(keys, identityKey(events[i]))
			i++
			continue
		}
		if isCodexShellSuccess(events[i]) {
			keys = append(keys, identityKey(events[i]))
		}
	}
	return keys
}

// isCodexShellSuccess is a successful shell outcome without an assistant→user pair (typical Codex NDJSON).
// Claude tool calls use type=assistant; those are counted only via assistant→user(success), not here.
func isCodexShellSuccess(e AgentEvent) bool {
	if e.SemanticLabel != "shell" && e.SemanticLabel != "shell/test" {
		return false
	}
	if isToolError(e) {
		return false
	}
	raw := extractJSONObject(e.Raw)
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	if typ, _ := m["type"].(string); typ == "assistant" {
		return false
	}
	return true
}

func countConsecutiveSameKeyEpisodes(keys []string, minLen int) int {
	if minLen < 1 {
		return 0
	}
	var n int
	var cur string
	streak := 0
	counted := false
	for _, k := range keys {
		if k != cur {
			cur, streak, counted = k, 1, false
			continue
		}
		streak++
		if streak >= minLen && !counted {
			n++
			counted = true
		}
	}
	return n
}

func isAssistantToolEvent(e AgentEvent) bool {
	switch e.SemanticLabel {
	case "write", "read", "shell", "shell/test", "web", "mcp":
		return true
	default:
		return false
	}
}

func isUserSuccessEvent(e AgentEvent) bool {
	if e.SemanticLabel != "user" {
		return false
	}
	raw := extractJSONObject(e.Raw)
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	if errVal, ok := m["is_error"].(bool); ok && errVal {
		return false
	}
	return true
}

// countCorrectionLoops counts consecutive turn pairs where turn N has a tool error on key K and turn N+1 has a successful same-key tool invocation.
func countCorrectionLoops(events []AgentEvent) int {
	if len(events) == 0 {
		return 0
	}
	turns := BuildTurns(events, "diff")
	if len(turns) < 2 {
		return 0
	}
	var n int
	for i := 0; i < len(turns)-1; i++ {
		errKeys := errorKeysInTurn(turns[i])
		if len(errKeys) == 0 {
			continue
		}
		succKeys := successKeysInTurn(turns[i+1])
		for k := range errKeys {
			if succKeys[k] {
				n++
				break
			}
		}
	}
	return n
}

func errorKeysInTurn(t Turn) map[string]bool {
	out := make(map[string]bool)
	evs := t.Events
	for i := 0; i < len(evs); i++ {
		if !isToolError(evs[i]) {
			continue
		}
		if i > 0 && isAssistantToolEvent(evs[i-1]) {
			out[identityKey(evs[i-1])] = true
		} else {
			out[identityKey(evs[i])] = true
		}
	}
	return out
}

func successKeysInTurn(t Turn) map[string]bool {
	out := make(map[string]bool)
	evs := t.Events
	for i := 0; i < len(evs)-1; i++ {
		if !isAssistantToolEvent(evs[i]) {
			continue
		}
		if !isUserSuccessEvent(evs[i+1]) {
			continue
		}
		out[identityKey(evs[i])] = true
	}
	for _, e := range evs {
		if isCodexShellSuccess(e) {
			out[identityKey(e)] = true
		}
	}
	return out
}

// identityKey combines semantic label and first stable argument so the same physical tool maps across turns.
func identityKey(e AgentEvent) string {
	lab := e.SemanticLabel
	raw := extractJSONObject(e.Raw)
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) != nil {
		return lab + "|"
	}
	arg := firstArgFromJSON(m, e.SemanticLabel)
	return lab + "|" + arg
}

func firstArgFromJSON(m map[string]interface{}, semantic string) string {
	if s := firstArgFromAssistantJSON(m); s != "" {
		return s
	}
	if s := firstArgFromUserJSON(m); s != "" {
		return s
	}
	if semantic == "shell" || semantic == "shell/test" {
		if s, ok := stringField(m, "command", "cmd", "shell"); ok {
			return s
		}
	}
	return ""
}

func firstArgFromAssistantJSON(m map[string]interface{}) string {
	msg, ok := m["message"].(map[string]interface{})
	if !ok {
		return ""
	}
	content, ok := msg["content"]
	if !ok {
		return ""
	}
	arr, ok := content.([]interface{})
	if !ok || len(arr) == 0 {
		return ""
	}
	for _, item := range arr {
		b, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if b["type"] != "tool_use" {
			continue
		}
		name, _ := b["name"].(string)
		in, _ := b["input"].(map[string]interface{})
		if in == nil {
			return name
		}
		for _, k := range []string{"command", "file_path", "path", "pattern", "glob"} {
			if s, ok := in[k].(string); ok && s != "" {
				return name + ":" + s
			}
		}
		raw, _ := json.Marshal(in)
		return name + ":" + string(raw)
	}
	return ""
}

func firstArgFromUserJSON(m map[string]interface{}) string {
	msg, ok := m["message"].(map[string]interface{})
	if !ok {
		return ""
	}
	content, ok := msg["content"]
	if !ok {
		return ""
	}
	arr, ok := content.([]interface{})
	if !ok {
		return ""
	}
	for _, item := range arr {
		b, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if b["type"] != "tool_result" {
			continue
		}
		if s, ok := b["tool_name"].(string); ok {
			return s
		}
		if s, ok := b["name"].(string); ok {
			return s
		}
	}
	return ""
}

func stringField(m map[string]interface{}, keys ...string) (string, bool) {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s, true
		}
	}
	return "", false
}

func extractJSONObject(raw []byte) []byte {
	idx := bytes.IndexByte(raw, '{')
	if idx < 0 {
		return raw
	}
	return bytes.TrimSpace(raw[idx:])
}

// ContextWindowForAgent returns the static window size for usage ratio (spec table); 0 means unknown.
func ContextWindowForAgent(agent string) int {
	a := strings.ToLower(strings.TrimSpace(agent))
	switch {
	case strings.HasPrefix(a, "claude-opus"):
		return 200000
	case strings.HasPrefix(a, "claude-sonnet"):
		return 200000
	case strings.HasPrefix(a, "claude-haiku"):
		return 200000
	case a == "codex" || strings.HasPrefix(a, "codex-"):
		return 192000
	case strings.HasPrefix(a, "gemini"):
		return 1000000
	case strings.HasPrefix(a, "qwen"):
		return 131072
	case strings.HasPrefix(a, "opencode"):
		return 128000
	default:
		return 0
	}
}

// SessionTokensIn sums tokens_in for all agent_completed lines in the manifest sharing sessionID.
func SessionTokensInByManifest(manifest []byte, sessionID string) int {
	if sessionID == "" {
		return 0
	}
	var total int
	sc := scanLines(manifest)
	for _, line := range sc {
		var ev map[string]interface{}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev["type"] != "agent_completed" {
			continue
		}
		sid, _ := ev["session_id"].(string)
		if sid != sessionID {
			continue
		}
		ti, _ := numToInt(ev["tokens_in"])
		total += ti
	}
	return total
}

func numToInt(v interface{}) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	default:
		return 0, false
	}
}

func scanLines(data []byte) [][]byte {
	var lines [][]byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

var (
	shortstatFilesRE = regexp.MustCompile(`(\d+)\s+files?\s+changed`)
	shortstatInsRE   = regexp.MustCompile(`(\d+)\s+insertions?\b`)
	shortstatDelRE   = regexp.MustCompile(`(\d+)\s+deletions?\b`)
)

// PatchShortstat parses git-style shortstat lines often present near the top of aggregated patches.
func PatchShortstat(patchText string) (files, insertions, deletions int) {
	if patchText == "" {
		return 0, 0, 0
	}
	if m := shortstatFilesRE.FindStringSubmatch(patchText); len(m) > 1 {
		files, _ = strconv.Atoi(m[1])
	}
	if m := shortstatInsRE.FindStringSubmatch(patchText); len(m) > 1 {
		insertions, _ = strconv.Atoi(m[1])
	}
	if m := shortstatDelRE.FindStringSubmatch(patchText); len(m) > 1 {
		deletions, _ = strconv.Atoi(m[1])
	}
	if files == 0 {
		files = strings.Count(patchText, "diff --git")
	}
	return files, insertions, deletions
}
