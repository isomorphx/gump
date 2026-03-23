package agent

// ModelInfo holds context window and output limits for a known model alias (for % context and gump models).
type ModelInfo struct {
	ContextWindow int
	MaxOutput     int
	Provider      string
	ModelID       string
}

// KnownModels is the static table of Gump aliases -> model info. Used by gump models and agent summary.
var KnownModels = []ModelInfo{
	// Claude (claude CLI)
	{Provider: "claude", ModelID: "(default)", ContextWindow: 200_000, MaxOutput: 32_000},           // claude
	{Provider: "claude", ModelID: "claude-opus-4-6", ContextWindow: 200_000, MaxOutput: 32_000},     // claude-opus
	{Provider: "claude", ModelID: "claude-opus-4-6", ContextWindow: 1_000_000, MaxOutput: 32_000},   // claude-opus[1m]
	{Provider: "claude", ModelID: "claude-sonnet-4-5-20250929", ContextWindow: 200_000, MaxOutput: 32_000},   // claude-sonnet
	{Provider: "claude", ModelID: "claude-sonnet-4-5-20250929", ContextWindow: 1_000_000, MaxOutput: 32_000}, // claude-sonnet[1m]
	{Provider: "claude", ModelID: "claude-haiku-4-5-20251001", ContextWindow: 200_000, MaxOutput: 32_000},     // claude-haiku
	// Codex
	{Provider: "codex", ModelID: "gpt-5.3-codex", ContextWindow: 400_000, MaxOutput: 128_000},       // codex, codex-gpt53
	{Provider: "codex", ModelID: "gpt-5.2-codex", ContextWindow: 400_000, MaxOutput: 128_000},       // codex-gpt52
	{Provider: "codex", ModelID: "gpt-5.1-codex", ContextWindow: 400_000, MaxOutput: 128_000},      // codex-gpt51
	{Provider: "codex", ModelID: "o3-codex", ContextWindow: 200_000, MaxOutput: 100_000},            // codex-o3
	{Provider: "codex", ModelID: "gpt-5.3-codex-spark", ContextWindow: 128_000, MaxOutput: 128_000}, // codex-spark
	// Gemini
	{Provider: "gemini", ModelID: "auto-gemini-3", ContextWindow: 1_000_000, MaxOutput: 64_000},             // gemini
	{Provider: "gemini", ModelID: "gemini-3-flash-preview", ContextWindow: 1_000_000, MaxOutput: 64_000},    // gemini-flash
	{Provider: "gemini", ModelID: "gemini-3.1-pro-preview", ContextWindow: 1_000_000, MaxOutput: 64_000},     // gemini-pro
	{Provider: "gemini", ModelID: "gemini-3.1-flash-lite-preview", ContextWindow: 1_000_000, MaxOutput: 64_000}, // gemini-flash-lite
	// Qwen
	{Provider: "qwen", ModelID: "qwen3-coder", ContextWindow: 256_000, MaxOutput: 65_536},        // qwen
	{Provider: "qwen", ModelID: "qwen3-coder-plus", ContextWindow: 1_000_000, MaxOutput: 65_536}, // qwen-plus
	{Provider: "qwen", ModelID: "(local)", ContextWindow: 256_000, MaxOutput: 65_536},            // qwen-local
	// OpenCode (user-configured, unknown)
	{Provider: "opencode", ModelID: "(user-configured)", ContextWindow: 0, MaxOutput: 0}, // opencode
}

// ModelAliases maps alias -> index into KnownModels (first occurrence per alias).
var ModelAliases = func() map[string]*ModelInfo {
	m := make(map[string]*ModelInfo)
	aliases := []struct {
		alias string
		idx   int
	}{
		{"claude", 0}, {"claude-opus", 1}, {"claude-opus[1m]", 2}, {"claude-sonnet", 3}, {"claude-sonnet[1m]", 4}, {"claude-haiku", 5},
		{"codex", 6}, {"codex-gpt53", 6}, {"codex-gpt52", 7}, {"codex-gpt51", 8}, {"codex-o3", 9}, {"codex-spark", 10},
		{"gemini", 11}, {"gemini-flash", 12}, {"gemini-pro", 13}, {"gemini-flash-lite", 14},
		{"qwen", 15}, {"qwen-plus", 16}, {"qwen-local", 17},
		{"opencode", 18},
	}
	for _, a := range aliases {
		m[a.alias] = &KnownModels[a.idx]
	}
	return m
}()

// LookupModel returns model info for a Gump alias, or nil if unknown.
func LookupModel(alias string) *ModelInfo {
	return ModelAliases[alias]
}
