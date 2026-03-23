package context

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	stdctx "context"
	"strings"
	"time"

	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/recipe"
	"github.com/isomorphx/gump/internal/template"
)

const (
	contextFileSizeWarnChars = 400_000
	bashContextTimeout       = 30 * time.Second

	defaultPlanTaskPrompt = `Analyze the specification below and the codebase. Decompose the work into independent items. For each item, list the files that will be affected.`
)

// ContextParams is the full input for the v4 system prompt. One struct keeps retry truncation,
// mode-specific sections, and provider-agnostic text in a single place so CLAUDE.md and AGENTS.md never diverge.
type ContextParams struct {
	OutputMode       string
	Prompt           string
	Spec             string
	IsRetry          bool
	Attempt          int
	MaxAttempts      int
	Error            string
	Diff             string
	ReviewComment    string
	BlastRadius      []string
	Conventions      string
	ContextSources   []ContextSourceResult
	SessionReuse     bool
	IsReplan         bool
	ItemName         string
	ItemDesc         string
	ItemFiles        string
	MaxErrorChars    int
	MaxDiffChars     int
}

// ContextSourceResult is one resolved recipe context: entry (file contents or bash stdout).
type ContextSourceResult struct {
	Type    string
	Label   string
	Content string
}

// RetrySection is passed from the engine on validation failure so the next attempt can see diff/stderr.
type RetrySection struct {
	Attempt        int
	MaxAttempts    int
	Diff           string
	Error          string
	Remaining      int
	EscalateFrom   string
	EscalateTo     string
	ReviewComment  string
}

var originalBackupByContextFile = map[string]string{
	"CLAUDE.md": brand.StateDir() + "-original-CLAUDE.md",
	"AGENTS.md": brand.StateDir() + "-original-AGENTS.md",
	"GEMINI.md": brand.StateDir() + "-original-GEMINI.md",
	"QWEN.md":   brand.StateDir() + "-original-QWEN.md",
}

// BuildAgentContext returns the full markdown for the provider context file (CLAUDE.md, AGENTS.md, …).
// Callers persist it to the worktree; content is identical across providers, only the filename changes.
func BuildAgentContext(p ContextParams) string {
	if p.IsReplan {
		return buildReplanMarkdown(p)
	}
	var b strings.Builder
	if p.SessionReuse {
		b.WriteString("## Context transition\n\n")
		b.WriteString("You are continuing from a previous step. The previous step completed successfully.\n")
		b.WriteString("Your new task is described below. Focus on this new task only.\n")
		b.WriteString("The codebase has been updated with the results of the previous step.\n\n")
	}
	switch p.OutputMode {
	case "plan":
		b.WriteString(buildPlanBody(p))
	case "artifact":
		b.WriteString(buildArtifactBody(p))
	case "review":
		b.WriteString(buildReviewBody(p))
	default:
		b.WriteString(buildDiffBody(p))
	}
	return b.String()
}

func buildDiffBody(p ContextParams) string {
	var b strings.Builder
	b.WriteString("# Gump — Agent Instructions\n\n")
	b.WriteString("You are executing a code step in a Gump workflow.\n\n")
	b.WriteString("## Your task\n\n")
	b.WriteString(p.Prompt)
	b.WriteString("\n")
	if p.IsRetry {
		b.WriteString(buildRetrySection(p))
	}
	b.WriteString("\n## Blast radius\n\n")
	b.WriteString(formatBlastRadius(p.BlastRadius))
	b.WriteString("\n\n## Output expectations\n\n")
	b.WriteString("Write code directly in the repository. Gump will capture your changes via git diff.\n")
	b.WriteString("Do not write plan/artifact/review deliverables to the output tree; those modes use separate paths.\n\n")
	b.WriteString("## Git rules\n\n")
	b.WriteString("- Do NOT run `git commit`, `git add`, `git push`, or any git command.\n")
	b.WriteString("- Do NOT switch branches.\n")
	b.WriteString("- You are in a Gump worktree. Gump manages git.\n")
	if strings.TrimSpace(p.Conventions) != "" {
		b.WriteString("\n## Project conventions\n\n")
		b.WriteString(p.Conventions)
		b.WriteString("\n")
	}
	if s := formatAdditionalContext(p.ContextSources); s != "" {
		b.WriteString("\n## Additional context\n\n")
		b.WriteString(s)
	}
	return b.String()
}

