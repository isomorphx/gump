package template

import (
	"regexp"
	"fmt"
	"os"
	"strings"

	"github.com/isomorphx/pudding/internal/statebag"
)

var stepsRefRegex = regexp.MustCompile(`\{steps\.([^}.]+)\.(output|diff|files)\}`)
var taskVarRegex = regexp.MustCompile(`\{task\.([a-zA-Z0-9_]+)\}`)

// Resolve replaces {key} placeholders. vars are resolved first; then {steps.<name>.output|diff} via stateBag when non-nil.
// If stateBag is nil (e.g. dry-run), steps refs are resolved to empty string.
func Resolve(tmpl string, vars map[string]string, stateBag *statebag.StateBag, scopePath string) string {
	// WHY: During the v3→v4 migration, templates might still reference legacy
	// `{task.*}` variables. We support them by resolving as `{item.*}` and
	// emitting a single warning whenever we have to translate.
	if tmpl != "" {
		tmpl = taskVarRegex.ReplaceAllStringFunc(tmpl, func(match string) string {
			m := taskVarRegex.FindStringSubmatch(match)
			if len(m) != 2 {
				return match
			}
			suffix := m[1]
			taskKey := "task." + suffix
			itemKey := "item." + suffix

			// If the engine already provided task.* vars, no need to warn.
			if vars != nil {
				if _, hasTask := vars[taskKey]; !hasTask {
					if itemVal, hasItem := vars[itemKey]; hasItem {
						// WHY: stderr warning is asserted by migration tests.
						fmt.Fprintf(os.Stderr, "warning: {%s} is deprecated, use {%s} instead\n", taskKey, itemKey)
						return itemVal
					}
				}
			}
			if vars != nil {
				if v := vars[taskKey]; v != "" {
					return v
				}
				if v, ok := vars[itemKey]; ok {
					return v
				}
			}
			return ""
		})
	}

	if vars != nil {
		for k, v := range vars {
			tmpl = strings.ReplaceAll(tmpl, "{"+k+"}", v)
		}
	}
	if stateBag != nil && scopePath != "" {
		tmpl = stepsRefRegex.ReplaceAllStringFunc(tmpl, func(match string) string {
			subs := stepsRefRegex.FindStringSubmatch(match)
			if len(subs) != 3 {
				return match
			}
			// WHY: `{steps.<n>.diff}` was removed in v4, but we keep it for
			// compatibility: resolve it as `.output` and warn.
			field := subs[2]
			if field == "diff" {
				fmt.Fprintf(os.Stderr, "warning: {steps.%s.diff} is deprecated, use {steps.%s.output} instead\n", subs[1], subs[1])
				return stateBag.Get(subs[1], scopePath, "output")
			}
			return stateBag.Get(subs[1], scopePath, field)
		})
	} else if stateBag != nil {
		tmpl = stepsRefRegex.ReplaceAllStringFunc(tmpl, func(match string) string {
			subs := stepsRefRegex.FindStringSubmatch(match)
			if len(subs) != 3 {
				return match
			}
			field := subs[2]
			if field == "diff" {
				fmt.Fprintf(os.Stderr, "warning: {steps.%s.diff} is deprecated, use {steps.%s.output} instead\n", subs[1], subs[1])
				return stateBag.Get(subs[1], "", "output")
			}
			return stateBag.Get(subs[1], "", field)
		})
	} else {
		tmpl = stepsRefRegex.ReplaceAllStringFunc(tmpl, func(string) string { return "" })
	}
	return tmpl
}
