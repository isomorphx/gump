package config

import "time"

// Config holds merged settings from env, user file, and project file.
// We keep validation commands as strings (not set = empty) so the engine can
// apply heuristics later without changing this type.
type Config struct {
	DefaultAgent string
	LogLevel     string
	Verbose      bool
	// Analytics controls anonymous telemetry; default true, opt-out via config set.
	Analytics   bool
	CompileCmd  string
	TestCmd     string
	LintCmd     string
	CoverageCmd string
	// ValidationTimeout is the max wall time for each shell-based gate validator (compile, test, bash, etc.).
	ValidationTimeout time.Duration
	// ErrorContextMax* bound gate stderr and diff injected on retry so huge validator output cannot blow the model budget.
	ErrorContextMaxErrorChars int
	ErrorContextMaxDiffChars  int
	// UpdateCheck controls whether the CLI performs a best-effort remote update check.
	// It is intentionally a simple bool so the update-check logic stays fully decoupled
	// from config parsing (internal/version must not import internal/config).
	UpdateCheck bool
}

// Source records the origin of each config value so users can see why a setting applies.
type Source struct {
	DefaultAgent      string
	LogLevel          string
	Verbose           string
	Analytics         string
	CompileCmd        string
	TestCmd           string
	LintCmd           string
	CoverageCmd       string
	ValidationTimeout string
}
