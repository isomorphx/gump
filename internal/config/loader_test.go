package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, src, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultAgent != "claude-sonnet" || cfg.LogLevel != "info" {
		t.Errorf("defaults: got DefaultAgent=%q LogLevel=%q", cfg.DefaultAgent, cfg.LogLevel)
	}
	if !cfg.UpdateCheck {
		t.Errorf("defaults: expected UpdateCheck=true")
	}
	if src.DefaultAgent != "default" || src.LogLevel != "default" {
		t.Errorf("sources: got %q %q", src.DefaultAgent, src.LogLevel)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	os.Setenv("PUDDING_DEFAULT_AGENT", "env-agent")
	os.Setenv("PUDDING_LOG_LEVEL", "debug")
	os.Setenv("PUDDING_NO_UPDATE_CHECK", "1")
	defer os.Unsetenv("PUDDING_DEFAULT_AGENT")
	defer os.Unsetenv("PUDDING_LOG_LEVEL")
	defer os.Unsetenv("PUDDING_NO_UPDATE_CHECK")
	cfg, src, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultAgent != "env-agent" || src.DefaultAgent != "env" {
		t.Errorf("got DefaultAgent=%q src=%q", cfg.DefaultAgent, src.DefaultAgent)
	}
	if cfg.LogLevel != "debug" || src.LogLevel != "env" {
		t.Errorf("got LogLevel=%q src=%q", cfg.LogLevel, src.LogLevel)
	}
	if cfg.UpdateCheck {
		t.Errorf("expected UpdateCheck=false when PUDDING_NO_UPDATE_CHECK is set")
	}
}

func TestLoad_ProjectConfig(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer os.Chdir(orig)
	path := filepath.Join(dir, "pudding.toml")
	_ = os.WriteFile(path, []byte(`default_agent = "proj-agent"
[validation]
compile_cmd = "make build"
`), 0644)
	cfg, src, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultAgent != "proj-agent" || src.DefaultAgent != "pudding.toml" {
		t.Errorf("got DefaultAgent=%q src=%q", cfg.DefaultAgent, src.DefaultAgent)
	}
	if cfg.CompileCmd != "make build" {
		t.Errorf("got CompileCmd=%q", cfg.CompileCmd)
	}
}

func TestLoad_ProjectConfig_DisableUpdateCheck(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer os.Chdir(orig)

	// Ensure env doesn't interfere.
	_ = os.Unsetenv("PUDDING_NO_UPDATE_CHECK")

	path := filepath.Join(dir, "pudding.toml")
	_ = os.WriteFile(path, []byte(`default_agent = "proj-agent"
[validation]
compile_cmd = "make build"

[update]
check = false
`), 0644)

	cfg, _, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpdateCheck {
		t.Errorf("expected UpdateCheck=false from project config")
	}
}

func TestProjectRoot_ReturnsDirWithPuddingToml(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer os.Chdir(orig)
	_ = os.WriteFile(filepath.Join(dir, "pudding.toml"), []byte(""), 0644)
	root := ProjectRoot()
	// Resolve symlinks so /private/var and /var match on macOS
	root, _ = filepath.EvalSymlinks(root)
	dir, _ = filepath.EvalSymlinks(dir)
	if root != dir {
		t.Errorf("ProjectRoot() = %q want %q", root, dir)
	}
}
