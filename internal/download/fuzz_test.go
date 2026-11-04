package download

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzURLsFromFile feeds arbitrary manifest bytes through the source-list
// parser. The one invariant that must hold for every accepted manifest is
// that the returned URL list contains no duplicates: deduplication runs on
// the canonical (scheme/host lower-cased) form, so a fuzzer-generated
// collision such as "HTTPS://EXAMPLE.COM/a" plus "https://example.com/a"
// would surface here as a double entry.
//
// Run with: go test -fuzz=FuzzURLsFromFile -fuzztime=60s ./internal/download/
func FuzzURLsFromFile(f *testing.F) {
	seeds := []string{
		"https://example.com/list.txt\n",
		"# comment\n\nhttps://example.com/a.txt\n",
		"\xef\xbb\xbfhttps://example.com/list.txt\n",
		"https://user:pass@example.com/private.txt\n",
		"https://example.com:0/list.txt\n",
		"https://example.com/list.txt#fragment\n",
		" https://example.com/spaced.txt\n",
		"https://example.com/list%23fragment.txt\n",
		"HTTPS://EXAMPLE.COM/list.txt\nhttps://example.com/list.txt\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, manifest string) {
		path := filepath.Join(t.TempDir(), "sources.txt")
		if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		urls, err := URLsFromFile(path)
		if err != nil {
			return
		}
		seen := make(map[string]struct{}, len(urls))
		for _, u := range urls {
			if _, dup := seen[u]; dup {
				t.Fatalf("URLsFromFile returned duplicate URL %q for manifest %q", u, manifest)
			}
			seen[u] = struct{}{}
		}
	})
}
