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

func (Builder) ReadFile(input, rejectedPath string) ([]string, Stats, error) {
	in, err := os.Open(input)
	if err != nil {
		return nil, Stats{}, fmt.Errorf("open %s: %w", input, err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(rejectedPath), 0o755); err != nil {
		return nil, Stats{}, fmt.Errorf("create rejected report directory: %w", err)
	}
	rej, err := os.Create(rejectedPath)
	if err != nil {
		return nil, Stats{}, fmt.Errorf("create rejected report: %w", err)
	}
	defer rej.Close()

	if _, err := fmt.Fprintln(rej, "# line\treason\trule"); err != nil {
		return nil, Stats{}, fmt.Errorf("write rejected report header: %w", err)
	}

	rules := make([]string, 0, 4096)
	stats := Stats{ByReason: make(map[string]int)}
	seen := make(map[string]struct{}, 4096)

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 2<<20)
	for sc.Scan() {
		stats.Read++
		rawLine := strings.TrimPrefix(strings.TrimSuffix(sc.Text(), "\r"), "\uFEFF")
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "!") {
			continue
		}

		rule, reason, ok := (Builder{}).normalize(rawLine)
		if !ok {
			stats.Rejected++
			stats.ByReason[reason]++
			if _, err := fmt.Fprintf(rej, "%d\t%s\t%s\n", stats.Read, reason, sanitizeReport(rawLine)); err != nil {
				return nil, stats, fmt.Errorf("write rejected report: %w", err)
			}
			continue
		}
		if _, exists := seen[rule]; exists {
			continue
		}
		seen[rule] = struct{}{}
		rules = append(rules, rule)
	}
	if err := sc.Err(); err != nil {
		return nil, stats, fmt.Errorf("scan %s: %w", input, err)
	}
	if err := rej.Sync(); err != nil {
		return nil, stats, fmt.Errorf("sync rejected report: %w", err)
	}
	return rules, stats, nil
}

// Optimize performs only transformations that are provably semantics-preserving
// for the legacy Chromium subresource_filter syntax:
//   - exact duplicate removal;
//   - canonical ordering; and
//   - Chromium's required third-party guard on bare ||host^ / ||host| blocks.
//
// It deliberately does not perform path/domain/modifier subsumption because
// those rules can differ in first-party/third-party or initiator-domain scope.
func Optimize(rules []string) ([]string, int) {
	set := make(map[string]struct{}, len(rules))
	duplicateCount := 0
	for _, rule := range rules {
		if rule == "" {
			continue
		}
		if _, exists := set[rule]; exists {
			duplicateCount++
			continue
		}
		set[rule] = struct{}{}
	}

	out := make([]string, 0, len(set))
	for rule := range set {
		if strings.HasPrefix(rule, "||") {
			if _, suffix, ok := splitAnchored(rule[2:]); ok && (suffix == "^" || suffix == "|") {
				rule += "$third-party"
			}
		}
		out = append(out, rule)
	}

	sort.Strings(out)
	return out, duplicateCount
}

