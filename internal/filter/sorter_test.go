package filter

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExternalSorterMergesDeduplicatesAndGuardsBareDomains(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "filters.txt")
	sorter, err := NewExternalSorter(filepath.Join(dir, "sort"), 24)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{
		"||z.example^",
		"||a.example^$third-party",
		"||a.example^",
		"||b.example/path",
		"||z.example^",
	} {
		if err := sorter.Add(rule); err != nil {
			t.Fatal(err)
		}
	}
	result, err := sorter.Finish(output)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rules != 3 || result.Duplicates != 2 {
		t.Fatalf("result=%+v, want 3 rules and 2 duplicates", result)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	want := "||a.example^$third-party\n||b.example/path\n||z.example^$third-party\n"
	if string(got) != want {
		t.Fatalf("output=%q, want %q", got, want)
	}
}

func TestExternalSorterForcesMultipleChunks(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "filters.txt")
	sorter, err := NewExternalSorter(filepath.Join(dir, "sort"), 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"||d.example/path", "||a.example/path", "||c.example/path", "||b.example/path", "||a.example/path"} {
		if err := sorter.Add(rule); err != nil {
			t.Fatal(err)
		}
	}
	result, err := sorter.Finish(output)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rules != 4 || result.Duplicates != 1 {
		t.Fatalf("result=%+v, want 4 rules and 1 duplicate", result)
	}
	gotText, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(gotText), "\n"), "\n")
	want := []string{"||a.example/path", "||b.example/path", "||c.example/path", "||d.example/path"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rules=%v, want %v", got, want)
	}
}
