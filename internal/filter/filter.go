package filter

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type Stats struct {
	Read     int
	Rejected int
	ByReason map[string]int
}

type Builder struct{}

type RuleSink func(string) error

var reportSanitizer = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ")

// ReadFileToSink parses and normalizes one input file without retaining its
// accepted rules. The sink owns downstream storage, which allows the
// production build to use an external sorter instead of a process-wide map
// and full in-memory rule slice.
func (b Builder) ReadFileToSink(input, rejectedPath string, sink RuleSink) (Stats, error) {
	if sink == nil {
		return Stats{}, fmt.Errorf("rule sink is nil")
	}
	in, err := os.Open(input)
	if err != nil {
		return Stats{}, fmt.Errorf("open %s: %w", input, err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(rejectedPath), 0o755); err != nil {
		return Stats{}, fmt.Errorf("create rejected report directory: %w", err)
	}
	rejFile, err := os.Create(rejectedPath)
	if err != nil {
		return Stats{}, fmt.Errorf("create rejected report: %w", err)
	}
	defer rejFile.Close()
	rej := bufio.NewWriterSize(rejFile, 64<<10)

	if _, err := fmt.Fprintln(rej, "# line\treason\trule"); err != nil {
		return Stats{}, fmt.Errorf("write rejected report header: %w", err)
	}

	stats := Stats{ByReason: make(map[string]int)}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 2<<20)
	for sc.Scan() {
		stats.Read++
		rawLine := strings.TrimPrefix(strings.TrimSuffix(sc.Text(), "\r"), "\uFEFF")
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "!") {
			continue
		}

		rule, reason, ok := normalize(rawLine)
		if !ok {
			stats.Rejected++
			stats.ByReason[reason]++
			if _, err := fmt.Fprintf(rej, "%d\t%s\t%s\n", stats.Read, reason, sanitizeReport(rawLine)); err != nil {
				return stats, fmt.Errorf("write rejected report: %w", err)
			}
			continue
		}
		if err := sink(rule); err != nil {
			return stats, fmt.Errorf("store normalized rule from %s: %w", input, err)
		}
	}
	if err := sc.Err(); err != nil {
		return stats, fmt.Errorf("scan %s: %w", input, err)
	}
	if err := rej.Flush(); err != nil {
		return stats, fmt.Errorf("write rejected report: %w", err)
	}
	return stats, nil
}

func optimizeRule(rule string) string {
	if isBareDomainBlock(rule) {
		return rule + "$third-party"
	}
	return rule
}

func isBareDomainBlock(rule string) bool {
	if !strings.HasPrefix(rule, "||") || !strings.HasSuffix(rule, "^") {
		return false
	}
	// validDomain already rejects every separator character (/?#^|), so the
	// host slice is validated directly without a redundant character scan.
	host := rule[2 : len(rule)-1]
	return validDomain(host)
}

func normalize(line string) (string, string, bool) {
	if !utf8.ValidString(line) {
		return "", "invalid-utf8", false
	}
	// Every cosmetic/scriptlet marker contains one of these three bytes.
	// Avoid the full marker scan for the overwhelmingly common network-rule case.
	if strings.ContainsAny(line, "#+:") {
		for _, marker := range []string{
			"##", "#@#", "#?#", "#$#", "#%#", "#^#", "#@%?#",
			"+js(", ":has-text(", ":contains(", ":matches-css(", ":xpath(", ":style(",
		} {
			if strings.Contains(line, marker) {
				return "", "unsupported-cosmetic-scriptlet", false
			}
		}
	}
	if len(line) >= 2 && line[0] == '/' && line[len(line)-1] == '/' {
		return "", "regex-filter", false
	}
	if len(line) >= 8 && strings.EqualFold(line[:8], "[adblock") {
		return "", "metadata", false
	}

	exception := false
	if strings.HasPrefix(line, "@@") {
		exception = true
		line = line[2:]
		if line == "" {
			return "", "unsupported-exception-rule", false
		}
	}

	// Hosts-file records legitimately contain a separator; only accept the
	// canonical address + exactly one hostname so extra fields are not ignored.
	// An exception-prefixed hosts record is not representable as a block rule,
	// so reject it before generic character validation can report a different
	// reason.
	for _, prefix := range []string{"0.0.0.0 ", "127.0.0.1 ", "::1 "} {
		if strings.HasPrefix(line, prefix) {
			if exception {
				return "", "unsupported-exception-rule", false
			}
			host := strings.TrimPrefix(line, prefix)
			if host != "" && !strings.ContainsAny(host, " \t") && validDomain(host) {
				return "||" + strings.ToLower(host) + "^", "", true
			}
			return "", "invalid-host-entry", false
		}
	}

	for _, r := range line {
		if r < 0x21 || r > 0x7e {
			return "", "non-ascii-or-whitespace", false
		}
	}

	rule, reason, ok := normalizeNetwork(line, exception)
	if !ok {
		if reason != "" {
			return "", reason, false
		}
		if exception {
			return "", "unsupported-exception-rule", false
		}
		return "", "unsupported-network-rule", false
	}
	if exception {
		return "@@" + rule, "", true
	}
	return rule, "", true
}

