package context

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/isomorphx/pudding/internal/config"
	"github.com/isomorphx/pudding/internal/recipe"
	"github.com/isomorphx/pudding/internal/template"
)

const contextFileSizeWarnChars = 400_000

var (
	headerPudding = `# Pudding Agent Context

You are being orchestrated by Pudding, a workflow runtime for coding agents.
Follow these rules strictly:

- Do NOT run ` + "`git commit`" + `, ` + "`git add`" + `, ` + "`git push`" + `, or any git command that modifies the repository state. Pudding manages git.
- Do NOT switch branches. You are in an isolated worktree.
- Do NOT modify files outside the worktree.
- Write clean, working code. Pudding will validate your work after you finish.
`

	sectionPlan = `
## Your Task: Planning

[PUDDING:plan]

You must produce a structured plan as a JSON array of tasks.
Write the plan to a file named ` + "`.pudding/out/plan.json`" + `.

Each task must have:
- "name": a short kebab-case identifier (e.g., "add-auth-middleware")
- "description": a clear description of what to implement
- "files": (optional) list of file paths this task should touch

Example output format:
[
  {
    "name": "add-auth-middleware",
    "description": "Create JWT authentication middleware in pkg/auth/",
    "files": ["pkg/auth/middleware.go", "pkg/auth/middleware_test.go"]
  }
]

Do not include any text before or after the JSON array in .pudding/out/plan.json.
`

	sectionArtifact = `
## Your Task: Analysis

[PUDDING:artifact]

You must produce a textual output (analysis, review, decision, specification).
Write your output to a file named ` + "`.pudding/out/artifact.txt`" + `.

Write the full content of your analysis in this file. Do not write code unless specifically asked.
`

	sectionDiff = `
## Your Task: Implementation

Write code to accomplish the task described below.
You may create, modify, or delete files as needed.
`
)

