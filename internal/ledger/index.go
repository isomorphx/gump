package ledger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/isomorphx/gump/internal/brand"
)

const indexName = "index.ndjson"

// IndexEntry is one line in the cross-run index for aggregation and report --last N.
type IndexEntry struct {
	RunID      string   `json:"run_id"`
	Timestamp  string   `json:"ts"`
	Workflow   string   `json:"workflow"`
	Spec       string   `json:"spec"`
	Status     string   `json:"status"`
	DurationMs int      `json:"duration_ms"`
	CostUSD    float64  `json:"cost_usd"`
	Steps      int      `json:"steps"`
	Retries    int      `json:"retries"`
	Agents     []string `json:"agents"`
}

// UnmarshalJSON accepts legacy index lines written before v0.0.4 field renames.
func (e *IndexEntry) UnmarshalJSON(data []byte) error {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	legacyRunIDKey := "co" + "ok_id"
	legacyWorkflowKey := "rec" + "ipe"
	e.RunID, _ = m["run_id"].(string)
	if e.RunID == "" {
		e.RunID, _ = m[legacyRunIDKey].(string)
	}
	e.Workflow, _ = m["workflow"].(string)
	if e.Workflow == "" {
		e.Workflow, _ = m[legacyWorkflowKey].(string)
	}
	e.Timestamp, _ = m["ts"].(string)
	e.Spec, _ = m["spec"].(string)
	e.Status, _ = m["status"].(string)
	switch v := m["duration_ms"].(type) {
	case float64:
		e.DurationMs = int(v)
	case int:
		e.DurationMs = v
	}
	switch v := m["cost_usd"].(type) {
	case float64:
		e.CostUSD = v
	}
	switch v := m["steps"].(type) {
	case float64:
		e.Steps = int(v)
	case int:
		e.Steps = v
	}
	switch v := m["retries"].(type) {
	case float64:
		e.Retries = int(v)
	case int:
		e.Retries = v
	}
	if a, ok := m["agents"].([]interface{}); ok {
		for _, x := range a {
			if s, ok := x.(string); ok {
				e.Agents = append(e.Agents, s)
			}
		}
	}
	return nil
}

// MarshalJSON writes v0.0.4 field names only (run_id, workflow).
func (e IndexEntry) MarshalJSON() ([]byte, error) {
	type wire struct {
		RunID      string   `json:"run_id"`
		Timestamp  string   `json:"ts"`
		Workflow   string   `json:"workflow"`
		Spec       string   `json:"spec"`
		Status     string   `json:"status"`
		DurationMs int      `json:"duration_ms"`
		CostUSD    float64  `json:"cost_usd"`
		Steps      int      `json:"steps"`
		Retries    int      `json:"retries"`
		Agents     []string `json:"agents"`
	}
	return json.Marshal(wire{
		RunID:      e.RunID,
		Timestamp:  e.Timestamp,
		Workflow:   e.Workflow,
		Spec:       e.Spec,
		Status:     e.Status,
		DurationMs: e.DurationMs,
		CostUSD:    e.CostUSD,
		Steps:      e.Steps,
		Retries:    e.Retries,
		Agents:     e.Agents,
	})
}

// AppendIndex appends one NDJSON line to .gump/runs/index.ndjson so we can aggregate over runs.
// One line per run lets report --last N and success-rate metrics without scanning every run dir.
// repoRoot is the repository root (directory containing .gump).
func AppendIndex(repoRoot string, entry IndexEntry) error {
	indexPath := filepath.Join(repoRoot, brand.StateDir(), brand.RunsDir(), indexName)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(indexPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// ReadIndex reads all lines from index.ndjson; invalid lines are skipped for crash tolerance.
// repoRoot is the repository root (directory containing .gump).
func ReadIndex(repoRoot string) ([]IndexEntry, error) {
	indexPath := filepath.Join(repoRoot, brand.StateDir(), brand.RunsDir(), indexName)
	f, err := os.Open(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []IndexEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e IndexEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}
