package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/workflow"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var cookbookCmd = &cobra.Command{
	Use:   "playbook",
	Short: "List or show workflows",
}

var cookbookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available recipes (project, user, built-in)",
	RunE:  runCookbookList,
}

var cookbookShowCmd = &cobra.Command{
	Use:   "show [name]",
	Short: "Show recipe YAML by name",
	Args:  cobra.ExactArgs(1),
	RunE:  runCookbookShow,
}

func init() {
	cookbookCmd.AddCommand(cookbookListCmd, cookbookShowCmd)
	rootCmd.AddCommand(cookbookCmd)
}

type recipeMeta struct {
	name        string
	description string
	source      string
}

func runCookbookList(cmd *cobra.Command, args []string) error {
	projectRoot := config.ProjectRoot()
	byName := make(map[string]recipeMeta)
	// Built-in first so project/user can override.
	for name, raw := range workflow.BuiltinWorkflows {
		base := strings.TrimSuffix(name, ".yaml")
		var r struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		_ = yaml.Unmarshal(raw, &r)
		byName[base] = recipeMeta{name: base, description: r.Description, source: "built-in"}
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		userDir := filepath.Join(home, brand.StateDir(), "workflows")
		entries, _ := os.ReadDir(userDir)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			base := strings.TrimSuffix(e.Name(), ".yaml")
			path := filepath.Join(userDir, e.Name())
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var r struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
			}
			_ = yaml.Unmarshal(b, &r)
			byName[base] = recipeMeta{name: base, description: r.Description, source: "user"}
		}
	}
	if projectRoot != "" {
		projDir := filepath.Join(projectRoot, brand.StateDir(), "workflows")
		entries, _ := os.ReadDir(projDir)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			base := strings.TrimSuffix(e.Name(), ".yaml")
			path := filepath.Join(projDir, e.Name())
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var r struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
			}
			_ = yaml.Unmarshal(b, &r)
			byName[base] = recipeMeta{name: base, description: r.Description, source: "project"}
		}
	}
	var names []string
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Println("Gump Playbook")
	fmt.Println()
	for _, n := range names {
		m := byName[n]
		fmt.Printf("  %-12s %-50s (%s)\n", m.name, m.description, m.source)
	}
	return nil
}

func runCookbookShow(cmd *cobra.Command, args []string) error {
	name := args[0]
	projectRoot := config.ProjectRoot()
	resolved, err := workflow.Resolve(name, projectRoot)
	if err != nil {
		return err
	}
	fmt.Printf("# Source: %s\n", resolved.Source)
	fmt.Println("---")
	fmt.Print(string(resolved.Raw))
	return nil
}
