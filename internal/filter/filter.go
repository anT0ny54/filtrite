package filter

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

type Stats struct {
	Read     int
	Rejected int
	ByReason map[string]int
}

type Builder struct{ KeepURLRules bool }

func (b Builder) ReadFile(input, rejectedPath string) ([]string, Stats, error) {
	in, err := os.Open(input)
	if err != nil {
		return nil, Stats{}, fmt.Errorf("open %s: %w", input, err)
	}
	defer in.Close()
	rej, err := os.Create(rejectedPath)
	if err != nil {
		return nil, Stats{}, fmt.Errorf("create rejected report: %w", err)
	}
	defer rej.Close()
	fmt.Fprintln(rej, "# line\treason\trule")
	var rules []string
	st := Stats{ByReason: map[string]int{}}
	seen := map[string]struct{}{}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 2<<20)
	for sc.Scan() {
		st.Read++
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(sc.Text(), "\r"), "\uFEFF"))
		if line == "" || strings.HasPrefix(line, "!") {
			continue
		}
		rule, reason, ok := b.normalize(line)
		if !ok {
			st.Rejected++
			st.ByReason[reason]++
			fmt.Fprintf(rej, "%d\t%s\t%s\n", st.Read, reason, sanitizeReport(line))
			continue
		}
		if _, exists := seen[rule]; exists {
			continue
		}
		seen[rule] = struct{}{}
		rules = append(rules, rule)
	}
	if err := sc.Err(); err != nil {
		return nil, st, fmt.Errorf("scan %s: %w", input, err)
	}
	return rules, st, nil
}

// Optimize deduplicates rules, drops network rules that are strictly
// subsumed by a broader rule already present in the set, and then guards
// every surviving unconditional host block so it cannot also match the
// top-level navigation to that same host. It returns the optimized rule
// set and the number of rules dropped as redundant.
func Optimize(rules []string) ([]string, int) {
	set := make(map[string]struct{}, len(rules))
	for _, r := range rules {
		if r != "" {
			set[r] = struct{}{}
		}
	}

	// A "domain block" is an unconditional ||host^ or ||host| rule: it has
	// no path/query/fragment restriction and no $-modifier, so it already
	// matches every request made to that host.
	domainBlocks := make(map[string]bool)
	for r := range set {
		if strings.HasPrefix(r, "||") {
			h, suffix, ok := splitAnchored(r[2:])
			if ok && (suffix == "^" || suffix == "|") {
				domainBlocks[h] = true
			}
		}
	}

	out := make([]string, 0, len(set))
	redundant := 0
	for r := range set {
		if strings.HasPrefix(r, "||") {
			h, suffix, ok := splitAnchored(r[2:])
			// Any narrower rule for a host that is already unconditionally
			// blocked is redundant, whether the narrowing comes from a
			// path/query/fragment or from a $-modifier such as
			// $third-party/$domain=...: the bare host block already
			// matches every one of those more specific requests too.
			if ok && domainBlocks[h] && suffix != "^" && suffix != "|" && suffix != "" && strings.ContainsAny(suffix, "/?#$") {
				redundant++
				continue
			}
		}
		out = append(out, r)
	}

	// Guard every surviving unconditional block with $third-party. Without
	// it, the legacy Chromium subresource_filter engine this project
	// targets also matches the main-frame request when the blocked host is
	// visited directly as a first-party page, which can make the page
	// appear broken instead of merely blocking it as a third-party embed
	// elsewhere. This mirrors the exact fix Chromium documents for
	// generating a filter list for this engine (see
	// components/subresource_filter/FILTER_LIST_GENERATION.md, which
	// references crbug.com/448915986). Exception (@@) rules are left
	// untouched, since an allow rule has no equivalent failure mode.
	for i, r := range out {
		if strings.HasPrefix(r, "@@") || !strings.HasPrefix(r, "||") {
			continue
		}
		if _, suffix, ok := splitAnchored(r[2:]); ok && (suffix == "^" || suffix == "|") {
			out[i] = r + "$third-party"
		}
	}

	sort.Strings(out)
	return out, redundant
}

