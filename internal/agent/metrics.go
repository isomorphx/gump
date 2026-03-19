package agent

import (
	"encoding/json"
	"errors"
)

// ParseResultJSON extracts RunResult from the final type=result NDJSON line.
// Raw token fields are kept so report/aggregation can compute effective input etc. later.
func ParseResultJSON(line []byte) (*RunResult, error) {
	var raw struct {
		Type          string  `json:"type"`
		SessionID     string  `json:"session_id"`
		IsError       bool    `json:"is_error"`
		DurationMs    int     `json:"duration_ms"`
		DurationAPI   int     `json:"duration_api_ms"`
		TotalCostUSD  float64 `json:"total_cost_usd"`
		NumTurns      int     `json:"num_turns"`
		Result        string  `json:"result"`
		Usage         *struct {
			InputTokens         int `json:"input_tokens"`
			OutputTokens        int `json:"output_tokens"`
			CacheCreationTokens int `json:"cache_creation_input_tokens"`
			CacheReadTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
		ModelUsage map[string]struct {
			InputTokens         int     `json:"inputTokens"`
			OutputTokens        int     `json:"outputTokens"`
			CacheCreationTokens int     `json:"cacheCreationInputTokens"`
			CacheReadTokens     int     `json:"cacheReadInputTokens"`
			CostUSD             float64 `json:"costUSD"`
		} `json:"modelUsage"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}
	if raw.Type != "result" {
		return nil, errors.New("not a result message")
	}

	res := &RunResult{
		SessionID:           raw.SessionID,
		IsError:             raw.IsError,
		DurationMs:          raw.DurationMs,
		DurationAPI:         raw.DurationAPI,
		CostUSD:             raw.TotalCostUSD,
		NumTurns:            raw.NumTurns,
		Result:              raw.Result,
		ModelUsage:          make(map[string]ModelMetrics),
	}
	if raw.Usage != nil {
		res.InputTokens = raw.Usage.InputTokens
		res.OutputTokens = raw.Usage.OutputTokens
		res.CacheCreationTokens = raw.Usage.CacheCreationTokens
		res.CacheReadTokens = raw.Usage.CacheReadTokens
	}
	for model, u := range raw.ModelUsage {
		res.ModelUsage[model] = ModelMetrics{
			InputTokens:         u.InputTokens,
			OutputTokens:        u.OutputTokens,
			CacheCreationTokens: u.CacheCreationTokens,
			CacheReadTokens:     u.CacheReadTokens,
			CostUSD:             u.CostUSD,
		}
	}
	return res, nil
}

// ParseQwenResultJSON extracts RunResult from a Qwen type=result NDJSON line (stream-json).
// Qwen does not expose cost or modelUsage; CostUSD and ModelUsage stay zero/empty.
func ParseQwenResultJSON(line []byte) (*RunResult, error) {
	var raw struct {
		Type        string `json:"type"`
		SessionID   string `json:"session_id"`
		IsError     bool   `json:"is_error"`
		DurationMs  int    `json:"duration_ms"`
		DurationAPI int    `json:"duration_api_ms"`
		NumTurns    int    `json:"num_turns"`
		Result      string `json:"result"`
		Usage       *struct {
			InputTokens     int `json:"input_tokens"`
			OutputTokens    int `json:"output_tokens"`
			CacheReadTokens int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}
	if raw.Type != "result" {
		return nil, errors.New("not a result message")
	}
	res := &RunResult{
		SessionID:       raw.SessionID,
		IsError:         raw.IsError,
		DurationMs:      raw.DurationMs,
		DurationAPI:     raw.DurationAPI,
		NumTurns:        raw.NumTurns,
		Result:          raw.Result,
		ModelUsage:      make(map[string]ModelMetrics),
	}
	if raw.Usage != nil {
		res.InputTokens = raw.Usage.InputTokens
		res.OutputTokens = raw.Usage.OutputTokens
		res.CacheReadTokens = raw.Usage.CacheReadTokens
	}
	return res, nil
}
