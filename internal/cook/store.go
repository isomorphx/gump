package cook

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

// ContextSnapshot is written at cook creation for audit and replay; lockfile hashes and runtime versions enable drift checks later.
type ContextSnapshot struct {
	CookID          string            `json:"cook_id"`
	Recipe          string            `json:"recipe"`
	Spec            string            `json:"spec"`
	RepoRoot        string            `json:"repo_root"`
	Branch          string            `json:"branch"`
	InitialCommit   string            `json:"initial_commit"`
	Timestamp       string            `json:"timestamp"`
	LockfileHashes  map[string]string `json:"lockfile_hashes"`
	RuntimeVersions map[string]string `json:"runtime_versions"`
}

// StatusFile is the JSON stored in .gump/runs/<uuid>/status.json for apply and gc.
type StatusFile struct {
	Status     string `json:"status"`
	UpdatedAt  string `json:"updated_at"`
	StepsCount int    `json:"steps_count,omitempty"`
}

// EnsureCookDir creates .gump/runs/<uuid>/ and artifacts/ so ledger and artifacts have a stable place.
func EnsureCookDir(cookDir string) error {
	artifacts := filepath.Join(cookDir, "artifacts")
	return os.MkdirAll(artifacts, 0755)
}

// WriteRecipeSnapshot copies the workflow YAML into the run dir so we know exactly what was run.
func WriteRecipeSnapshot(cookDir string, recipeYAML []byte) error {
	p := filepath.Join(cookDir, "workflow-snapshot.yaml")
	return os.WriteFile(p, recipeYAML, 0644)
}

// WriteStatus persists status and updated_at so apply/gc can filter by pass and recency.
func WriteStatus(cookDir, status string) error {
	return WriteStatusWithSteps(cookDir, status, 0)
}

// WriteStatusWithSteps persists status with step count so apply can show it in the merge message.
func WriteStatusWithSteps(cookDir, status string, stepsCount int) error {
	p := filepath.Join(cookDir, "status.json")
	body := StatusFile{Status: status, UpdatedAt: time.Now().UTC().Format(time.RFC3339), StepsCount: stepsCount}
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}

// ReadStatus reads status.json from a cook dir; missing file returns error.
func ReadStatus(cookDir string) (*StatusFile, error) {
	data, err := os.ReadFile(filepath.Join(cookDir, "status.json"))
	if err != nil {
		return nil, err
	}
	var s StatusFile
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// AppendLedgerEvent appends one NDJSON line to ledger.ndjson in the cook dir for step 4 agent events (agent_launched, agent_completed, agent_timeout, agent_error).
func AppendLedgerEvent(cookDir string, event map[string]interface{}) error {
	if event == nil {
		return nil
	}
	p := filepath.Join(cookDir, "ledger.ndjson")
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
func WriteContextSnapshot(cookDir string, ctx *ContextSnapshot) error {
	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cookDir, "context-snapshot.json"), data, 0644)
}

// WriteState persists flat workflow state for replay (v0.0.4 path: state.json).
func WriteState(cookDir string, st *state.State) error {
	if st == nil {
		return nil
	}
	data, err := st.Serialize()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cookDir, "state.json"), data, 0644)
}

// ReadStateFile returns state.json bytes when present, otherwise legacy state-bag.json.
func ReadStateFile(cookDir string) ([]byte, error) {
	p := filepath.Join(cookDir, "state.json")
	if data, err := os.ReadFile(p); err == nil {
		return data, nil
	}
	return os.ReadFile(filepath.Join(cookDir, "state-bag.json"))
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

// RuntimeVersionsForDir runs version commands from worktreeDir so we record go/node/python/rust versions at cook time.
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

// ListCooks returns cook IDs under cooksDir sorted by updated_at descending (newest first).
func ListCooks(cooksDir string) ([]CookEntry, error) {
	entries, err := os.ReadDir(cooksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cooks []CookEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		dir := filepath.Join(cooksDir, id)
		st, err := ReadStatus(dir)
		if err != nil {
			continue
		}
		cooks = append(cooks, CookEntry{ID: id, Status: st.Status, UpdatedAt: st.UpdatedAt})
	}
	sort.Slice(cooks, func(i, j int) bool { return cooks[i].UpdatedAt > cooks[j].UpdatedAt })
	return cooks, nil
}

// CookEntry is one cook directory with status; used by apply and gc.
type CookEntry struct {
	ID        string
	Status    string
	UpdatedAt string
}

// FindLatestPassingCook returns the most recent cook with status "pass", or empty string if none.
func FindLatestPassingCook(cooksDir string) (string, error) {
	cooks, err := ListCooks(cooksDir)
	if err != nil {
		return "", err
	}
	for _, c := range cooks {
		if c.Status == "pass" {
			return c.ID, nil
		}
	}
	return "", nil
}

// WorktreePath returns the worktree path for a cook id (used to check existence before apply).
func WorktreePath(repoRoot, cookID string) string {
	return filepath.Join(repoRoot, brand.StateDir(), "worktrees", brand.WorktreeDirPrefix()+cookID)
}

// WorktreeExists checks if the worktree directory exists so we can refuse apply after gc.
func WorktreeExists(repoRoot, cookID string) bool {
	_, err := os.Stat(WorktreePath(repoRoot, cookID))
	return err == nil
}

// CookDir returns .gump/runs/<uuid>/ for a given repo and run id.
func CookDir(repoRoot, cookID string) string {
	return filepath.Join(repoRoot, brand.StateDir(), brand.RunsDir(), cookID)
}

// LoadCookFromDir loads minimal cook metadata from a cook dir (for apply when we have uuid but not a live Cook).
func LoadCookFromDir(repoRoot, cookID string) (*Cook, error) {
	dir := CookDir(repoRoot, cookID)
	data, err := os.ReadFile(filepath.Join(dir, "context-snapshot.json"))
	if err != nil {
		return nil, fmt.Errorf("cook %s: %w", cookID, err)
	}
	var ctx ContextSnapshot
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, err
	}
	c := &Cook{
		ID:            cookID,
		RecipeName:    ctx.Recipe,
		SpecPath:      ctx.Spec,
		RepoRoot:      repoRoot,
		OrigBranch:    ctx.Branch,
		InitialCommit: ctx.InitialCommit,
		WorktreeDir:   WorktreePath(repoRoot, cookID),
		BranchName:    brand.WorktreeBranchPrefix() + cookID,
		CookDir:       dir,
	}
	st, err := ReadStatus(dir)
	if err == nil {
		c.Status = st.Status
	}
	return c, nil
}
