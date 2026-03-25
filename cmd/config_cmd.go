package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/isomorphx/gump/internal/brand"
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
	configCmd.AddCommand(configSetCmd)
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a user configuration value",
	Args:  cobra.ExactArgs(2),
	RunE:  runConfigSet,
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
	printRow("verbose", strconv.FormatBool(cfg.Verbose), src.Verbose)
	printRow("analytics", strconv.FormatBool(cfg.Analytics), src.Analytics)
	printRow("compile_cmd", cfg.CompileCmd, src.CompileCmd)
	printRow("test_cmd", cfg.TestCmd, src.TestCmd)
	printRow("lint_cmd", cfg.LintCmd, src.LintCmd)
	printRow("coverage_cmd", cfg.CoverageCmd, src.CoverageCmd)
	return nil
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	key := strings.ToLower(strings.TrimSpace(args[0]))
	val := strings.TrimSpace(args[1])
	switch key {
	case "verbose":
		enabled, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("verbose must be true or false")
		}
		return setUserDisplayVerbose(enabled)
	case "analytics":
		enabled, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("analytics must be true or false")
		}
		return setUserAnalytics(enabled)
	default:
		return fmt.Errorf("unsupported config key %q", key)
	}
}

func setUserAnalytics(enabled bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cfgDir := filepath.Join(home, brand.StateDir())
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		return err
	}
	cfgPath := filepath.Join(cfgDir, "config.toml")

	var doc map[string]interface{}
	if raw, err := os.ReadFile(cfgPath); err == nil && len(raw) > 0 {
		_, _ = toml.Decode(string(raw), &doc)
	}
	if doc == nil {
		doc = map[string]interface{}{}
	}
	analytics, _ := doc["analytics"].(map[string]interface{})
	if analytics == nil {
		analytics = map[string]interface{}{}
	}
	// WHY: writing under [analytics].enabled keeps opt-out explicit and stable across future flags.
	analytics["enabled"] = enabled
	doc["analytics"] = analytics

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, buf.Bytes(), 0644); err != nil {
		return err
	}
	fmt.Printf("Set analytics=%t in %s\n", enabled, cfgPath)
	return nil
}

func setUserDisplayVerbose(enabled bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cfgDir := filepath.Join(home, brand.StateDir())
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		return err
	}
	cfgPath := filepath.Join(cfgDir, "config.toml")

	var doc map[string]interface{}
	if raw, err := os.ReadFile(cfgPath); err == nil && len(raw) > 0 {
		_, _ = toml.Decode(string(raw), &doc)
	}
	if doc == nil {
		doc = map[string]interface{}{}
	}
	display, _ := doc["display"].(map[string]interface{})
	if display == nil {
		display = map[string]interface{}{}
	}
	// WHY: display.verbose is persistent UX preference across runs.
	display["verbose"] = enabled
	doc["display"] = display

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, buf.Bytes(), 0644); err != nil {
		return err
	}
	fmt.Printf("Set verbose=%t in %s\n", enabled, cfgPath)
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
