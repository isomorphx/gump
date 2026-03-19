package ledger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

const indexName = "index.ndjson"

// IndexEntry is one line in the cross-cook index for aggregation and report --last N.
type IndexEntry struct {
	CookID     string   `json:"cook_id"`
	Timestamp  string   `json:"ts"`
	Recipe     string   `json:"recipe"`
	Spec       string   `json:"spec"`
	Status     string   `json:"status"`
	DurationMs int      `json:"duration_ms"`
	CostUSD    float64  `json:"cost_usd"`
	Steps      int      `json:"steps"`
	Retries    int      `json:"retries"`
	Agents     []string `json:"agents"`
}

// AppendIndex appends one NDJSON line to .pudding/cooks/index.ndjson so we can aggregate over cooks.
// One line per cook lets report --last N and success-rate metrics without scanning every cook dir.
// puddingDir is the repo root (directory containing .pudding).
func AppendIndex(puddingDir string, entry IndexEntry) error {
	indexPath := filepath.Join(puddingDir, ".pudding", "cooks", indexName)
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
// puddingDir is the repo root (directory containing .pudding).
func ReadIndex(puddingDir string) ([]IndexEntry, error) {
	indexPath := filepath.Join(puddingDir, ".pudding", "cooks", indexName)
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
