package cmd

import (
	"fmt"
	"time"

	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/ledger"
	"github.com/isomorphx/gump/internal/sandbox"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the current run in progress",
	Long:  "Reads the latest in-progress run manifest and prints duration, cost, current step, and completed steps.",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	projectRoot := config.ProjectRoot()
	repoRoot, err := sandbox.GitRepoRoot(projectRoot)
	if err != nil {
		return fmt.Errorf("gump status must be executed inside a git repository: %w", err)
	}
	cookDir := ledger.FindInProgressCook(repoRoot)
	if cookDir == "" {
		fmt.Println("No run in progress.")
		return nil
	}
	snap, err := ledger.ReadStatus(cookDir)
	if err != nil {
		return fmt.Errorf("read status: %w", err)
	}
	dur := snap.LastEventAt.Sub(snap.StartedAt)
	if snap.LastEventAt.IsZero() {
		dur = 0
	}
	fmt.Printf("Run %s (%s) — in progress\n", snap.CookID[:8], snap.Recipe)
	fmt.Printf("Spec: %s\n", snap.Spec)
	fmt.Printf("Duration: %s\n", formatDurationStatus(dur))
	fmt.Printf("Cost: $%.2f\n\n", snap.TotalCostUSD)
	if snap.CurrentStep != "" {
		fmt.Printf("Current step: %s (%s)\n", snap.CurrentStep, snap.CurrentAgent)
		if snap.CurrentTask != "" {
			fmt.Printf("  Task: %s\n", snap.CurrentTask)
		}
		if snap.CurrentAttempt > 0 {
			fmt.Printf("  Attempt: %d\n", snap.CurrentAttempt)
		}
		if !snap.AgentRunningSince.IsZero() {
			fmt.Printf("  Agent running for: %s\n", formatDurationStatus(snap.LastEventAt.Sub(snap.AgentRunningSince)))
		}
		fmt.Println()
	}
	if len(snap.CompletedSteps) > 0 {
		fmt.Println("Completed steps:")
		for _, row := range snap.CompletedSteps {
			agent := row.Agent
			if agent == "" {
				agent = "-"
			}
			fmt.Printf("  ✓ %-20s %6s  $%.2f  %s%s\n", row.Step, formatDurationStatus(row.Duration), row.CostUSD, agent, row.Extra)
		}
	}
	return nil
}

func formatDurationStatus(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	if s < 3600 {
		return fmt.Sprintf("%dm%ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh%dm%ds", s/3600, (s%3600)/60, s%60)
}
