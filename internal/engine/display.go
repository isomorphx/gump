package engine

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/isomorphx/gump/internal/agent"
)

// Verbose enables full streaming output (no truncation). Set by CLI --verbose.
var Verbose bool

const (
	streamTruncMessage = 80
	streamTruncShell   = 60
	boxWidth           = 60
)

var streamTurnCounter int
var activeTurnTracker *TurnTracker
var liveTurnNumber int
var liveTurnWidth int
var carriageReturnEnabled *bool

// RunHeader prints the run box header (Feature 12).
func RunHeader(workflowName, runID, spec string) {
	title := fmt.Sprintf("%s (run %s)", workflowName, runID)
	if len(title) > 45 {
		title = workflowName + " (run " + runID[:8] + "…)"
	}
	nDash := boxWidth - 4 - len(title) // "╭─ " + title + " " + "╮"
	if nDash < 0 {
		nDash = 0
	}
	fmt.Fprintf(os.Stderr, "╭─ %s %s╮\n", title, strings.Repeat("─", nDash))
	contentW := boxWidth - 2 - 7 // "│ Spec: " = 7
	if len(spec) > contentW {
		spec = spec[:contentW-3] + "..."
	}
	fmt.Fprintf(os.Stderr, "│ Spec: %-*s│\n", contentW, spec)
	fmt.Fprintf(os.Stderr, "╰%s╯\n", strings.Repeat("─", boxWidth-2))
}

// StepHeader prints [N/total] step-name (agent) [task 1/4] [retry 2/3] [session: X] (Feature 12).
func StepHeader(stepIndex, stepTotal int, stepPath, agent string, taskInfo, retryInfo, sessionInfo string) {
	resetTurnCounter()
	activeTurnTracker = NewTurnTracker(agent)
	liveTurnNumber = 0
	liveTurnWidth = 0
	idxStr := fmt.Sprintf("%d", stepIndex)
	if stepTotal > 0 {
		idxStr = fmt.Sprintf("%d/%d", stepIndex, stepTotal)
	}
	parts := []string{fmt.Sprintf("[%s] %s (%s)", idxStr, stepPath, agent)}
	if taskInfo != "" {
		parts = append(parts, taskInfo)
	}
	if retryInfo != "" {
		parts = append(parts, retryInfo)
	}
	if sessionInfo != "" {
		parts = append(parts, sessionInfo)
	}
	fmt.Fprintf(os.Stderr, "%s ...\n", strings.Join(parts, " "))
}

// AgentResultText prints RunResult.Result indented 8 spaces; no-op if result is empty.
func AgentResultText(result string) {
	result = strings.TrimSpace(result)
	if result == "" {
		return
	}
	for _, line := range strings.Split(result, "\n") {
		fmt.Fprintf(os.Stderr, "        %s\n", line)
	}
}

// AgentSummaryLine prints "        ✓ done 106s | $0.30 | 12k/200k ctx (6%) | 6 turns" (or "1 turn" when numTurns == 1).
func AgentSummaryLine(stepName string, durationSec, costUSD float64, ctxStr string, numTurns int) {
	flushTurnDisplay()
	durStr := formatDurationSec(durationSec)
	costStr := formatCost(costUSD)
	turnStr := "turns"
	if numTurns == 1 {
		turnStr = "turn"
	}
	fmt.Fprintf(os.Stderr, "        ✓ done %s | %s%s | %d %s\n", durStr, costStr, ctxStr, numTurns, turnStr)
}

func formatDurationSec(sec float64) string {
	if sec < 60 {
		return fmt.Sprintf("%.0fs", sec)
	}
	m := int(sec / 60)
	s := int(sec) % 60
	return fmt.Sprintf("%dm%02ds", m, s)
}

func formatCost(usd float64) string {
	if usd < 0.01 && usd > 0 {
		return fmt.Sprintf("$%.4f", usd)
	}
	if usd == 0 {
		return "$0.00"
	}
	return fmt.Sprintf("$%.2f", usd)
}

// ValidationSummaryLine prints "        ✓ compile | ✓ test | ⊘ lint (skipped)".
func ValidationSummaryLine(passed, skipped []string) {
	var parts []string
	for _, p := range passed {
		parts = append(parts, "✓ "+p)
	}
	for _, s := range skipped {
		parts = append(parts, "⊘ "+s+" (skipped)")
	}
	fmt.Fprintf(os.Stderr, "        %s\n", strings.Join(parts, " | "))
}

// ContextLine prints "        4 tasks planned" or "        2 files changed" per output mode.
func ContextLine(outputMode string, taskCount int, filesChanged int) {
	switch outputMode {
	case "plan":
		if taskCount > 0 {
			fmt.Fprintf(os.Stderr, "        %d tasks planned\n", taskCount)
		}
	case "diff":
		if filesChanged >= 0 {
			fmt.Fprintf(os.Stderr, "        %d files changed\n", filesChanged)
		}
	}
}

// RetryValidationFailed prints "        ✗ validation failed: test" and optionally "        → retry 2/3 (same)" when retryInfo is non-empty.
func RetryValidationFailed(validationErr, retryInfo string) {
	fmt.Fprintf(os.Stderr, "        ✗ %s\n", validationErr)
	if retryInfo != "" {
		fmt.Fprintf(os.Stderr, "        → %s\n", retryInfo)
	}
}

// RetryTriggerLine prints "        → retry 2/3 (same)" when triggering a retry.
func RetryTriggerLine(retryInfo string) {
	if retryInfo != "" {
		fmt.Fprintf(os.Stderr, "        → %s\n", retryInfo)
	}
}

