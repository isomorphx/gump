// main is the single entry point so the binary can be built and tested from repo root.
package main

import (
	"os"

	_ "github.com/isomorphx/pudding/internal/builtin"
	"github.com/isomorphx/pudding/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
