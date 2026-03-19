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
