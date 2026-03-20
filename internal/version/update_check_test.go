package version

import "testing"

func TestIsNewerSemverStrict(t *testing.T) {
	cases := []struct {
		name         string
		remote       string
		current      string
		wantNewer    bool
	}{
		{"major bump", "v1.0.0", "v0.9.0", true},
		{"minor bump", "v0.2.0", "v0.1.0", true},
		{"patch bump", "v0.1.1", "v0.1.0", true},
		{"same version", "v0.1.0", "v0.1.0", false},
		{"same version v1", "v1.0.0", "v1.0.0", false},
		{"remote older", "v0.1.0", "v0.2.0", false},
		{"invalid remote", "invalid", "v0.1.0", false},
		{"invalid current", "v0.1.0", "invalid", false},
		{"numeric compare not lexicographic", "v0.10.0", "v0.9.0", true},
		{"dev current is never newer", "v0.1.0", "dev", false},
		{"dev remote is never newer", "dev", "v0.1.0", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNewer(tc.remote, tc.current); got != tc.wantNewer {
				t.Fatalf("isNewer(%q, %q) = %v, want %v", tc.remote, tc.current, got, tc.wantNewer)
			}
		})
	}
}

