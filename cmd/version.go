package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/isomorphx/pudding/internal/version"
)

// versionCmd prints the binary version for support and scripting.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print pudding version",
	RunE: func(cmd *cobra.Command, args []string) error {
		if version.Version == "dev" {
			fmt.Printf("pudding dev (%s, %s)\n", version.Commit, version.BuildDate)
			return nil
		}

		fmt.Printf("pudding %s (%s, %s)\n", version.Version, version.Commit, version.BuildDate)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
