package engine

import (
	"strings"
	"testing"
)

func TestReplanSubTaskPromptContainsMarker(t *testing.T) {
	// So the stub can detect replan sub-tasks and use root scenario files only.
	if !strings.Contains(replanSubTaskPrompt, "replan sub-task") {
		t.Error("replanSubTaskPrompt should contain 'replan sub-task' for stub detection")
	}
	if !strings.Contains(replanSubTaskPrompt, "original_prompt") {
		t.Error("replanSubTaskPrompt should contain {original_prompt}")
	}
}
