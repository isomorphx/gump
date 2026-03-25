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
	{Provider: "claude", ModelID: "(default)", ContextWindow: 200_000, MaxOutput: 32_000},                 // claude
	{Provider: "claude", ModelID: "opusplan", ContextWindow: 200_000, MaxOutput: 32_000},                  // claude-opusplan
	{Provider: "claude", ModelID: "claude-opus-4-6", ContextWindow: 200_000, MaxOutput: 32_000},           // claude-opus
	{Provider: "claude", ModelID: "claude-opus-4-6", ContextWindow: 1_000_000, MaxOutput: 32_000},         // claude-opus[1m]
	{Provider: "claude", ModelID: "claude-sonnet-4-6", ContextWindow: 200_000, MaxOutput: 32_000},         // claude-sonnet
	{Provider: "claude", ModelID: "claude-sonnet-4-6", ContextWindow: 1_000_000, MaxOutput: 32_000},       // claude-sonnet[1m]
	{Provider: "claude", ModelID: "claude-haiku-4-5-20251001", ContextWindow: 200_000, MaxOutput: 32_000}, // claude-haiku
	// Codex
	{Provider: "codex", ModelID: "(default)", ContextWindow: 400_000, MaxOutput: 128_000},           // codex
	{Provider: "codex", ModelID: "gpt-5.4", ContextWindow: 400_000, MaxOutput: 128_000},             // codex-gpt54
	{Provider: "codex", ModelID: "gpt-5.4-mini", ContextWindow: 400_000, MaxOutput: 128_000},        // codex-gpt54-mini
	{Provider: "codex", ModelID: "gpt-5.3-codex", ContextWindow: 400_000, MaxOutput: 128_000},       // codex-gpt53
	{Provider: "codex", ModelID: "gpt-5.3-codex-spark", ContextWindow: 128_000, MaxOutput: 128_000}, // codex-gpt53-spark
	{Provider: "codex", ModelID: "gpt-5.2-codex", ContextWindow: 400_000, MaxOutput: 128_000},       // codex-gpt52
	{Provider: "codex", ModelID: "gpt-5.1-codex-max", ContextWindow: 400_000, MaxOutput: 128_000},   // codex-gpt51-max
	{Provider: "codex", ModelID: "o3-codex", ContextWindow: 200_000, MaxOutput: 100_000},            // codex-o3
	// Gemini
	{Provider: "gemini", ModelID: "(default)", ContextWindow: 1_000_000, MaxOutput: 64_000},              // gemini
	{Provider: "gemini", ModelID: "gemini-3-flash", ContextWindow: 1_000_000, MaxOutput: 64_000},         // gemini-flash
	{Provider: "gemini", ModelID: "gemini-3.1-pro-preview", ContextWindow: 1_000_000, MaxOutput: 64_000}, // gemini-pro
	{Provider: "gemini", ModelID: "gemini-3.1-flash-lite", ContextWindow: 1_000_000, MaxOutput: 64_000},  // gemini-flash-lite
	{Provider: "gemini", ModelID: "gemini-2.5-pro-preview", ContextWindow: 1_000_000, MaxOutput: 64_000}, // gemini-25-pro
	{Provider: "gemini", ModelID: "gemini-2.5-flash", ContextWindow: 1_000_000, MaxOutput: 64_000},       // gemini-25-flash
	// Qwen
	{Provider: "qwen", ModelID: "qwen3-coder", ContextWindow: 256_000, MaxOutput: 65_536},        // qwen
	{Provider: "qwen", ModelID: "qwen3-coder-plus", ContextWindow: 1_000_000, MaxOutput: 65_536}, // qwen-plus
	{Provider: "qwen", ModelID: "(local)", ContextWindow: 256_000, MaxOutput: 65_536},            // qwen-local
	// OpenCode
	{Provider: "opencode", ModelID: "(user-configured)", ContextWindow: 0, MaxOutput: 0},           // opencode
	{Provider: "opencode", ModelID: "anthropic/claude-opus-4-6", ContextWindow: 0, MaxOutput: 0},   // opencode-opus
	{Provider: "opencode", ModelID: "anthropic/claude-sonnet-4-6", ContextWindow: 0, MaxOutput: 0}, // opencode-sonnet
	{Provider: "opencode", ModelID: "anthropic/claude-haiku-4-5", ContextWindow: 0, MaxOutput: 0},  // opencode-haiku
	{Provider: "opencode", ModelID: "openai/gpt-5.4", ContextWindow: 0, MaxOutput: 0},              // opencode-gpt54
	{Provider: "opencode", ModelID: "openai/gpt-5.3", ContextWindow: 0, MaxOutput: 0},              // opencode-gpt53
	{Provider: "opencode", ModelID: "google/gemini-3.1-pro", ContextWindow: 0, MaxOutput: 0},       // opencode-gemini
	// Cursor Agent
	{Provider: "cursor", ModelID: "(default)", ContextWindow: 200_000, MaxOutput: 32_000},                         // cursor
	{Provider: "cursor", ModelID: "claude-4.6-sonnet-medium", ContextWindow: 200_000, MaxOutput: 32_000},          // cursor-sonnet
	{Provider: "cursor", ModelID: "claude-4.6-sonnet-medium-thinking", ContextWindow: 200_000, MaxOutput: 32_000}, // cursor-sonnet-thinking
	{Provider: "cursor", ModelID: "claude-4.6-opus-high", ContextWindow: 200_000, MaxOutput: 32_000},              // cursor-opus
	{Provider: "cursor", ModelID: "claude-4.6-opus-high-thinking", ContextWindow: 200_000, MaxOutput: 32_000},     // cursor-opus-thinking
	{Provider: "cursor", ModelID: "gpt-5.4-medium", ContextWindow: 400_000, MaxOutput: 128_000},                   // cursor-gpt5
	{Provider: "cursor", ModelID: "gemini-3.1-pro", ContextWindow: 1_000_000, MaxOutput: 64_000},                  // cursor-gemini
}

// ModelAliases maps alias -> index into KnownModels (first occurrence per alias).
var ModelAliases = func() map[string]*ModelInfo {
	m := make(map[string]*ModelInfo)
	aliases := []struct {
		alias string
		idx   int
	}{
		{"claude", 0}, {"claude-opusplan", 1}, {"claude-opus", 2}, {"claude-opus[1m]", 3}, {"claude-sonnet", 4}, {"claude-sonnet[1m]", 5}, {"claude-haiku", 6},
		{"codex", 7}, {"codex-gpt54", 8}, {"codex-gpt54-mini", 9}, {"codex-gpt53", 10}, {"codex-gpt53-spark", 11}, {"codex-gpt52", 12}, {"codex-gpt51-max", 13}, {"codex-o3", 14},
		{"gemini", 15}, {"gemini-flash", 16}, {"gemini-pro", 17}, {"gemini-flash-lite", 18}, {"gemini-25-pro", 19}, {"gemini-25-flash", 20},
		{"qwen", 21}, {"qwen-plus", 22}, {"qwen-local", 23},
		{"opencode", 24}, {"opencode-opus", 25}, {"opencode-sonnet", 26}, {"opencode-haiku", 27}, {"opencode-gpt54", 28}, {"opencode-gpt53", 29}, {"opencode-gemini", 30},
		{"cursor", 31}, {"cursor-sonnet", 32}, {"cursor-sonnet-thinking", 33}, {"cursor-opus", 34}, {"cursor-opus-thinking", 35}, {"cursor-gpt5", 36}, {"cursor-gemini", 37},
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
