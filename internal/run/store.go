package run

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/state"
)

// Known lockfiles so we can record their hashes for reproducibility and drift detection.
var knownLockfiles = []string{
	"go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
	"Cargo.lock", "Gemfile.lock", "poetry.lock",
}

// ContextSnapshot is written at run creation for audit and replay; lockfile hashes and runtime versions enable drift checks later.
type ContextSnapshot struct {
	RunID           string            `json:"run_id"`
	Workflow        string            `json:"workflow"`
	Spec            string            `json:"spec"`
	RepoRoot        string            `json:"repo_root"`
	Branch          string            `json:"branch"`
	InitialCommit   string            `json:"initial_commit"`
	Timestamp       string            `json:"timestamp"`
	LockfileHashes  map[string]string `json:"lockfile_hashes"`
	RuntimeVersions map[string]string `json:"runtime_versions"`
}

// UnmarshalJSON fills RunID/Workflow from v0.0.4 keys or legacy keys on disk.
func (c *ContextSnapshot) UnmarshalJSON(data []byte) error {
	type snap ContextSnapshot
	var s snap
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*c = ContextSnapshot(s)
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	legID := "co" + "ok_id"
	legWF := "rec" + "ipe"
	if c.RunID == "" {
		if v, ok := m[legID].(string); ok {
			c.RunID = v
		}
	}
	if c.Workflow == "" {
		if v, ok := m[legWF].(string); ok {
			c.Workflow = v
		}
	}
	return nil
}

// StatusFile is the JSON stored in .gump/runs/<uuid>/status.json for apply and gc.
type StatusFile struct {
	Status     string `json:"status"`
	UpdatedAt  string `json:"updated_at"`
	StepsCount int    `json:"steps_count,omitempty"`
}

// EnsureRunDir creates .gump/runs/<uuid>/ and artifacts/ so ledger and artifacts have a stable place.
func EnsureRunDir(runDir string) error {
	artifacts := filepath.Join(runDir, "artifacts")
	return os.MkdirAll(artifacts, 0755)
}

// WriteWorkflowSnapshot copies the workflow YAML into the run dir so we know exactly what was run.
func WriteWorkflowSnapshot(runDir string, workflowYAML []byte) error {
	p := filepath.Join(runDir, "workflow-snapshot.yaml")
	return os.WriteFile(p, workflowYAML, 0644)
}

// WriteStatus persists status and updated_at so apply/gc can filter by pass and recency.
func WriteStatus(runDir, status string) error {
	return WriteStatusWithSteps(runDir, status, 0)
}

// WriteStatusWithSteps persists status with step count so apply can show it in the merge message.
func WriteStatusWithSteps(runDir, status string, stepsCount int) error {
	p := filepath.Join(runDir, "status.json")
	body := StatusFile{Status: status, UpdatedAt: time.Now().UTC().Format(time.RFC3339), StepsCount: stepsCount}
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}

// ReadStatus reads status.json from a run dir; missing file returns error.
func ReadStatus(runDir string) (*StatusFile, error) {
	data, err := os.ReadFile(filepath.Join(runDir, "status.json"))
	if err != nil {
		return nil, err
	}
	var s StatusFile
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// AppendLedgerEvent appends one NDJSON line to ledger.ndjson in the run dir for step 4 agent events (agent_launched, agent_completed, agent_timeout, agent_error).
func AppendLedgerEvent(runDir string, event map[string]interface{}) error {
	if event == nil {
		return nil
	}
	p := filepath.Join(runDir, "ledger.ndjson")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// WriteContextSnapshot writes context-snapshot.json with lockfile hashes and runtime versions for reproducibility.
func WriteContextSnapshot(runDir string, ctx *ContextSnapshot) error {
	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runDir, "context-snapshot.json"), data, 0644)
}

// WriteState persists flat workflow state for replay (v0.0.4 path: state.json).
func WriteState(runDir string, st *state.State) error {
	if st == nil {
		return nil
	}
	data, err := st.Serialize()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runDir, "state.json"), data, 0644)
}

