package template

import (
	"regexp"
	"strings"

	"github.com/isomorphx/pudding/internal/statebag"
)

var stepsRefRegex = regexp.MustCompile(`\{steps\.([^}.]+)\.(output|diff)\}`)

// Resolve replaces {key} placeholders. vars are resolved first; then {steps.<name>.output|diff} via stateBag when non-nil.
// If stateBag is nil (e.g. dry-run), steps refs are resolved to empty string.
func Resolve(tmpl string, vars map[string]string, stateBag *statebag.StateBag, scopePath string) string {
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
			return stateBag.Get(subs[1], scopePath, subs[2])
		})
	} else if stateBag != nil {
		tmpl = stepsRefRegex.ReplaceAllStringFunc(tmpl, func(match string) string {
			subs := stepsRefRegex.FindStringSubmatch(match)
			if len(subs) != 3 {
				return match
			}
			return stateBag.Get(subs[1], "", subs[2])
		})
	} else {
		tmpl = stepsRefRegex.ReplaceAllStringFunc(tmpl, func(string) string { return "" })
	}
	return tmpl
}
