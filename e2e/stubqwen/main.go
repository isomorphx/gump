// Stub for E2E: behaves like the qwen CLI (parse -p, read scenario, write files, emit Qwen NDJSON). Build as "qwen" and put in PATH.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const stableSessionID = "6548daf5-1bff-4e8e-b1cb-4a0561cac525"

func main() {
	// Parse only -p and --resume; ignore other flags (--output-format, --yolo, --allowed-tools, etc.) so adapter's CLI works.
	var prompt, resumeID string
	for i := 0; i < len(os.Args)-1; i++ {
		switch os.Args[i] {
		case "-p":
			prompt = os.Args[i+1]
		case "--resume":
			resumeID = os.Args[i+1]
		}
	}
	sessionID := stableSessionID
	if resumeID != "" {
		sessionID = resumeID
	}

	cwd, _ := os.Getwd()
	if wt := os.Getenv("PUDDING_WORKTREE"); wt != "" {
		cwd = wt
	}
	// E2E sentinel: prove this stub ran; content = exe path and cwd for debugging.
	if exe, err := os.Executable(); err == nil {
		body := fmt.Sprintf("exe=%s\ncwd=%s", exe, cwd)
		_ = os.WriteFile(filepath.Join(cwd, ".pudding-e2e-stub-qwen"), []byte(body), 0644)
	}
	if os.Getenv("PUDDING_STUB_QWEN_NO_RESULT") == "1" {
		_ = os.WriteFile(filepath.Join(cwd, "hello.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
		emitQwenNDJSONNoResult(cwd, sessionID)
		os.Exit(0)
	}
	scenarioPath := filepath.Join(cwd, ".pudding-test-scenario.json")
	if data, err := os.ReadFile(scenarioPath); err == nil {
		var scenario struct {
			Files []struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			} `json:"files"`
			NoResultEvent bool `json:"no_result_event"` // E2E 9: omit type=result
		}
		if json.Unmarshal(data, &scenario) == nil {
			for _, f := range scenario.Files {
				path := filepath.Join(cwd, f.Path)
				_ = os.MkdirAll(filepath.Dir(path), 0755)
				_ = os.WriteFile(path, []byte(f.Content), 0644)
			}
			if scenario.NoResultEvent {
				emitQwenNDJSONNoResult(cwd, sessionID)
				os.Exit(0)
			}
		}
	}
	if prompt != "" && strings.Contains(prompt, "[PUDDING:plan]") {
		planPath := filepath.Join(cwd, "plan-output.json")
		_ = os.WriteFile(planPath, []byte(`[{"name":"task-1","description":"Stub task","files":["math_test.go","math.go"]}]`), 0644)
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

	emitQwenNDJSON(cwd, sessionID)
}

func emitQwenNDJSON(cwd, sessionID string) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]interface{}{
		"type": "system", "subtype": "init", "session_id": sessionID, "cwd": cwd,
		"model": "coder-model", "permission_mode": "yolo", "qwen_code_version": "0.11.0",
	})
	_ = enc.Encode(map[string]interface{}{
		"type": "assistant", "message": map[string]interface{}{
			"content": []map[string]interface{}{{"type": "tool_use", "id": "call_001", "name": "write_file", "input": map[string]string{"file_path": "hello.go", "content": "package main\n\nfunc main() { println(\"hello world\") }\n"}}},
			"stop_reason": "tool_use", "usage": map[string]int{"input_tokens": 1000, "output_tokens": 100, "cache_read_input_tokens": 500},
		},
	})
	_ = enc.Encode(map[string]interface{}{
		"type": "user", "message": map[string]interface{}{
			"content": []map[string]interface{}{{"type": "tool_result", "tool_use_id": "call_001", "is_error": false, "content": "Successfully created and wrote to new file: hello.go."}},
		},
	})
	_ = enc.Encode(map[string]interface{}{
		"type": "assistant", "message": map[string]interface{}{
			"content": []map[string]interface{}{{"type": "text", "text": "Done."}},
			"usage": map[string]int{"input_tokens": 1100, "output_tokens": 30, "cache_read_input_tokens": 600},
		},
	})
	_ = enc.Encode(map[string]interface{}{
		"type": "result", "subtype": "success", "session_id": sessionID, "is_error": false,
		"duration_ms": 5000, "duration_api_ms": 4900, "num_turns": 2, "result": "Done.",
		"usage": map[string]int{"input_tokens": 2100, "output_tokens": 130, "cache_read_input_tokens": 1100},
	})
	_ = os.Stdout.Sync() // ensure adapter sees type=result before process exits
}

func emitQwenNDJSONNoResult(cwd, sessionID string) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]interface{}{
		"type": "system", "subtype": "init", "session_id": sessionID, "cwd": cwd,
		"model": "coder-model", "permission_mode": "yolo", "qwen_code_version": "0.11.0",
	})
	_ = enc.Encode(map[string]interface{}{
		"type": "assistant", "message": map[string]interface{}{
			"content": []map[string]interface{}{{"type": "tool_use", "id": "call_001", "name": "write_file"}},
		},
	})
	_ = enc.Encode(map[string]interface{}{
		"type": "user", "message": map[string]interface{}{
			"content": []map[string]interface{}{{"type": "tool_result", "tool_use_id": "call_001", "is_error": false}},
		},
	})
	// No type=result → compat mode
	fmt.Fprintln(os.Stderr, "qwen stub: no_result_event mode")
}
