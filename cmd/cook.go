package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/isomorphx/pudding/internal/agent"
	"github.com/isomorphx/pudding/internal/config"
	"github.com/isomorphx/pudding/internal/cook"
	"github.com/isomorphx/pudding/internal/engine"
	"github.com/isomorphx/pudding/internal/recipe"
	"github.com/isomorphx/pudding/internal/sandbox"
	"github.com/spf13/cobra"
)

var (
	cookRecipe    string
	cookAgent     string
	cookDryRun    bool
	cookAgentStub bool
	cookReplay    bool
	cookFromStep  string
	cookCookID    string
	cookVerbose   bool
)

var cookCmd = &cobra.Command{
	Use:   "cook [spec-file]",
	Short: "Run a recipe against a spec file",
	Long:  "Resolve the recipe, parse and validate it, then run the workflow (or dry-run to only show the plan).",
	Args: func(cmd *cobra.Command, args []string) error {
		if cookReplay {
			return nil
		}
		return cobra.ExactArgs(1)(cmd, args)
	},
	RunE: runCook,
}

func init() {
	cookCmd.Flags().StringVar(&cookRecipe, "recipe", "", "Recipe name (e.g. tdd, freeform). Omitted for --replay (uses last cook's recipe).")
	cookCmd.Flags().StringVar(&cookAgent, "agent", "", "Override agent for all steps (e.g. claude-sonnet, gemini)")
	cookCmd.Flags().BoolVar(&cookDryRun, "dry-run", false, "Only show plan, do not execute")
	cookCmd.Flags().BoolVar(&cookAgentStub, "agent-stub", false, "Use stub agent for testing (writes files, no real agent)")
	cookCmd.Flags().BoolVar(&cookReplay, "replay", false, "Replay from a step of the last fatal cook (use with --from-step)")
	cookCmd.Flags().StringVar(&cookFromStep, "from-step", "", "Step path or short name to start from (required for --replay)")
	cookCmd.Flags().StringVar(&cookCookID, "cook", "", "Cook UUID to replay (default: last fatal cook)")
	cookCmd.Flags().BoolVar(&cookVerbose, "verbose", false, "Full streaming output (no truncation)")
	rootCmd.AddCommand(cookCmd)
}

// runCookReplay runs a replay from the last fatal cook (or --cook <id>), restoring state bag and worktree, then running from --from-step.
func runCookReplay(specPath string, cfg *config.Config, resolved *recipe.ResolvedRecipe, rec *recipe.Recipe) error {
	if rec == nil {
		return fmt.Errorf("replay: recipe is nil")
	}
	if resolved == nil {
		return fmt.Errorf("replay: resolved recipe is nil")
	}
	cwd, _ := os.Getwd()
	repoRoot, err := sandbox.GitRepoRoot(cwd)
	if err != nil {
		return err
	}
	if cookFromStep == "" {
		return fmt.Errorf("--from-step is required when using --replay")
	}
	var resolver agent.AdapterResolver
	if cookAgentStub {
		resolver = &agent.StubResolver{Stub: &agent.StubAdapter{}}
	} else {
		resolver = &agent.Registry{
			Claude:   agent.NewClaudeAdapter(),
			Codex:    agent.NewCodexAdapter(),
			Gemini:   agent.NewGeminiAdapter(),
			Qwen:     agent.NewQwenAdapter(),
			OpenCode: agent.NewOpenCodeAdapter(),
		}
	}
	agentsCLI := agentsCLIFromRecipe(rec, cookAgentStub)
	c, stepsCount, err := engine.RunReplay(repoRoot, specPath, cookFromStep, cookCookID, rec, resolved.Raw, resolver, cfg, agentsCLI)
	if err != nil {
		if c != nil {
			_ = cook.WriteStatus(c.CookDir, "fatal")
			fmt.Fprintf(os.Stderr, "Cook failed at step: %v\n", err)
			fmt.Fprintf(os.Stderr, "Worktree preserved at %s\n", c.WorktreeDir)
		} else {
			fmt.Fprintf(os.Stderr, "Replay failed: %v\n", err)
		}
		return err
	}
	if c == nil {
		return fmt.Errorf("replay: internal error (no cook returned)")
	}
	if err := cook.WriteStatusWithSteps(c.CookDir, "pass", stepsCount); err != nil {
		return err
	}
	fmt.Printf("Replay complete (cook %s, %d steps). Run 'pudding apply' to merge results.\n", c.ID, stepsCount)
	return nil
}

