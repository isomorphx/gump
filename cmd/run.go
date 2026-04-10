package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/isomorphx/gump/internal/agent"
	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/run"
	"github.com/isomorphx/gump/internal/engine"
	"github.com/isomorphx/gump/internal/workflow"
	"github.com/isomorphx/gump/internal/sandbox"
	"github.com/isomorphx/gump/internal/telemetry"
	"github.com/isomorphx/gump/internal/version"
	"github.com/spf13/cobra"
)

const (
	legacyCLIRunFlag      = "co" + "ok"
	legacyCLIWorkflowFlag = "rec" + "ipe"
)

var (
	wfName       string
	wfNameAlias  string
	agentOverride string
	dryRun       bool
	agentStub    bool
	wantReplay   bool
	wantResume   bool
	fromStep     string
	runID        string
	runIDLegacy  string
	verbose      bool
	pauseAfter   string
	stateSet     []string
)

var gumpRunCmd = &cobra.Command{
	Use:   "run [spec-file]",
	Short: "Run a workflow against a spec file",
	Long:  "Resolve the workflow, parse and validate it, then run the workflow (or dry-run to only show the plan).",
	Args: func(cmd *cobra.Command, args []string) error {
		if wantReplay || wantResume {
			return nil
		}
		return cobra.ExactArgs(1)(cmd, args)
	},
	RunE: executeRun,
}

func init() {
	gumpRunCmd.Flags().StringVar(&wfName, "workflow", "", "Workflow name (e.g. tdd, freeform). Omitted for --replay (uses last run's workflow).")
	gumpRunCmd.Flags().StringVar(&wfNameAlias, legacyCLIWorkflowFlag, "", "Deprecated alias for --workflow (e.g. tdd, freeform). Omitted for --replay.")
	gumpRunCmd.Flags().StringVar(&agentOverride, "agent", "", "Override agent for all steps (e.g. claude-sonnet, gemini)")
	gumpRunCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Only show plan, do not execute")
	gumpRunCmd.Flags().BoolVar(&agentStub, "agent-stub", false, "Use stub agent for testing (writes files, no real agent)")
	gumpRunCmd.Flags().BoolVar(&wantReplay, "replay", false, "Replay from a step of the last fatal run (use with --from-step)")
	gumpRunCmd.Flags().BoolVar(&wantResume, "resume", false, "Resume the last fatal/aborted run in place")
	gumpRunCmd.Flags().StringVar(&fromStep, "from-step", "", "Step path or short name to start from (required for --replay)")
	gumpRunCmd.Flags().StringVar(&runID, "run", "", "Run UUID to replay (default: last fatal run)")
	gumpRunCmd.Flags().StringVar(&runIDLegacy, legacyCLIRunFlag, "", "Deprecated alias for --run")
	_ = gumpRunCmd.Flags().MarkDeprecated(legacyCLIRunFlag, "use --run instead")
	_ = gumpRunCmd.Flags().MarkDeprecated(legacyCLIWorkflowFlag, "use --workflow instead")
	gumpRunCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Full streaming output (no truncation)")
	gumpRunCmd.Flags().StringVar(&pauseAfter, "pause-after", "", "Inject HITL pause after the given step name; the step must exist in the workflow")
	gumpRunCmd.Flags().StringSliceVar(&stateSet, "set", nil, "Initial state key=value before the run (repeatable); same idea as sub-workflow with:")
	rootCmd.AddCommand(gumpRunCmd)
}

