package brand

// Name returns the product branding string for logs and merge trailers.
// WHY: v0.0.4 standardizes on Gump; dual-binary naming was only for a short migration window.
func Name() string {
	return Lower()
}

func Lower() string { return "gump" }
func Upper() string { return "GUMP" }

// StateDir is the dot-directory for runs, worktrees, and engine state under the repo.
func StateDir() string { return ".gump" }

func RunsDir() string { return "runs" }

func WorktreeBranchPrefix() string { return "gump/run-" }

func WorktreeDirPrefix() string { return "run-" }

func MergeTrailer() string { return "Gump-Run:" }
