package cmd

import (
	"fmt"
	"strings"

	"github.com/isomorphx/pudding/internal/agent"
	"github.com/spf13/cobra"
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List known model aliases and context window sizes",
	Long:  "Prints the table of Pudding model aliases with provider, model ID, context window, and max output. Used to choose agents in recipes.",
	RunE:  runModels,
}

func init() {
	rootCmd.AddCommand(modelsCmd)
}

// displayOrder is the order of aliases for "pudding models" output (by provider: claude, codex, gemini, qwen, opencode).
var modelsDisplayOrder = []string{
	"claude", "claude-opus", "claude-opus[1m]", "claude-sonnet", "claude-sonnet[1m]", "claude-haiku",
	"codex", "codex-gpt53", "codex-gpt52", "codex-gpt51", "codex-o3", "codex-spark",
	"gemini", "gemini-flash", "gemini-pro", "gemini-flash-lite",
	"qwen", "qwen-plus", "qwen-local",
	"opencode",
}

func runModels(cmd *cobra.Command, args []string) error {
	type row struct {
		provider string
		alias    string
		modelID  string
		ctx      string
		maxOut   string
	}
	var rows []row
	for _, a := range modelsDisplayOrder {
		info := agent.LookupModel(a)
		if info == nil {
			continue
		}
		ctx := "?"
		if info.ContextWindow > 0 {
			if info.ContextWindow >= 1_000_000 {
				ctx = "1M"
			} else {
				ctx = fmt.Sprintf("%dk", info.ContextWindow/1000)
			}
		}
		maxOut := "?"
		if info.MaxOutput > 0 {
			if info.MaxOutput >= 1000 {
				maxOut = fmt.Sprintf("%dk", info.MaxOutput/1000)
			} else {
				maxOut = fmt.Sprintf("%d", info.MaxOutput)
			}
		}
		rows = append(rows, row{info.Provider, a, info.ModelID, ctx, maxOut})
	}
	fmt.Println("Provider   Alias              Model ID                         Context    Max Output")
	fmt.Println(strings.Repeat("-", 85))
	for _, r := range rows {
		fmt.Printf("%-10s %-18s %-32s %-10s %s\n", r.provider, r.alias, truncate(r.modelID, 32), r.ctx, r.maxOut)
	}
	fmt.Println()
	fmt.Println("⚠ Context window sizes are estimates. Actual limits may vary by account tier, CLI version, or provider configuration.")
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