func buildPlanBody(p ContextParams) string {
	task := strings.TrimSpace(p.Prompt)
	if task == "" {
		task = defaultPlanTaskPrompt
	}
	var b strings.Builder
	b.WriteString("# Gump — Agent Instructions\n\n")
	b.WriteString("You are executing a plan step in a Gump workflow.\n\n")
	b.WriteString("## Your task\n\n")
	b.WriteString(task)
	b.WriteString("\n")
	if p.IsRetry {
		b.WriteString(buildRetrySection(p))
	}
	b.WriteString("\n## Output format\n\n")
	b.WriteString("You MUST create a file called `"+brand.StateDir()+"/out/plan.json` in this repository.\n")
	b.WriteString("The file MUST contain a JSON array of items with this exact schema:\n\n")
	b.WriteString("```json\n[\n  {\n")
	b.WriteString(`    "name": "short-kebab-case-name",` + "\n")
	b.WriteString(`    "description": "What this item accomplishes. Be specific and actionable.",` + "\n")
	b.WriteString(`    "files": ["path/to/file1.go", "path/to/file2.go"]` + "\n")
	b.WriteString("  }\n]\n```\n\n")
	b.WriteString("Rules for the plan:\n\n")
	b.WriteString("- Each item must be independently implementable and testable.\n")
	b.WriteString("- `files` is the blast radius: list every file that will be created, modified, or deleted.\n")
	b.WriteString("- `files` supports globs (e.g., `internal/auth/*_test.go`).\n")
	b.WriteString("- If you cannot determine the blast radius, omit the `files` field for that item.\n")
	b.WriteString("- Order items by dependency: if item B depends on item A's output, A comes first.\n")
	b.WriteString("- Do NOT implement any code. Only produce the plan.\n\n")
	b.WriteString("## Git rules\n\n")
	b.WriteString("- Do NOT run `git commit`, `git add`, `git push`, or any git command.\n")
	b.WriteString("- Do NOT switch branches.\n")
	b.WriteString("- You are in a Gump worktree. Gump manages git.\n\n")
	b.WriteString("## Specification\n\n")
	b.WriteString(p.Spec)
	b.WriteString("\n")
	if s := formatAdditionalContext(p.ContextSources); s != "" {
		b.WriteString("\n## Additional context\n\n")
		b.WriteString(s)
	}
	return b.String()
}

func buildArtifactBody(p ContextParams) string {
	var b strings.Builder
	b.WriteString("# Gump — Agent Instructions\n\n")
	b.WriteString("You are executing an artifact step in a Gump workflow.\n\n")
	b.WriteString("## Your task\n\n")
	b.WriteString(p.Prompt)
	b.WriteString("\n")
	if p.IsRetry {
		b.WriteString(buildRetrySection(p))
	}
	b.WriteString("\n## Output format\n\n")
	b.WriteString("You MUST write your output to the file `"+brand.StateDir()+"/out/artifact.txt` in this repository.\n")
	b.WriteString("The content is free-form text. Write whatever the task requires.\n")
	b.WriteString("Do NOT modify any source code files unless the task explicitly requires it.\n\n")
	b.WriteString("## Git rules\n\n")
	b.WriteString("- Do NOT run `git commit`, `git add`, `git push`, or any git command.\n")
	b.WriteString("- Do NOT switch branches.\n")
	b.WriteString("- You are in a Gump worktree. Gump manages git.\n")
	if s := formatAdditionalContext(p.ContextSources); s != "" {
		b.WriteString("\n## Additional context\n\n")
		b.WriteString(s)
	}
	return b.String()
}