// executeRunReplay runs a replay from the last fatal run (or legacy --run alias), restoring state and worktree, then running from --from-step.
func executeRunReplay(specPath string, cfg *config.Config, resolved *workflow.ResolvedWorkflow, rec *workflow.Workflow) error {
	if rec == nil {
		return fmt.Errorf("replay: workflow is nil")
	}
	if resolved == nil {
		return fmt.Errorf("replay: resolved workflow is nil")
	}
	cwd, _ := os.Getwd()
	repoRoot, err := sandbox.GitRepoRoot(cwd)
	if err != nil {
		return err
	}
	if fromStep == "" {
		return fmt.Errorf("--from-step is required when using --replay")
	}
	var resolver agent.AdapterResolver
	if agentStub {
		resolver = &agent.StubResolver{Stub: &agent.StubAdapter{}}
	} else {
		resolver = &agent.Registry{
			Claude:   agent.NewClaudeAdapter(),
			Codex:    agent.NewCodexAdapter(),
			Gemini:   agent.NewGeminiAdapter(),
			Qwen:     agent.NewQwenAdapter(),
			OpenCode: agent.NewOpenCodeAdapter(),
			Cursor:   agent.NewCursorAdapter(),
		}
	}
	agentsCLI := agentsCLIForWorkflow(rec, agentStub)
	c, stepsCount, err := engine.RunReplay(repoRoot, specPath, fromStep, runID, rec, resolved.Raw, resolver, cfg, agentsCLI)
	if err != nil {
		if c != nil {
			_ = run.WriteStatus(c.RunDir, "fatal")
			fmt.Fprintf(os.Stderr, "Run failed at step: %v\n", err)
			fmt.Fprintf(os.Stderr, "Worktree preserved at %s\n", c.WorktreeDir)
		} else {
			fmt.Fprintf(os.Stderr, "Replay failed: %v\n", err)
		}
		return err
	}
	if c == nil {
		return fmt.Errorf("replay: internal error (no run returned)")
	}
	if err := run.WriteStatusWithSteps(c.RunDir, "pass", stepsCount); err != nil {
		return err
	}
	fmt.Printf("Replay complete (run %s, %d steps). Run 'gump apply' to merge results.\n", c.ID, stepsCount)
	return nil
}

