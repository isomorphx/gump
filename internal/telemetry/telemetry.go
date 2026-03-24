package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/isomorphx/gump/internal/brand"
)

const defaultURL = "https://telemetry.gump.dev/v1/events"

type StepPayload struct {
	Name          string  `json:"name"`
	Agent         string  `json:"agent"`
	Output        string  `json:"output_mode"`
	Status        string  `json:"status"`
	Duration      int     `json:"duration_ms"`
	Cost          float64 `json:"cost_usd"`
	Turns         int     `json:"turns"`
	Retries       int     `json:"retries"`
	GuardHits     int     `json:"guard_triggers"`
	TokensIn      int     `json:"tokens_in"`
	TokensOut     int     `json:"tokens_out"`
	ContextUsage  float64 `json:"context_usage"`
	TTFD          int     `json:"ttfd"`
	EscalatedFrom *string `json:"escalated_from"`
	EscalatedTo   *string `json:"escalated_to"`
}

type RunPayload struct {
	Workflow         string        `json:"workflow"`
	WorkflowSource   string        `json:"workflow_source"`
	IsCustomWorkflow bool          `json:"is_custom_workflow"`
	RunStatus        string        `json:"run_status"`
	DurationMs       int           `json:"duration_ms"`
	TotalCostUSD     float64       `json:"total_cost_usd"`
	AgentsUsed       []string      `json:"agents_used"`
	AgentCount       int           `json:"agent_count"`
	StepCount        int           `json:"step_count"`
	HasForeach       bool          `json:"has_foreach"`
	HasParallel      bool          `json:"has_parallel"`
	HasGuard         bool          `json:"has_guard"`
	HasHITL          bool          `json:"has_hitl"`
	HasSubworkflow   bool          `json:"has_subworkflow"`
	UsesSessionReuse bool          `json:"uses_session_reuse"`
	TotalRetries     int           `json:"total_retries"`
	GuardTriggers    int           `json:"guard_triggers"`
	RepoLanguage     string        `json:"repo_language"`
	RepoSizeBucket   string        `json:"repo_size_bucket"`
	Steps            []StepPayload `json:"steps"`
}

type envelope struct {
	V           int       `json:"v"`
	AnonymousID string    `json:"anonymous_id"`
	GumpVersion string    `json:"gump_version"`
	OS          string    `json:"os"`
	Arch        string    `json:"arch"`
	Event       string    `json:"event"`
	Timestamp   time.Time `json:"timestamp"`
	RunPayload
}

// InitAnonymousID ensures the id file exists and returns id + creation marker.
func InitAnonymousID(enabled bool, stderr *os.File) (id string, created bool) {
	if !enabled {
		return "", false
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	idPath := filepath.Join(home, brand.StateDir(), "anonymous_id")
	if b, err := os.ReadFile(idPath); err == nil {
		return strings.TrimSpace(string(b)), false
	}
	_ = os.MkdirAll(filepath.Dir(idPath), 0755)
	id = uuid.NewString()
	if err := os.WriteFile(idPath, []byte(id), 0644); err != nil {
		return "", false
	}
	if stderr != nil {
		// WHY: first run must inform the user before any future background upload occurs.
		stderr.WriteString("📊 Gump collects anonymous workflow metrics to improve workflows and publish benchmarks.\n")
		stderr.WriteString("   Shared: workflow name, agents, pass/fail, duration, cost per step.\n")
		stderr.WriteString("   Never shared: code, prompts, file paths, spec content.\n")
		stderr.WriteString("   Opt out: gump config set analytics false\n")
		stderr.WriteString("   Details: https://gump.dev/telemetry\n")
	}
	return id, true
}

func Send(enabled bool, anonymousID string, createdThisRun bool, gumpVersion string, p RunPayload) {
	if !enabled || anonymousID == "" || createdThisRun {
		return
	}
	url := os.Getenv("GUMP_TELEMETRY_URL")
	if strings.TrimSpace(url) == "" {
		url = defaultURL
	}
	body := envelope{
		V:           1,
		AnonymousID: anonymousID,
		GumpVersion: gumpVersion,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Event:       "run_completed",
		Timestamp:   time.Now().UTC(),
		RunPayload:  p,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()
	// WHY: CLI processes can exit immediately after scheduling the upload; give the
	// goroutine a tiny scheduling window without waiting for HTTP completion.
	time.Sleep(20 * time.Millisecond)
}
