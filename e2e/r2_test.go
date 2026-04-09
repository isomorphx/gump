package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pkgcontext "github.com/isomorphx/gump/internal/context"
	"github.com/isomorphx/gump/internal/state"
	"github.com/isomorphx/gump/internal/template"
)

func TestE2E_R2_01_StateSetGetBasic(t *testing.T) {
	s := state.New()
	s.Set("impl.output", "some code")
	if s.Get("impl.output") != "some code" {
		t.Fatalf("get output")
	}
	if s.Get("nonexistent.output") != "" {
		t.Fatalf("missing key")
	}
	s.Set("impl.agent", "claude-sonnet")
	if s.Get("impl.agent") != "claude-sonnet" {
		t.Fatalf("agent")
	}
}

func TestE2E_R2_02_StateQualifiedEachKeys(t *testing.T) {
	s := state.New()
	s.Set("build/task-1/converge.output", "diff1")
	s.Set("build/task-2/converge.output", "diff2")
	if s.Get("build/task-1/converge.output") != "diff1" || s.Get("build/task-2/converge.output") != "diff2" {
		t.Fatal("qualified keys")
	}
}

func TestE2E_R2_03_RotatePrevGetPrev(t *testing.T) {
	s := state.New()
	s.Set("impl.output", "attempt1_diff")
	s.Set("impl.gate.compile", "true")
	s.Set("impl.gate.review.comments", "needs fix")
	s.RotatePrev("impl")
	s.Set("impl.output", "attempt2_diff")
	s.Set("impl.gate.compile", "true")
	s.Set("impl.gate.review.comments", "ok")
	if s.Get("impl.output") != "attempt2_diff" {
		t.Fatal("current output")
	}
	if s.GetPrev("impl", "output") != "attempt1_diff" {
		t.Fatal("prev output")
	}
	if s.GetPrev("impl", "gate.review.comments") != "needs fix" {
		t.Fatal("prev nested gate")
	}
	if s.Get("impl.gate.review.comments") != "ok" {
		t.Fatal("current gate comments")
	}
}

func TestE2E_R2_04_SerializeRestore(t *testing.T) {
	s := state.New()
	s.Set("a.x", "1")
	s.Set("b.y", "2")
	s.RotatePrev("a")
	s.Set("a.x", "3")
	data, err := s.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	r, err := state.Restore(data)
	if err != nil {
		t.Fatal(err)
	}
	if r.Get("a.x") != "3" || r.Get("b.y") != "2" {
		t.Fatal("round-trip entries")
	}
	if r.GetPrev("a", "x") != "" {
		t.Fatal("prev must not serialize")
	}
}

func TestE2E_R2_05_ResolverSpecials(t *testing.T) {
	ctx := &state.ResolveContext{
		Spec: "implement feature X", Attempt: 3, Error: "compile failed", Diff: "diff --git...",
	}
	if ctx.Resolve("spec") != "implement feature X" || ctx.Resolve("attempt") != "3" ||
		ctx.Resolve("error") != "compile failed" || ctx.Resolve("diff") != "diff --git..." {
		t.Fatal("specials")
	}
}

func TestE2E_R2_06_ResolverTask(t *testing.T) {
	ctx := &state.ResolveContext{
		Task: &state.TaskVars{Name: "auth", Description: "Add auth middleware", Files: "pkg/auth/middleware.go, pkg/auth/middleware_test.go"},
	}
	if ctx.Resolve("task.name") != "auth" || ctx.Resolve("task.description") != "Add auth middleware" ||
		ctx.Resolve("task.files") != "pkg/auth/middleware.go, pkg/auth/middleware_test.go" {
		t.Fatal("task")
	}
}

func TestE2E_R2_07_ResolverPrev(t *testing.T) {
	st := state.New()
	st.Set("impl.output", "old_diff")
	st.Set("impl.gate.review.comments", "needs work")
	st.RotatePrev("impl")
	ctx := &state.ResolveContext{State: st, StepPath: "impl", Attempt: 2}
	if ctx.Resolve("prev.output") != "old_diff" || ctx.Resolve("prev.gate.review.comments") != "needs work" {
		t.Fatal("prev")
	}
}

func TestE2E_R2_08_ResolverGate(t *testing.T) {
	ctx := &state.ResolveContext{
		GateResults: map[string]string{"compile": "true", "test": "false"},
		GateMeta:    map[string]map[string]string{"review": {"pass": "false", "comments": "Architecture violation in auth module"}},
	}
	if ctx.Resolve("gate.compile") != "true" || ctx.Resolve("gate.test") != "false" ||
		ctx.Resolve("gate.review.pass") != "false" ||
		ctx.Resolve("gate.review.comments") != "Architecture violation in auth module" {
		t.Fatal("gate")
	}
}

func TestE2E_R2_09_ResolverScopeEach(t *testing.T) {
	st := state.New()
	st.Set("build/task-1/impl.output", "impl_diff")
	st.Set("build/task-1/smoke.output", "smoke_ok")
	st.Set("decompose.output", "plan_json")
	ctx := &state.ResolveContext{State: st, StepPath: "build/task-1/smoke"}
	if ctx.Resolve("impl.output") != "impl_diff" || ctx.Resolve("decompose.output") != "plan_json" ||
		ctx.Resolve("nonexistent.output") != "" {
		t.Fatal("scope each")
	}
}