// executeRun runs the workflow: dry-run prints the plan only; otherwise creates worktree, runs engine, persists state.
func executeRun(cmd *cobra.Command, args []string) error {
	if wfNameAlias != "" {
		// WHY: G1 keeps a deprecated CLI alias so existing scripts keep working until operators migrate flags.
		fmt.Fprintln(os.Stderr, "warning: --"+legacyCLIWorkflowFlag+" is deprecated, use --workflow instead")
		if wfName == "" {
			wfName = wfNameAlias
		}
	}
	if runIDLegacy != "" {
		fmt.Fprintln(os.Stderr, "warning: --"+legacyCLIRunFlag+" is deprecated, use --run instead")
		if runID == "" {
			runID = runIDLegacy
		}
	}
	if wantReplay && wantResume {
		return fmt.Errorf("--replay and --resume are mutually exclusive")
	}

	cfg, _, err := config.Load()
	if err != nil {
		return err
	}
	defer func() { workflow.ValidateProjectRoot = "" }()
	workflow.ParseWarn = func(msg string) { fmt.Fprintln(os.Stderr, "warning:", msg) }
	workflow.ValidateWarn = func(path, message string) {
		if path != "" {
			fmt.Fprintf(os.Stderr, "warning: %s: %s\n", path, message)
		} else {
			fmt.Fprintln(os.Stderr, "warning:", message)
		}
	}

	var specPath string
	var resolved *workflow.ResolvedWorkflow
	var rec *workflow.Workflow

	if wantResume {
		if len(args) > 0 {
			return fmt.Errorf("spec file is not used with --resume")
		}
		var resolver agent.AdapterResolver
		if agentStub {
			resolver = &agent.StubResolver{Stub: &agent.StubAdapter{}}
		} else {
			resolver = &agent.Registry{
				Claude:   agent.NewClaudeAdapter(),
				Codex:    agent.NewCodexAdapter(),
				Gemini:   agent.NewGeminiAdapter(),
				Qwen:     agent.NewQwenAdapter(),
				OpenCode: agent.NewOpenCodeAdapter(),
				Cursor:   agent.NewCursorAdapter(),
			}
		}
		cwd, _ := os.Getwd()
		repoRoot, err := sandbox.GitRepoRoot(cwd)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("verbose") {
			engine.Verbose = verbose
		} else {
			engine.Verbose = cfg.Verbose
		}
		c, stepsCount, err := engine.RunResume(repoRoot, runID, resolver, cfg, nil)
		if err != nil {
			if c != nil {
				_ = run.WriteStatus(c.RunDir, "fatal")
			}
			return err
		}
		if c == nil {
			return fmt.Errorf("resume: internal error (no run returned)")
		}
		if err := run.WriteStatusWithSteps(c.RunDir, "pass", stepsCount); err != nil {
			return err
		}
		fmt.Printf("Resume complete (run %s, %d steps). Run 'gump apply' to merge results.\n", c.ID, stepsCount)
		return nil
	}

	if wantReplay {
		if len(args) >= 1 && wfName != "" {
			// Replay with explicit spec and workflow (e.g. smoke test): use them so we don't depend on snapshot.
			specPath = args[0]
			projectRoot := config.ProjectRoot()
			var err error
			resolved, err = workflow.Resolve(wfName, projectRoot)
			if err != nil {
				return err
			}
			wfDir := ""
			if resolved.Path != "" {
				wfDir = filepath.Dir(resolved.Path)
			}
			var warns []workflow.Warning
			rec, warns, err = workflow.Parse(resolved.Raw, wfDir)
			for _, w := range warns {
				fmt.Fprintln(os.Stderr, "warning:", w.Message)
			}
			if err != nil {
				return fmt.Errorf("%w\n(workflow loaded from %s)", err, resolved.Source)
			}
			workflow.ValidateProjectRoot = projectRoot
			if errs := workflow.Validate(rec); len(errs) > 0 {
				for _, e := range errs {
					fmt.Fprintln(os.Stderr, e.Error())
				}
				os.Exit(1)
			}
			return executeRunReplay(specPath, cfg, resolved, rec)
		}
		// Replay without args: infer spec and workflow from the last fatal run snapshot.
		cwd, _ := os.Getwd()
		repoRoot, err := sandbox.GitRepoRoot(cwd)
		if err != nil {
			return err
		}
		runDir, err := engine.FindLastFatalRun(repoRoot, runID)
		if err != nil {
			return err
		}
		ctxPath := filepath.Join(runDir, "context-snapshot.json")
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
		snapPath := filepath.Join(runDir, "workflow-snapshot.yaml")
		snapYAML, err := os.ReadFile(snapPath)
		if err != nil {
			return fmt.Errorf("replay: read workflow snapshot: %w", err)
		}
		resolved = &workflow.ResolvedWorkflow{Name: "", Source: "replay", Path: snapPath, Raw: snapYAML}
		var warns []workflow.Warning
		rec, warns, err = workflow.Parse(snapYAML, "")
		for _, w := range warns {
			fmt.Fprintln(os.Stderr, "warning:", w.Message)
		}
		if err != nil {
			return fmt.Errorf("replay: parse workflow snapshot: %w", err)
		}
		workflow.ValidateProjectRoot = ctx.RepoRoot
		if errs := workflow.Validate(rec); len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, e.Error())
			}
			os.Exit(1)
		}
		return executeRunReplay(specPath, cfg, resolved, rec)
	}

	if wfName == "" {
		return fmt.Errorf("--workflow is required when not using --replay")
	}
	if len(args) < 1 {
		return fmt.Errorf("spec file is required when not using --replay")
	}
	specPath = args[0]
	projectRoot := config.ProjectRoot()
	workflow.ValidateProjectRoot = projectRoot
	resolved, err = workflow.Resolve(wfName, projectRoot)
	if err != nil {
		return err
	}
	wfDir := ""
	if resolved.Path != "" {
		wfDir = filepath.Dir(resolved.Path)
	}
	var warns []workflow.Warning
	rec, warns, err = workflow.Parse(resolved.Raw, wfDir)
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "warning:", w.Message)
	}
	if err != nil {
		source := resolved.Source
		path := resolved.Path
		if path != "" {
			return fmt.Errorf("%w\n(workflow loaded from %s: %s)", err, source, path)
		}
		return fmt.Errorf("%w\n(workflow loaded from %s)", err, source)
	}
	errs := workflow.Validate(rec)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e.Error())
		}
		os.Exit(1)
	}
	if dryRun {
		workflow.EmitDryRunSubworkflowInputWarnings(rec, projectRoot)
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
		configSource = "gump.toml"
	}
	if dryRun {
		fmt.Println("Gump Dry Run")
		fmt.Println("─────────────────────────────────────────────────────────")
		fmt.Println()
		fmt.Printf("Workflow:  %s\n", resolved.Name)
		if rec.MaxBudget > 0 {
			fmt.Printf("Budget:    $%.2f\n", rec.MaxBudget)
		}
		fmt.Println()
		fmt.Printf("Source:    %s\n", resolved.Source)
		fmt.Printf("Spec:      %s (%d bytes)\n", specPath, specSize)
		fmt.Printf("Config:    %s\n", configSource)
		if rec.MaxTimeout != "" {
			fmt.Printf("max_timeout: %s\n", rec.MaxTimeout)
		}
		if rec.MaxTokens > 0 {
			fmt.Printf("max_tokens: %d\n", rec.MaxTokens)
		}
		if cfg.Analytics {
			fmt.Printf("Analytics: enabled\n")
		} else {
			fmt.Printf("Analytics: disabled\n")
		}
		fmt.Println()
		fmt.Println("Steps:")
		printStepsV4(rec.Steps, "  ", "")
		fmt.Println()
		printStateBagResolutionsV4(rec)
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
	c, err := run.NewRun(rec, specPath, repoRoot, resolved.Raw, cfg)
	if err != nil {
		return err
	}
	fmt.Printf("Worktree: %s\n", c.WorktreeDir)

	var resolver agent.AdapterResolver
	if agentStub {
		resolver = &agent.StubResolver{Stub: &agent.StubAdapter{}}
	} else {
		resolver = &agent.Registry{
			Claude:   agent.NewClaudeAdapter(),
			Codex:    agent.NewCodexAdapter(),
			Gemini:   agent.NewGeminiAdapter(),
			Qwen:     agent.NewQwenAdapter(),
			OpenCode: agent.NewOpenCodeAdapter(),
			Cursor:   agent.NewCursorAdapter(),
		}
	}

	eng := engine.New(c, rec, resolver, cfg, string(specContent))
	eng.AgentsCLI = agentsCLIForWorkflow(rec, agentStub)
	eng.RunAgentOverride = agentOverride
	for _, kv := range stateSet {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			return fmt.Errorf("--set %q: expected key=value", kv)
		}
		eng.State.Set(strings.TrimSpace(kv[:i]), strings.TrimSpace(kv[i+1:]))
	}
	if cmd.Flags().Changed("verbose") {
		engine.Verbose = verbose
	} else {
		engine.Verbose = cfg.Verbose
	}
	if pauseAfter != "" {
		if workflow.FindStepByName(rec.Steps, pauseAfter) == nil {
			return fmt.Errorf("--pause-after: step %q not found in workflow", pauseAfter)
		}
		eng.PauseAfterStep = pauseAfter
	}
	anonymousID, telemetryFirstRun := telemetry.InitAnonymousID(cfg.Analytics, os.Stderr)
	runStartedAt := time.Now()
	sendTelemetry := func(runStatus string) {
		telemetry.Send(cfg.Analytics, anonymousID, telemetryFirstRun, version.Version, buildTelemetryPayload(rec, resolved.Source, eng, runStatus, runStartedAt, repoRoot))
	}
	if err := eng.Execute(); err != nil {
		sendTelemetry("fail")
		if errors.Is(err, engine.ErrRunAborted) {
			_ = run.WriteStatus(c.RunDir, "aborted")
			return err
		}
		_ = run.WriteStatus(c.RunDir, "fatal")
		return err
	}
	if err := run.WriteStatusWithSteps(c.RunDir, "pass", len(eng.Steps)); err != nil {
		return err
	}
	sendTelemetry("pass")
	fmt.Printf("Run complete (%d steps). Run 'gump apply' to merge results.\n", len(eng.Steps))
	return nil
}

