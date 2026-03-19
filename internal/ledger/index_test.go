package ledger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendAndReadIndex(t *testing.T) {
	dir := t.TempDir()
	puddingDir := dir

	if err := AppendIndex(puddingDir, IndexEntry{
		CookID: "c1", Timestamp: "2025-06-15T14:30:00.123Z", Recipe: "tdd", Spec: "spec.md",
		Status: "pass", DurationMs: 1000, CostUSD: 0.1, Steps: 3, Retries: 0, Agents: []string{"claude"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := AppendIndex(puddingDir, IndexEntry{
		CookID: "c2", Timestamp: "2025-06-15T15:00:00.000Z", Recipe: "freeform", Spec: "spec.md",
		Status: "fatal", DurationMs: 500, CostUSD: 0.05, Steps: 1, Retries: 1, Agents: []string{"claude"},
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := ReadIndex(puddingDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].CookID != "c1" || entries[0].Status != "pass" {
		t.Errorf("first entry: %+v", entries[0])
	}
	if entries[1].CookID != "c2" || entries[1].Status != "fatal" {
		t.Errorf("second entry: %+v", entries[1])
	}
}

func TestReadIndexMissing(t *testing.T) {
	dir := t.TempDir()
	entries, err := ReadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if entries != nil {
		t.Errorf("expected nil entries, got %d", len(entries))
	}
}

func TestReadIndexSkipsInvalidLines(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, ".pudding", "cooks", indexName)
	_ = os.MkdirAll(filepath.Dir(indexPath), 0755)
	_ = os.WriteFile(indexPath, []byte(`{"cook_id":"a","ts":"2025-01-01T00:00:00Z","recipe":"r","spec":"s","status":"pass","duration_ms":0,"cost_usd":0,"steps":0,"retries":0,"agents":[]}
not json
{"cook_id":"b","ts":"2025-01-01T00:00:01Z","recipe":"r","spec":"s","status":"pass","duration_ms":0,"cost_usd":0,"steps":0,"retries":0,"agents":[]}
`), 0644)

	entries, err := ReadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 valid entries, got %d", len(entries))
	}
	if entries[0].CookID != "a" || entries[1].CookID != "b" {
		t.Errorf("%+v", entries)
	}
}
