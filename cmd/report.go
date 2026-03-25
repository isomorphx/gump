package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/ledger"
	"github.com/isomorphx/gump/internal/report"
	"github.com/spf13/cobra"
)

var (
	reportLastN  int
	reportDetail string
)

var reportCmd = &cobra.Command{
	Use:   "report [run-id]",
	Short: "Show metrics for a run or aggregate over recent runs",
	Long:  "With no args or --last 1: show the latest run. With run-id: show that run. With --last N: aggregate metrics over the last N runs.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runReport,
}

func init() {
	reportCmd.Flags().IntVar(&reportLastN, "last", 0, "Aggregate over the last N runs (default 1 if no run-id)")
	reportCmd.Flags().StringVar(&reportDetail, "detail", "", "Show detailed artifacts for a step (e.g. --detail impl)")
	rootCmd.AddCommand(reportCmd)
}

func runReport(cmd *cobra.Command, args []string) error {
	_, _, err := config.Load()
	if err != nil {
		return err
	}
	projectRoot := config.ProjectRoot()
	runsDir := filepath.Join(projectRoot, brand.StateDir(), brand.RunsDir())
	if st, err := os.Stat(filepath.Dir(runsDir)); err != nil || !st.IsDir() {
		return fmt.Errorf("no %s/%s directory — run a run first", brand.StateDir(), brand.RunsDir())
	}

	var cookIDs []string
	if len(args) == 1 {
		cookIDs = []string{args[0]}
	} else {
		entries, err := ledger.ReadIndex(projectRoot)
		if err != nil {
			return err
		}
		n := reportLastN
		if n <= 0 {
			n = 1
		}
		from := len(entries) - n
		if from < 0 {
			from = 0
		}
		for i := len(entries) - 1; i >= from && len(cookIDs) < n; i-- {
			cookIDs = append([]string{entries[i].CookID}, cookIDs...)
		}
		if len(cookIDs) == 0 {
			return fmt.Errorf("no runs found — execute gump run first")
		}
	}

	if reportDetail != "" {
		if len(cookIDs) != 1 {
			return fmt.Errorf("--detail expects a single run")
		}
		cookDir := filepath.Join(runsDir, cookIDs[0])
		detail, err := report.BuildStepDetail(cookDir, reportDetail)
		if err != nil {
			return err
		}
		fmt.Print(report.RenderStepDetail(detail))
		return nil
	}

	// Single-run TUI: one id and not a multi-run aggregate request (--last 2+).
	if len(cookIDs) == 1 && (reportLastN <= 1 || len(args) == 1) {
		return reportSingle(filepath.Join(runsDir, cookIDs[0]))
	}
	ar, err := report.BuildAggregateReport(projectRoot, cookIDs)
	if err != nil {
		return err
	}
	opts := report.TerminalRenderOpts()
	fmt.Print(report.RenderAggregateReport(ar, opts))
	return nil
}

func reportSingle(cookDir string) error {
	if st, err := os.Stat(filepath.Join(cookDir, "manifest.ndjson")); err != nil || st.IsDir() {
		return fmt.Errorf("run not found or no manifest")
	}
	cr, err := report.BuildCookReport(cookDir)
	if err != nil {
		return err
	}
	opts := report.TerminalRenderOpts()
	fmt.Print(report.RenderCookReport(cr, opts))
	return nil
}