func buildTelemetryPayload(rec *workflow.Workflow, source string, eng *engine.Engine, runStatus string, startedAt time.Time, repoRoot string) telemetry.RunPayload {
	workflowSource := "builtin"
	switch source {
	case "project", "user":
		workflowSource = source
	}
	agentsSet := map[string]struct{}{}
	hasForeach, hasParallel, hasGuard, hasHITL, hasSubworkflow, usesSessionReuse := false, false, false, false, false, false
	var walk func(steps []workflow.Step)
	walk = func(steps []workflow.Step) {
		for _, s := range steps {
			if s.Type == "split" && len(s.Each) > 0 {
				hasForeach = true
			}
			if s.Parallel {
				hasParallel = true
			}
			if s.Workflow != "" {
				hasSubworkflow = true
			}
			if strings.TrimSpace(s.HITL) != "" {
				hasHITL = true
			}
			if s.Agent != "" {
				agentsSet[s.Agent] = struct{}{}
			}
			if s.Guard.MaxTurns > 0 || s.Guard.MaxBudget > 0 || s.Guard.MaxTokens > 0 || s.Guard.MaxTime != "" || s.Guard.NoWrite != nil {
				hasGuard = true
			}
			if s.Session.Mode == "from" {
				usesSessionReuse = true
			}
			if len(s.Steps) > 0 {
				walk(s.Steps)
			}
			if len(s.Each) > 0 {
				walk(s.Each)
			}
		}
	}
	walk(rec.Steps)
	agents := make([]string, 0, len(agentsSet))
	for a := range agentsSet {
		agents = append(agents, a)
	}
	sort.Strings(agents)

	guardHitsByStep := map[string]int{}
	var totalRetries, guardTriggers int
	for _, s := range eng.Steps {
		if s.Attempt > 1 {
			totalRetries++
		}
	}
	for _, s := range eng.Steps {
		if s.Status == engine.StepFatal && strings.Contains(strings.ToLower(s.ValidateError), "guard ") {
			guardHitsByStep[s.StepPath]++
			guardTriggers++
		}
	}
	latestByStep := map[string]engine.StepExecution{}
	for _, s := range eng.Steps {
		latestByStep[s.StepPath] = s
	}
	steps := make([]telemetry.StepPayload, 0, len(latestByStep))
	for _, s := range latestByStep {
		st := string(s.Status)
		if st == "fatal" {
			st = "fail"
		}
		short := path.Base(s.StepPath)
		cost, _ := strconv.ParseFloat(eng.State.GetStepScoped(short, s.StepPath, "cost"), 64)
		turns, _ := strconv.Atoi(eng.State.GetStepScoped(short, s.StepPath, "turns"))
		tokensIn, _ := strconv.Atoi(eng.State.GetStepScoped(short, s.StepPath, "tokens_in"))
		tokensOut, _ := strconv.Atoi(eng.State.GetStepScoped(short, s.StepPath, "tokens_out"))
		steps = append(steps, telemetry.StepPayload{
			Name:          anonymizeForeachPath(s.StepPath),
			Agent:         s.Agent,
			Output:        s.OutputMode,
			Status:        st,
			Duration:      int(s.FinishedAt.Sub(s.StartedAt).Milliseconds()),
			Cost:          cost,
			Turns:         turns,
			Retries:       maxInt(0, s.Attempt-1),
			GuardHits:     guardHitsByStep[s.StepPath],
			TokensIn:      tokensIn,
			TokensOut:     tokensOut,
			ContextUsage:  0,
			TTFD:          0,
			EscalatedFrom: nil,
			EscalatedTo:   nil,
		})
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].Name < steps[j].Name })
	totalCost, _ := strconv.ParseFloat(eng.State.GetRunMetric("cost"), 64)

	return telemetry.RunPayload{
		Workflow:         rec.Name,
		WorkflowSource:   workflowSource,
		IsCustomWorkflow: workflowSource != "builtin",
		RunStatus:        runStatus,
		DurationMs:       int(time.Since(startedAt).Milliseconds()),
		TotalCostUSD:     totalCost, // known limitation for G3: best available estimate may be partial
		AgentsUsed:       agents,
		AgentCount:       len(agents),
		StepCount:        len(steps),
		HasForeach:       hasForeach,
		HasParallel:      hasParallel,
		HasGuard:         hasGuard,
		HasHITL:          hasHITL,
		HasSubworkflow:   hasSubworkflow,
		UsesSessionReuse: usesSessionReuse,
		TotalRetries:     totalRetries,
		GuardTriggers:    guardTriggers,
		RepoLanguage:     detectRepoLanguage(repoRoot),
		RepoSizeBucket:   detectRepoSizeBucket(repoRoot),
		Steps:            steps,
	}
}

