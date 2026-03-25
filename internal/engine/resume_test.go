package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseManifestForResume_passedAndFatal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.ndjson")
	body := `{"type":"step_completed","step":"prep","status":"pass"}
{"type":"step_completed","step":"build/code","status":"fatal"}
`
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	fatal, passed, err := parseManifestForResume(p)
	if err != nil {
		t.Fatal(err)
	}
	if fatal != "build/code" {
		t.Fatalf("fatal step: got %q", fatal)
	}
	if !passed["prep"] {
		t.Fatalf("expected prep in passed map: %#v", passed)
	}
	if passed["build/code"] {
		t.Fatal("fatal step should not be marked pass")
	}
}

func TestParseManifestForResume_circuitBreakerFallback(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.ndjson")
	body := `{"type":"circuit_breaker","step":"impl"}
`
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	fatal, _, err := parseManifestForResume(p)
	if err != nil {
		t.Fatal(err)
	}
	if fatal != "impl" {
		t.Fatalf("expected impl from circuit_breaker, got %q", fatal)
	}
}

func TestParseManifestForResume_noFatalError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "manifest.ndjson")
	body := `{"type":"step_completed","step":"x","status":"pass"}
`
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err := parseManifestForResume(p)
	if err == nil || !strings.Contains(err.Error(), "fatal") {
		t.Fatalf("expected error about fatal step, got %v", err)
	}
}
