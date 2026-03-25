package cmd

import (
	"fmt"
	"strings"

	"github.com/isomorphx/gump/internal/agent"
	"github.com/spf13/cobra"
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List known model aliases and context window sizes",
	Long:  "Prints the table of Gump model aliases with provider, model ID, context window, and max output. Used to choose agents in workflows.",
	RunE:  runModels,
}

func init() {
	rootCmd.AddCommand(modelsCmd)
}

// displayOrder is the order of aliases for "gump models" output by provider.
var modelsDisplayOrder = []string{
	"claude", "claude-opusplan", "claude-opus", "claude-opus[1m]", "claude-sonnet", "claude-sonnet[1m]", "claude-haiku",
	"codex", "codex-gpt54", "codex-gpt54-mini", "codex-gpt53", "codex-gpt53-spark", "codex-gpt52", "codex-gpt51-max", "codex-o3",
	"gemini", "gemini-flash", "gemini-pro", "gemini-flash-lite", "gemini-25-pro", "gemini-25-flash",
	"qwen", "qwen-plus", "qwen-local",
	"opencode", "opencode-opus", "opencode-sonnet", "opencode-haiku", "opencode-gpt54", "opencode-gpt53", "opencode-gemini",
	"cursor", "cursor-sonnet", "cursor-sonnet-thinking", "cursor-opus", "cursor-opus-thinking", "cursor-gpt5", "cursor-gemini",
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
