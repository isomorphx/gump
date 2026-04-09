package template

import (
	"regexp"
	"strings"

	"github.com/isomorphx/gump/internal/state"
)

// braceVar matches a single placeholder after `{{` / `}}` escaping has been applied.
var braceVarRegex = regexp.MustCompile(`\{([a-zA-Z0-9_./-]+)\}`)

// Resolve substitutes `{var}` using strict v0.0.4 rules; nil ctx yields empty resolutions (dry-run safe).
func Resolve(tmpl string, ctx *state.ResolveContext) string {
	linesWithPlaceholder := markLinesWithPlaceholder(tmpl)
	const lbraceSentinel = "\x00GUMP_LBRACE\x00"
	const rbraceSentinel = "\x00GUMP_RBRACE\x00"
	// WHY: JSON and examples use doubled braces; they must not be treated as template opens.
	tmpl = strings.ReplaceAll(tmpl, "{{", lbraceSentinel)
	tmpl = strings.ReplaceAll(tmpl, "}}", rbraceSentinel)

	tmpl = braceVarRegex.ReplaceAllStringFunc(tmpl, func(match string) string {
		subs := braceVarRegex.FindStringSubmatch(match)
		if len(subs) != 2 {
			return match
		}
		if ctx == nil {
			return ""
		}
		return ctx.Resolve(subs[1])
	})

	tmpl = cleanupUnresolvedPlaceholders(tmpl, linesWithPlaceholder)
	tmpl = strings.ReplaceAll(tmpl, lbraceSentinel, "{")
	tmpl = strings.ReplaceAll(tmpl, rbraceSentinel, "}")
	return tmpl
}

func cleanupUnresolvedPlaceholders(input string, originalPlaceholderLines map[int]bool) string {
	lines := strings.Split(input, "\n")
	kept := make([]string, 0, len(lines))
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if braceVarRegex.MatchString(trimmed) && braceVarRegex.ReplaceAllString(trimmed, "") == "" {
			continue
		}
		cleaned := braceVarRegex.ReplaceAllString(line, "")
		if originalPlaceholderLines[idx] && strings.TrimSpace(cleaned) == "" {
			continue
		}
		kept = append(kept, cleaned)
	}
	return strings.Join(kept, "\n")
}

func markLinesWithPlaceholder(input string) map[int]bool {
	out := map[int]bool{}
	lines := strings.Split(input, "\n")
	for i, line := range lines {
		if braceVarRegex.MatchString(line) {
			out[i] = true
		}
	}
	return out
}
