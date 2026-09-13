package filter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuilderAndOptimizer(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	rej := filepath.Join(dir, "rej.txt")
	input := strings.Join([]string{"! c", "||Example.COM^", "||example.com/path/*", "@@||example.com/ok.js", "0.0.0.0 ads.example", "example.net", "example.net##.ad", "||bad.com^$script"}, "\n")
	if err := os.WriteFile(in, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}
	rules, st, err := (Builder{KeepURLRules: true}).ReadFile(in, rej)
	if err != nil {
		t.Fatal(err)
	}
	final, red := Optimize(rules)
	if len(final) != 3 || red != 1 {
		t.Fatalf("final=%v redundant=%d", final, red)
	}
	// Bare host blocks are auto-guarded with $third-party (see Optimize),
	// so they no longer also match a direct/first-party navigation to the
	// blocked host; the path-scoped duplicate stays dropped as redundant
	// and the exception rule is left untouched.
	for _, want := range []string{"||ads.example^$third-party", "||example.com^$third-party", "@@||example.com/ok.js"} {
		found := false
		for _, r := range final {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q in %v", want, final)
		}
	}
	if st.Rejected < 2 {
		t.Fatalf("rejected=%d", st.Rejected)
	}
}

// TestModifierHandling exercises the $-suffix keywords the legacy Chromium
// subresource_filter's own filter-list parser actually accepts (third-party,
// match-case, domain=) and confirms keywords that are recognized by the
// engine but explicitly unsupported/deprecated/whitelist-only there
// (sitekey, collapse, document) are rejected rather than silently dropped
// or misapplied.
func TestModifierHandling(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	rej := filepath.Join(dir, "rej.txt")
	input := strings.Join([]string{
		"||track.example^$third-party",
		"@@||safe.example^$~third-party",
		"||scopea.example^$domain=foo.example|~bar.example",
		"||scopeb.example^$domain=~bar.example|foo.example",
		"||nocase.example^$match-case",
		"||sitekeyed.example^$sitekey=abc123",
		"||activation.example^$document",
		"||collapsed.example^$collapse",
	}, "\n")
	if err := os.WriteFile(in, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}
	rules, st, err := (Builder{KeepURLRules: true}).ReadFile(in, rej)
	if err != nil {
		t.Fatal(err)
	}
	final, _ := Optimize(rules)

	present := map[string]bool{}
	for _, r := range final {
		present[r] = true
	}
	for _, want := range []string{
		"||track.example^$third-party",
		"@@||safe.example^$~third-party",
		"||nocase.example^$match-case",
	} {
		if !present[want] {
			t.Fatalf("missing %q in %v", want, final)
		}
	}

	// The two domain= rules list the same domains in a different order and
	// with a different ~ placement; both must canonicalize (and sort) to
	// the exact same suffix so equivalent scoping compares/dedupes equal.
	var scopeASuffix, scopeBSuffix string
	for _, r := range final {
		if strings.HasPrefix(r, "||scopea.example^$") {
			scopeASuffix = strings.TrimPrefix(r, "||scopea.example^")
		}
		if strings.HasPrefix(r, "||scopeb.example^$") {
			scopeBSuffix = strings.TrimPrefix(r, "||scopeb.example^")
		}
	}
	if scopeASuffix == "" || scopeASuffix != scopeBSuffix {
		t.Fatalf("expected identical canonicalized domain= suffix regardless of input order, got %q vs %q", scopeASuffix, scopeBSuffix)
	}

	if got := st.ByReason["unsupported-modifier"]; got != 3 {
		t.Fatalf("unsupported-modifier=%d, want 3 (sitekey, document, collapse); reasons=%v", got, st.ByReason)
	}
}

// TestOptimizeIsIdempotent replaces the old baseline_test.go, which asserted
// idempotency against a generated filters.txt snapshot that isn't part of
// this repository (it's a build artifact, not a committed file) and that
// hard-coded a rule count that would drift every time the upstream sources
// change. This exercises the same property -- running Optimize on its own
// output must not find anything further to remove or rewrite -- against
// fixed synthetic input instead.
func TestOptimizeIsIdempotent(t *testing.T) {
	rules := []string{
		"||a.example^",
		"||a.example/path^",
		"||a.example^$domain=b.example",
		"@@||a.example/allowed.js",
		"||c.example^$third-party",
		"||d.example/x?y=1",
	}
	first, _ := Optimize(rules)
	second, redundantAgain := Optimize(first)
	if redundantAgain != 0 {
		t.Fatalf("optimized output is not idempotent: second pass removed %d more rules from %v", redundantAgain, first)
	}
	if len(second) != len(first) {
		t.Fatalf("rule count changed on second pass: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("rule text changed on second pass: %q vs %q", first[i], second[i])
		}
	}
}
