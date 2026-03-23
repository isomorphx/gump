package validate

import (
	"testing"

	"github.com/isomorphx/gump/internal/diff"
)

func TestRunTouchedValidator_NoChanges(t *testing.T) {
	r := RunTouchedValidator("*_test.*", nil)
	if r.Pass {
		t.Error("expected fail")
	}
	r2 := RunTouchedValidator("*_test.*", &diff.DiffContract{})
	if r2.Pass {
		t.Error("expected fail")
	}
}

func TestRunTouchedValidator_Match(t *testing.T) {
	dc := &diff.DiffContract{FilesChanged: []string{"pkg/a_test.go", "main.go"}}
	r := RunTouchedValidator("*_test.*", dc)
	if !r.Pass {
		t.Errorf("expected pass: %+v", r)
	}
}

func TestRunTouchedValidator_NoMatch(t *testing.T) {
	dc := &diff.DiffContract{FilesChanged: []string{"main.go"}}
	r := RunTouchedValidator("*_test.*", dc)
	if r.Pass {
		t.Error("expected fail")
	}
	if r.Stderr == "" {
		t.Error("expected stderr")
	}
}

func TestRunUntouchedValidator_NoChanges(t *testing.T) {
	r := RunUntouchedValidator("*_test.*", nil)
	if !r.Pass {
		t.Error("expected pass")
	}
}

func TestRunUntouchedValidator_NoneMatch(t *testing.T) {
	dc := &diff.DiffContract{FilesChanged: []string{"main.go"}}
	r := RunUntouchedValidator("*_test.*", dc)
	if !r.Pass {
		t.Error("expected pass")
	}
}

func TestRunUntouchedValidator_MatchFails(t *testing.T) {
	dc := &diff.DiffContract{FilesChanged: []string{"a_test.go"}}
	r := RunUntouchedValidator("*_test.*", dc)
	if r.Pass {
		t.Error("expected fail")
	}
	if r.Stderr == "" {
		t.Error("expected stderr")
	}
}
