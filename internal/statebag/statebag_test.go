package statebag

import (
	"encoding/json"
	"testing"
)

func TestStateBag_SetGet(t *testing.T) {
	sb := New()
	sb.Set("decompose", `[{"name":"t1"}]`, "", nil, "")
	sb.Set("implement/task-1/code", "", "diff --git a/x.go", nil, "")
	if v := sb.Get("decompose", "implement/task-1/code", "output"); v != `[{"name":"t1"}]` {
		t.Errorf("Get output: got %q", v)
	}
	if v := sb.Get("code", "implement/task-1/code", "diff"); v != "diff --git a/x.go" {
		t.Errorf("Get diff: got %q", v)
	}
	if v := sb.Get("missing", "any", "output"); v != "" {
		t.Errorf("Get missing: got %q", v)
	}
}

func TestStateBag_SerializeRestore(t *testing.T) {
	sb := New()
	sb.Set("a", "out1", "d1", nil, "")
	data, err := sb.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["entries"] == nil {
		t.Error("entries missing")
	}
	restored, err := Restore(data)
	if err != nil {
		t.Fatal(err)
	}
	if v := restored.Get("a", "", "output"); v != "out1" {
		t.Errorf("restored Get: got %q", v)
	}
}

func TestStateBag_ResetGroup(t *testing.T) {
	sb := New()
	sb.Set("converge/a", "v1", "", nil, "")
	sb.Set("converge/b", "v2", "", nil, "")
	moved := sb.ResetGroup("converge")
	if len(moved) != 2 {
		t.Errorf("expected 2 moved, got %d", len(moved))
	}
	if v := sb.Get("a", "converge/x", "output"); v != "" {
		t.Errorf("after reset scope must resolve to empty (no prev fallback): got %q", v)
	}
}
