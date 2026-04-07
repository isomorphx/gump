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

func TestStateBag_AmbiguousReference(t *testing.T) {
	sb := New()
	sb.Set("group1/arbiter", "value1", "", nil, "")
	sb.Set("group2/arbiter", "value2", "", nil, "")
	// From root scope, both group1/arbiter and group2/arbiter are candidates but
	// neither resolves at any scope level (both have slashes), falling through to
	// the ambiguous case which logs a warning and returns "" instead of panicking.
	got := sb.Get("arbiter", "", "output")
	if got != "" {
		t.Errorf("expected empty string for ambiguous reference, got %q", got)
	}
}

func TestStateBag_AmbiguousReference_FullyQualifiedWorks(t *testing.T) {
	sb := New()
	sb.Set("group1/arbiter", "value1", "", nil, "")
	sb.Set("group2/arbiter", "value2", "", nil, "")
	// With scope "group1", only group1/arbiter is in scope at the group1 level,
	// so the fully-qualified path resolves unambiguously to the correct entry.
	got := sb.Get("group1/arbiter", "group1", "output")
	if got != "value1" {
		t.Errorf("expected %q for fully-qualified reference, got %q", "value1", got)
	}
}

func TestStateBag_Graft(t *testing.T) {
	parent := New()
	parent.SetRunMetric("cost", "1.00")
	child := New()
	child.Set("echo", "hello", "", nil, "")
	child.SetRunMetric("cost", "2.00")

	parent.Graft("call-sub", child)

	if got := parent.Get("call-sub.steps.echo", "", "output"); got != "hello" {
		t.Fatalf("graft output = %q", got)
	}
	if got := parent.GetRunMetric("cost"); got != "2.00" {
		t.Fatalf("run cost not propagated, got %q", got)
	}
}

func TestStateBag_ClearSessionIDsForGroup(t *testing.T) {
	sb := New()
	sb.Set("build/impl", "out", "", nil, "sess-1")
	sb.Set("build/reviews/arch-review", "out", "", nil, "sess-2")
	sb.Set("other/code", "out", "", nil, "sess-3")

	cleared := sb.ClearSessionIDsForGroup("build")
	if len(cleared) != 2 {
		t.Fatalf("expected 2 cleared entries, got %d", len(cleared))
	}
	if got := sb.Get("build/impl", "build/impl", "session_id"); got != "" {
		t.Fatalf("expected build/impl session cleared, got %q", got)
	}
	if got := sb.Get("build/reviews/arch-review", "build/reviews/arch-review", "session_id"); got != "" {
		t.Fatalf("expected build/reviews/arch-review session cleared, got %q", got)
	}
	if got := sb.Get("other/code", "other/code", "session_id"); got != "sess-3" {
		t.Fatalf("expected other/code session unchanged, got %q", got)
	}
}

func TestStateBag_ResetThenClearSessionsForGroup(t *testing.T) {
	sb := New()
	sb.Set("build/impl", "out", "", nil, "sess-1")
	sb.Set("build/reviews/arch-review", "out", "", nil, "sess-2")

	sb.ResetGroup("build")
	cleared := sb.ClearSessionIDsForGroup("build")
	if len(cleared) != 2 {
		t.Fatalf("expected 2 cleared entries after reset->clear, got %d", len(cleared))
	}
	if got := sb.PrevSessionID("build/impl"); got != "" {
		t.Fatalf("expected prev session cleared for build/impl, got %q", got)
	}
	if got := sb.PrevSessionID("build/reviews/arch-review"); got != "" {
		t.Fatalf("expected prev session cleared for build/reviews/arch-review, got %q", got)
	}
}
