package run

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
	"github.com/isomorphx/gump/internal/config"
	"github.com/isomorphx/gump/internal/ledger"
	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/workflow"
	"github.com/isomorphx/gump/internal/sandbox"
)

// Run is one workflow execution: isolated worktree, persisted state, and append-only ledger.
// WHY: v0.0.4 ties durable state to the run directory so retries and reports read one coherent snapshot.
type Run struct {
	ID            string
	Workflow      *workflow.Workflow
	WorkflowName  string
	SpecPath      string
	SpecContent   string
	RepoRoot      string
	OrigBranch    string
	InitialCommit string
	BaseCommit    string
	WorktreeDir   string
	BranchName    string
	RunDir        string
	Status        string
	StartedAt     time.Time
	Ledger        *ledger.Ledger
	State         *state.State
	Config        *config.Config
}

// NewRun creates a worktree, run directory, initial state, and ledger; fails if the repo is dirty or not a git checkout.
func NewRun(rec *workflow.Workflow, specPath string, repoRoot string, workflowRaw []byte, cfg *config.Config) (*Run, error) {
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
	runDir := filepath.Join(root, brand.StateDir(), brand.RunsDir(), id)
	if err := EnsureRunDir(runDir); err != nil {
		_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		return nil, err
	}
	specContent, err := os.ReadFile(specPath)
	if err != nil {
		_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		return nil, err
	}
	if len(workflowRaw) > 0 {
		_ = WriteWorkflowSnapshot(runDir, workflowRaw)
	}
	ctx := &ContextSnapshot{
		RunID:           id,
		Workflow:        rec.Name,
		Spec:            filepath.Base(specPath),
		RepoRoot:        root,
		Branch:          origBranch,
		InitialCommit:   initialCommit,
		Timestamp:       time.Now().UTC().Format(time.RFC3339),
		LockfileHashes:  LockfileHashesForDir(worktreeDir),
		RuntimeVersions: RuntimeVersionsForDir(worktreeDir),
	}
	if err := WriteContextSnapshot(runDir, ctx); err != nil {
		_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		return nil, err
	}
	if err := WriteStatus(runDir, "running"); err != nil {
		_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		return nil, err
	}
	if err := ensureGitignoreStateDir(worktreeDir); err != nil {
		_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		return nil, err
	}
	led, err := ledger.New(runDir, id)
	if err != nil {
		_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		return nil, err
	}
	r := &Run{
		ID:            id,
		Workflow:      rec,
		WorkflowName:  rec.Name,
		SpecPath:      specPath,
		SpecContent:   string(specContent),
		RepoRoot:      root,
		OrigBranch:    origBranch,
		InitialCommit: initialCommit,
		BaseCommit:    initialCommit,
		WorktreeDir:   worktreeDir,
		BranchName:    branchName,
		RunDir:        runDir,
		Status:        "running",
		StartedAt:     time.Now(),
		Ledger:        led,
		State:         state.New(),
		Config:        cfg,
	}
	return r, nil
}

// NewRunForReplay creates a new run for replay or resume. If originalWorktreeDir is non-empty, that worktree is reused; otherwise a new worktree is created at restoreCommit.
func NewRunForReplay(rec *workflow.Workflow, specPath string, repoRoot string, workflowRaw []byte, restoreCommit string, originalWorktreeDir string, cfg *config.Config) (*Run, error) {
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
	runDir := filepath.Join(root, brand.StateDir(), brand.RunsDir(), id)
	if err := EnsureRunDir(runDir); err != nil {
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
	if len(workflowRaw) > 0 {
		_ = WriteWorkflowSnapshot(runDir, workflowRaw)
	}
	if err := WriteStatus(runDir, "running"); err != nil {
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
	led, err := ledger.New(runDir, id)
	if err != nil {
		if originalWorktreeDir == "" {
			_ = sandbox.RemoveWorktree(root, worktreeDir, branchName)
		}
		return nil, err
	}
	r := &Run{
		ID:            id,
		Workflow:      rec,
		WorkflowName:  rec.Name,
		SpecPath:      specPath,
		SpecContent:   string(specContent),
		RepoRoot:      root,
		OrigBranch:    origBranch,
		InitialCommit: restoreCommit,
		BaseCommit:    restoreCommit,
		WorktreeDir:   worktreeDir,
		BranchName:    branchName,
		RunDir:        runDir,
		Status:        "running",
		StartedAt:     time.Now(),
		Ledger:        led,
		State:         state.New(),
		Config:        cfg,
	}
	return r, nil
}

// Snapshot commits the current worktree state with a structured message and returns the diff contract.
func (r *Run) Snapshot(stepName, taskName string, attempt int) (*diff.DiffContract, error) {
	return sandbox.Snapshot(r.WorktreeDir, stepName, taskName, attempt)
}

// ResetTo resets the worktree to a previous commit for retry or replay.
func (r *Run) ResetTo(commitHash string) error {
	return sandbox.ResetTo(r.WorktreeDir, commitHash)
}

// FinalDiff returns the total diff between initial commit and current worktree HEAD for review/apply.
func (r *Run) FinalDiff() (*diff.DiffContract, error) {
	return sandbox.FinalDiff(r.WorktreeDir, r.InitialCommit)
}

// Teardown removes the worktree and branch; run dir is kept for ledger and artifacts.
func (r *Run) Teardown() error {
	return sandbox.RemoveWorktree(r.RepoRoot, r.WorktreeDir, r.BranchName)
}

// Close matches the R3 lifecycle name; today it only tears down the worktree (same as Teardown).
func (r *Run) Close() error {
	return r.Teardown()
}

// CloneForWorktree returns a copy of the run with a different worktree and branch (parallel execution; deferred in R3).
func (r *Run) CloneForWorktree(worktreeDir, branchName string) *Run {
	clone := *r
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

// Apply merges the run branch into the current branch of the main repo and runs teardown on success.
// The trailer is brand-aware (e.g. Gump-Run:) so merge commits stay traceable after rebranding.
func (r *Run) Apply() error {
	dirty, err := sandbox.HasUncommittedChanges(r.RepoRoot)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("working directory has uncommitted changes — commit or stash before applying")
	}
	currentBranch, err := sandbox.CurrentBranch(r.RepoRoot)
	if err != nil {
		return err
	}
	if currentBranch != r.OrigBranch {
		fmt.Fprintf(os.Stderr, "warning: you are on branch %s, run was started on %s — merge may produce unexpected results\n", currentBranch, r.OrigBranch)
	}
	stepsCount := 0
	if st, err := ReadStatus(r.RunDir); err == nil {
		stepsCount = st.StepsCount
	}
	titlePrefix := "Gump run"
	idLabel := "Run-ID"
	msg := fmt.Sprintf("%s: %s\n\nSpec: %s\n%s: %s\nSteps: %d\n\n%s %s",
		titlePrefix, r.WorkflowName, filepath.Base(r.SpecPath), idLabel, r.ID, stepsCount, brand.MergeTrailer(), r.ID)
	cmd := exec.Command("git", "merge", r.BranchName, "--no-ff", "-m", msg)
	cmd.Dir = r.RepoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() != 0 {
			fmt.Fprintf(os.Stderr, "Merge conflict — resolve conflicts and run 'git commit' to complete.\n")
			return fmt.Errorf("merge failed: %s", out)
		}
		return err
	}
	dc, _ := r.FinalDiff()
	n := 0
	if dc != nil {
		n = len(dc.FilesChanged)
	}
	if err := r.Teardown(); err != nil {
		return err
	}
	fmt.Printf("Run %s applied successfully. %d files changed.\n", r.ID, n)
	return nil
}
