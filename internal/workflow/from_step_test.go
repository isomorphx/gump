package workflow

import "testing"

func TestResolveEngineStepPath_splitEachQualified(t *testing.T) {
	raw := `name: w
steps:
  - name: decompose
    type: split
    agent: claude-opus
    prompt: "p"
    gate: [schema]
    each:
      - name: impl
        type: code
        agent: claude-sonnet
        prompt: "x"
`
	rec, _, err := Parse([]byte(raw), "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveEngineStepPath("decompose/api/impl", rec)
	if err != nil || got != "decompose/api/impl" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestResolveEngineStepPath_splitAnchor(t *testing.T) {
	raw := `name: w
steps:
  - name: decompose
    type: split
    agent: a
    prompt: p
    gate: [schema]
    each:
      - name: impl
        type: code
        agent: s
        prompt: x
`
	rec, _, err := Parse([]byte(raw), "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveEngineStepPath("decompose", rec)
	if err != nil || got != "decompose" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestResolveEngineStepPath_shortNameInEachAmbiguous(t *testing.T) {
	raw := `name: w
steps:
  - name: decompose
    type: split
    agent: a
    prompt: p
    gate: [schema]
    each:
      - name: impl
        type: code
        agent: s
        prompt: x
`
	rec, _, err := Parse([]byte(raw), "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ResolveEngineStepPath("impl", rec)
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
}

func TestLeafSteps_skipsSplitEachChildren(t *testing.T) {
	raw := `name: w
steps:
  - name: top
    type: code
    agent: a
    prompt: x
  - name: decompose
    type: split
    agent: a
    prompt: p
    each:
      - name: impl
        type: code
        agent: s
        prompt: y
`
	rec, _, err := Parse([]byte(raw), "")
	if err != nil {
		t.Fatal(err)
	}
	leaves := LeafSteps(rec)
	if len(leaves) != 1 || leaves[0].Name != "top" {
		t.Fatalf("leaves: %+v", leaves)
	}
}