// runCook runs the recipe: dry-run prints the plan only; otherwise creates worktree, runs engine, persists state-bag.
func runCook(cmd *cobra.Command, args []string) error {
	cfg, _, err := config.Load()
	if err != nil {
		return err
	}
	recipe.ParseWarn = func(msg string) { fmt.Fprintln(os.Stderr, "warning:", msg) }
	recipe.ValidateWarn = func(path, message string) {
		if path != "" {
			fmt.Fprintf(os.Stderr, "warning: %s: %s\n", path, message)
		} else {
			fmt.Fprintln(os.Stderr, "warning:", message)
		}
	}

	var specPath string
	var resolved *recipe.ResolvedRecipe
	var rec *recipe.Recipe

	if cookReplay {
		if len(args) >= 1 && cookRecipe != "" {
			// Replay with explicit spec and recipe (e.g. smoke test): use them so we don't depend on snapshot.
			specPath = args[0]
			projectRoot := config.ProjectRoot()
			var err error
			resolved, err = recipe.Resolve(cookRecipe, projectRoot)
			if err != nil {
				return err
			}
			recipeDir := ""
			if resolved.Path != "" {
				recipeDir = filepath.Dir(resolved.Path)
			}
			rec, err = recipe.Parse(resolved.Raw, recipeDir)
			if err != nil {
				return fmt.Errorf("%w\n(recipe loaded from %s)", err, resolved.Source)
			}
			if errs := recipe.Validate(rec); len(errs) > 0 {
				for _, e := range errs {
					fmt.Fprintln(os.Stderr, e.Error())
				}
				os.Exit(1)
			}
			return runCookReplay(specPath, cfg, resolved, rec)
		}
		// Replay without args: infer spec and recipe from the last fatal cook's snapshot.
		cwd, _ := os.Getwd()
		repoRoot, err := sandbox.GitRepoRoot(cwd)
		if err != nil {
			return err
		}
		cookDir, err := engine.FindLastFatalCook(repoRoot, cookCookID)
		if err != nil {
			return err
		}
		ctxPath := filepath.Join(cookDir, "context-snapshot.json")
		ctxData, err := os.ReadFile(ctxPath)
		if err != nil {
			return fmt.Errorf("replay: read context snapshot: %w", err)
		}
		var ctx struct {
			Spec     string `json:"spec"`
			RepoRoot string `json:"repo_root"`
		}
		if err := json.Unmarshal(ctxData, &ctx); err != nil {
			return fmt.Errorf("replay: parse context snapshot: %w", err)
		}
		specPath = filepath.Join(ctx.RepoRoot, ctx.Spec)
		recipePath := filepath.Join(cookDir, "recipe-snapshot.yaml")
		recipeRaw, err := os.ReadFile(recipePath)
		if err != nil {
			return fmt.Errorf("replay: read recipe snapshot: %w", err)
		}
		resolved = &recipe.ResolvedRecipe{Name: "", Source: "replay", Path: recipePath, Raw: recipeRaw}
		rec, err = recipe.Parse(recipeRaw, "")
		if err != nil {
			return fmt.Errorf("replay: parse recipe snapshot: %w", err)
		}
		if errs := recipe.Validate(rec); len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, e.Error())
			}
			os.Exit(1)
		}
		return runCookReplay(specPath, cfg, resolved, rec)
	}

	if cookRecipe == "" {
		return fmt.Errorf("--recipe is required when not using --replay")
	}
	if len(args) < 1 {
		return fmt.Errorf("spec file is required when not using --replay")
	}
	specPath = args[0]
	projectRoot := config.ProjectRoot()
	resolved, err = recipe.Resolve(cookRecipe, projectRoot)
	if err != nil {
		return err
	}
	recipeDir := ""
	if resolved.Path != "" {
		recipeDir = filepath.Dir(resolved.Path)
	}
	rec, err = recipe.Parse(resolved.Raw, recipeDir)
	if err != nil {
		source := resolved.Source
		path := resolved.Path
		if path != "" {
			return fmt.Errorf("%w\n(recipe loaded from %s: %s)", err, source, path)
		}
		return fmt.Errorf("%w\n(recipe loaded from %s)", err, source)
	}
	errs := recipe.Validate(rec)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e.Error())
		}
		os.Exit(1)
	}
	specInfo, err := os.Stat(specPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("spec file %s: no such file or directory", specPath)
		}
		return fmt.Errorf("spec file %s: %w", specPath, err)
	}
	specSize := specInfo.Size()
	configSource := "default"
	if p := config.ProjectConfigPath(); p != "" {
		configSource = "pudding.toml"
	}
	if cookDryRun {
		fmt.Println("Pudding — dry run")
		fmt.Println()
		fmt.Printf("Recipe:    %s (%s)\n", resolved.Name, resolved.Source)
		if rec.Description != "" {
			fmt.Printf("Description: %s\n", rec.Description)
		}
		fmt.Printf("Spec:      %s (%d bytes)\n", specPath, specSize)
		fmt.Printf("Config:    %s\n", configSource)
		fmt.Println()
		fmt.Println("Steps:")
		printSteps(rec.Steps, "  ")
		fmt.Println()
		return nil
	}

	cwd, _ := os.Getwd()
	repoRoot, err := sandbox.GitRepoRoot(cwd)
	if err != nil {
		return err
	}
	specContent, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	c, err := cook.NewCook(rec, specPath, repoRoot, resolved.Raw)
	if err != nil {
		return err
	}
	fmt.Printf("Worktree: %s\n", c.WorktreeDir)

	var resolver agent.AdapterResolver
	if cookAgentStub {
		resolver = &agent.StubResolver{Stub: &agent.StubAdapter{}}
	} else {
		resolver = &agent.Registry{
			Claude:   agent.NewClaudeAdapter(),
			Codex:    agent.NewCodexAdapter(),
			Gemini:   agent.NewGeminiAdapter(),
			Qwen:     agent.NewQwenAdapter(),
			OpenCode: agent.NewOpenCodeAdapter(),
		}
	}

	eng := engine.New(c, rec, resolver, cfg, string(specContent))
	eng.AgentsCLI = agentsCLIFromRecipe(rec, cookAgentStub)
	eng.CookAgentOverride = cookAgent
	engine.Verbose = cookVerbose
	if err := eng.Run(); err != nil {
		_ = cook.WriteStatus(c.CookDir, "fatal")
		return err
	}
	if err := cook.WriteStatusWithSteps(c.CookDir, "pass", len(eng.Steps)); err != nil {
		return err
	}
	fmt.Printf("Cook complete (%d steps). Run 'pudding apply' to merge results.\n", len(eng.Steps))
	return nil
}

