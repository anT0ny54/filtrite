package filter

import (
	"fmt"
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

func TestExternalSorterRejectsUseAfterFinish(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "filters.txt")
	sorter, err := NewExternalSorter(filepath.Join(dir, "sort"), 24)
	if err != nil {
		t.Fatal(err)
	}
	if err := sorter.Add("||example.com^"); err != nil {
		t.Fatal(err)
	}
	if _, err := sorter.Finish(output); err != nil {
		t.Fatal(err)
	}
	if err := sorter.Add("||another.example^"); err == nil {
		t.Fatal("Add after Finish succeeded")
	}
	if _, err := sorter.Finish(output); err == nil {
		t.Fatal("second Finish succeeded")
	}
}

func TestExternalSorterScalesInitialCapacityForSmallChunks(t *testing.T) {
	sorter, err := NewExternalSorter(t.TempDir(), 8)
	if err != nil {
		t.Fatal(err)
	}
	if cap(sorter.rules) != 1 {
		t.Fatalf("initial rule capacity=%d, want 1 for an 8-byte chunk", cap(sorter.rules))
	}
}

func TestExternalSorterFlushesOversizedSingleRuleImmediatelyAndReleasesIt(t *testing.T) {
	dir := t.TempDir()
	sorter, err := NewExternalSorter(filepath.Join(dir, "sort"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := sorter.Add("||large.example/" + strings.Repeat("x", 128)); err != nil {
		t.Fatal(err)
	}
	if len(sorter.rules) != 0 || len(sorter.chunks) != 1 {
		t.Fatalf("after oversized Add: buffered rules=%d chunks=%d, want 0 and 1", len(sorter.rules), len(sorter.chunks))
	}
	for i, rule := range sorter.rules[:cap(sorter.rules)] {
		if rule != "" {
			t.Fatalf("sorter retained flushed rule reference at slot %d", i)
		}
	}
}

func TestExternalSorterDoesNotFollowPreexistingChunkLinks(t *testing.T) {
	for _, linkType := range []string{"symlink", "hardlink"} {
		t.Run(linkType, func(t *testing.T) {
			dir := t.TempDir()
			sortDir := filepath.Join(dir, "sort")
			if err := os.Mkdir(sortDir, 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "outside.txt")
			const sentinel = "preserve external data\n"
			if err := os.WriteFile(target, []byte(sentinel), 0o644); err != nil {
				t.Fatal(err)
			}
			staleChunk := filepath.Join(sortDir, ".chunk-000000.txt")
			switch linkType {
			case "symlink":
				if err := os.Symlink(target, staleChunk); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "hardlink":
				if err := os.Link(target, staleChunk); err != nil {
					t.Skipf("hard links unavailable: %v", err)
				}
			}

			sorter, err := NewExternalSorter(sortDir, 8)
			if err != nil {
				t.Fatal(err)
			}
			if err := sorter.Add("||safe.example/path"); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(dir, "filters.txt")
			if _, err := sorter.Finish(output); err != nil {
				t.Fatal(err)
			}

			if got, err := os.ReadFile(target); err != nil || string(got) != sentinel {
				t.Fatalf("external chunk target changed: got=%q err=%v", got, err)
			}
			if _, err := os.Lstat(staleChunk); err != nil {
				t.Fatalf("pre-existing scratch entry was unexpectedly removed: %v", err)
			}
			got, err := os.ReadFile(output)
			if err != nil || string(got) != "||safe.example/path\n" {
				t.Fatalf("output=%q err=%v, want normalized sorted rule", got, err)
			}
		})
	}
}

func TestExternalSorterMergesBeyondFanInAndDeduplicatesAcrossGroups(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "filters.txt")
	sorter, err := NewExternalSorter(filepath.Join(dir, "sort"), 1)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < maxMergeFanIn+1; i++ {
		var rule string
		switch i {
		case 0:
			rule = "||duplicate.example^$third-party"
		case maxMergeFanIn:
			rule = "||duplicate.example^"
		default:
			rule = "||rule-" + fmt.Sprintf("%03d", i) + ".example/path"
		}
		if err := sorter.Add(rule); err != nil {
			t.Fatal(err)
		}
	}

	result, err := sorter.Finish(output)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rules != maxMergeFanIn || result.Duplicates != 1 {
		t.Fatalf("result=%+v, want %d rules and 1 duplicate across merge groups", result, maxMergeFanIn)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(got), "\n"), "\n")
	if len(lines) != maxMergeFanIn {
		t.Fatalf("output has %d lines, want %d", len(lines), maxMergeFanIn)
	}
	for i := 1; i < len(lines); i++ {
		if lines[i-1] > lines[i] {
			t.Fatalf("output is not sorted at line %d: %q > %q", i+1, lines[i-1], lines[i])
		}
	}
}
