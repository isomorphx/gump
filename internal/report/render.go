package report

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// RenderOpts controls TUI symbols and ANSI coloring.
type RenderOpts struct {
	Dumb    bool
	NoColor bool
}

// TerminalRenderOpts derives report styling from the environment (spec §7).
func TerminalRenderOpts() RenderOpts {
	term := os.Getenv("TERM")
	dumb := term == "" || term == "dumb" || os.Getenv("NO_COLOR") != ""
	return RenderOpts{Dumb: dumb, NoColor: dumb}
}

const boxWidth = 57

func (o RenderOpts) hBar() string {
	if o.Dumb {
		return strings.Repeat("-", boxWidth)
	}
	return strings.Repeat("─", boxWidth)
}

func (o RenderOpts) topLeft() string {
	if o.Dumb {
		return "+"
	}
	return "╭"
}
func (o RenderOpts) topRight() string {
	if o.Dumb {
		return "+"
	}
	return "╮"
}
func (o RenderOpts) botLeft() string {
	if o.Dumb {
		return "+"
	}
	return "╰"
}
func (o RenderOpts) botRight() string {
	if o.Dumb {
		return "+"
	}
	return "╯"
}
func (o RenderOpts) vBar() string {
	if o.Dumb {
		return "|"
	}
	return "│"
}

func (o RenderOpts) barFull() string {
	if o.Dumb {
		return "#"
	}
	return "█"
}

func (o RenderOpts) barEmpty() string {
	if o.Dumb {
		return "."
	}
	return "░"
}

func (o RenderOpts) colorPass() string {
	if o.NoColor {
		return ""
	}
	return "\033[32m"
}
func (o RenderOpts) colorFatal() string {
	if o.NoColor {
		return ""
	}
	return "\033[31m"
}
func (o RenderOpts) colorAbort() string {
	if o.NoColor {
		return ""
	}
	return "\033[33m"
}
func (o RenderOpts) colorReset() string {
	if o.NoColor {
		return ""
	}
	return "\033[0m"
}