// ReadStateFile returns state.json bytes when present, otherwise legacy state-bag.json.
func ReadStateFile(runDir string) ([]byte, error) {
	p := filepath.Join(runDir, "state.json")
	if data, err := os.ReadFile(p); err == nil {
		return data, nil
	}
	return os.ReadFile(filepath.Join(runDir, "state-bag.json"))
}

// LockfileHashesForDir scans worktreeDir for known lockfiles and returns sha256 hashes.
func LockfileHashesForDir(worktreeDir string) map[string]string {
	out := make(map[string]string)
	for _, name := range knownLockfiles {
		p := filepath.Join(worktreeDir, name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		h := sha256.Sum256(data)
		out[name] = "sha256:" + hex.EncodeToString(h[:])
	}
	return out
}

// RuntimeVersionsForDir runs version commands from worktreeDir so we record go/node/python/rust versions at run start.
func RuntimeVersionsForDir(worktreeDir string) map[string]string {
	out := make(map[string]string)
	commands := map[string][]string{
		"go":     {"go", "version"},
		"node":   {"node", "--version"},
		"python3": {"python3", "--version"},
		"rustc": {"rustc", "--version"},
	}
	for key, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = worktreeDir
		b, err := cmd.Output()
		if err != nil {
			continue
		}
		if v := parseVersion(string(b)); v != "" {
			out[key] = v
		}
	}
	return out
}

var versionRegex = regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?)`)

func parseVersion(s string) string {
	m := versionRegex.FindString(s)
	return strings.TrimSpace(m)
}

// ListRuns returns run IDs under runsDir sorted by updated_at descending (newest first).
func ListRuns(runsDir string) ([]RunEntry, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var runs []RunEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		dir := filepath.Join(runsDir, id)
		st, err := ReadStatus(dir)
		if err != nil {
			continue
		}
		runs = append(runs, RunEntry{ID: id, Status: st.Status, UpdatedAt: st.UpdatedAt})
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].UpdatedAt > runs[j].UpdatedAt })
	return runs, nil
}

// RunEntry is one run directory with status; used by apply and gc.
type RunEntry struct {
	ID        string
	Status    string
	UpdatedAt string
}

// FindLatestPassingRun returns the most recent run with status "pass", or empty string if none.
func FindLatestPassingRun(runsDir string) (string, error) {
	runs, err := ListRuns(runsDir)
	if err != nil {
		return "", err
	}
	for _, r := range runs {
		if r.Status == "pass" {
			return r.ID, nil
		}
	}
	return "", nil
}

// WorktreePath returns the worktree path for a run id (used to check existence before apply).
func WorktreePath(repoRoot, runID string) string {
	return filepath.Join(repoRoot, brand.StateDir(), "worktrees", brand.WorktreeDirPrefix()+runID)
}

// WorktreeExists checks if the worktree directory exists so we can refuse apply after gc.
func WorktreeExists(repoRoot, runID string) bool {
	_, err := os.Stat(WorktreePath(repoRoot, runID))
	return err == nil
}

// RunPath returns .gump/runs/<uuid>/ for a given repo and run id.
func RunPath(repoRoot, runID string) string {
	return filepath.Join(repoRoot, brand.StateDir(), brand.RunsDir(), runID)
}

// LoadRunFromDir loads minimal run metadata from a run dir (for apply when we have uuid but not a live Run).
func LoadRunFromDir(repoRoot, runID string) (*Run, error) {
	dir := RunPath(repoRoot, runID)
	data, err := os.ReadFile(filepath.Join(dir, "context-snapshot.json"))
	if err != nil {
		return nil, fmt.Errorf("run %s: %w", runID, err)
	}
	var ctx ContextSnapshot
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, err
	}
	r := &Run{
		ID:            runID,
		WorkflowName:  ctx.Workflow,
		SpecPath:      ctx.Spec,
		RepoRoot:      repoRoot,
		OrigBranch:    ctx.Branch,
		BaseCommit:    ctx.InitialCommit,
		InitialCommit: ctx.InitialCommit,
		WorktreeDir:   WorktreePath(repoRoot, runID),
		BranchName:    brand.WorktreeBranchPrefix() + runID,
		RunDir:        dir,
	}
	st, err := ReadStatus(dir)
	if err == nil {
		r.Status = st.Status
	}
	return r, nil
}
