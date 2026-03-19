// Stub for E2E: behaves like the opencode CLI (parse run + prompt, read scenario, write files, emit OpenCode NDJSON). Build as "opencode" and put in PATH.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const stableSessionID = "ses_test_stable_id"

func main() {
	var dir string
	flag.StringVar(&dir, "dir", "", "dir")
	flag.Parse()
	args := flag.Args()
	prompt := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "run" && i+1 < len(args) {
			i++
			for i < len(args) && strings.HasPrefix(args[i], "-") {
				if args[i] == "--session" || args[i] == "-s" {
					i += 2
					continue
				}
				if args[i] == "--dir" || args[i] == "--format" || args[i] == "--model" {
					i += 2
					continue
				}
				i++
			}
			if i < len(args) {
				prompt = args[i]
			}
			break
		}
	}
	sessionID := stableSessionID
	for i := 0; i < len(os.Args)-1; i++ {
		if os.Args[i] == "--session" || os.Args[i] == "-s" {
			sessionID = os.Args[i+1]
			break
		}
	}
	cwd := dir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if os.Getenv("PUDDING_STUB_OPENCODE_MULTI_STEP") == "1" {
		emitOpenCodeNDJSONMultiStep(cwd, sessionID)
		os.Exit(0)
	}
	if os.Getenv("PUDDING_STUB_OPENCODE_MALFORMED_TOKENS") == "1" {
		_ = os.WriteFile(filepath.Join(cwd, "hello.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
		emitOpenCodeNDJSONMalformed(cwd, sessionID)
		os.Exit(0)
	}
	scenarioPath := filepath.Join(cwd, ".pudding-test-scenario.json")
	if data, err := os.ReadFile(scenarioPath); err == nil {
		var scenario struct {
			Files []struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			} `json:"files"`
			MultiStepTokens bool `json:"multi_step_tokens"` // E2E 7: two step_finish with specific token counts
			MalformedTokens bool `json:"malformed_tokens"`  // E2E 10: step_finish without part.tokens
		}
		if json.Unmarshal(data, &scenario) == nil {
			for _, f := range scenario.Files {
				path := filepath.Join(cwd, f.Path)
				_ = os.MkdirAll(filepath.Dir(path), 0755)
				_ = os.WriteFile(path, []byte(f.Content), 0644)
			}
			if scenario.MalformedTokens {
				emitOpenCodeNDJSONMalformed(cwd, sessionID)
				os.Exit(0)
			}
			if scenario.MultiStepTokens {
				emitOpenCodeNDJSONMultiStep(cwd, sessionID)
				os.Exit(0)
			}
		}
	}
	if prompt != "" && strings.Contains(prompt, "[PUDDING:plan]") {
		_ = os.WriteFile(filepath.Join(cwd, "plan-output.json"), []byte(`[{"name":"task-1","description":"Stub task","files":["math_test.go","math.go"]}]`), 0644)
	}
	if prompt != "" && strings.Contains(prompt, "[PUDDING:step:red]") {
		_ = os.WriteFile(filepath.Join(cwd, "math_test.go"), []byte("package math\n\nimport \"testing\"\nfunc TestAdd(t *testing.T) {}\n"), 0644)
	}
	if prompt != "" && strings.Contains(prompt, "[PUDDING:step:green]") {
		_ = os.WriteFile(filepath.Join(cwd, "math.go"), []byte("package math\n\nfunc Add(a, b int) int { return a + b }\n"), 0644)
	}
	if prompt != "" && !strings.Contains(prompt, "[PUDDING:plan]") && !strings.Contains(prompt, "[PUDDING:step:red]") && !strings.Contains(prompt, "[PUDDING:step:green]") {
		_ = os.MkdirAll(cwd, 0755)
		_ = os.WriteFile(filepath.Join(cwd, "hello.go"), []byte("package main\n\nfunc main() { println(\"hello world\") }\n"), 0644)
	}
	emitOpenCodeNDJSON(cwd, sessionID)
}