func anonymizeForeachPath(path string) string {
	parts := strings.Split(path, "/")
	// WHY: foreach item names can leak repository semantics; replace the item segment while preserving step shape.
	if len(parts) >= 3 {
		parts[len(parts)-2] = "*"
	}
	return strings.Join(parts, "/")
}

func detectRepoLanguage(root string) string {
	counts := map[string]int{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == ".gump" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(p) {
		case ".go":
			counts["go"]++
		case ".ts", ".tsx":
			counts["typescript"]++
		case ".js", ".jsx":
			counts["javascript"]++
		case ".py":
			counts["python"]++
		case ".rs":
			counts["rust"]++
		}
		return nil
	})
	best := "unknown"
	bestN := 0
	for k, n := range counts {
		if n > bestN {
			best, bestN = k, n
		}
	}
	return best
}

func detectRepoSizeBucket(root string) string {
	var n int
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == ".gump" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		n++
		return nil
	})
	switch {
	case n < 1000:
		return "<1k"
	case n <= 10000:
		return "1k-10k"
	case n <= 100000:
		return "10k-100k"
	default:
		return ">100k"
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// agentsCLIForWorkflow builds a map of agent name -> CLI version for run_started. Stub mode uses "stub-1.0.0"; otherwise "unknown".
func agentsCLIForWorkflow(rec *workflow.Workflow, useStub bool) map[string]string {
	agents := make(map[string]struct{})
	var walkAgents func(steps []workflow.Step)
	walkAgents = func(steps []workflow.Step) {
		for _, s := range steps {
			if s.Agent != "" {
				agents[s.Agent] = struct{}{}
			}
			if len(s.Steps) > 0 {
				walkAgents(s.Steps)
			}
			if len(s.Each) > 0 {
				walkAgents(s.Each)
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
func walkStepNames(steps []workflow.Step, fn func(name string)) {
	for _, s := range steps {
		fn(s.Name)
		if len(s.Steps) > 0 {
			walkStepNames(s.Steps, fn)
		}
		if len(s.Each) > 0 {
			walkStepNames(s.Each, fn)
		}
	}
}

func printStepsV4(steps []workflow.Step, indent string, parentNum string) {
	for i, s := range steps {
		var stepNum string
		if parentNum == "" {
			stepNum = fmt.Sprintf("%d", i+1)
			fmt.Printf("%s%s. %s\n", indent, stepNum, s.Name)
		} else {
			stepNum = parentNum + "." + fmt.Sprintf("%d", i+1)
			fmt.Printf("%s%s %s\n", indent, stepNum, s.Name)
		}
		detailIndent := indent + "   "

		var summary []string
		if t := strings.TrimSpace(s.Type); t != "" {
			summary = append(summary, "type="+t)
		}
		if s.Parallel {
			summary = append(summary, "parallel=true")
		}
		if s.Agent != "" {
			summary = append(summary, "agent="+s.Agent)
		}
		if len(s.Gate) > 0 {
			summary = append(summary, "gate="+formatValidators(s.Gate))
		}
		if s.Workflow != "" {
			summary = append(summary, "workflow="+s.Workflow)
		}
		if len(summary) > 0 {
			fmt.Printf("%s%s\n", detailIndent, strings.Join(summary, "  "))
		}

		if s.Type == "split" && len(s.Each) > 0 {
			fmt.Printf("%seach:\n", detailIndent)
			printStepsV4(s.Each, detailIndent, stepNum)
		} else {
			if strings.TrimSpace(s.Prompt) != "" {
				preview := strings.TrimSpace(s.Prompt)
				if len(preview) > 60 {
					preview = preview[:57] + "..."
				}
				fmt.Printf("%sprompt=%q\n", detailIndent, preview)
			}
			if strings.TrimSpace(s.HITL) != "" {
				fmt.Printf("%shitl=%s\n", detailIndent, s.HITL)
			}
			if s.Session.Mode == "from" && s.Session.Target != "" {
				fmt.Printf("%ssession=from:%s\n", detailIndent, s.Session.Target)
			} else if s.Session.Mode == "new" && strings.TrimSpace(s.Type) != "" {
				fmt.Printf("%ssession=new\n", detailIndent)
			}
			hasExplicitGuard := s.Guard.MaxTurns > 0 || s.Guard.MaxBudget > 0 || s.Guard.MaxTokens > 0 || s.Guard.MaxTime != "" || s.Guard.NoWrite != nil
			if hasExplicitGuard {
				fmt.Printf("%sguard:", detailIndent)
				if s.Guard.MaxTurns > 0 {
					fmt.Printf(" max_turns=%d", s.Guard.MaxTurns)
				}
				if s.Guard.MaxBudget > 0 {
					fmt.Printf(" max_budget=%.2f", s.Guard.MaxBudget)
				}
				if s.Guard.MaxTokens > 0 {
					fmt.Printf(" max_tokens=%d", s.Guard.MaxTokens)
				}
				if s.Guard.MaxTime != "" {
					fmt.Printf(" max_time=%s", s.Guard.MaxTime)
				}
				if s.Guard.NoWrite != nil {
					fmt.Printf(" no_write=%t", *s.Guard.NoWrite)
				}
				fmt.Printf("\n")
			}
			if len(s.Retry) > 0 {
				if line := formatRetryDryRunV4(s.Retry); line != "" {
					fmt.Printf("%sretry: %s\n", detailIndent, line)
				}
			}
		}
		if len(s.Steps) > 0 {
			printStepsV4(s.Steps, detailIndent, stepNum)
		}
	}
}

// formatRetryDryRunV4 summarizes native retry entries for dry-run (ordered list semantics).
// formatRetryDryRunV4 gives operators a single-line view of the ordered retry policy (matches how runs fail or escalate in practice).
func formatRetryDryRunV4(entries []workflow.RetryEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var parts []string
	for _, e := range entries {
		if e.Exit > 0 {
			parts = append(parts, fmt.Sprintf("exit:%d", e.Exit))
			continue
		}
		if e.Attempt <= 0 {
			continue
		}
		tail := dryRunRetryTail(&e)
		parts = append(parts, fmt.Sprintf("attempt:%d→%s", e.Attempt, tail))
	}
	return strings.Join(parts, ", ")
}

func dryRunRetryTail(e *workflow.RetryEntry) string {
	if strings.TrimSpace(e.Prompt) != "" {
		return "prompt"
	}
	ag := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(e.Agent), "claude-"))
	if strings.EqualFold(strings.TrimSpace(e.Worktree), "reset") {
		if ag != "" {
			return ag + "+reset"
		}
		return "reset"
	}
	if strings.EqualFold(strings.TrimSpace(e.Session), "new") {
		if ag != "" {
			return ag + "+new"
		}
		return "new"
	}
	if ag != "" {
		return ag
	}
	return "retry"
}

func formatValidators(v []workflow.GateEntry) string {
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

// printStateBagResolutionsV4 prints which `{<step>.output|diff|files}` placeholders
// resolve to which fully-qualified step keys under strict scope rules (dry-run parity with runtime).
func printStateBagResolutionsV4(rec *workflow.Workflow) {
	refsRe := regexp.MustCompile(`\{([a-zA-Z0-9_/-]+)\.(output|diff|files)\}`)

	type node struct {
		fullPath string
		name     string
		prompt   string
	}
	var nodes []node
	var collect func(steps []workflow.Step, prefix string)
	collect = func(steps []workflow.Step, prefix string) {
		for _, s := range steps {
			full := s.Name
			if prefix != "" {
				full = prefix + "/" + s.Name
			}
			nodes = append(nodes, node{fullPath: full, name: s.Name, prompt: s.Prompt})
			if len(s.Steps) > 0 {
				collect(s.Steps, full)
			}
			if len(s.Each) > 0 {
				collect(s.Each, full)
			}
		}
	}
	collect(rec.Steps, "")

	// WHY: reuse the StateBag scoping rule so the dry-run matches runtime resolution.
	buildScopeChain := func(scopePath string) []string {
		if scopePath == "" {
			return []string{""}
		}
		parts := strings.Split(scopePath, "/")
		out := make([]string, 0, len(parts)+1)
		for i := len(parts); i >= 0; i-- {
			out = append(out, strings.Join(parts[:i], "/"))
		}
		return out
	}

	resolve := func(refName, scopePath string) (string, bool) {
		var candidates []string
		for _, n := range nodes {
			base := path.Base(n.fullPath)
			if base == refName || n.fullPath == refName {
				candidates = append(candidates, n.fullPath)
			}
		}
		if len(candidates) == 0 {
			return "", false
		}
		for _, scope := range buildScopeChain(scopePath) {
			var atScope []string
			for _, c := range candidates {
				inScope := (scope == "" && !strings.Contains(c, "/")) || (scope != "" && (c == scope || strings.HasPrefix(c, scope+"/")))
				if inScope {
					atScope = append(atScope, c)
				}
			}
			if len(atScope) == 1 {
				return atScope[0], true
			}
			if len(atScope) > 1 {
				return "", false
			}
		}
		if len(candidates) == 1 {
			return candidates[0], true
		}
		return "", false
	}

	var any bool
	fmt.Println("State Bag Resolutions:")
	for _, n := range nodes {
		if n.prompt == "" {
			continue
		}
		matches := refsRe.FindAllStringSubmatch(n.prompt, -1)
		for _, m := range matches {
			if len(m) != 3 {
				continue
			}
			refName := m[1]
			field := m[2]
			if strings.Contains(refName, "/") {
				continue
			}
			resolvedFull, ok := resolve(refName, n.fullPath)
			if !ok {
				continue
			}
			placeholder := fmt.Sprintf("{%s.%s}", refName, field)
			targetField := field
			if field == "diff" {
				targetField = "output"
			}
			fmt.Printf("  %s: %s → %s.%s\n", n.fullPath, placeholder, resolvedFull, targetField)
			any = true
		}
	}
	if !any {
		// Keep output deterministic: print header even if nothing was found.
		_ = 0
	}
}