// RunFooter prints the footer block (pass or fatal). stepTotal 0 means show only N steps.
func RunFooter(pass bool, totalCost float64, steps, stepTotal, retries int, duration time.Duration, fatalStep, errMsg, worktreePreserved string) {
	sep := strings.Repeat("─", 60)
	fmt.Fprintf(os.Stderr, "%s\n", sep)
	stepsStr := fmt.Sprintf("%d", steps)
	if stepTotal > 0 {
		stepsStr = fmt.Sprintf("%d/%d", steps, stepTotal)
	}
	if pass {
		fmt.Fprintf(os.Stderr, "Run total: %s | %s steps | %d retries | %s\n", formatCost(totalCost), stepsStr, retries, formatDuration(duration))
		fmt.Fprintf(os.Stderr, "Result: PASS\n")
		fmt.Fprintf(os.Stderr, "\nNext steps:\n  gump report\n  gump apply\n")
	} else {
		fmt.Fprintf(os.Stderr, "Run FATAL at step: %s\n", fatalStep)
		fmt.Fprintf(os.Stderr, "Error: %s\n", errMsg)
		fmt.Fprintf(os.Stderr, "Total: %s | %s steps | %d retries | %s\n", formatCost(totalCost), stepsStr, retries, formatDuration(duration))
		if worktreePreserved != "" {
			fmt.Fprintf(os.Stderr, "Worktree preserved: %s\n", worktreePreserved)
		}
		fmt.Fprintf(os.Stderr, "\nNext steps:\n  gump report\n  gump run --replay --from-step %s\n  gump gc --keep-last 1\n", fallbackStepName(fatalStep))
	}
	fmt.Fprintf(os.Stderr, "%s\n", sep)
}

func fallbackStepName(step string) string {
	if strings.TrimSpace(step) == "" {
		return "impl"
	}
	return step
}

func formatDuration(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm%02ds", s/60, s%60)
}

// RunTotalLine prints a brand-aware running total after each step.
func RunTotalLine(totalCost float64, steps, stepTotal int) {
	stepsStr := fmt.Sprintf("%d", steps)
	if stepTotal > 0 {
		stepsStr = fmt.Sprintf("%d/%d", steps, stepTotal)
	}
	fmt.Fprintf(os.Stderr, "[gump] run total: %s | %s steps\n", formatCost(totalCost), stepsStr)
}

// TruncateStreamMessage returns first phrase truncated to n chars when !Verbose.
func TruncateStreamMessage(s string, n int) string {
	if Verbose {
		return s
	}
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// TruncateStreamShell returns command truncated to n chars when !Verbose.
func TruncateStreamShell(s string, n int) string {
	if Verbose {
		return s
	}
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func handleTurnEvent(ev agent.StreamEvent, agentName string) {
	if activeTurnTracker == nil {
		activeTurnTracker = NewTurnTracker(agentName)
	}
	completed, current := activeTurnTracker.Consume(ev)
	if terminalSupportsCarriageReturn() && current != nil && !current.Interrupted {
		renderLiveTurn(*current)
	}
	if completed != nil {
		printTurn(*completed)
	}
}

func flushTurnDisplay() {
	if activeTurnTracker == nil {
		return
	}
	if t := activeTurnTracker.Flush(); t != nil {
		printTurn(*t)
	}
}

func interruptActiveTurn() {
	if activeTurnTracker == nil {
		return
	}
	if t := activeTurnTracker.Interrupt(); t != nil {
		printTurn(*t)
	}
}

func printTurn(t Turn) {
	if terminalSupportsCarriageReturn() && liveTurnNumber == t.Number {
		line := "     " + formatTurnLine(t)
		if len(line) < liveTurnWidth {
			line += strings.Repeat(" ", liveTurnWidth-len(line))
		}
		fmt.Fprintf(os.Stderr, "\r%s\n", line)
		liveTurnNumber = 0
		liveTurnWidth = 0
	} else if !Verbose {
		fmt.Fprintf(os.Stderr, "     %s\n", formatTurnLine(t))
	} else {
		fmt.Fprintf(os.Stderr, "     %s\n", formatTurnLine(t))
	}
	if !Verbose {
		return
	}
	actions := t.Actions
	if len(actions) > 20 {
		for _, a := range actions[:10] {
			fmt.Fprintf(os.Stderr, "         %s\n", formatActionLine(a))
		}
		fmt.Fprintf(os.Stderr, "         ... +%d more\n", len(actions)-10)
		return
	}
	for _, a := range actions {
		fmt.Fprintf(os.Stderr, "         %s\n", formatActionLine(a))
	}
}

func renderLiveTurn(t Turn) {
	if !terminalSupportsCarriageReturn() || t.IsComplete {
		return
	}
	line := "     " + formatTurnLine(t)
	if len(line) < liveTurnWidth {
		line += strings.Repeat(" ", liveTurnWidth-len(line))
	} else {
		liveTurnWidth = len(line)
	}
	liveTurnNumber = t.Number
	fmt.Fprintf(os.Stderr, "\r%s", line)
}

func terminalSupportsCarriageReturn() bool {
	if carriageReturnEnabled != nil {
		return *carriageReturnEnabled
	}
	ok := true
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		ok = false
	}
	if fi, err := os.Stderr.Stat(); err == nil {
		if fi.Mode()&os.ModeCharDevice == 0 {
			ok = false
		}
	}
	carriageReturnEnabled = &ok
	return ok
}

func resetTurnCounter() { streamTurnCounter = 0 }

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func numFromAny(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}
