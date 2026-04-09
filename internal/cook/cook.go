package cook

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/isomorphx/gump/internal/diff"
	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/ledger"
	"github.com/isomorphx/gump/internal/workflow"
	"github.com/isomorphx/gump/internal/sandbox"
)

// Cook represents one full run of a recipe; worktree and branch are isolated so the dev repo stays clean.
// Ledger is the event log for this cook; it is created in NewCook and must be Closed by the caller when done.
type Cook struct {
	ID            string
	Recipe        *workflow.Workflow
	RecipeName    string   // recipe name for merge message (set from Recipe.Name or context when loading)
	SpecPath      string
	SpecContent   string
	RepoRoot      string
	OrigBranch    string
	InitialCommit string
	WorktreeDir   string
	BranchName    string
	CookDir       string
	Status        string
	StartedAt     time.Time
	Ledger        *ledger.Ledger
}

// NewCook creates a new cook: checks repo state, creates worktree and branch, and persists context snapshot.
// recipeRaw is the YAML bytes (e.g. resolved.Raw) to store as workflow-snapshot.yaml.
func NewCook(rec *workflow.Workflow, specPath string, repoRoot string, recipeRaw []byte) (*Cook, error) {
	root, err := sandbox.GitRepoRoot(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("gump run must be executed inside a git repository")
	}
	dirty, err := sandbox.HasUncommittedChanges(root)
	if err != nil {
		return nil, err
	}
	if dirty {
		return nil, fmt.Errorf("working directory has uncommitted changes — commit or stash before running gump run")
	}
	origBranch, err := sandbox.CurrentBranch(root)
	if err != nil {
		return nil, err
	}
	initialCommit, err := sandbox.HeadCommit(root)
	if err != nil {
		return nil, err
	}
	id := uuid.New().String()
	worktreeDir := filepath.Join(root, brand.StateDir(), "worktrees", brand.WorktreeDirPrefix()+id)
	branchName := brand.WorktreeBranchPrefix() + id
	if err := sandbox.CreateWorktree(root, worktreeDir, branchName); err != nil {
		return nil, err
	}
	cookDir := filepath.Join(root, brand.StateDir(), brand.RunsDir(), id)
	if err := EnsureCookDir(cookDir); err != nil {
		_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		return nil, err
	}
	specContent, err := os.ReadFile(specPath)
	if err != nil {
		_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		return nil, err
	}
	if len(recipeRaw) > 0 {
		_ = WriteRecipeSnapshot(cookDir, recipeRaw)
	}
	ctx := &ContextSnapshot{
		CookID:          id,
		Recipe:          rec.Name,
		Spec:            filepath.Base(specPath),
		RepoRoot:        root,
		Branch:          origBranch,
		InitialCommit:   initialCommit,
		Timestamp:       time.Now().UTC().Format(time.RFC3339),
		LockfileHashes:  LockfileHashesForDir(worktreeDir),
		RuntimeVersions: RuntimeVersionsForDir(worktreeDir),
	}
	if err := WriteContextSnapshot(cookDir, ctx); err != nil {
		_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		return nil, err
	}
	if err := WriteStatus(cookDir, "running"); err != nil {
		_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		return nil, err
	}
	if err := ensureGitignoreStateDir(worktreeDir); err != nil {
		_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		return nil, err
	}
	led, err := ledger.New(cookDir, id)
	if err != nil {
		_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		return nil, err
	}
	c := &Cook{
		ID:            id,
		Recipe:        rec,
		RecipeName:    rec.Name,
		SpecPath:      specPath,
		SpecContent:   string(specContent),
		RepoRoot:      root,
		OrigBranch:    origBranch,
		InitialCommit: initialCommit,
		WorktreeDir:   worktreeDir,
		BranchName:    branchName,
		CookDir:       cookDir,
		Status:        "running",
		StartedAt:     time.Now(),
		Ledger:        led,
	}
	return c, nil
}

