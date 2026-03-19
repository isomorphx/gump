package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const manifestName = "manifest.ndjson"
const artifactsDir = "artifacts"

// Ledger appends events to manifest.ndjson so every transition is traceable without storing heavy payloads inline.
// We use a single append-only file so that crash recovery never loses committed events and report can aggregate by reading one file per cook.
type Ledger struct {
	cookDir   string
	file      *os.File
	cookID    string
	startedAt time.Time
	mu        sync.Mutex
}

// New creates a ledger for a cook and opens manifest.ndjson append-only so crashes don't lose prior events.
func New(cookDir string, cookID string) (*Ledger, error) {
	manifestPath := filepath.Join(cookDir, manifestName)
	f, err := os.OpenFile(manifestPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &Ledger{cookDir: cookDir, file: f, cookID: cookID, startedAt: time.Now()}, nil
}

// Emit writes one NDJSON line with ts and type first so cat manifest.ndjson is human-readable; goroutine-safe.
func (l *Ledger) Emit(event Event) error {
	if l == nil || l.file == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	payload := map[string]interface{}{"ts": ts, "type": event.EventType()}
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	for k, v := range raw {
		payload[k] = v
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = l.file.Write(append(line, '\n'))
	return err
}

// Close closes the manifest file; safe to call multiple times.
func (l *Ledger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// ArtifactPath returns the absolute path for an artifact name under this cook's artifacts dir.
func (l *Ledger) ArtifactPath(name string) string {
	return filepath.Join(l.cookDir, artifactsDir, name)
}

// WriteArtifact writes content under artifacts/ and returns the relative path for the ledger (artifacts/<name>).
// Artefacts are written before emitting events that reference them so the ledger never points to a missing file after a crash.
func (l *Ledger) WriteArtifact(name string, content []byte) (string, error) {
	if l == nil {
		return "", nil
	}
	dir := filepath.Join(l.cookDir, artifactsDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0644); err != nil {
		return "", err
	}
	return filepath.Join(artifactsDir, name), nil
}

// CookDir returns the cook directory path.
func (l *Ledger) CookDir() string {
	if l == nil {
		return ""
	}
	return l.cookDir
}

// SanitizeStepPath turns a step path like "implement/task-1/red" into a safe filename prefix "implement-task-1-red".
func SanitizeStepPath(stepPath string) string {
	return strings.ReplaceAll(stepPath, "/", "-")
}

// ArtifactName builds the artifact filename for a step: <step-sanitized>[-attemptN]-<type>.<ext>.
func ArtifactName(stepPath string, attempt int, kind, ext string) string {
	s := SanitizeStepPath(stepPath)
	if attempt > 1 {
		s += fmt.Sprintf("-attempt%d", attempt)
	}
	if kind != "" {
		s += "-" + kind
	}
	return s + "." + ext
}
