package filter

import "testing"

// FuzzNormalize exercises the rule normalizer with arbitrary input and
// asserts two invariants on every accepted rule:
//
//  1. the accepted rule text is never empty and carries no rejection reason;
//  2. normalization is idempotent — feeding an already-normalized rule back
//     through normalize must accept it and return the identical text (the
//     builder relies on this when the sorter streams canonical strings).
//
// Run with: go test -fuzz=FuzzNormalize -fuzztime=60s ./internal/filter/
func FuzzNormalize(f *testing.F) {
	seeds := []string{
		"||example.com^",
		"||example.com^$third-party",
		"@@||example.com^$document",
		"0.0.0.0 ads.example",
		"127.0.0.1 ads.example",
		"::1 ads.example",
		"|https://example.com/ads.js|",
		"@@|https://Example.COM/allowed.js|",
		"||example.com/path$script,image",
		"||example.com^$domain=foo.example|~bar.example",
		"||example.com^$~image,~stylesheet",
		"example.com##.ad",
		"||bad.com^$sitekey=abc123",
		"@@0.0.0.0 exception.example",
		"||example.com^$third-party$match-case",
		"||bare.example",
		"||a..b^",
		"||-bad.example^",
		"! comment",
		"",
		"||example.com^$popup",
		"||example.com\x00bad",
		"\xef\xbb\xbf||bom.example^",
		"||example.com^$domain=foo.example|foo.example",
		"||internal.example/path|suffix",
		"||repeated.example/path||",
		"||valid.example/path|",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		rule, reason, ok := normalize(line)
		if !ok {
			return
		}
		if rule == "" {
			t.Fatalf("normalize(%q) accepted with an empty rule", line)
		}
		if reason != "" {
			t.Fatalf("normalize(%q) accepted with reason %q", line, reason)
		}
		again, reason2, ok2 := normalize(rule)
		if !ok2 {
			t.Fatalf("normalize(%q) = %q rejected on second pass: %s", line, rule, reason2)
		}
		if again != rule {
			t.Fatalf("normalize not idempotent: %q -> %q -> %q", line, rule, again)
		}
	})
}
