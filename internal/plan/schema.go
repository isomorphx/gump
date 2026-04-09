package plan

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Task is one item from the plan step output; blast radius (Files) is optional so recipes can suggest scope without enforcing it.
type Task struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Files       []string `json:"files,omitempty"`
}

// ParsePlanOutput parses plan step output (file content or stdout) into a list of tasks.
func ParsePlanOutput(raw []byte) ([]Task, error) {
	var tasks []Task
	if err := json.Unmarshal(raw, &tasks); err != nil {
		return nil, fmt.Errorf("plan step did not produce valid JSON output: %w", err)
	}
	return tasks, nil
}

// ValidateSplitTasks enforces task identity after a split (R6); empty list is valid (caller may warn).
func ValidateSplitTasks(tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tasks))
	for i, t := range tasks {
		n := strings.TrimSpace(t.Name)
		if n == "" {
			return fmt.Errorf("task at index %d: name is required", i)
		}
		if _, ok := seen[n]; ok {
			return fmt.Errorf("duplicate task name %q", n)
		}
		seen[n] = struct{}{}
	}
	return nil
}

// ValidatePlanSchema checks the plan shape so foreach_task can rely on name/description and optional files.
// Failing early avoids confusing errors when expanding tasks.
// Empty JSON array is valid for split output (R6); the engine warns and skips each.
func ValidatePlanSchema(tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}
	for i, t := range tasks {
		if strings.TrimSpace(t.Name) == "" {
			return fmt.Errorf("task at index %d: name is required", i)
		}
		if strings.TrimSpace(t.Description) == "" {
			return fmt.Errorf("task at index %d: description is required", i)
		}
		for j, f := range t.Files {
			if strings.TrimSpace(f) == "" {
				return fmt.Errorf("task at index %d: files[%d] must be non-empty", i, j)
			}
		}
	}
	return nil
}
