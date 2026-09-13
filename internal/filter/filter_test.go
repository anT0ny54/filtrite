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
	for _, want := range []string{"||ads.example^", "||example.com^", "@@||example.com/ok.js"} {
		found := false
		for _, r := range final {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q", want)
		}
	}
	if st.Rejected < 2 {
		t.Fatalf("rejected=%d", st.Rejected)
	}
}
