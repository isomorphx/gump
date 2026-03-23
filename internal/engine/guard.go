package engine

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/recipe"
)

type GuardRuntime struct {
	cfg                recipe.Guard
	outputMode         string
	turns              int
	cost               float64
	sawAction          bool
	sawAssistantInTurn bool
}

func NewGuardRuntime(step *recipe.Step) *GuardRuntime {
	cfg := step.Guard
	if cfg.NoWrite == nil {
		if step.Output == "plan" || step.Output == "review" || step.Output == "artifact" {
			v := true
			cfg.NoWrite = &v
		}
	}
	if cfg.MaxTurns <= 0 && cfg.MaxBudget <= 0 && cfg.NoWrite == nil {
		return nil
	}
	return &GuardRuntime{cfg: cfg, outputMode: step.Output}
}

func (g *GuardRuntime) AddCost(cost float64) {
	g.cost += cost
}

func (g *GuardRuntime) CheckEvent(raw []byte) (guardName, reason string, ok bool) {
	if g == nil {
		return "", "", false
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
		return norm == filepath.ToSlash(brand.StateDir()+"/out/review.json")
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
