# Gump — Agent Instructions

You are executing a code step in a Gump workflow.

## Your task

Implement: Add two test cases to statebag_test.go: (1) TestStateBag_AmbiguousReference — set up entries at group1/arbiter and group2/arbiter, call Get("arbiter", "group1/reviews", "output"), assert it returns "" without panicking. (2) TestStateBag_AmbiguousReference_FullyQualifiedWorks — same setup, call Get("group1/arbiter", "", "output"), assert it returns the correct value from group1/arbiter.
Files: internal/statebag/statebag_test.go
## Arbiter Decision

Both reviews pass. The core implementation is correct and complete. Two minor follow-ups should be addressed:

### Integrate

1. **Update documentation comments that still reference panics.**
   Both reviewers flagged this. The function comment on `resolveInSource` (around line 154) still says "panics if ambiguous." Update it to reflect the new behavior: logs a warning and returns nil. Check for any other doc comments in `internal/statebag/statebag.go` that mention panic behavior and update them to match.

### Ignore

1. **Adding tests for the new behavior.** The security review noted no test was added. This is out of scope for this task — the spec requested replacing panics with warnings, not adding test coverage. Tests can be added in a follow-up if desired.

### Summary of instructions for the implementer

- In `internal/statebag/statebag.go`, find all comments that mention "panic" or "panics" in relation to ambiguous references and update them to say the function logs a warning and returns nil instead.
- Do not change any logic or behavior — only update comments/documentation to match the implemented behavior.
- Do not add tests in this change.



## Previous attempt failed

This is retry attempt 2 of 3.

The previous attempt produced this diff:

```diff
diff --git a/CLAUDE.md b/CLAUDE.md
index 9ce4876..bc0d841 100644
--- a/CLAUDE.md
+++ b/CLAUDE.md
@@ -1,28 +1,44 @@
 # Gump — Agent Instructions
 
-You are executing an artifact step in a Gump workflow.
+You are executing a code step in a Gump workflow.
 
 ## Your task
 
-Analyze both reviews. Decide what to integrate and what to ignore.
-Arch review: {
-  "pass": true,
-  "comment": "The change correctly replaces both panic() calls with log.Printf + return nil, matching the spec exactly. The 'log' import is properly added. The warning message format matches the spec. The nil return propagates correctly through Get() to return \"\". One minor nit: the function comment on line 154 still says 'panics if ambiguous' but this is cosmetic and does not affect correctness. No other panics remain in statebag.go. Blast radius is minimal — only statebag.go was touched as expected."
-}
+Implement: Add two test cases to statebag_test.go: (1) TestStateBag_AmbiguousReference — set up entries at group1/arbiter and group2/arbiter, call Get("arbiter", "group1/reviews", "output"), assert it returns "" without panicking. (2) TestStateBag_AmbiguousReference_FullyQualifiedWorks — same setup, call Get("group1/arbiter", "", "output"), assert it returns the correct value from group1/arbiter.
+Files: internal/statebag/statebag_test.go
+## Arbiter Decision
 
-Security review: {
-  "pass": true,

[... truncated 38 lines ...]
 
 ## Git rules
 
diff --git a/internal/statebag/statebag.go b/internal/statebag/statebag.go
index d46a004..41ae12e 100644
--- a/internal/statebag/statebag.go
+++ b/internal/statebag/statebag.go
@@ -96,7 +96,7 @@ func (sb *StateBag) Set(fullPath string, value string, diff string, files []stri
 }
 
 // Get resolves {steps.<shortName>.output} or {steps.<shortName>.diff} using scope proximity:
-// current entries first (precedence over prev), then parent-by-parent scope chain. First match wins; multiple matches at same scope panic.
+// current entries first (precedence over prev), then parent-by-parent scope chain. First match wins; multiple matches at same scope log a warning and return "".
 func (sb *StateBag) Get(shortName string, scopePath string, field string) string {
 	sb.mu.RLock()
 	defer sb.mu.RUnlock()
@@ -151,7 +151,7 @@ func buildScopeChain(scopePath string) []string {
 	return out
 }
 
-// resolveInSource finds the entry for shortName in a single source map by walking the scope chain (closest scope first). Returns nil if none; panics if ambiguous.
+// resolveInSource finds the entry for shortName in a single source map by walking the scope chain (closest scope first). Returns nil if none or ambiguous; logs a warning if ambiguous.
 func resolveInSource(source map[string]Entry, shortName string, scopePath string) *Entry {
 	shortNameSlash := strings.ReplaceAll(strings.ReplaceAll(shortName, ".steps.", "/"), ".", "/")
 	var candidates []Entry

```

The validation failed with this error:

```
blast radius violation: files modified outside task.files scope:
  - internal/statebag/statebag.go (not in: [internal/statebag/statebag_test.go])
Allowed: internal/statebag/statebag_test.go
```

Analyze the error, understand what went wrong, and try a different approach. Do NOT repeat the same mistake.

## Blast radius

You SHOULD only modify these files:
- internal/statebag/statebag_test.go

If you need to modify files outside this list, do so, but be aware this may
trigger a validation warning.

## Output expectations

Write code directly in the repository. Gump will capture your changes via git diff.
Do not write plan/artifact/review deliverables to the output tree; those modes use separate paths.

## Git rules

- Do NOT run `git commit`, `git add`, `git push`, or any git command.
- Do NOT switch branches.
- You are in a Gump worktree. Gump manages git.
