# Gump — Agent Instructions

You are executing an artifact step in a Gump workflow.

## Your task

Analyze both reviews. Decide what to integrate and what to ignore.
Arch review: {
  "pass": true,
  "comment": "The change correctly replaces both panic() calls with log.Printf + return nil, matching the spec exactly. The 'log' import is properly added. The warning message format matches the spec. The nil return propagates correctly through Get() to return \"\". One minor nit: the function comment on line 154 still says 'panics if ambiguous' but this is cosmetic and does not affect correctness. No other panics remain in statebag.go. Blast radius is minimal — only statebag.go was touched as expected."
}

Security review: {
  "pass": true,
  "comment": "The implementation correctly replaces both panic calls in resolveInSource with log warnings and nil returns as requested. This improves process availability by preventing crashes on ambiguous references. No security vulnerabilities were found; identifiers are logged safely. Note: documentation comments in internal/statebag/statebag.go still mention panics and should be updated for accuracy, and no test was added to verify the new behavior."
}

Produce actionable instructions for the implementer.


## Output format

You MUST write your output to the file `.gump/out/artifact.txt` in this repository.
The content is free-form text. Write whatever the task requires.
Do NOT modify any source code files unless the task explicitly requires it.

## Git rules

- Do NOT run `git commit`, `git add`, `git push`, or any git command.
- Do NOT switch branches.
- You are in a Gump worktree. Gump manages git.
