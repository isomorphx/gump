package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/isomorphx/pudding/internal/config"
	"github.com/isomorphx/pudding/internal/ledger"
	"github.com/isomorphx/pudding/internal/report"
	"github.com/spf13/cobra"
)

var (
	reportLastN int
)

var reportCmd = &cobra.Command{
	Use:   "report [cook-id]",
	Short: "Show metrics for a cook or aggregate over recent cooks",
	Long:  "With no args or --last 1: show the latest cook. With cook-id: show that cook. With --last N: aggregate metrics over the last N cooks.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runReport,
}

func init() {
	reportCmd.Flags().IntVar(&reportLastN, "last", 0, "Aggregate over the last N cooks (default 1 if no cook-id)")
	rootCmd.AddCommand(reportCmd)
}

func runReport(cmd *cobra.Command, args []string) error {
	_, _, err := config.Load()
	if err != nil {
		return err
	}
	projectRoot := config.ProjectRoot()
	puddingDir := filepath.Join(projectRoot, ".pudding", "cooks")
	if st, err := os.Stat(filepath.Dir(puddingDir)); err != nil || !st.IsDir() {
		return fmt.Errorf("no .pudding/cooks directory — run a cook first")
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
			return fmt.Errorf("no cooks found — run pudding cook first")
		}
	}

	// Single-cook TUI: one id and not a multi-cook aggregate request (--last 2+).
	if len(cookIDs) == 1 && (reportLastN <= 1 || len(args) == 1) {
		return reportSingle(filepath.Join(puddingDir, cookIDs[0]))
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
		return fmt.Errorf("cook not found or no manifest")
	}
	cr, err := report.BuildCookReport(cookDir)
	if err != nil {
		return err
	}
	opts := report.TerminalRenderOpts()
	fmt.Print(report.RenderCookReport(cr, opts))
	return nil
}
