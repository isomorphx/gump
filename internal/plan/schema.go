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

// ValidatePlanSchema checks the plan shape so foreach_task can rely on name/description and optional files.
// Failing early avoids confusing errors when expanding tasks.
func ValidatePlanSchema(tasks []Task) error {
	if len(tasks) == 0 {
		return fmt.Errorf("plan must contain at least one task")
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