// agentsCLIFromRecipe builds a map of agent name -> CLI version for cook_started. Stub mode uses "stub-1.0.0"; otherwise "unknown".
func agentsCLIFromRecipe(rec *recipe.Recipe, useStub bool) map[string]string {
	agents := make(map[string]struct{})
	var walkAgents func(steps []recipe.Step)
	walkAgents = func(steps []recipe.Step) {
		for _, s := range steps {
			if s.Agent != "" {
				agents[s.Agent] = struct{}{}
			}
			if len(s.Steps) > 0 {
				walkAgents(s.Steps)
			}
		}
	}
	walkAgents(rec.Steps)
	out := make(map[string]string, len(agents))
	version := "unknown"
	if useStub {
		version = "stub-1.0.0"
	}
	for a := range agents {
		out[a] = version
	}
	return out
}

// walkStepNames calls fn for each step name (including nested steps in foreach_task).
func walkStepNames(steps []recipe.Step, fn func(name string)) {
	for _, s := range steps {
		fn(s.Name)
		if len(s.Steps) > 0 {
			walkStepNames(s.Steps, fn)
		}
	}
}

func printSteps(steps []recipe.Step, indent string) {
	for i, s := range steps {
		prefix := indent + fmt.Sprintf("%d. ", i+1)
		fmt.Printf("%s%s", prefix, s.Name)
		if s.Output != "" && s.Output != "diff" {
			fmt.Printf("  output=%s", s.Output)
		}
		if s.Foreach != "" {
			fmt.Printf("  foreach=%s", s.Foreach)
		}
		if s.Agent != "" {
			fmt.Printf("  agent=%s", s.Agent)
		}
		if len(s.Validate) > 0 && s.Agent == "" && len(s.Steps) == 0 {
			fmt.Printf("  validate=%s", formatValidators(s.Validate))
		} else if len(s.Validate) > 0 {
			fmt.Printf("  validate=%s", formatValidators(s.Validate))
		}
		if s.Retry != nil {
			fmt.Printf("  retry=%d", s.Retry.MaxAttempts)
		}
		if s.Session.Mode != "" && s.Session.Mode != "reuse" {
			if s.Session.Mode == "reuse-targeted" {
				fmt.Printf("  session=reuse:%s", s.Session.Target)
			} else {
				fmt.Printf("  session=%s", s.Session.Mode)
			}
		}
		fmt.Println()
		if len(s.Steps) > 0 {
			subIndent := indent + "     "
			for j, sub := range s.Steps {
				fmt.Printf("%s%d.%d %s", subIndent, i+1, j+1, sub.Name)
				if sub.Output != "" && sub.Output != "diff" {
					fmt.Printf("  output=%s", sub.Output)
				}
				if sub.Agent != "" {
					fmt.Printf("  agent=%s", sub.Agent)
				}
				if len(sub.Validate) > 0 {
					fmt.Printf("  validate=%s", formatValidators(sub.Validate))
				}
				if sub.Retry != nil {
					fmt.Printf("  retry=%d", sub.Retry.MaxAttempts)
				}
				fmt.Println()
			}
		}
	}
}

func formatValidators(v []recipe.Validator) string {
	var parts []string
	for _, x := range v {
		if x.Arg != "" {
			parts = append(parts, x.Type+":"+x.Arg)
		} else {
			parts = append(parts, x.Type)
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