func TestE2E_R2_10_ResolverScopeEachWins(t *testing.T) {
	st := state.New()
	st.Set("build/task-1/check.output", "each_check")
	st.Set("check.output", "top_check")
	ctx := &state.ResolveContext{State: st, StepPath: "build/task-1/impl"}
	if ctx.Resolve("check.output") != "each_check" {
		t.Fatal("each wins")
	}
	ctx2 := &state.ResolveContext{State: st, StepPath: "final-step"}
	if ctx2.Resolve("check.output") != "top_check" {
		t.Fatal("workflow scope")
	}
}

func TestE2E_R2_11_ResolverQualifiedPath(t *testing.T) {
	st := state.New()
	st.Set("build/task-2/converge.output", "task2_diff")
	ctx := &state.ResolveContext{State: st, StepPath: "build/task-1/smoke"}
	if ctx.Resolve("build/task-2/converge.output") != "task2_diff" {
		t.Fatal("qualified path")
	}
}

func TestE2E_R2_12_ResolverConvergeAgent(t *testing.T) {
	st := state.New()
	st.Set("build/task-1/converge.agent", "claude-opus")
	ctx := &state.ResolveContext{State: st, StepPath: "build/task-1/smoke"}
	if ctx.Resolve("converge.agent") != "claude-opus" {
		t.Fatal("converge.agent")
	}
}

func TestE2E_R2_13_TemplateFullResolve(t *testing.T) {
	st := state.New()
	st.Set("impl.gate.review.comments", "needs work")
	st.RotatePrev("impl")
	st.Set("impl.gate.review.comments", "ok")
	ctx := &state.ResolveContext{
		State: st, StepPath: "impl", Attempt: 2,
		Task: &state.TaskVars{Name: "auth", Description: "Add auth", Files: "auth.go"},
		Error: "",
	}
	tmpl := "Implement: {task.description} Files: {task.files} Previous review: {prev.gate.review.comments}\n{error}\n"
	out := template.Resolve(tmpl, ctx)
	want := "Implement: Add auth Files: auth.go Previous review: needs work\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestE2E_R2_14_TemplateEscaping(t *testing.T) {
	ctx := &state.ResolveContext{Extra: map[string]string{"name": "test"}}
	out := template.Resolve(`Output format: {{"name": "value"}}`, ctx)
	if out != `Output format: {"name": "value"}` {
		t.Fatalf("got %q", out)
	}
}

func TestE2E_R2_15_ContextRetryConditional(t *testing.T) {
	dir := t.TempDir()
	rcFalse := &pkgcontext.RetryContext{Attempt: 2, MaxAttempts: 6, Diff: "some diff", Error: "compile failed", IsPromptOverridden: false}
	if err := pkgcontext.Build("code", "P", nil, "", nil, dir, "", nil, "CLAUDE.md", rcFalse, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	s := string(b)
	if !strings.Contains(s, "Previous Attempt Failed (Attempt 2/6)") || !strings.Contains(s, "some diff") || !strings.Contains(s, "compile failed") {
		t.Fatalf("expected injected retry section: %s", s)
	}
	dir2 := t.TempDir()
	rcTrue := &pkgcontext.RetryContext{Attempt: 2, MaxAttempts: 6, Diff: "some diff", Error: "compile failed", IsPromptOverridden: true}
	if err := pkgcontext.Build("code", "P", nil, "", nil, dir2, "", nil, "CLAUDE.md", rcTrue, nil); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(filepath.Join(dir2, "CLAUDE.md"))
	if strings.Contains(string(b2), "Previous Attempt Failed") {
		t.Fatal("must not inject retry when prompt overridden")
	}
}

func TestE2E_R2_16_ContextEscalationNote(t *testing.T) {
	dir := t.TempDir()
	rc := &pkgcontext.RetryContext{
		Attempt: 2, MaxAttempts: 4, Diff: "d", Error: "e",
		EscalatedFrom: "claude-sonnet", EscalatedTo: "claude-opus",
	}
	if err := pkgcontext.Build("code", "P", nil, "", nil, dir, "", nil, "CLAUDE.md", rc, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(b), "You are a more capable agent (escalated from claude-sonnet to claude-opus)") {
		t.Fatal("escalation note")
	}
}

func TestE2E_R2_17_ItemAndRunRemoved(t *testing.T) {
	ctx := &state.ResolveContext{Spec: "x"}
	out := template.Resolve("{item.name}\n{run.cost}", ctx)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("got %q", out)
	}
}

func TestE2E_R2_18_StepsPrefixRemoved(t *testing.T) {
	st := state.New()
	st.Set("impl.output", "v")
	ctx := &state.ResolveContext{State: st, StepPath: "x"}
	if template.Resolve("{steps.impl.output}", ctx) != "" || template.Resolve("{impl.output}", ctx) != "v" {
		t.Fatal("steps prefix")
	}
}
