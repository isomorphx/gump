package engine

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func normalizeToolType(tool string) string {
	t := strings.ToLower(strings.TrimSpace(tool))
	switch t {
	case "read", "read_file":
		return "read"
	case "write", "write_file":
		return "write"
	case "edit", "edit_file":
		return "edit"
	case "apply_patch":
		return "patch"
	case "bash", "shell", "command_execution", "run_shell_command":
		return "bash"
	case "grep", "grep_search", "search":
		return "search"
	case "glob", "list_directory":
		return "glob"
	default:
		if t == "" {
			return "tool"
		}
		return t
	}
}

func compactTokens(total int) string {
	if total <= 0 {
		return ""
	}
	if total < 1000 {
		return fmt.Sprintf("%d tok", total)
	}
	if total%1000 == 0 {
		return fmt.Sprintf("%dk tok", total/1000)
	}
	s := fmt.Sprintf("%.1f", float64(total)/1000.0)
	s = strings.TrimSuffix(s, ".0")
	return fmt.Sprintf("%sk tok", s)
}

func compactDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	sec := int(d.Seconds())
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	return fmt.Sprintf("%dm%02ds", sec/60, sec%60)
}

func spinnerForTurn(t Turn) string {
	if !t.IsComplete {
		if t.Interrupted {
			return "✗"
		}
		return "⠿"
	}
	return "✓"
}

func formatTurnLine(t Turn) string {
	parts := []string{fmt.Sprintf("T%d", t.Number), spinnerForTurn(t), t.Label, summarizeActions(t.Actions)}
	if tok := compactTokens(t.TokensIn + t.TokensOut); tok != "" {
		parts = append(parts, tok)
	}
	parts = append(parts, compactDuration(t.Duration))
	return strings.Join(parts, "  ")
}

func summarizeActions(actions []Action) string {
	if len(actions) == 0 {
		return "0 actions"
	}
	counts := map[string]int{}
	order := []string{"read", "write", "edit", "patch", "bash", "search", "glob"}
	for _, a := range actions {
		counts[normalizeToolType(a.Type)]++
	}
	out := make([]string, 0, len(counts))
	for _, k := range order {
		if counts[k] > 0 {
			out = append(out, fmt.Sprintf("%d %s", counts[k], k))
		}
	}
	for k, v := range counts {
		if k == "read" || k == "write" || k == "edit" || k == "patch" || k == "bash" || k == "search" || k == "glob" {
			continue
		}
		out = append(out, fmt.Sprintf("%d %s", v, k))
	}
	return strings.Join(out, ", ")
}

func formatActionLine(a Action) string {
	typ := strings.Title(normalizeToolType(a.Type))
	target := strings.TrimSpace(a.Target)
	if target == "" {
		return typ
	}
	target = filepath.ToSlash(target)
	if normalizeToolType(a.Type) == "bash" {
		target = truncateSuffix(target, 80)
	} else {
		target = truncateSuffix(target, 60)
	}
	return fmt.Sprintf("%s %s", typ, target)
}

func truncateSuffix(s string, max int) string {
	if max <= 3 || len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
