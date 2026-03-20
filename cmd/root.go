package cmd

import (
	"github.com/isomorphx/pudding/internal/version"
	"github.com/spf13/cobra"
)

// rootCmd is the pudding CLI. Log level is overridable so users can debug without editing config files.
var rootCmd = &cobra.Command{
	Use:   "pudding",
	Short: "Orchestrate code agents via declarative recipes",
	Long:  "Pudding runs workflows defined in YAML recipes: plan, code steps, validation, and review.",
}

func init() {
	rootCmd.PersistentFlags().String("log-level", "info", "Override config log level (debug, info, warn, error)")
	rootCmd.Version = version.Version

	if version.Version == "dev" {
		rootCmd.SetVersionTemplate("pudding dev (" + version.Commit + ", " + version.BuildDate + ")\n")
	} else {
		rootCmd.SetVersionTemplate("pudding " + version.Version + " (" + version.Commit + ", " + version.BuildDate + ")\n")
	}
}

// Execute runs the root command and all subcommands.
func Execute() error {
	return rootCmd.Execute()
}
