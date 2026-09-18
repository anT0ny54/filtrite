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
	input := strings.Join([]string{
		"! c",
		"||Example.COM^",
		"||example.com/path/*",
		"@@||example.com/ok.js",
		"0.0.0.0 ads.example",
		"example.net",
		"example.net##.ad",
		"||bad.com^$sitekey=abc123",
	}, "\n")
	if err := os.WriteFile(in, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, st, err := (Builder{}).ReadFile(in, rej)
	if err != nil {
		t.Fatal(err)
	}
	final, duplicates := Optimize(rules)
	if len(final) != 4 || duplicates != 0 {
		t.Fatalf("final=%v duplicates=%d", final, duplicates)
	}
	for _, want := range []string{
		"||ads.example^$third-party",
		"||example.com^$third-party",
		"||example.com/path/*",
		"@@||example.com/ok.js",
	} {
		found := false
		for _, rule := range final {
			if rule == want {
				found = true
				break
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

func TestOptimizeKeepsDifferentMetadataScopes(t *testing.T) {
	rules := []string{
		"||scope.example^",
		"||scope.example^$~third-party",
		"||scope.example^$domain=publisher.example",
		"||scope.example/path^",
	}
	final, duplicates := Optimize(rules)
	if duplicates != 0 || len(final) != 4 {
		t.Fatalf("final=%v duplicates=%d", final, duplicates)
	}
	for _, want := range []string{
		"||scope.example^$third-party",
		"||scope.example^$~third-party",
		"||scope.example^$domain=publisher.example",
		"||scope.example/path^",
	} {
		found := false
		for _, rule := range final {
			if rule == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %q in %v", want, final)
		}
	}
}

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
		"||duplicate-mod.example^$third-party,third-party",
		"||conflict-mod.example^$third-party,~third-party",
		"||duplicate-domain.example^$domain=foo.example|foo.example",
	}, "\n")
	if err := os.WriteFile(in, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, st, err := (Builder{}).ReadFile(in, rej)
	if err != nil {
		t.Fatal(err)
	}
	final, _ := Optimize(rules)

	present := map[string]bool{}
	for _, rule := range final {
		present[rule] = true
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

	var scopeASuffix, scopeBSuffix string
	for _, rule := range final {
		if strings.HasPrefix(rule, "||scopea.example^$") {
			scopeASuffix = strings.TrimPrefix(rule, "||scopea.example^")
		}
		if strings.HasPrefix(rule, "||scopeb.example^$") {
			scopeBSuffix = strings.TrimPrefix(rule, "||scopeb.example^")
		}
	}
	if scopeASuffix == "" || scopeASuffix != scopeBSuffix {
		t.Fatalf("expected identical canonicalized domain= suffix, got %q vs %q", scopeASuffix, scopeBSuffix)
	}
	if got := st.ByReason["unsupported-modifier"]; got != 6 {
		t.Fatalf("unsupported-modifier=%d, want 6; reasons=%v", got, st.ByReason)
	}
}

func TestOptimizeDeduplicatesAfterThirdPartyGuard(t *testing.T) {
	// Merging many source lists commonly yields both a bare "||host^" rule
	// from one list and an already-"$third-party" tagged copy of the same
	// rule from another. The guard must run before dedup so these collapse
	// into a single output line instead of surviving as two distinct
	// pre-guard strings that both transform into an undetected duplicate.
	rules := []string{
		"||dup.example^",
		"||dup.example^$third-party",
		"||other.example^",
	}
	final, duplicates := Optimize(rules)
	if duplicates != 1 {
		t.Fatalf("duplicates=%d, want 1 (final=%v)", duplicates, final)
	}
	if len(final) != 2 {
		t.Fatalf("final=%v, want 2 rules", final)
	}
	present := map[string]bool{}
	for _, rule := range final {
		present[rule] = true
	}
	for _, want := range []string{"||dup.example^$third-party", "||other.example^$third-party"} {
		if !present[want] {
			t.Fatalf("missing %q in %v", want, final)
		}
	}
	// The literal duplicate line must not appear twice in the output.
	count := 0
	for _, rule := range final {
		if rule == "||dup.example^$third-party" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("||dup.example^$third-party appears %d times in %v, want 1", count, final)
	}
}

func TestDomainListRejectsConflictingScope(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	rej := filepath.Join(dir, "rej.txt")
	input := strings.Join([]string{
		"||conflict.example^$domain=foo.example|~foo.example",
		"||ok.example^$domain=foo.example|~bar.example",
	}, "\n")
	if err := os.WriteFile(in, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, st, err := (Builder{}).ReadFile(in, rej)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || st.ByReason["unsupported-modifier"] != 1 {
		t.Fatalf("rules=%v reasons=%v", rules, st.ByReason)
	}
	if rules[0] != "||ok.example^$domain=foo.example|~bar.example" {
		t.Fatalf("unexpected surviving rule: %q", rules[0])
	}
}

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
	second, duplicatesAgain := Optimize(first)
	if duplicatesAgain != 0 {
		t.Fatalf("optimized output is not idempotent: second pass removed %d duplicates from %v", duplicatesAgain, first)
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

func TestRuleWhitespaceIsRejected(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	rej := filepath.Join(dir, "rej.txt")
	if err := os.WriteFile(in, []byte(" ||space.example^\n||ok.example^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, st, err := (Builder{}).ReadFile(in, rej)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || st.Rejected != 1 || st.ByReason["non-ascii-or-whitespace"] != 1 {
		t.Fatalf("rules=%v stats=%+v", rules, st)
	}
}

func TestElementTypeModifiers(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	rej := filepath.Join(dir, "rej.txt")
	input := strings.Join([]string{
		"||ads.example^$image,script",
		"||sorted.example^$xmlhttprequest,font",
		"||neg.example^$~image,~stylesheet",
		"||mixed.example^$script,~image",
		"||dup.example^$script,script",
		"||withtp.example^$object-subrequest,third-party,domain=foo.example",
		"||popup.example^$popup",
	}, "\n")
	if err := os.WriteFile(in, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, st, err := (Builder{}).ReadFile(in, rej)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, r := range rules {
		present[r] = true
	}
	for _, want := range []string{
		"||ads.example^$image,script",
		"||sorted.example^$font,xmlhttprequest",
		"||neg.example^$~image,~stylesheet",
		"||withtp.example^$object-subrequest,third-party,domain=foo.example",
		"||popup.example^$popup",
	} {
		if !present[want] {
			t.Fatalf("missing %q in %v", want, rules)
		}
	}
	// A rule that mixes positive and negative element types, or repeats one,
	// is order-dependent (or ambiguous) to canonicalize, so it must have been
	// rejected rather than silently accepted/approximated.
	for _, unwanted := range []string{"mixed.example", "dup.example"} {
		for _, r := range rules {
			if strings.Contains(r, unwanted) {
				t.Fatalf("rule for %q should have been rejected, got %q", unwanted, r)
			}
		}
	}
	if got := st.ByReason["unsupported-modifier"]; got != 2 {
		t.Fatalf("unsupported-modifier=%d, want 2 (mixed-polarity + duplicate); reasons=%v", got, st.ByReason)
	}
}

func TestActivationTypeModifiersAreExceptionOnly(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	rej := filepath.Join(dir, "rej.txt")
	input := strings.Join([]string{
		"@@||safe.example^$document,elemhide",
		"||blocked.example^$document",          // not an exception rule: rejected
		"@@||neg.example^$~document",            // activation types aren't tristate: rejected
		"@@||both.example^$script,generichide", // can't mix element and activation types
	}, "\n")
	if err := os.WriteFile(in, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, st, err := (Builder{}).ReadFile(in, rej)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, r := range rules {
		present[r] = true
	}
	if !present["@@||safe.example^$document,elemhide"] {
		t.Fatalf("missing accepted activation-type exception rule in %v", rules)
	}
	if len(rules) != 1 {
		t.Fatalf("rules=%v, want exactly 1 accepted rule", rules)
	}
	if got := st.ByReason["unsupported-modifier"]; got != 3 {
		t.Fatalf("unsupported-modifier=%d, want 3; reasons=%v", got, st.ByReason)
	}
}

func TestElementTypeModifiersSurviveOptimize(t *testing.T) {
	// Optimize must not disturb resource-type/activation modifiers: the
	// third-party guard only ever applies to a completely bare "||host^" or
	// "||host|" pattern, never to one that already carries any "$" options.
	rules := []string{
		"||a.example^$script,image",
		"@@||b.example^$document",
	}
	final, duplicates := Optimize(rules)
	if duplicates != 0 || len(final) != 2 {
		t.Fatalf("final=%v duplicates=%d", final, duplicates)
	}
	present := map[string]bool{}
	for _, r := range final {
		present[r] = true
	}
	for _, want := range rules {
		if !present[want] {
			t.Fatalf("missing %q in %v", want, final)
		}
	}
}

func TestFullyAnchoredURLRules(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	rej := filepath.Join(dir, "rej.txt")
	input := strings.Join([]string{
		"|https://Example.COM/Ads.js",
		"@@|https://Example.COM/allowed.js|",
		"|https://Example.COM/track.js$third-party",
		"|http://Example.ORG/x",
		"|https://exa_mple.com/x",       // invalid host: rejected
		"|https://example.com/<script>", // disallowed character: rejected
	}, "\n")
	if err := os.WriteFile(in, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, st, err := (Builder{}).ReadFile(in, rej)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, r := range rules {
		present[r] = true
	}
	for _, want := range []string{
		// The host is lower-cased (like the "||host" form), but the path
		// keeps its original case: paths are case-sensitive and hosts are
		// not, so only the host half is canonicalized.
		"|https://example.com/Ads.js",
		"@@|https://example.com/allowed.js|",
		"|https://example.com/track.js$third-party",
		"|http://example.org/x",
	} {
		if !present[want] {
			t.Fatalf("missing %q in %v", want, rules)
		}
	}
	if len(rules) != 4 {
		t.Fatalf("rules=%v, want exactly 4 accepted rules", rules)
	}
	if got := st.Rejected; got != 2 {
		t.Fatalf("rejected=%d, want 2; reasons=%v", got, st.ByReason)
	}
}

func TestFullyAnchoredURLHostCasingDeduplicates(t *testing.T) {
	// Two sources shipping the same fully-anchored rule with different host
	// casing must normalize to the same string and collapse to one rule.
	// ReadFile dedups on the normalized string as it reads, so this already
	// happens before Optimize ever sees the rules; Optimize is idempotent on
	// the result either way.
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	rej := filepath.Join(dir, "rej.txt")
	input := strings.Join([]string{
		"|https://Ads.Example.com/x",
		"|https://ads.example.com/x",
	}, "\n")
	if err := os.WriteFile(in, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, _, err := (Builder{}).ReadFile(in, rej)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0] != "|https://ads.example.com/x" {
		t.Fatalf("rules=%v, want exactly 1 rule %q", rules, "|https://ads.example.com/x")
	}
	final, duplicates := Optimize(rules)
	if duplicates != 0 || len(final) != 1 || final[0] != "|https://ads.example.com/x" {
		t.Fatalf("final=%v duplicates=%d", final, duplicates)
	}
}

func TestHostsEntriesRejectTrailingFields(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	rej := filepath.Join(dir, "rej.txt")
	if err := os.WriteFile(in, []byte("0.0.0.0 ads.example extra\n127.0.0.1 ads2.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, st, err := (Builder{}).ReadFile(in, rej)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || st.ByReason["invalid-host-entry"] != 1 {
		t.Fatalf("rules=%v reasons=%v", rules, st.ByReason)
	}
}
