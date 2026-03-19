package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	conventionsPath = ".pudding/conventions.md"
)

// originalBackupByContextFile keeps provider-specific backups so cleanup restores the user's file, not another provider's.
var originalBackupByContextFile = map[string]string{
	"CLAUDE.md":  ".pudding-original-claude.md",
	"AGENTS.md":  ".pudding-original-agents.md",
	"GEMINI.md":  ".pudding-original-gemini.md",
	"QWEN.md":   ".pudding-original-qwen.md",
}

// AllProviderContextFiles lists every context file so we can remove others on cross-provider steps and restore all on cleanup.
var AllProviderContextFiles = []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", "QWEN.md"}

// WritePlanContext writes the context file (CLAUDE.md, AGENTS.md, or GEMINI.md) for a plan step so the agent produces plan-output.json.
func WritePlanContext(worktreeDir, spec, contextFile string) error {
	return writeWithBackup(worktreeDir, planTemplate(spec), contextFile)
}

// WriteCodeContext writes the context file for a code step with blast radius and conventions.
func WriteCodeContext(worktreeDir, prompt string, taskFiles []string, contextFile string) error {
	return writeWithBackup(worktreeDir, codeTemplate(prompt, blastRadiusSection(taskFiles), conventionsSection(worktreeDir)), contextFile)
}

// WriteCodeRetryContext writes the context file for a retry attempt with previous diff and error.
func WriteCodeRetryContext(worktreeDir, prompt string, taskFiles []string, attempt, maxAttempts int, diff, errMsg string, contextFile string) error {
	body := codeRetryTemplate(prompt, attempt, maxAttempts, diff, errMsg, blastRadiusSection(taskFiles), conventionsSection(worktreeDir))
	return writeWithBackup(worktreeDir, body, contextFile)
}

// WriteReplanContext writes the context file for a replan step after a failed implementation.
func WriteReplanContext(worktreeDir, taskName, taskDesc string, taskFiles []string, diff, errMsg string, contextFile string) error {
	return writeWithBackup(worktreeDir, replanTemplate(taskName, taskDesc, taskFiles, diff, errMsg), contextFile)
}

