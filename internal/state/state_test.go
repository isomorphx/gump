package state

import (
	"testing"
)

// WHY: persistence must round-trip without losing keys so resume/replay stay faithful across process boundaries.
func TestState_SerializeRestore_roundTrip(t *testing.T) {
	s := New()
	for i := 0; i < 10; i++ {
		s.Set(string(rune('a'+i))+".output", string(rune('0'+i)))
	}
	data, err := s.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Restore(data)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		k := string(rune('a'+i)) + ".output"
		if r.Get(k) != s.Get(k) {
			t.Fatalf("key %s", k)
		}
	}
}

func TestState_PrevSerializeRestore_roundTrip(t *testing.T) {
	s := New()
	s.Set("step.output", "attempt1")
	s.RotatePrev("step")
	s.Set("step.output", "attempt2")
	data, err := s.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Restore(data)
	if err != nil {
		t.Fatal(err)
	}
	if r.Get("step.output") != "attempt2" {
		t.Fatalf("live output: %q", r.Get("step.output"))
	}
	if r.GetPrev("step", "output") != "attempt1" {
		t.Fatalf("prev output: %q", r.GetPrev("step", "output"))
	}
}