// NewCookForReplay creates a new cook for replay. If originalWorktreeDir is non-empty (HITL), that worktree is reused as-is; otherwise a new worktree is created at restoreCommit.
func NewCookForReplay(rec *workflow.Workflow, specPath string, repoRoot string, recipeRaw []byte, restoreCommit string, originalWorktreeDir string) (*Cook, error) {
	root, err := sandbox.GitRepoRoot(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("gump run must be executed inside a git repository")
	}
	origBranch, err := sandbox.CurrentBranch(root)
	if err != nil {
		origBranch = "main"
	}
	id := uuid.New().String()
	var worktreeDir, branchName string
	if originalWorktreeDir != "" {
		worktreeDir = originalWorktreeDir
		branchName, _ = sandbox.CurrentBranch(worktreeDir)
		if branchName == "" {
			branchName = brand.WorktreeBranchPrefix() + id
		}
	} else {
		worktreeDir = filepath.Join(root, brand.StateDir(), "worktrees", brand.WorktreeDirPrefix()+id)
		branchName = brand.WorktreeBranchPrefix() + id
		if err := sandbox.CreateWorktreeAtCommit(root, worktreeDir, branchName, restoreCommit); err != nil {
			return nil, err
		}
	}
	cookDir := filepath.Join(root, brand.StateDir(), brand.RunsDir(), id)
	if err := EnsureCookDir(cookDir); err != nil {
		if originalWorktreeDir == "" {
			_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		}
		return nil, err
	}
	specContent, err := os.ReadFile(specPath)
	if err != nil {
		if originalWorktreeDir == "" {
			_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		}
		return nil, err
	}
	if len(recipeRaw) > 0 {
		_ = WriteRecipeSnapshot(cookDir, recipeRaw)
	}
	if err := WriteStatus(cookDir, "running"); err != nil {
		if originalWorktreeDir == "" {
			_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		}
		return nil, err
	}
	if err := ensureGitignoreStateDir(worktreeDir); err != nil {
		if originalWorktreeDir == "" {
			_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		}
		return nil, err
	}
	led, err := ledger.New(cookDir, id)
	if err != nil {
		if originalWorktreeDir == "" {
			_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		}
		return nil, err
	}
	c := &Cook{
		ID:            id,
		Recipe:        rec,
		RecipeName:    rec.Name,
		SpecPath:      specPath,
		SpecContent:   string(specContent),
		RepoRoot:      root,
		OrigBranch:    origBranch,
		InitialCommit: restoreCommit,
		WorktreeDir:   worktreeDir,
		BranchName:    branchName,
		CookDir:       cookDir,
		Status:        "running",
		StartedAt:     time.Now(),
		Ledger:        led,
	}
	return c, nil
}

// Snapshot commits the current worktree state with a structured message and returns the diff contract.
func (c *Cook) Snapshot(stepName, taskName string, attempt int) (*diff.DiffContract, error) {
	return sandbox.Snapshot(c.WorktreeDir, stepName, taskName, attempt)
}

// ResetTo resets the worktree to a previous commit for retry or replay.
func (c *Cook) ResetTo(commitHash string) error {
	return sandbox.ResetTo(c.WorktreeDir, commitHash)
}

// FinalDiff returns the total diff between initial commit and current worktree HEAD for review/apply.
func (c *Cook) FinalDiff() (*diff.DiffContract, error) {
	return sandbox.FinalDiff(c.WorktreeDir, c.InitialCommit)
}

// Teardown removes the worktree and branch; cook dir is kept for ledger and artifacts.
func (c *Cook) Teardown() error {
	return sandbox.RemoveWorktree(c.RepoRoot, c.WorktreeDir, c.BranchName)
}