func buildReviewBody(p ContextParams) string {
	var b strings.Builder
	b.WriteString("# Gump — Agent Instructions\n\n")
	b.WriteString("You are executing a review step in a Gump workflow.\n\n")
	b.WriteString("## Your task\n\n")
	b.WriteString(p.Prompt)
	b.WriteString("\n")
	if p.IsRetry {
		b.WriteString(buildRetrySection(p))
	}
	b.WriteString("\n## Output format\n\n")
	b.WriteString("You MUST create a file called `"+brand.StateDir()+"/out/review.json` in this repository.\n")
	b.WriteString("The file MUST contain a JSON object with this exact schema:\n\n")
	b.WriteString("```json\n{\n")
	b.WriteString(`  "pass": true,` + "\n")
	b.WriteString(`  "comment": "Brief explanation of why the review passes or fails."` + "\n")
	b.WriteString("}\n```\n\n")
	b.WriteString("Rules for the review:\n\n")
	b.WriteString("- `pass` is a boolean. `true` means the code meets the criteria. `false` means it does not.\n")
	b.WriteString("- `comment` is a string. Explain your reasoning. If `pass` is `false`, be specific about what needs to change.\n")
	b.WriteString("- Do NOT modify any source code files. You are a reviewer, not an implementer.\n")
	b.WriteString("- Be precise and actionable in your feedback.\n\n")
	b.WriteString("## Git rules\n\n")
	b.WriteString("- Do NOT run `git commit`, `git add`, `git push`, or any git command.\n")
	b.WriteString("- Do NOT switch branches.\n")
	b.WriteString("- You are in a Gump worktree. Gump manages git.\n")
	if s := formatAdditionalContext(p.ContextSources); s != "" {
		b.WriteString("\n## Additional context\n\n")
		b.WriteString(s)
	}
	return b.String()
}

func buildRetrySection(p ContextParams) string {
	maxE := p.MaxErrorChars
	maxD := p.MaxDiffChars
	if maxE <= 0 {
		maxE = 2000
	}
	if maxD <= 0 {
		maxD = 3000
	}
	td := TruncateLines(p.Diff, maxD)
	te := TruncateLines(p.Error, maxE)
	var b strings.Builder
	b.WriteString("\n## Previous attempt failed\n\n")
	fmt.Fprintf(&b, "This is retry attempt %d of %d.\n\n", p.Attempt, p.MaxAttempts)
	b.WriteString("The previous attempt produced this diff:\n\n")
	b.WriteString("```diff\n")
	b.WriteString(td)
	b.WriteString("\n```\n\n")
	b.WriteString("The validation failed with this error:\n\n")
	b.WriteString("```\n")
	b.WriteString(te)
	b.WriteString("\n```\n")
	if strings.TrimSpace(p.ReviewComment) != "" {
		b.WriteString("\nA reviewer identified this issue:\n\n")
		b.WriteString(p.ReviewComment)
		b.WriteString("\n")
	}
	b.WriteString("\nAnalyze the error, understand what went wrong, and try a different approach. Do NOT repeat the same mistake.\n")
	return b.String()
}