func Write(path string, rules []string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".filters.tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	w := bufio.NewWriterSize(tmp, 1<<20)
	for _, rule := range rules {
		if _, err := fmt.Fprintln(w, rule); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func (Builder) normalize(line string) (string, string, bool) {
	// Hosts-file records legitimately contain a separator; only accept the
	// canonical address + exactly one hostname so extra fields are not ignored.
	for _, prefix := range []string{"0.0.0.0 ", "127.0.0.1 ", "::1 "} {
		if strings.HasPrefix(line, prefix) {
			fields := strings.Fields(line)
			if len(fields) == 2 && validDomain(fields[1]) {
				return "||" + strings.ToLower(fields[1]) + "^", "", true
			}
			return "", "invalid-host-entry", false
		}
	}

	if !utf8.ValidString(line) {
		return "", "invalid-utf8", false
	}
	for _, r := range line {
		if r < 0x21 || r > 0x7e {
			return "", "non-ascii-or-whitespace", false
		}
	}

	for _, marker := range []string{
		"##", "#@#", "#?#", "#$#", "#%#", "#^#", "#@%?#",
		"+js(", ":has-text(", ":contains(", ":matches-css(", ":xpath(", ":style(",
	} {
		if strings.Contains(line, marker) {
			return "", "unsupported-cosmetic-scriptlet", false
		}
	}
	if len(line) >= 2 && line[0] == '/' && line[len(line)-1] == '/' {
		return "", "regex-filter", false
	}
	if strings.HasPrefix(strings.ToLower(line), "[adblock") {
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

	rule, reason, ok := normalizeNetwork(line)
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

func normalizeNetwork(line string) (string, string, bool) {
	pattern := line
	modSuffix := ""
	if idx := strings.LastIndexByte(line, '$'); idx >= 0 {
		suffix, ok := parseModifiers(line[idx+1:])
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
		case "", "^", "|":
			return "||" + strings.ToLower(host) + "^" + modSuffix, "", true
		}
		if !validPath(rest) {
			return "", "", false
		}
		return "||" + strings.ToLower(host) + rest + modSuffix, "", true
	}

	if strings.HasPrefix(pattern, "|https://") || strings.HasPrefix(pattern, "|http://") {
		hasEnd := strings.HasSuffix(pattern, "|")
		plain := strings.TrimSuffix(strings.TrimPrefix(pattern, "|"), "|")
		rest := plain[strings.Index(plain, "://")+3:]
		pos := strings.IndexAny(rest, "/?#")
		host := rest
		if pos >= 0 {
			host = rest[:pos]
		}
		if !validDomain(host) || strings.ContainsAny(plain, " \t<>\\") {
			return "", "", false
		}
		if hasEnd {
			return "|" + plain + "|" + modSuffix, "", true
		}
		return "|" + plain + modSuffix, "", true
	}
	return "", "", false
}

// parseModifiers accepts only modifiers supported by the legacy Chromium
// rule parser for the subset this project intentionally emits. Duplicate or
// conflicting modifiers are rejected instead of being resolved implicitly.
func parseModifiers(opts string) (string, bool) {
	if opts == "" {
		return "", false
	}
	var thirdParty string
	var matchCase bool
	var domains string
	seen := make(map[string]struct{}, 3)

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

		switch name {
		case "third-party":
			if hasValue || negated && thirdParty == "third-party" || !negated && thirdParty == "~third-party" {
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
	if thirdParty != "" {
		parts = append(parts, thirdParty)
	}
	if matchCase {
		parts = append(parts, "match-case")
	}
	if domains != "" {
		parts = append(parts, "domain="+domains)
	}
	if len(parts) == 0 {
		return "", false
	}
	return "$" + strings.Join(parts, ","), true
}

// Chromium canonicalizes domain filters by decreasing domain length and then
// lexicographically within equal-length groups. Matching that order makes
// generated output stable and aligns with the converter's own representation.
func canonicalDomainList(value string) (string, bool) {
	entries := strings.Split(value, "|")
	out := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
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
		canonical := exclude + strings.ToLower(domain)
		if _, exists := seen[canonical]; exists {
			return "", false
		}
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

func validPath(s string) bool {
	for _, r := range s {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

func validDomain(s string) bool {
	if len(s) == 0 || len(s) > 253 || strings.ContainsAny(s, "/?#^|") {
		return false
	}
	lower := strings.ToLower(s)
	parts := strings.Split(lower, ".")
	if len(parts) < 2 || lower[0] == '.' || lower[len(lower)-1] == '.' || strings.Contains(lower, "..") {
		return false
	}
	allNumeric := true
	for _, part := range parts {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, r := range part {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
				return false
			}
			if r < '0' || r > '9' {
				allNumeric = false
			}
		}
	}
	return !allNumeric
}

func splitAnchored(x string) (string, string, bool) {
	pos := strings.IndexAny(x, "/?#^|")
	if pos < 0 {
		return strings.ToLower(x), "", validDomain(x)
	}
	return strings.ToLower(x[:pos]), x[pos:], validDomain(x[:pos])
}

func sanitizeReport(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}
