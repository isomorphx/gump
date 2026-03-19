package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const version = "v0.1.0"

// versionCmd prints the binary version for support and scripting.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print pudding version",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("pudding", version)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