func writeWithBackup(worktreeDir, content, contextFile string) error {
	path := filepath.Join(worktreeDir, contextFile)
	backupName := originalBackupByContextFile[contextFile]
	if backupName == "" {
		backupName = ".pudding-original-" + contextFile
	}
	backupPath := filepath.Join(worktreeDir, backupName)
	if _, err := os.Stat(path); err == nil {
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			data, _ := os.ReadFile(path)
			_ = os.WriteFile(backupPath, data, 0644)
		}
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// RemoveOtherContextFiles deletes CLAUDE.md, AGENTS.md, GEMINI.md except keep, so the next step's CLI only sees its own context file.
func RemoveOtherContextFiles(worktreeDir, keep string) {
	for _, name := range AllProviderContextFiles {
		if name == keep {
			continue
		}
		path := filepath.Join(worktreeDir, name)
		_ = os.Remove(path)
	}
}

// RestoreAllContextFiles restores any backed-up context file so the worktree ends with the user's originals after cook.
func RestoreAllContextFiles(worktreeDir string) {
	for contextFile, backupName := range originalBackupByContextFile {
		backupPath := filepath.Join(worktreeDir, backupName)
		data, err := os.ReadFile(backupPath)
		if err != nil {
			continue
		}
		path := filepath.Join(worktreeDir, contextFile)
		_ = os.WriteFile(path, data, 0644)
		_ = os.Remove(backupPath)
	}
}

func blastRadiusSection(files []string) string {
	if len(files) == 0 {
		return "No blast radius constraint. Modify any files needed to complete the task."
	}
	var b strings.Builder
	b.WriteString("You SHOULD only modify these files:\n")
	for _, f := range files {
		b.WriteString(f)
		b.WriteString("\n")
	}
	b.WriteString("\nIf you need to modify files outside this list, do so, but be aware this may trigger a validation warning.")
	return b.String()
}

func conventionsSection(worktreeDir string) string {
	p := filepath.Join(worktreeDir, conventionsPath)
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(data)
}

func planTemplate(spec string) string {
	return `# Pudding — Agent Instructions

You are executing a plan step in a Pudding workflow.

## Your task

Analyze the specification below and the codebase. Decompose the work into independent tasks.

## Output format

You MUST create a file called ` + "`plan-output.json`" + ` at the root of this repository.
The file MUST contain a JSON array of tasks with this exact schema:

` + "```json" + `
[
  {
    "name": "short-kebab-case-name",
    "description": "What this task accomplishes. Be specific and actionable.",
    "files": ["path/to/file1.go", "path/to/file2.go"]
  }
]
` + "```" + `

Rules for the plan:
- Each task must be independently implementable and testable.
- ` + "`files`" + ` is the blast radius: list every file that will be created, modified, or deleted.
- ` + "`files`" + ` supports globs (e.g., ` + "`internal/auth/*_test.go`" + `).
- If you cannot determine the blast radius, omit the ` + "`files`" + ` field for that task.
- Order tasks by dependency: if task B depends on task A's output, A comes first.
- Do NOT implement any code. Only produce the plan.

## Git rules

- Do NOT run ` + "`git commit`" + `, ` + "`git add`" + `, ` + "`git push`" + `, or any git command.
- Do NOT switch branches.
- You are in a Pudding worktree. Pudding manages git.

## Specification

` + spec + "\n"
}

func codeTemplate(prompt, blastRadius, conventions string) string {
	s := `# Pudding — Agent Instructions

You are executing a code step in a Pudding workflow.

## Your task

` + prompt + `

## Blast radius

` + blastRadius + `

## Git rules

- Do NOT run ` + "`git commit`" + `, ` + "`git add`" + `, ` + "`git push`" + `, or any git command.
- Do NOT switch branches.
- You are in a Pudding worktree. Pudding manages git.
`
	if conventions != "" {
		s += "\n## Project conventions\n\n" + conventions + "\n"
	}
	return s
}

func codeRetryTemplate(prompt string, attempt, maxAttempts int, diff, errMsg, blastRadius, conventions string) string {
	s := `# Pudding — Agent Instructions

You are executing a code step in a Pudding workflow.
This is retry attempt ` + fmt.Sprintf("%d", attempt) + ` of ` + fmt.Sprintf("%d", maxAttempts) + `.

## Your task

` + prompt + `

## Previous attempt failed

The previous attempt produced this diff:

` + "```diff" + "\n" + diff + "\n" + "```" + `

The validation failed with this error:

` + "```" + "\n" + errMsg + "\n" + "```" + `

Analyze the error, understand what went wrong, and try a different approach.

## Blast radius

` + blastRadius + `

## Git rules

- Do NOT run ` + "`git commit`" + `, ` + "`git add`" + `, ` + "`git push`" + `, or any git command.
- Do NOT switch branches.
- You are in a Pudding worktree. Pudding manages git.
`
	if conventions != "" {
		s += "\n## Project conventions\n\n" + conventions + "\n"
	}
	return s
}

func replanTemplate(taskName, taskDesc string, taskFiles []string, diff, errMsg string) string {
	filesStr := strings.Join(taskFiles, ", ")
	return `# Pudding — Agent Instructions

You are re-planning a task that failed during implementation.

## Original task

Name: ` + taskName + `
Description: ` + taskDesc + `
Files: ` + filesStr + `

## What failed

The implementation attempt produced this diff:

` + "```diff" + "\n" + diff + "\n" + "```" + `

The validation failed with this error:

` + "```" + "\n" + errMsg + "\n" + "```" + `

## Your job

Re-decompose this task. You can:
1. Break it into smaller sub-tasks with more precise blast radii.
2. Propose a different approach entirely.
3. Implement it directly if the decomposition is unnecessary.

## Output format

Same as a plan step. Create ` + "`plan-output.json`" + ` at the root with the schema:

` + "```json" + `
[
  {
    "name": "short-kebab-case-name",
    "description": "What this task accomplishes.",
    "files": ["path/to/file.go"]
  }
]
` + "```" + `

## Git rules

- Do NOT run ` + "`git commit`" + `, ` + "`git add`" + `, ` + "`git push`" + `, or any git command.
- Do NOT switch branches.
` + "\n"
}