func normalizeNetwork(line string, exception bool) (string, string, bool) {
	pattern := line
	modSuffix := ""
	if idx := strings.LastIndexByte(line, '$'); idx >= 0 {
		suffix, ok := parseModifiers(line[idx+1:], exception)
		if !ok {
			return "", "unsupported-modifier", false
		}
		pattern, modSuffix = line[:idx], suffix
	}

	if strings.HasPrefix(pattern, "||") {
		x := pattern[2:]
		pos := strings.IndexAny(x, "/?#^|")
		host, rest := x, ""
		if pos >= 0 {
			host, rest = x[:pos], x[pos:]
		}
		if !validDomain(host) {
			return "", "", false
		}
		switch rest {
		case "^", "|":
			return "||" + strings.ToLower(host) + rest + modSuffix, "", true
		case "":
			return "", "unterminated-host-rule", false
		}
		return "||" + strings.ToLower(host) + rest + modSuffix, "", true
	}

	if strings.HasPrefix(pattern, "|https://") || strings.HasPrefix(pattern, "|http://") {
		hasEnd := strings.HasSuffix(pattern, "|")
		plain := strings.TrimSuffix(strings.TrimPrefix(pattern, "|"), "|")
		if strings.ContainsAny(plain, "<>\\") {
			return "", "", false
		}
		schemeEnd := strings.Index(plain, "://") + 3
		scheme, afterScheme := plain[:schemeEnd], plain[schemeEnd:]
		pos := strings.IndexAny(afterScheme, "/?#")
		host, tail := afterScheme, ""
		if pos >= 0 {
			host, tail = afterScheme[:pos], afterScheme[pos:]
		}
		if !validDomain(host) {
			return "", "", false
		}
		// Hostnames are case-insensitive, and GURL lower-cases the host
		// component of every request URL before matching regardless of
		// $match-case, so canonicalizing it here (like the "||host" branch
		// above already does) is always safe and lets rules that only
		// differ in host casing collapse into a single output line.
		canonical := scheme + strings.ToLower(host) + tail
		if hasEnd {
			return "|" + canonical + "|" + modSuffix, "", true
		}
		return "|" + canonical + modSuffix, "", true
	}
	return "", "", false
}

// elementTypeOptions are the resource-type keywords Chromium's legacy
// subresource_filter rule_parser recognizes as tristate ElementType options
// (see components/subresource_filter/tools/rule_parser/rule_parser.cc). Each
// one may appear negated (e.g. "~image"), but a single rule may not mix
// positive and negative element types: which sign is used first decides
// whether the unspecified types start out included or excluded, so only a
// uniform sign per rule keeps the result independent of token order and
// therefore safe to canonicalize by sorting.
var elementTypeOptions = map[string]struct{}{
	"script": {}, "image": {}, "stylesheet": {}, "object": {},
	"xmlhttprequest": {}, "object-subrequest": {}, "subdocument": {},
	"ping": {}, "media": {}, "font": {}, "websocket": {}, "other": {},
}

// activationTypeOptions are the ActivationType keywords, which the upstream
// parser marks whitelist-only (FLAG_IS_WHITELIST_ONLY) and non-tristate: they
// may only appear on "@@" exception rules and never negated. CSS-related activation types are intentionally omitted because the legacy engine strips them. Each one simply
// ORs a bit into the rule, independent of every other option, so any subset
// of them can be safely reordered/sorted.
var activationTypeOptions = map[string]struct{}{
	"document": {}, "genericblock": {},
}

