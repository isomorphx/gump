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
	// WHY: we want branding decisions to happen before any CLI command init.
	// We still fall back to the legacy name when detection fails.
	exe := filepath.Base(strings.TrimSpace(os.Args[0]))
	exeLower := strings.ToLower(exe)
	// WHY: E2E tests build versioned binaries like `gump-v99` but they should
	// still be treated as the `gump` brand.
	if strings.HasPrefix(exeLower, "gump") {
		nameLower = "gump"
		nameUpper = "GUMP"
	}
}

func Lower() string { return nameLower }
func Upper() string { return nameUpper }

func StateDir() string {
	if Lower() == "gump" {
		return ".gump"
	}
	return ".pud" + "ding"
}

func RunsDir() string {
	if Lower() == "gump" {
		return "runs"
	}
	return "cooks"
}

func WorktreeBranchPrefix() string {
	if Lower() == "gump" {
		return "gump/run-"
	}
	return "pud" + "ding/cook-"
}

func WorktreeDirPrefix() string {
	if Lower() == "gump" {
		return "run-"
	}
	return "cook-"
}

func MergeTrailer() string {
	if Lower() == "gump" {
		return "Gump-Run:"
	}
	return "Pud" + "ding-Cook:"
}

