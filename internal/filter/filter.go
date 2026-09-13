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
	Read      int
	Emitted   int
	Rejected  int
	Redundant int
	ByReason  map[string]int
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

func Optimize(rules []string) ([]string, int) {
	set := make(map[string]struct{}, len(rules))
	for _, r := range rules {
		if r != "" {
			set[r] = struct{}{}
		}
	}
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
			if ok && domainBlocks[h] && suffix != "^" && suffix != "|" && suffix != "" && strings.ContainsAny(suffix, "/?#") {
				redundant++
				continue
			}
		}
		out = append(out, r)
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
	if strings.Contains(line, "$") {
		return "", "unsupported-modifier", false
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
	r, ok := normalizeNetwork(line, b.KeepURLRules)
	if !ok {
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

func normalizeNetwork(line string, keepURL bool) (string, bool) {
	if strings.HasPrefix(line, "||") {
		x := line[2:]
		pos := strings.IndexAny(x, "/?#^|")
		host, rest := x, ""
		if pos >= 0 {
			host, rest = x[:pos], x[pos:]
		}
		if !validDomain(host) {
			return "", false
		}
		if rest == "" || rest == "^" || rest == "|" || !keepURL {
			return "||" + strings.ToLower(host) + "^", true
		}
		if !validPath(rest) {
			return "", false
		}
		return "||" + strings.ToLower(host) + rest, true
	}
	if strings.HasPrefix(line, "|https://") || strings.HasPrefix(line, "|http://") {
		hasEnd := strings.HasSuffix(line, "|")
		s := strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
		rest := s[strings.Index(s, "://")+3:]
		pos := strings.IndexAny(rest, "/?#")
		host := rest
		if pos >= 0 {
			host = rest[:pos]
		}
		if !validDomain(host) || strings.ContainsAny(s, " \t<>\\") {
			return "", false
		}
		if hasEnd {
			return "|" + s + "|", true
		}
		return "|" + s, true
	}
	return "", false
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