func Write(path string, rules []string) error {
	dir := filepathDir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".filters.tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	w := bufio.NewWriterSize(tmp, 1<<20)
	for _, r := range rules {
		if _, err := fmt.Fprintln(w, r); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (b Builder) normalize(line string) (string, string, bool) {
	// Hosts-file records legitimately contain a separating space; parse them
	// before enforcing the no-whitespace rule used by filter syntax.
	for _, prefix := range []string{"0.0.0.0 ", "127.0.0.1 ", "::1 "} {
		if strings.HasPrefix(line, prefix) {
			f := strings.Fields(line)
			if len(f) >= 2 && validDomain(f[1]) {
				return "||" + strings.ToLower(f[1]) + "^", "", true
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
	for _, m := range []string{"##", "#@#", "#?#", "#$#", "#%#", "#^#", "#@%?#", "+js(", ":has-text(", ":contains(", ":matches-css(", ":xpath(", ":style("} {
		if strings.Contains(line, m) {
			return "", "unsupported-cosmetic-scriptlet", false
		}
	}
	if len(line) >= 2 && line[0] == '/' && line[len(line)-1] == '/' {
		return "", "regex-filter", false
	}
	if strings.HasPrefix(line, "[Adblock") {
		return "", "metadata", false
	}
	exception := false
	if strings.HasPrefix(line, "@@") {
		exception = true
		line = line[2:]
	}
	r, reason, ok := normalizeNetwork(line, b.KeepURLRules)
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
		return "@@" + r, "", true
	}
	return r, "", true
}

// normalizeNetwork parses a (non-cosmetic, non-regex, already
// exception-prefix-stripped) network rule. Any $-suffix is validated by
// parseModifiers before the URL pattern itself is parsed, and re-attached
// to the canonicalized pattern on success.
func normalizeNetwork(line string, keepURL bool) (string, string, bool) {
	pattern := line
	modSuffix := ""
	if idx := strings.LastIndex(line, "$"); idx >= 0 {
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
		if rest == "" || rest == "^" || rest == "|" || !keepURL {
			return "||" + strings.ToLower(host) + "^" + modSuffix, "", true
		}
		if !validPath(rest) {
			return "", "", false
		}
		return "||" + strings.ToLower(host) + rest + modSuffix, "", true
	}
	if strings.HasPrefix(pattern, "|https://") || strings.HasPrefix(pattern, "|http://") {
		hasEnd := strings.HasSuffix(pattern, "|")
		s := strings.TrimSuffix(strings.TrimPrefix(pattern, "|"), "|")
		rest := s[strings.Index(s, "://")+3:]
		pos := strings.IndexAny(rest, "/?#")
		host := rest
		if pos >= 0 {
			host = rest[:pos]
		}
		if !validDomain(host) || strings.ContainsAny(s, " \t<>\\") {
			return "", "", false
		}
		if hasEnd {
			return "|" + s + "|" + modSuffix, "", true
		}
		return "|" + s + modSuffix, "", true
	}
	return "", "", false
}

// parseModifiers validates a network rule's $-suffix against the modifier
// keywords that the Chromium subresource_filter's own filter-list parser
// (components/subresource_filter/tools/rule_parser) accepts without
// flagging them deprecated, unsupported, or whitelist-only: third-party
// (tristate), match-case, and domain= (pipe-separated, each entry
// optionally excluded with ~). Every other keyword -- element-type options
// such as script/image/subdocument, activation options such as
// document/genericblock, sitekey, collapse, donottrack, or anything
// unrecognized -- is rejected outright, so this tool never emits a rule
// that the real converter would refuse or silently reinterpret.
func parseModifiers(opts string) (string, bool) {
	if opts == "" {
		return "", false
	}
	var thirdParty string
	var matchCase bool
	var domains string
	for _, tok := range strings.Split(opts, ",") {
		if tok == "" {
			return "", false
		}
		negated := false
		if strings.HasPrefix(tok, "~") {
			negated = true
			tok = tok[1:]
		}
		name, value, hasValue := tok, "", false
		if i := strings.IndexByte(tok, '='); i >= 0 {
			name, value, hasValue = tok[:i], tok[i+1:], true
		}
		switch name {
		case "third-party":
			if hasValue {
				return "", false
			}
			if negated {
				thirdParty = "~third-party"
			} else {
				thirdParty = "third-party"
			}
		case "match-case":
			if negated || hasValue {
				return "", false
			}
			matchCase = true
		case "domain":
			if negated || !hasValue || value == "" {
				return "", false
			}
			d, ok := canonicalDomainList(value)
			if !ok {
				return "", false
			}
			domains = d
		default:
			return "", false
		}
	}
	// At least one of the three is always set here: every switch case
	// above that succeeds assigns a non-empty value to exactly one of
	// them, and an empty/unrecognized token returns false before this
	// point is ever reached.
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
	return "$" + strings.Join(parts, ","), true
}

// canonicalDomainList validates a pipe-separated domain= value (each entry
// optionally prefixed with ~ to exclude that domain) and sorts it so that
// equivalent lists written in a different order compare equal, which lets
// Optimize's exact-string deduplication catch more duplicates.
func canonicalDomainList(value string) (string, bool) {
	entries := strings.Split(value, "|")
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		excl, d := "", e
		if strings.HasPrefix(d, "~") {
			excl, d = "~", d[1:]
		}
		if !validDomain(d) {
			return "", false
		}
		out = append(out, excl+strings.ToLower(d))
	}
	if len(out) == 0 {
		return "", false
	}
	sort.Strings(out)
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
	p := strings.Split(strings.ToLower(s), ".")
	if len(p) < 2 || s[0] == '.' || s[len(s)-1] == '.' || strings.Contains(s, "..") {
		return false
	}
	allNumeric := true
	for _, part := range p {
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
func filepathDir(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[:i]
	}
	return "."
}
