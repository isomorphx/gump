package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isomorphx/gump/internal/brand"
)

func TestInitAnonymousID_FirstRunCreatesIDAndNotice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	stderrPath := filepath.Join(t.TempDir(), "stderr.log")
	f, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	id, created := InitAnonymousID(true, f)
	if !created {
		t.Fatal("expected created=true on first run")
	}
	if strings.TrimSpace(id) == "" {
		t.Fatal("expected non-empty anonymous id")
	}
	idPath := filepath.Join(home, brand.StateDir(), "anonymous_id")
	b, err := os.ReadFile(idPath)
	if err != nil {
		t.Fatalf("expected anonymous_id file: %v", err)
	}
	if strings.TrimSpace(string(b)) != id {
		t.Fatalf("id file mismatch: got %q want %q", strings.TrimSpace(string(b)), id)
	}
	log, _ := os.ReadFile(stderrPath)
	if !strings.Contains(string(log), "Gump collects anonymous workflow metrics") {
		t.Fatalf("missing first-run telemetry notice: %s", string(log))
	}
}

func TestInitAnonymousID_SecondRunNoNotice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, brand.StateDir()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, brand.StateDir(), "anonymous_id"), []byte("existing-id"), 0644); err != nil {
		t.Fatal(err)
	}

	stderrPath := filepath.Join(t.TempDir(), "stderr.log")
	f, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	id, created := InitAnonymousID(true, f)
	if created {
		t.Fatal("expected created=false when id already exists")
	}
	if id != "existing-id" {
		t.Fatalf("unexpected id: %q", id)
	}
	log, _ := os.ReadFile(stderrPath)
	if strings.TrimSpace(string(log)) != "" {
		t.Fatalf("expected no notice on second run, got: %s", string(log))
	}
}
