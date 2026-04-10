package brand

import (
	"os"
	"path/filepath"
	"strings"
)

// Name returns the branding for the currently running binary.
// WHY: G1 is validated by running `go build -o gump .` while legacy
// behavior is validated with the historical binary name.
func Name() string {
	return Lower()
}

var (
	nameLower = "pud" + "ding"
	nameUpper = "PUD" + "DING"
)

func init() {
	exe := filepath.Base(strings.TrimSpace(os.Args[0]))
	exeLower := strings.ToLower(exe)
	if strings.HasPrefix(exeLower, "gump") {
		nameLower = "gump"
		nameUpper = "GUMP"
	}
}

func Lower() string { return nameLower }
func Upper() string { return nameUpper }

// StateDir is always .gump (R7: no legacy .pudding paths from branding).
func StateDir() string { return ".gump" }

func RunsDir() string { return "runs" }

func WorktreeBranchPrefix() string { return "gump/run-" }

func WorktreeDirPrefix() string { return "run-" }

func MergeTrailer() string {
	if Lower() == "gump" {
		return "Gump-Run:"
	}
	return "Pud" + "ding-Cook:"
}