// Build writes the provider context file (CLAUDE.md, AGENTS.md, etc.) so the agent sees rules, conventions, blast radius, prompt.
// When retry is non-nil (attempt > 1), the "Previous Attempt Failed" section is inserted so the agent can fix validation failures.
func Build(outputMode string, prompt string, contextSources []recipe.ContextSource, worktreeDir string, cfg *config.Config, taskFiles []string, vars map[string]string, contextFile string, retry *RetrySection) error {
	var b strings.Builder
	b.WriteString(headerPudding)

	switch outputMode {
	case "plan":
		b.WriteString(sectionPlan)
	case "artifact":
		b.WriteString(sectionArtifact)
	case "diff", "":
		b.WriteString(sectionDiff)
	}

	if retry != nil {
		b.WriteString("\n## Previous Attempt Failed (Attempt ")
		b.WriteString(fmt.Sprintf("%d", retry.Attempt))
		b.WriteString("/")
		b.WriteString(fmt.Sprintf("%d", retry.MaxAttempts))
		b.WriteString(")\n\nYour previous attempt to complete this task failed validation.\n\n### What you produced\n\n```diff\n")
		b.WriteString(retry.Diff)
		b.WriteString("\n```\n\n### Why it failed\n\n```\n")
		b.WriteString(retry.Error)
		b.WriteString("\n```\n\n### Instructions\n\n- Read the error carefully before starting\n- Do NOT repeat the same approach — the previous diff shows what did not work\n- Focus specifically on addressing the validation failures\n- You have ")
		b.WriteString(fmt.Sprintf("%d", retry.Remaining))
		b.WriteString(" attempts remaining before this task is marked as failed\n")
		if retry.EscalateTo != "" && retry.EscalateFrom != "" {
			b.WriteString("\nNote: You are a more powerful agent (escalated from ")
			b.WriteString(retry.EscalateFrom)
			b.WriteString(" to ")
			b.WriteString(retry.EscalateTo)
			b.WriteString(").\nThe previous agent was unable to solve this. Use your stronger capabilities.\n")
		}
		b.WriteString("\n")
	}

	if len(taskFiles) > 0 {
		b.WriteString("\n## Blast Radius\n\nThe following files are the expected scope of your changes:\n")
		for _, f := range taskFiles {
			b.WriteString("- ")
			b.WriteString(f)
			b.WriteString("\n")
		}
		b.WriteString("\nStay within this scope unless absolutely necessary. If you need to touch other files, proceed but be aware that validation may check for unexpected changes.\n")
	}

	conventionsPath := filepath.Join(worktreeDir, ".pudding", "conventions.md")
	if data, err := os.ReadFile(conventionsPath); err == nil {
		b.WriteString("\n## Project Conventions\n\n")
		b.Write(data)
		b.WriteString("\n")
	}

	if cfg != nil && (cfg.CompileCmd != "" || cfg.TestCmd != "" || cfg.LintCmd != "") {
		b.WriteString("\n## Validation Commands\n\nAfter your work, Pudding will run these commands to validate:\n")
		if cfg.CompileCmd != "" {
			b.WriteString("- Compile: `" + cfg.CompileCmd + "`\n")
		}
		if cfg.TestCmd != "" {
			b.WriteString("- Test: `" + cfg.TestCmd + "`\n")
		}
		if cfg.LintCmd != "" {
			b.WriteString("- Lint: `" + cfg.LintCmd + "`\n")
		}
		b.WriteString("\nMake sure your code passes these checks.\n")
	}

	// Context files (spec: ## Context Files then ### path per file; error if file missing, warn if >400k chars).
	if len(contextSources) > 0 {
		hasFile := false
		for _, src := range contextSources {
			if src.File != "" {
				hasFile = true
				break
			}
		}
		if hasFile {
			b.WriteString("\n## Context Files\n\n")
		}
		for _, src := range contextSources {
			if src.File != "" {
				resolvedPath := template.Resolve(strings.TrimSpace(src.File), vars, nil, "")
				fullPath := filepath.Join(worktreeDir, resolvedPath)
				data, err := os.ReadFile(fullPath)
				if err != nil {
					if os.IsNotExist(err) {
						return fmt.Errorf("context file not found: %s", resolvedPath)
					}
					return fmt.Errorf("read context file %s: %w", resolvedPath, err)
				}
				if len(data) > contextFileSizeWarnChars {
					estTokens := len(data) / 4
					log.Printf("context file %s is large (estimated ~%dk tokens), may consume significant context window", resolvedPath, estTokens/1000)
				}
				b.WriteString("### ")
				b.WriteString(resolvedPath)
				b.WriteString("\n\n")
				b.Write(data)
				b.WriteString("\n\n")
			}
			if src.Bash != "" {
				cmd := exec.Command("sh", "-c", src.Bash)
				cmd.Dir = worktreeDir
				out, err := cmd.Output()
				if err != nil {
					continue
				}
				b.WriteString("\n## Context: `")
				b.WriteString(src.Bash)
				b.WriteString("`\n\n")
				b.Write(out)
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("\n## Your task\n\n")
	b.WriteString(prompt)
	b.WriteString("\n")

	if contextFile == "" {
		contextFile = "CLAUDE.md"
	}
	outPath := filepath.Join(worktreeDir, contextFile)
	if err := os.WriteFile(outPath, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write %s: %w", contextFile, err)
	}
	return nil
}

// RetrySection is passed to Build when attempt > 1 so the agent sees why the previous attempt failed.
type RetrySection struct {
	Attempt      int
	MaxAttempts  int
	Diff         string
	Error        string
	Remaining    int
	EscalateFrom string
	EscalateTo   string
}

// BuildReplan writes the context file for the replan agent (re-decompose after failure) and returns the body for the prompt.
func BuildReplan(worktreeDir, originalPrompt, diff, errMsg, contextFile string) (string, error) {
	body := `# Pudding Agent Context

You are being orchestrated by Pudding, a workflow runtime for coding agents.
Follow these rules strictly:

- Do NOT run ` + "`git commit`" + `, ` + "`git add`" + `, ` + "`git push`" + `, or any git command that modifies the repository state. Pudding manages git.
- Do NOT switch branches. You are in an isolated worktree.
- Do NOT modify files outside the worktree.
- Write clean, working code. Pudding will validate your work after you finish.

## Your Task: Re-planning

[PUDDING:plan]

A previous attempt to complete the following task has failed.
Your job is to re-decompose this task into smaller, more achievable sub-tasks.

### Original Task

` + originalPrompt + `

### Previous Attempt — What Failed

The agent produced the following changes:

` + "```diff" + "\n" + diff + "\n" + "```" + `

But validation failed with:

` + "```" + "\n" + errMsg + "\n" + "```" + `

### Instructions

Analyze why the previous attempt failed. Then produce a new plan that either:
- Breaks the task into smaller, independent sub-tasks
- Takes a different implementation approach
- Addresses the specific validation failures

Write the plan to .pudding/out/plan.json.

Each task must have:
- "name": a short kebab-case identifier
- "description": a clear description of what to implement
- "files": (optional) list of file paths

Do not include any text before or after the JSON array in .pudding/out/plan.json.
`
	if contextFile == "" {
		contextFile = "CLAUDE.md"
	}
	outPath := filepath.Join(worktreeDir, contextFile)
	if err := os.WriteFile(outPath, []byte(body), 0644); err != nil {
		return "", fmt.Errorf("write %s: %w", contextFile, err)
	}
	return body, nil
}