func buildReplanMarkdown(p ContextParams) string {
	maxE := p.MaxErrorChars
	maxD := p.MaxDiffChars
	if maxE <= 0 {
		maxE = 2000
	}
	if maxD <= 0 {
		maxD = 3000
	}
	td := TruncateLines(p.Diff, maxD)
	te := TruncateLines(p.Error, maxE)
	var b strings.Builder
	b.WriteString("# Gump — Agent Instructions\n\n")
	b.WriteString("You are re-planning a task that failed during implementation.\n\n")
	b.WriteString("## Original task\n\n")
	fmt.Fprintf(&b, "Name: %s\nDescription: %s\nFiles: %s\n\n", p.ItemName, p.ItemDesc, p.ItemFiles)
	b.WriteString("## What failed\n\n")
	b.WriteString("The implementation attempt produced this diff:\n\n")
	b.WriteString("```diff\n")
	b.WriteString(td)
	b.WriteString("\n```\n\n")
	b.WriteString("The validation failed with this error:\n\n")
	b.WriteString("```\n")
	b.WriteString(te)
	b.WriteString("\n```\n\n")
	b.WriteString("## Your job\n\n")
	b.WriteString("Re-decompose this task. You can:\n\n")
	b.WriteString("1. Break it into smaller sub-tasks with more precise blast radii.\n")
	b.WriteString("2. Propose a different approach entirely.\n")
	b.WriteString("3. Implement it directly if the decomposition is unnecessary.\n\n")
	b.WriteString("## Output format\n\n")
	b.WriteString("Same as a plan step. Create `"+brand.StateDir()+"/out/plan.json` with the schema:\n\n")
	b.WriteString("```json\n[\n  {\n")
	b.WriteString(`    "name": "short-kebab-case-name",` + "\n")
	b.WriteString(`    "description": "What this task accomplishes.",` + "\n")
	b.WriteString(`    "files": ["path/to/file.go"]` + "\n")
	b.WriteString("  }\n]\n```\n\n")
	b.WriteString("## Git rules\n\n")
	b.WriteString("- Do NOT run `git commit`, `git add`, `git push`, or any git command.\n")
	b.WriteString("- Do NOT switch branches.\n")
	return b.String()
}

func formatBlastRadius(files []string) string {
	if len(files) == 0 {
		return "No blast radius constraint. Modify any files needed to complete the task."
	}
	var b strings.Builder
	b.WriteString("You SHOULD only modify these files:\n")
	for _, f := range files {
		b.WriteString("- ")
		b.WriteString(f)
		b.WriteString("\n")
	}
	b.WriteString("\nIf you need to modify files outside this list, do so, but be aware this may\ntrigger a validation warning.")
	return b.String()
}