// CloneForWorktree returns a copy of the cook with a different worktree and branch so parallel steps can run in isolated worktrees sharing the same ledger and cook dir.
func (c *Cook) CloneForWorktree(worktreeDir, branchName string) *Cook {
	clone := *c
	clone.WorktreeDir = worktreeDir
	clone.BranchName = branchName
	return &clone
}

// ensureGitignoreStateDir adds the runtime state dir to worktree .gitignore
// so the provider output dir is not snapshotted, then commits.
func ensureGitignoreStateDir(worktreeDir string) error {
	gitignorePath := filepath.Join(worktreeDir, ".gitignore")
	needStateDir := true
	var data []byte
	var err error
	if data, err = os.ReadFile(gitignorePath); err == nil {
		if bytes.Contains(data, []byte(brand.StateDir())) {
			needStateDir = false
		}
	}
	if needStateDir {
		var out []byte
		if len(data) > 0 {
			out = data
			if !strings.HasSuffix(strings.TrimSpace(string(data)), "\n") {
				out = append(out, '\n')
			}
			out = append(out, brand.StateDir()+"/\n"...)
		} else {
			out = []byte(brand.StateDir() + "/\n")
		}
		if err := os.WriteFile(gitignorePath, out, 0644); err != nil {
			return err
		}
		env := append(os.Environ(),
			"GIT_AUTHOR_NAME="+brand.Lower(),
			"GIT_AUTHOR_EMAIL="+brand.Lower()+"@local",
			"GIT_CONFIG_GLOBAL=/dev/null",
		)
		add := exec.Command("git", "add", ".gitignore")
		add.Dir = worktreeDir
		add.Env = env
		if out, err := add.CombinedOutput(); err != nil {
			return fmt.Errorf("git add .gitignore: %w %s", err, out)
		}
		commit := exec.Command("git", "commit", "-m", "["+brand.Lower()+"] gitignore setup")
		commit.Dir = worktreeDir
		commit.Env = env
		if out, err := commit.CombinedOutput(); err != nil {
			return fmt.Errorf("git commit gitignore: %w %s", err, out)
		}
	}
	return nil
}

// Apply merges the cook branch into the current branch of the main repo and runs teardown on success.
// The trailer is brand-aware (e.g. Gump-Run:) so merge commits stay traceable after rebranding.
func (c *Cook) Apply() error {
	dirty, err := sandbox.HasUncommittedChanges(c.RepoRoot)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("working directory has uncommitted changes — commit or stash before applying")
	}
	currentBranch, err := sandbox.CurrentBranch(c.RepoRoot)
	if err != nil {
		return err
	}
	if currentBranch != c.OrigBranch {
		// Warning only; merge may still work.
		fmt.Fprintf(os.Stderr, "warning: you are on branch %s, run was started on %s — merge may produce unexpected results\n", currentBranch, c.OrigBranch)
	}
	stepsCount := 0
	if st, err := ReadStatus(c.CookDir); err == nil {
		stepsCount = st.StepsCount
	}
	titlePrefix := "Gump run"
	idLabel := "Run-ID"
	msg := fmt.Sprintf("%s: %s\n\nSpec: %s\n%s: %s\nSteps: %d\n\n%s %s",
		titlePrefix, c.RecipeName, filepath.Base(c.SpecPath), idLabel, c.ID, stepsCount, brand.MergeTrailer(), c.ID)
	cmd := exec.Command("git", "merge", c.BranchName, "--no-ff", "-m", msg)
	cmd.Dir = c.RepoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() != 0 {
			fmt.Fprintf(os.Stderr, "Merge conflict — resolve conflicts and run 'git commit' to complete.\n")
			return fmt.Errorf("merge failed: %s", out)
		}
		return err
	}
	dc, _ := c.FinalDiff()
	n := 0
	if dc != nil {
		n = len(dc.FilesChanged)
	}
	if err := c.Teardown(); err != nil {
		return err
	}
	fmt.Printf("Run %s applied successfully. %d files changed.\n", c.ID, n)
	return nil
}
