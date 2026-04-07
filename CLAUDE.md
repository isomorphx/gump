# Gump — Agent Instructions

You are executing a code step in a Gump workflow.

## Your task

Implement: Replace both panic() calls in resolveInSource (lines 178 and 184) with a log.Printf warning and return nil. Add 'log' to the import block. The warning message format: "statebag: ambiguous reference '%s' in scope '%s', returning empty (use fully-qualified path)". This ensures Get() returns "" for ambiguous references instead of crashing the process.
Files: internal/statebag/statebag.go


## Blast radius

You SHOULD only modify these files:
- internal/statebag/statebag.go

If you need to modify files outside this list, do so, but be aware this may
trigger a validation warning.

## Output expectations

Write code directly in the repository. Gump will capture your changes via git diff.
Do not write plan/artifact/review deliverables to the output tree; those modes use separate paths.

## Git rules

- Do NOT run `git commit`, `git add`, `git push`, or any git command.
- Do NOT switch branches.
- You are in a Gump worktree. Gump manages git.
