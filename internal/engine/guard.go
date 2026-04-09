package engine

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/workflow"
)

type GuardRuntime struct {
	cfg                workflow.Guard
	outputMode         string
	turns              int
	cost               float64
	tokensAccum        int
	stepStart          time.Time
	sawAction          bool
	sawAssistantInTurn bool
}

func NewGuardRuntime(step *workflow.Step) *GuardRuntime {
	cfg := step.Guard
	om := step.OutputMode()
	if cfg.NoWrite == nil {
		if om == "plan" || om == "review" || om == "validate" || om == "artifact" {
			v := true
			cfg.NoWrite = &v
		}
	}
	if cfg.MaxTurns <= 0 && cfg.MaxBudget <= 0 && cfg.MaxTokens <= 0 && cfg.MaxTime == "" && cfg.NoWrite == nil {
		return nil
	}
	return &GuardRuntime{cfg: cfg, outputMode: om, stepStart: time.Now()}
}

func (g *GuardRuntime) AddCost(cost float64) {
	g.cost += cost
}

func (g *GuardRuntime) AddTokens(in, out int) {
	if g == nil {
		return
	}
	g.tokensAccum += in + out
}

// SyncTokensFromResult sets token totals from the provider's final result when stream deltas were incomplete (e.g. stub usage only on result).
func (g *GuardRuntime) SyncTokensFromResult(in, out int) {
	if g == nil {
		return
	}
	total := in + out
	if total > g.tokensAccum {
		g.tokensAccum = total
	}
}

func (g *GuardRuntime) CheckMaxTokens() (guardName, reason string, ok bool) {
	if g == nil || g.cfg.MaxTokens <= 0 {
		return "", "", false
	}
	if g.tokensAccum > g.cfg.MaxTokens {
		return "max_tokens", fmt.Sprintf("token limit exceeded (%d > %d)", g.tokensAccum, g.cfg.MaxTokens), true
	}
	return "", "", false
}

func (g *GuardRuntime) CheckMaxTime() (guardName, reason string, ok bool) {
	if g == nil || strings.TrimSpace(g.cfg.MaxTime) == "" {
		return "", "", false
	}
	d, err := time.ParseDuration(strings.TrimSpace(g.cfg.MaxTime))
	if err != nil || d <= 0 {
		return "", "", false
	}
	if time.Since(g.stepStart) > d {
		return "max_time", "step time limit exceeded", true
	}
	return "", "", false
}

func (g *GuardRuntime) CheckEvent(raw []byte) (guardName, reason string, ok bool) {
	if g == nil {
		return "", "", false
	}
	if gn, gr, ok := g.CheckMaxTime(); ok {
		return gn, gr, true
	}
	if g.cfg.MaxTurns > 0 && isAssistantTextEvent(raw) {
		// WHY: a turn starts when assistant speaks after an action, not for each text chunk.
		if g.sawAction && !g.sawAssistantInTurn {
			g.turns++
			g.sawAssistantInTurn = true
			if g.turns > g.cfg.MaxTurns {
				return "max_turns", "assistant turn limit exceeded", true
			}
		}
	}
	if isActionEvent(raw) {
		g.sawAction = true
		g.sawAssistantInTurn = false
	}
	if g.cfg.NoWrite != nil && *g.cfg.NoWrite {
		if p := writePathFromEvent(raw); p != "" && !g.isAllowedWritePath(p) {
			return "no_write", "file write outside .gump/out", true
		}
	}
	return "", "", false
}

func (g *GuardRuntime) CheckBudget() (guardName, reason string, ok bool) {
	if g == nil || g.cfg.MaxBudget <= 0 {
		return "", "", false
	}
	if g.cost > g.cfg.MaxBudget {
		return "max_budget", "step budget exceeded", true
	}
	return "", "", false
}

func isAssistantTextEvent(raw []byte) bool {
	var v struct {
		Type    string `json:"type"`
		Message *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
		Content string `json:"content"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	if v.Type == "assistant" && v.Message != nil {
		for _, c := range v.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				return true
			}
		}
	}
	return false
}

func writePathFromEvent(raw []byte) string {
	var v struct {
		Type string `json:"type"`
		Name string `json:"name"`
		Item *struct {
			Type     string `json:"type"`
			FilePath string `json:"file_path"`
			Command  string `json:"command"`
			ToolName string `json:"tool_name"`
		} `json:"item"`
		Input map[string]interface{} `json:"input"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	if v.Type == "action" && strings.EqualFold(v.Name, "write") {
		if p, ok := v.Input["path"].(string); ok {
			return p
		}
	}
	if v.Item != nil {
		if p := strings.TrimSpace(v.Item.FilePath); p != "" {
			return p
		}
	}
	return ""
}

func isAllowedOutPath(p string) bool {
	norm := filepath.ToSlash(strings.TrimSpace(p))
	norm = strings.TrimPrefix(norm, "./")
	allowed := filepath.ToSlash(brand.StateDir() + "/out/")
	return strings.HasPrefix(norm, allowed)
}

func (g *GuardRuntime) isAllowedWritePath(p string) bool {
	norm := filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(p), "./"))
	if g.outputMode == "plan" {
		return norm == filepath.ToSlash(brand.StateDir()+"/out/plan.json")
	}
	if g.outputMode == "review" {
		return norm == filepath.ToSlash(brand.StateDir()+"/out/review.json") ||
			norm == filepath.ToSlash(brand.StateDir()+"/out/validate.json")
	}
	if g.outputMode == "artifact" {
		return norm == filepath.ToSlash(brand.StateDir()+"/out/artifact.txt")
	}
	return isAllowedOutPath(norm)
}

func isActionEvent(raw []byte) bool {
	var v struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	return strings.EqualFold(v.Type, "action")
}
