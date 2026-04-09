package plan

import (
	"strings"
	"testing"
)

func TestParsePlanOutput_Valid(t *testing.T) {
	raw := []byte(`[
  {"name": "task-1", "description": "Do thing 1", "files": ["a.go"]},
  {"name": "task-2", "description": "Do thing 2"}
]`)
	tasks, err := ParsePlanOutput(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	if tasks[0].Name != "task-1" || tasks[0].Description != "Do thing 1" || len(tasks[0].Files) != 1 || tasks[0].Files[0] != "a.go" {
		t.Errorf("task 0: %+v", tasks[0])
	}
	if tasks[1].Name != "task-2" || tasks[1].Description != "Do thing 2" || len(tasks[1].Files) != 0 {
		t.Errorf("task 1: %+v", tasks[1])
	}
}

func TestParsePlanOutput_InvalidJSON(t *testing.T) {
	_, err := ParsePlanOutput([]byte("not json"))
	if err == nil {
		t.Error("expected error")
	}
	if !strings.Contains(err.Error(), "valid JSON") {
		t.Errorf("error should mention valid JSON: %v", err)
	}
}

func TestValidatePlanSchema_Empty(t *testing.T) {
	if err := ValidatePlanSchema(nil); err != nil {
		t.Errorf("nil slice: %v", err)
	}
	if err := ValidatePlanSchema([]Task{}); err != nil {
		t.Errorf("empty slice: %v", err)
	}
}

func TestValidatePlanSchema_Valid(t *testing.T) {
	tasks := []Task{
		{Name: "a", Description: "Desc A", Files: []string{"x.go"}},
		{Name: "b", Description: "Desc B"},
	}
	if err := ValidatePlanSchema(tasks); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePlanSchema_EmptyName(t *testing.T) {
	err := ValidatePlanSchema([]Task{{Name: "", Description: "x"}})
	if err == nil {
		t.Error("expected error")
	}
}

func TestValidatePlanSchema_EmptyDescription(t *testing.T) {
	err := ValidatePlanSchema([]Task{{Name: "a", Description: ""}})
	if err == nil {
		t.Error("expected error")
	}
}

func TestValidatePlanSchema_EmptyFile(t *testing.T) {
	err := ValidatePlanSchema([]Task{{Name: "a", Description: "b", Files: []string{""}}})
	if err == nil {
		t.Error("expected error")
	}
}

