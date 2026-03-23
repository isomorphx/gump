package engine

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Verbose enables full streaming output (no truncation). Set by CLI --verbose.
var Verbose bool

const (
	streamTruncMessage = 80
	streamTruncShell   = 60
	boxWidth           = 60
)

// CookHeader prints the run box header (Feature 12).
func CookHeader(recipeName, cookID, spec string) {
	title := fmt.Sprintf("%s (run %s)", recipeName, cookID)
	if len(title) > 45 {
		title = recipeName + " (run " + cookID[:8] + "…)"
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

// CookFooter prints the footer block (pass or fatal). stepTotal 0 means show only N steps.
func CookFooter(pass bool, totalCost float64, steps, stepTotal, retries int, duration time.Duration, fatalStep, errMsg, worktreePreserved string) {
	sep := strings.Repeat("─", 60)
	fmt.Fprintf(os.Stderr, "%s\n", sep)
	stepsStr := fmt.Sprintf("%d", steps)
	if stepTotal > 0 {
		stepsStr = fmt.Sprintf("%d/%d", steps, stepTotal)
	}
	if pass {
		fmt.Fprintf(os.Stderr, "Run total: %s | %s steps | %d retries | %s\n", formatCost(totalCost), stepsStr, retries, formatDuration(duration))
		fmt.Fprintf(os.Stderr, "Result: PASS\n")
	} else {
		fmt.Fprintf(os.Stderr, "Run FATAL at step: %s\n", fatalStep)
		fmt.Fprintf(os.Stderr, "Error: %s\n", errMsg)
		fmt.Fprintf(os.Stderr, "Total: %s | %s steps | %d retries | %s\n", formatCost(totalCost), stepsStr, retries, formatDuration(duration))
		if worktreePreserved != "" {
			fmt.Fprintf(os.Stderr, "Worktree preserved: %s\n", worktreePreserved)
		}
	}
	fmt.Fprintf(os.Stderr, "%s\n", sep)
}

func formatDuration(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm%02ds", s/60, s%60)
}

// CookTotalLine prints a brand-aware running total after each step.
func CookTotalLine(totalCost float64, steps, stepTotal int) {
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