// parseModifiers accepts only modifiers supported by the legacy Chromium
// rule parser for the subset this project intentionally emits. Duplicate or
// conflicting modifiers are rejected instead of being resolved implicitly.
func parseModifiers(opts string, exception bool) (string, bool) {
	if opts == "" {
		return "", false
	}
	var thirdParty string
	var matchCase bool
	var domains string
	seen := make(map[string]struct{}, 3)

	// Resource-type (ElementType) options: same-sign-only, see comment above.
	var typeNegated *bool
	typeSeen := make(map[string]struct{}, 4)
	var typeNames []string

	// Whitelist-only ActivationType options: never negated, plain set union.
	activationSeen := make(map[string]struct{}, 4)
	var activationNames []string

	for _, token := range strings.Split(opts, ",") {
		if token == "" {
			return "", false
		}
		negated := strings.HasPrefix(token, "~")
		nameValue := token
		if negated {
			nameValue = token[1:]
			if nameValue == "" {
				return "", false
			}
		}

		name, value, hasValue := nameValue, "", false
		if i := strings.IndexByte(nameValue, '='); i >= 0 {
			name, value, hasValue = nameValue[:i], nameValue[i+1:], true
		}

		if _, ok := elementTypeOptions[name]; ok {
			// Values aren't valid on ElementType options, and mixing them
			// with ActivationType options in the same rule isn't a pattern
			// real filter lists use, so it's rejected rather than guessed at.
			if hasValue || len(activationNames) > 0 {
				return "", false
			}
			if _, exists := typeSeen[name]; exists {
				return "", false
			}
			if typeNegated == nil {
				n := negated
				typeNegated = &n
			} else if *typeNegated != negated {
				return "", false
			}
			typeSeen[name] = struct{}{}
			typeNames = append(typeNames, name)
			continue
		}

		if _, ok := activationTypeOptions[name]; ok {
			if hasValue || negated || len(typeNames) > 0 || !exception {
				return "", false
			}
			if _, exists := activationSeen[name]; exists {
				return "", false
			}
			activationSeen[name] = struct{}{}
			activationNames = append(activationNames, name)
			continue
		}

		switch name {
		case "third-party":
			// Duplicate detection via seen[name] already rejects both
			// "third-party,third-party" and "third-party,~third-party": "~" is
			// stripped before the switch, so both signs share the name.
			if hasValue {
				return "", false
			}
			if _, exists := seen[name]; exists {
				return "", false
			}
			seen[name] = struct{}{}
			if negated {
				thirdParty = "~third-party"
			} else {
				thirdParty = "third-party"
			}
		case "match-case":
			if negated || hasValue || matchCase {
				return "", false
			}
			matchCase = true
		case "domain":
			if negated || !hasValue || value == "" {
				return "", false
			}
			if _, exists := seen[name]; exists {
				return "", false
			}
			seen[name] = struct{}{}
			canonical, ok := canonicalDomainList(value)
			if !ok {
				return "", false
			}
			domains = canonical
		default:
			return "", false
		}
	}

	var parts []string
	if len(typeNames) > 0 {
		sort.Strings(typeNames)
		prefix := ""
		if typeNegated != nil && *typeNegated {
			prefix = "~"
		}
		for _, n := range typeNames {
			parts = append(parts, prefix+n)
		}
	}
	if len(activationNames) > 0 {
		sort.Strings(activationNames)
		parts = append(parts, activationNames...)
	}
	if thirdParty != "" {
		parts = append(parts, thirdParty)
	}
	if matchCase {
		parts = append(parts, "match-case")
	}
	if domains != "" {
		parts = append(parts, "domain="+domains)
	}
	// opts is non-empty and every token either appends a part or returns
	// early, so parts is never empty here.
	return "$" + strings.Join(parts, ","), true
}

// Chromium canonicalizes domain filters by decreasing domain length and then
// lexicographically within equal-length groups. Matching that order makes
// generated output stable and aligns with the converter's own representation.
func canonicalDomainList(value string) (string, bool) {
	entries := strings.Split(value, "|")
	out := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	excludedBase := make(map[string]bool, len(entries))
	for _, entry := range entries {
		exclude := ""
		domain := entry
		if strings.HasPrefix(domain, "~") {
			exclude = "~"
			domain = domain[1:]
		}
		if !validDomain(domain) {
			return "", false
		}
		base := strings.ToLower(domain)
		canonical := exclude + base
		if _, exists := seen[canonical]; exists {
			return "", false
		}
		// Reject the same base domain appearing both included and
		// excluded in one list (e.g. "foo.example|~foo.example"): that is
		// a direct conflict, not an exact duplicate, so it is invisible to
		// the exact-string check above.
		if prevExcluded, exists := excludedBase[base]; exists && prevExcluded != (exclude == "~") {
			return "", false
		}
		excludedBase[base] = exclude == "~"
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	if len(out) == 0 {
		return "", false
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.TrimPrefix(out[i], "~"), strings.TrimPrefix(out[j], "~")
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return out[i] < out[j]
	})
	return strings.Join(out, "|"), true
}

func validDomain(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	// Empty labels (leading/trailing dot, "..") fail the per-label length
	// check below; the character check rejects "/?#^|" and anything else
	// outside [A-Za-z0-9-] (case is preserved here; callers lower-case).
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	allNumeric := true
	for _, part := range parts {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, r := range part {
			switch {
			case r >= '0' && r <= '9':
			case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '-':
				allNumeric = false
			default:
				return false
			}
		}
	}
	return !allNumeric
}

func sanitizeReport(s string) string {
	return reportSanitizer.Replace(s)
}