func emitOpenCodeNDJSON(cwd, sessionID string) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	ts := int64(1772481616332)
	_ = enc.Encode(map[string]interface{}{"type": "step_start", "timestamp": ts, "sessionID": sessionID, "part": map[string]string{"type": "step-start", "snapshot": "abc123"}})
	ts += 5000
	_ = enc.Encode(map[string]interface{}{"type": "tool_use", "timestamp": ts, "sessionID": sessionID, "part": map[string]interface{}{"type": "tool", "callID": "call_001", "tool": "apply_patch", "state": map[string]interface{}{"status": "completed", "output": "Success."}}})
	ts += 50
	_ = enc.Encode(map[string]interface{}{"type": "step_finish", "timestamp": ts, "sessionID": sessionID, "part": map[string]interface{}{"type": "step-finish", "reason": "tool-calls", "cost": 0, "tokens": map[string]interface{}{"total": 8862, "input": 8631, "output": 231, "reasoning": 190, "cache": map[string]int{"read": 0, "write": 0}}}})
	ts += 3000
	_ = enc.Encode(map[string]interface{}{"type": "step_start", "timestamp": ts, "sessionID": sessionID, "part": map[string]string{"type": "step-start", "snapshot": "def456"}})
	ts++
	_ = enc.Encode(map[string]interface{}{"type": "text", "timestamp": ts, "sessionID": sessionID, "part": map[string]string{"type": "text", "text": "Done."}})
	ts += 30
	_ = enc.Encode(map[string]interface{}{"type": "step_finish", "timestamp": ts, "sessionID": sessionID, "part": map[string]interface{}{"type": "step-finish", "reason": "stop", "cost": 0, "tokens": map[string]interface{}{"total": 8904, "input": 180, "output": 20, "reasoning": 0, "cache": map[string]int{"read": 8704, "write": 0}}}})
}

func emitOpenCodeNDJSONMultiStep(cwd, sessionID string) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	ts := int64(1772481616332)
	_ = enc.Encode(map[string]interface{}{"type": "step_start", "timestamp": ts, "sessionID": sessionID, "part": map[string]string{"type": "step-start"}})
	ts += 1000
	_ = enc.Encode(map[string]interface{}{"type": "step_finish", "timestamp": ts, "sessionID": sessionID, "part": map[string]interface{}{"type": "step-finish", "reason": "tool-calls", "tokens": map[string]interface{}{"input": 8631, "output": 231, "reasoning": 190, "cache": map[string]int{"read": 0}}}})
	ts += 2000
	_ = enc.Encode(map[string]interface{}{"type": "step_start", "timestamp": ts, "sessionID": sessionID, "part": map[string]string{"type": "step-start"}})
	_ = enc.Encode(map[string]interface{}{"type": "text", "timestamp": ts + 1, "sessionID": sessionID, "part": map[string]string{"type": "text", "text": "Done."}})
	_ = enc.Encode(map[string]interface{}{"type": "step_finish", "timestamp": ts + 30, "sessionID": sessionID, "part": map[string]interface{}{"type": "step-finish", "reason": "stop", "tokens": map[string]interface{}{"input": 180, "output": 20, "reasoning": 0, "cache": map[string]int{"read": 8704}}}})
}

func emitOpenCodeNDJSONMalformed(cwd, sessionID string) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	ts := int64(1772481616332)
	_ = enc.Encode(map[string]interface{}{"type": "step_start", "timestamp": ts, "sessionID": sessionID, "part": map[string]string{"type": "step-start"}})
	_ = enc.Encode(map[string]interface{}{"type": "text", "timestamp": ts + 1, "sessionID": sessionID, "part": map[string]string{"type": "text", "text": "Done."}})
	// step_finish without part.tokens → compat
	_ = enc.Encode(map[string]interface{}{"type": "step_finish", "timestamp": ts + 30, "sessionID": sessionID, "part": map[string]interface{}{"type": "step-finish", "reason": "stop"}})
	fmt.Fprintln(os.Stderr, "opencode stub: malformed_tokens mode")
}
