package filter

import (
	"path/filepath"
	"testing"
)

func TestSuppliedBaselineParses(t *testing.T) {
	input := filepath.Join("..", "..", "filters.txt")
	rules, st, err := (Builder{KeepURLRules: true}).ReadFile(input, filepath.Join(t.TempDir(), "rejected.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Rejected != 0 {
		t.Fatalf("baseline parser rejected %d of %d lines", st.Rejected, st.Read)
	}
	if len(rules) != 203507 {
		t.Fatalf("supplied optimized snapshot rule count changed: got %d want 203507", len(rules))
	}
	optimized, redundant := Optimize(rules)
	if redundant != 0 {
		t.Fatalf("optimized snapshot is not idempotent: removed %d more rules", redundant)
	}
	if len(optimized) != 203507 {
		t.Fatalf("optimized rule count changed: got %d want 203507", len(optimized))
	}
}
