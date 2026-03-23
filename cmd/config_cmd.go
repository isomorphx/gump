package cmd

import (
	"fmt"

	"github.com/isomorphx/gump/internal/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show merged configuration and source of each value",
	RunE:  runConfig,
}

func init() {
	rootCmd.AddCommand(configCmd)
}

func runConfig(cmd *cobra.Command, args []string) error {
	cfg, src, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Println("Gump Configuration")
	fmt.Println()
	printRow("default_agent", cfg.DefaultAgent, src.DefaultAgent)
	printRow("log_level", cfg.LogLevel, src.LogLevel)
	printRow("compile_cmd", cfg.CompileCmd, src.CompileCmd)
	printRow("test_cmd", cfg.TestCmd, src.TestCmd)
	printRow("lint_cmd", cfg.LintCmd, src.LintCmd)
	printRow("coverage_cmd", cfg.CoverageCmd, src.CoverageCmd)
	return nil
}

func printRow(key, value, source string) {
	if value == "" {
		value = "—"
		source = "not set"
	}
	if source == "" {
		source = "not set"
	}
	fmt.Printf("  %-14s %-20s (%s)\n", key, value, source)
}