// RenderRunReport builds the single-run TUI (spec §7).
func RenderRunReport(cr *RunReport, o RenderOpts) string {
	var b strings.Builder

	h := o.hBar()
	fmt.Fprintf(&b, "%s%s%s\n", o.topLeft(), h, o.topRight())
	fmt.Fprintf(&b, "%s  Gump Report%*s%s\n", o.vBar(), boxWidth-16, "", o.vBar())
	cid := cr.RunID
	if len(cid) > 8 {
		cid = cid[:8]
	}
	fmt.Fprintf(&b, "%s  Run:    %-*s%s\n", o.vBar(), boxWidth-10, cid, o.vBar())
	fmt.Fprintf(&b, "%s  Workflow: %-*s%s\n", o.vBar(), boxWidth-12, cr.Workflow, o.vBar())
	st := strings.ToUpper(cr.Status)
	stCol := ""
	switch strings.ToLower(cr.Status) {
	case "pass":
		stCol = o.colorPass()
	case "fatal":
		stCol = o.colorFatal()
	case "aborted":
		stCol = o.colorAbort()
	}
	fmt.Fprintf(&b, "%s  Status: %s%-*s%s%s\n", o.vBar(), stCol, boxWidth-10-len(st), st, o.colorReset(), o.vBar())
	if cr.MaxBudgetUSD > 0 {
		fmt.Fprintf(&b, "%s  Budget: $%.2f / $%.2f%s\n", o.vBar(), cr.TotalCostUSD, cr.MaxBudgetUSD, o.vBar())
	}
	fmt.Fprintf(&b, "%s%s%s\n", o.botLeft(), h, o.botRight())

	fmt.Fprintf(&b, "\nDuration     %s\n", formatDuration(cr.DurationMs))
	fmt.Fprintf(&b, "Cost         $%.2f\n", cr.TotalCostUSD)
	fmt.Fprintf(&b, "Tokens       %s in / %s out\n", formatIntThousands(cr.TotalTokensIn), formatIntThousands(cr.TotalTokensOut))
	fmt.Fprintf(&b, "Files        %d changed (+%d / -%d)\n", cr.FilesChanged, cr.LinesAdded, cr.LinesRemoved)
	fmt.Fprintf(&b, "Retries      %d\n", cr.Retries)
	fmt.Fprintf(&b, "Guard triggers: %d\n", cr.GuardTriggers)
	fmt.Fprintf(&b, "Agents       %s\n", strings.Join(cr.Agents, ", "))

	fmt.Fprintf(&b, "\nSteps\n")
	fmt.Fprintf(&b, "%s\n", o.hBar())
	fmt.Fprintf(&b, "  #  Step                 Agent          Status   Cost     Turns  TTFD\n")
	for i, s := range cr.Steps {
		st := strings.ToUpper(s.Status)
		if st == "" {
			st = "—"
		}
		ag := s.Agent
		if ag == "" {
			ag = "(gate)"
		}
		ttfd := "-"
		if s.TTFD >= 0 {
			ttfd = fmt.Sprintf("%d", s.TTFD)
		}
		fmt.Fprintf(&b, "  %-2d %-20s %-14s %-8s $%-7.2f %-6d %s\n",
			i+1, trimW(s.Name, 20), trimW(ag, 14), trimW(st, 8), s.CostUSD, len(s.Turns), ttfd)
	}

	fmt.Fprintf(&b, "\nTurn Distribution\n")
	fmt.Fprintf(&b, "%s\n", o.hBar())
	renderTurnDist(&b, cr.TurnDist, o)

	if !isZeroStall(cr.StallTotal) {
		fmt.Fprintf(&b, "\nStall Detection\n")
		fmt.Fprintf(&b, "%s\n", o.hBar())
		fmt.Fprintf(&b, "  Tool errors: %d\n", cr.StallTotal.ToolErrorCount)
		fmt.Fprintf(&b, "  Correction loops: %d\n", cr.StallTotal.CorrectionLoops)
		fmt.Fprintf(&b, "  Fatal loops: %d\n", cr.StallTotal.FatalLoops)
		fmt.Fprintf(&b, "  Repeated actions: %d\n", cr.StallTotal.RepeatedActionLoops)
	}

	if len(cr.ContextUsage) > 0 {
		fmt.Fprintf(&b, "\nContext Usage\n")
		fmt.Fprintf(&b, "%s\n", o.hBar())
		for _, s := range cr.Steps {
			if s.TokensIn <= 0 {
				continue
			}
			pct := s.ContextPct
			label := s.ContextLabel
			if label == "" {
				label = "N/A"
			}
			bar := contextBar(pct/100, 20, o)
			fmt.Fprintf(&b, "  %-18s %5s %s\n", trimW(s.Name, 18), label, bar)
		}
	}
	if len(cr.Guards) > 0 {
		fmt.Fprintf(&b, "\nGuards\n")
		fmt.Fprintf(&b, "%s\n", o.hBar())
		for _, g := range cr.Guards {
			fmt.Fprintf(&b, "  %-20s %s (attempt %d)\n", trimW(g.Step, 20), g.Guard, g.Attempt)
		}
	}

	return b.String()
}

func isZeroStall(s StallMetrics) bool {
	return s.ToolErrorCount == 0 && s.CorrectionLoops == 0 && s.FatalLoops == 0 && s.RepeatedActionLoops == 0
}

func trimW(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

func renderTurnDist(b *strings.Builder, dist map[string]int, o RenderOpts) {
	order := []string{"coding", "execution", "exploration", "reasoning", "communication", "planning", "writing", "reviewing"}
	maxC := 0
	for _, k := range order {
		if dist[k] > maxC {
			maxC = dist[k]
		}
	}
	if maxC == 0 {
		return
	}
	for _, k := range order {
		c := dist[k]
		if c <= 0 {
			continue
		}
		w := int(math.Ceil(float64(c) * 20 / float64(maxC)))
		if w > 20 {
			w = 20
		}
		bar := strings.Repeat(o.barFull(), w)
		fmt.Fprintf(b, "  %-15s %4d  %s\n", k, c, bar)
	}
}

func contextBar(fraction float64, width int, o RenderOpts) string {
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction * float64(width))
	if filled > width {
		filled = width
	}
	return strings.Repeat(o.barFull(), filled) + strings.Repeat(o.barEmpty(), width-filled)
}

func formatDuration(ms int) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	s := ms / 1000
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	s %= 60
	if s > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%dm", m)
}

func formatIntThousands(n int) string {
	if n < 0 {
		return "-" + formatIntThousands(-n)
	}
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	if s != "" {
		parts = append([]string{s}, parts...)
	}
	return strings.Join(parts, ",")
}