func formatAdditionalContext(srcs []ContextSourceResult) string {
	if len(srcs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range srcs {
		b.WriteString("### ")
		b.WriteString(s.Label)
		b.WriteString("\n\n")
		b.WriteString(s.Content)
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// TruncateLines limits text for retry/replan prompts so gate output cannot exhaust the model context window.
func TruncateLines(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	headSize := (maxChars + 1) / 2
	tailSize := maxChars - headSize

	headEnd := headSize
	if headEnd > len(s) {
		headEnd = len(s)
	}
	if lastNL := strings.LastIndex(s[:headEnd], "\n"); lastNL >= 0 {
		headEnd = lastNL + 1
	} else {
		headEnd = 0
	}

	tailStart := len(s) - tailSize
	if tailStart < 0 {
		tailStart = 0
	}
	if tailStart < len(s) {
		if idx := strings.IndexByte(s[tailStart:], '\n'); idx >= 0 {
			tailStart += idx + 1
		}
	}

	if headEnd >= tailStart {
		return truncateLinesGreedyLines(s, maxChars)
	}

	middle := s[headEnd:tailStart]
	nLines := strings.Count(middle, "\n")
	return s[:headEnd] + fmt.Sprintf("\n[... truncated %d lines ...]\n", nLines) + s[tailStart:]
}

// truncateLinesGreedyLines is used when head/tail windows overlap (e.g. one very long line); we still
// prefer whole lines from the start and end so the middle marker never cuts mid-line.
func truncateLinesGreedyLines(s string, maxChars int) string {
	lines := strings.Split(s, "\n")
	if len(lines) == 1 {
		return truncateSingleLineNoNewline(lines[0], maxChars)
	}
	markerNL := "\n[... truncated ...]\n"
	budget := maxChars - len(markerNL)
	if budget < 40 {
		return s[:maxChars]
	}
	headBudget := budget / 2
	tailBudget := budget - headBudget
	var headLines []string
	used := 0
	for i := 0; i < len(lines); i++ {
		add := len(lines[i])
		if i > 0 {
			add++
		}
		if used+add > headBudget && len(headLines) > 0 {
			break
		}
		headLines = append(headLines, lines[i])
		used += add
	}
	var tailLines []string
	used2 := 0
	for i := len(lines) - 1; i >= 0; i-- {
		add := len(lines[i])
		if i < len(lines)-1 {
			add++
		}
		if used2+add > tailBudget && len(tailLines) > 0 {
			break
		}
		tailLines = append([]string{lines[i]}, tailLines...)
		used2 += add
	}
	for len(headLines)+len(tailLines) > len(lines) {
		if len(headLines) >= len(tailLines) {
			headLines = headLines[:len(headLines)-1]
		} else {
			tailLines = tailLines[1:]
		}
	}
	midLines := len(lines) - len(headLines) - len(tailLines)
	headStr := strings.Join(headLines, "\n")
	tailStr := strings.Join(tailLines, "\n")
	if midLines == 0 {
		return strings.Join(append(headLines, tailLines...), "\n")
	}
	out := headStr + fmt.Sprintf("\n[... truncated %d lines ...]\n", midLines) + tailStr
	if len(out) <= maxChars {
		return out
	}
	// Budget exceeded (very long lines): drop lines from the longer side until it fits.
	for len(out) > maxChars && (len(headLines) > 0 || len(tailLines) > 0) {
		if len(headLines) >= len(tailLines) && len(headLines) > 0 {
			headLines = headLines[:len(headLines)-1]
		} else if len(tailLines) > 0 {
			tailLines = tailLines[1:]
		}
		for len(headLines)+len(tailLines) > len(lines) {
			if len(headLines) >= len(tailLines) && len(headLines) > 0 {
				headLines = headLines[:len(headLines)-1]
			} else if len(tailLines) > 0 {
				tailLines = tailLines[1:]
			} else {
				break
			}
		}
		midLines = len(lines) - len(headLines) - len(tailLines)
		headStr = strings.Join(headLines, "\n")
		tailStr = strings.Join(tailLines, "\n")
		if midLines == 0 {
			out = headStr + tailStr
		} else {
			out = headStr + fmt.Sprintf("\n[... truncated %d lines ...]\n", midLines) + tailStr
		}
	}
	if len(out) <= maxChars {
		return out
	}
	return s[:maxChars]
}

func truncateSingleLineNoNewline(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	head := maxChars / 2
	tail := maxChars - head - len("\n[... truncated ...]\n")
	if tail < 1 {
		tail = 1
	}
	if head+tail > len(s) {
		return s
	}
	return s[:head] + "\n[... truncated ...]\n" + s[len(s)-tail:]
}

// BuildOptions carries fields the engine will supply in M3; SessionReuse is wired today for explicit session reuse steps.
type BuildOptions struct {
	SessionReuse bool
}

// Build resolves recipe context sources, builds the v4 prompt, and writes the provider file in the worktree.
func Build(outputMode string, prompt string, contextSources []recipe.ContextSource, worktreeDir string, cfg *config.Config, taskFiles []string, vars map[string]string, contextFile string, retry *RetrySection, opts *BuildOptions) error {
	spec := ""
	if vars != nil {
		spec = vars["spec"]
	}
	maxErr, maxDiff := 2000, 3000
	if cfg != nil {
		if cfg.ErrorContextMaxErrorChars > 0 {
			maxErr = cfg.ErrorContextMaxErrorChars
		}
		if cfg.ErrorContextMaxDiffChars > 0 {
			maxDiff = cfg.ErrorContextMaxDiffChars
		}
	}

	srcResults, err := resolveContextSources(contextSources, worktreeDir, vars)
	if err != nil {
		return err
	}

	conventions := ""
	if data, err := os.ReadFile(filepath.Join(worktreeDir, brand.StateDir(), "conventions.md")); err == nil {
		conventions = string(data)
	}

	sessionReuse := false
	if opts != nil {
		sessionReuse = opts.SessionReuse
	}

	conv := ""
	if outputMode == "diff" {
		conv = conventions
	}
	cp := ContextParams{
		OutputMode:     outputMode,
		Prompt:         prompt,
		Spec:           spec,
		BlastRadius:    taskFiles,
		Conventions:    conv,
		ContextSources: srcResults,
		SessionReuse:   sessionReuse,
		MaxErrorChars:  maxErr,
		MaxDiffChars:   maxDiff,
	}
	if retry != nil {
		cp.IsRetry = true
		cp.Attempt = retry.Attempt
		cp.MaxAttempts = retry.MaxAttempts
		cp.Error = retry.Error
		cp.Diff = retry.Diff
		cp.ReviewComment = retry.ReviewComment
	}

	body := BuildAgentContext(cp)
	if contextFile == "" {
		contextFile = "CLAUDE.md"
	}
	return writeAgentContextFile(worktreeDir, contextFile, body)
}

// resolveContextSources materializes recipe context: files are required; bash failures are dropped with a log line
// so a flaky diagnostic command does not abort the whole cook.
func resolveContextSources(contextSources []recipe.ContextSource, worktreeDir string, vars map[string]string) ([]ContextSourceResult, error) {
	var out []ContextSourceResult
	for _, src := range contextSources {
		if src.File != "" {
			resolvedPath := template.Resolve(strings.TrimSpace(src.File), vars, nil, "")
			fullPath := filepath.Join(worktreeDir, resolvedPath)
			data, err := os.ReadFile(fullPath)
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("context file not found: %q", resolvedPath)
				}
				return nil, fmt.Errorf("read context file %s: %w", resolvedPath, err)
			}
			if len(data) > contextFileSizeWarnChars {
				estTokens := len(data) / 4
				log.Printf("context file %s is large (estimated ~%dk tokens), may consume significant context window", resolvedPath, estTokens/1000)
			}
			out = append(out, ContextSourceResult{Type: "file", Label: resolvedPath, Content: string(data)})
		}
		if src.Bash != "" {
			ctx, cancel := stdctx.WithTimeout(stdctx.Background(), bashContextTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh", "-c", src.Bash)
			cmd.Dir = worktreeDir
			outBytes, err := cmd.Output()
			if err != nil {
				log.Printf("context bash failed (non-fatal): %s: %v", src.Bash, err)
				continue
			}
			out = append(out, ContextSourceResult{Type: "bash", Label: src.Bash, Content: string(outBytes)})
		}
	}
	return out, nil
}

var agentBrandTitle = strings.ToUpper(brand.Lower()[:1]) + brand.Lower()[1:]
var agentInstructionsHeader = "# " + agentBrandTitle + " — Agent Instructions"

// writeAgentContextFile preserves a pre-existing user provider file once per run; Gump-generated
// content from a previous step must not be mistaken for the user’s original (restore would undo the last step).
func writeAgentContextFile(worktreeDir, contextFile, body string) error {
	path := filepath.Join(worktreeDir, contextFile)
	backupName := originalBackupByContextFile[contextFile]
	if backupName == "" {
		backupName = brand.StateDir() + "-original-" + contextFile
	}
	backupPath := filepath.Join(worktreeDir, backupName)
	if _, err := os.Stat(path); err == nil {
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			data, _ := os.ReadFile(path)
			if !strings.HasPrefix(strings.TrimSpace(string(data)), agentInstructionsHeader) {
				_ = os.WriteFile(backupPath, data, 0644)
			}
		}
	}
	return os.WriteFile(path, []byte(body), 0644)
}

// BuildReplan writes the replan agent file and returns the same body for the launch prompt.
func BuildReplan(worktreeDir, itemName, itemDesc, itemFiles, diff, errMsg string, contextFile string, maxErr, maxDiff int) (string, error) {
	if maxErr <= 0 {
		maxErr = 2000
	}
	if maxDiff <= 0 {
		maxDiff = 3000
	}
	p := ContextParams{
		IsReplan:      true,
		ItemName:      itemName,
		ItemDesc:      itemDesc,
		ItemFiles:     itemFiles,
		Diff:          diff,
		Error:         errMsg,
		MaxErrorChars: maxErr,
		MaxDiffChars:  maxDiff,
	}
	body := BuildAgentContext(p)
	if contextFile == "" {
		contextFile = "CLAUDE.md"
	}
	if err := writeAgentContextFile(worktreeDir, contextFile, body); err != nil {
		return "", err
	}
	return body, nil
}
