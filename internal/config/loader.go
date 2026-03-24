package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/isomorphx/gump/internal/brand"
)

const (
	userConfigFile = "config.toml"
)

func userConfigDir() string     { return brand.StateDir() }
func projectConfigName() string { return brand.Lower() + ".toml" }
func envPrefix() string         { return brand.Upper() }

// Load merges config in priority order so project overrides user overrides defaults.
// We do not fail when config files are missing so a fresh install works without setup.
func Load() (*Config, *Source, error) {
	cfg := &Config{
		DefaultAgent: "claude-sonnet",
		LogLevel:     "info",
		Analytics:    true,
		UpdateCheck:  true,
	}
	src := &Source{
		DefaultAgent: "default",
		LogLevel:     "default",
		Analytics:    "default",
	}

	// User config: ~/.<brand>/config.toml
	home, _ := os.UserHomeDir()
	if home != "" {
		path := filepath.Join(home, userConfigDir(), userConfigFile)
		applyFile(cfg, src, path, "~/"+userConfigDir()+"/"+userConfigFile)
	}

	// Project config: <brand>.toml from cwd upward
	cwd, _ := os.Getwd()
	if cwd != "" {
		path := findProjectConfig(cwd)
		if path != "" {
			applyFile(cfg, src, path, projectConfigName())
		}
	}

	// Env overrides (highest priority)
	applyEnv(cfg, src)

	return cfg, src, nil
}

// findProjectConfig walks up from dir until it finds <brand>.toml or a .git (repo root); it does not traverse above .git.
func findProjectConfig(dir string) string {
	for {
		path := filepath.Join(dir, projectConfigName())
		if _, err := os.Stat(path); err == nil {
			return path
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

type fileConfig struct {
	DefaultAgent string `toml:"default_agent"`
	LogLevel     string `toml:"log_level"`
	Analytics    struct {
		Enabled *bool `toml:"enabled"`
	} `toml:"analytics"`
	Update struct {
		// Pointer lets us detect "not set" vs "set to false/true".
		Check *bool `toml:"check"`
	} `toml:"update"`
	Validation struct {
		CompileCmd  string `toml:"compile_cmd"`
		TestCmd     string `toml:"test_cmd"`
		LintCmd     string `toml:"lint_cmd"`
		CoverageCmd string `toml:"coverage_cmd"`
	} `toml:"validation"`
	ErrorContext struct {
		MaxErrorChars int `toml:"max_error_chars"`
		MaxDiffChars  int `toml:"max_diff_chars"`
	} `toml:"error_context"`
}

func applyFile(cfg *Config, src *Source, path, label string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var f fileConfig
	if _, err := toml.Decode(string(data), &f); err != nil {
		return
	}
	if f.DefaultAgent != "" {
		cfg.DefaultAgent = f.DefaultAgent
		src.DefaultAgent = label
	}
	if f.LogLevel != "" {
		cfg.LogLevel = f.LogLevel
		src.LogLevel = label
	}
	if f.Analytics.Enabled != nil {
		cfg.Analytics = *f.Analytics.Enabled
		src.Analytics = label
	}
	// Spec semantics: if any source says `check = false`, disable update checking.
	// This is NOT a priority/cascade; it is an OR on "false means disabled".
	if f.Update.Check != nil && !*f.Update.Check {
		cfg.UpdateCheck = false
	}
	if f.Validation.CompileCmd != "" {
		cfg.CompileCmd = f.Validation.CompileCmd
		src.CompileCmd = label
	}
	if f.Validation.TestCmd != "" {
		cfg.TestCmd = f.Validation.TestCmd
		src.TestCmd = label
	}
	if f.Validation.LintCmd != "" {
		cfg.LintCmd = f.Validation.LintCmd
		src.LintCmd = label
	}
	if f.Validation.CoverageCmd != "" {
		cfg.CoverageCmd = f.Validation.CoverageCmd
		src.CoverageCmd = label
	}
	if f.ErrorContext.MaxErrorChars > 0 {
		cfg.ErrorContextMaxErrorChars = f.ErrorContext.MaxErrorChars
	}
	if f.ErrorContext.MaxDiffChars > 0 {
		cfg.ErrorContextMaxDiffChars = f.ErrorContext.MaxDiffChars
	}
}

func applyEnv(cfg *Config, src *Source) {
	if v := os.Getenv(envPrefix() + "_DEFAULT_AGENT"); v != "" {
		cfg.DefaultAgent = v
		src.DefaultAgent = "env"
	}
	if v := os.Getenv(envPrefix() + "_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
		src.LogLevel = "env"
	}
	// Any non-empty value disables the update check.
	if v := os.Getenv(envPrefix() + "_NO_UPDATE_CHECK"); v != "" {
		cfg.UpdateCheck = false
	}
}

// ProjectConfigPath returns the path to <brand>.toml if found from cwd, else "".
func ProjectConfigPath() string {
	cwd, _ := os.Getwd()
	if cwd == "" {
		return ""
	}
	return findProjectConfig(cwd)
}

// ProjectRoot returns the directory containing <brand>.toml or .git, or cwd if none.
func ProjectRoot() string {
	cwd, _ := os.Getwd()
	if cwd == "" {
		return ""
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, projectConfigName())); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd
		}
		dir = parent
	}
}
