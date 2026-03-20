package config

// Config holds merged settings from env, user file, and project file.
// We keep validation commands as strings (not set = empty) so the engine can
// apply heuristics later without changing this type.
type Config struct {
	DefaultAgent string
	LogLevel     string
	CompileCmd   string
	TestCmd      string
	LintCmd      string
	CoverageCmd  string
	// UpdateCheck controls whether the CLI performs a best-effort remote update check.
	// It is intentionally a simple bool so the update-check logic stays fully decoupled
	// from config parsing (internal/version must not import internal/config).
	UpdateCheck bool
}

// Source records the origin of each config value so users can see why a setting applies.
type Source struct {
	DefaultAgent string
	LogLevel     string
	CompileCmd   string
	TestCmd      string
	LintCmd      string
	CoverageCmd  string
}
