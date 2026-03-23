package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/isomorphx/gump/internal/config"
)

func TestResolveCommand_Compile_ConfigOverride(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{CompileCmd: "make build"}
	got, err := ResolveCommand("compile", cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "make build" {
		t.Errorf("got %q", got)
	}
}

func TestResolveCommand_Compile_GoMod(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCommand("compile", nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "go build ./..." {
		t.Errorf("got %q", got)
	}
}

func TestResolveCommand_Compile_PackageJson(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCommand("compile", nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "npm run build" {
		t.Errorf("got %q", got)
	}
}

func TestResolveCommand_Compile_NoMatch(t *testing.T) {
	dir := t.TempDir()
	_, err := ResolveCommand("compile", nil, dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "cannot resolve 'compile': no known build system detected. Configure compile_cmd in gump.toml" {
		t.Errorf("got %q", err.Error())
	}
}

func TestResolveCommand_Test_GoMod(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCommand("test", nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "go test ./..." {
		t.Errorf("got %q", got)
	}
}

func TestResolveCommand_MakefileBuild(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:\n\techo ok\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCommand("compile", nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "make build" {
		t.Errorf("got %q", got)
	}
}

func TestResolveCommand_MakefileTest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("test:\n\techo ok\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCommand("test", nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "make test" {
		t.Errorf("got %q", got)
	}
}

func TestResolveCommand_Lint_GoMod(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCommand("lint", nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "golangci-lint run" {
		t.Errorf("got %q", got)
	}
}

func TestResolveCommand_Coverage_GoMod(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCommand("coverage", nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out" {
		t.Errorf("got %q", got)
	}
}

func TestCheckCommandAvailable_ExtractsBinary(t *testing.T) {
	tests := []struct {
		command string
		wantBin string
	}{
		{"golangci-lint run", "golangci-lint"},
		{"go build ./...", "go"},
		{"npm run lint", "npm"},
		{"python -m pytest", "python"},
		{"cmd1 && cmd2", "cmd1"},
		{"/usr/bin/go build", "/usr/bin/go"},
		{"cmd1 | cmd2", "cmd1"},   // single | must not be split
		{"go test ./... &", "go"}, // single & must not be split
	}
	for _, tt := range tests {
		gotBin, _ := CheckCommandAvailable(tt.command)
		if gotBin != tt.wantBin {
			t.Errorf("CheckCommandAvailable(%q) binary = %q, want %q", tt.command, gotBin, tt.wantBin)
		}
	}
}

func TestIsOptionalValidator(t *testing.T) {
	if !IsOptionalValidator("lint") {
		t.Error("lint should be optional")
	}
	if !IsOptionalValidator("coverage") {
		t.Error("coverage should be optional")
	}
	if IsOptionalValidator("compile") {
		t.Error("compile should not be optional")
	}
	if IsOptionalValidator("test") {
		t.Error("test should not be optional")
	}
}
