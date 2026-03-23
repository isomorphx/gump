package cmd

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/version"
	"github.com/spf13/cobra"
)

// updateCh is a best-effort async result channel used by the root command hooks.
// It stays nil when the update checker should not run (dev build, disabled config,
// command not eligible, or help/version flows).
var updateCh chan string

var updateCheckWhitelist = map[string]bool{
	"run":       true,
	"apply":     true,
	"doctor":    true,
	"report":    true,
	"playbook":  true,
	"config":    true,
	"gc":        true,
	"status":    true,
	"guard":     true, // not implemented in this repo yet, but kept for spec compliance
	"models":    true,
}

// rootCmd is the gump CLI. Log level is overridable so users can debug without editing config files.
var rootCmd = &cobra.Command{
	Use:   "gump",
	Short: "Orchestrate code agents via declarative workflows",
	Long:  "Gump runs workflows defined in YAML recipes: plan, code steps, validation, and review.",
}

func init() {
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// WHY: dev builds are local/in-progress; surfacing upgrade hints there is noise
		// and breaks determinism for tests.
		if version.Version == "dev" {
			updateCh = nil
			return nil
		}

		// WHY: request help/version should be instant and never trigger side effects.
		if cmd == rootCmd {
			updateCh = nil
			return nil
		}
		if cmd.Flags() != nil && cmd.Flags().Lookup("help") != nil && cmd.Flags().Changed("help") {
			updateCh = nil
			return nil
		}

		cmdName := cmd.Name()
		parentName := ""
		if cmd.Parent() != nil {
			parentName = cmd.Parent().Name()
		}
		eligible := updateCheckWhitelist[cmdName] || updateCheckWhitelist[parentName]
		if !eligible {
			updateCh = nil
			return nil
		}

		cfg, _, err := config.Load()
		if err != nil || !cfg.UpdateCheck {
			updateCh = nil
			return nil
		}

		// WHY: keep CLI responsive; the check runs concurrently and the message is
		// only displayed if the goroutine already produced a result.
		currentVersion := version.Version
		updateCh = make(chan string, 1)
		go func() {
			latest := version.CheckForUpdate(currentVersion)
			updateCh <- latest
			close(updateCh)
		}()
		// WHY: for very fast commands (like "cookbook list") we want the scheduler
		// to run the update goroutine before PostRunE performs a non-blocking
		// receive. This keeps the CLI responsive while making the best-effort
		// message deterministic in practice.
		runtime.Gosched()
		return nil
	}

	rootCmd.PersistentPostRunE = func(cmd *cobra.Command, args []string) error {
		if updateCh == nil {
			return nil
		}

		select {
		case latest := <-updateCh:
			if latest == "" {
				return nil
			}

			// WHY: write the whole block in a single stderr write to avoid interleaving.
			fmt.Fprint(os.Stderr,
				"\nA new version of Gump is available: "+latest+" (current: "+version.Version+")\n\n"+
					"  curl -fsSL https://gump.build/install.sh | bash\n\n"+
					"  or: brew upgrade gump\n\n",
			)
		case <-time.After(1200 * time.Millisecond):
			// WHY: bounded wait so the HTTP fetch can complete and the cache
			// is actually written (tests assert on checked_at). We still avoid
			// unbounded blocking: network timeout in the checker is 1s.
		}
		return nil
	}

	rootCmd.PersistentFlags().String("log-level", "info", "Override config log level (debug, info, warn, error)")
	rootCmd.Version = version.Version

	if version.Version == "dev" {
		rootCmd.SetVersionTemplate("gump dev (" + version.Commit + ", " + version.BuildDate + ")\n")
	} else {
		rootCmd.SetVersionTemplate("gump " + version.Version + " (" + version.Commit + ", " + version.BuildDate + ")\n")
	}
}

// Execute runs the root command and all subcommands.
func Execute() error {
	return rootCmd.Execute()
}
