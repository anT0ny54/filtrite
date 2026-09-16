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
		"||bad.com^$script",
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
