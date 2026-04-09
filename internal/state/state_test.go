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
