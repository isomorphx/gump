package engine

import "strings"

func (e *Engine) ledgerStepPath(localStepPath string) string {
	pfx := strings.TrimSpace(e.LedgerStepPrefix)
	if pfx == "" {
		return localStepPath
	}
	return strings.TrimSuffix(pfx, "/") + "/" + localStepPath
}
