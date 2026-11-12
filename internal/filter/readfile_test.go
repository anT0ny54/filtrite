package filter

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestReadFileToSinkReportsPhysicalLineNumbers mixes a BOM, comments, blank
// lines, indented comments, CRLF endings, and two rejected rules. Rejected
// report entries must carry the physical source line number, and comment or
// blank lines must count toward Read without being reported.
func TestReadFileToSinkReportsPhysicalLineNumbers(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	rej := filepath.Join(dir, "rej.txt")
	input := "\uFEFF! header\n" +
		"\n" +
		"   # hosts-style comment\r\n" +
		"  ! indented comment\n" +
		"||ok.example^\r\n" +
		"||bad.example##.ad\n" +
		"\t\n" +
		"||also.example/x\n"
	if err := os.WriteFile(in, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	var got []string
	stats, err := ReadFileToSink(in, rej, func(rule string) error {
		got = append(got, rule)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"||ok.example^", "||also.example/x"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rules=%q, want %q", got, want)
	}
	if stats.Read != 8 || stats.Rejected != 2 {
		t.Fatalf("stats=%+v, want Read=8 Rejected=2", stats)
	}

	report, err := os.ReadFile(rej)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(report), "\n"), "\n")
	want := []string{
		"# line\treason\trule",
		"3\tnon-ascii-or-whitespace\t   # hosts-style comment",
		"6\tunsupported-cosmetic-scriptlet\t||bad.example##.ad",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("rejected report=%q, want %q", lines, want)
	}
}

func TestReadFileToSinkDoesNotTruncateExternalHardLinkToReport(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	report := filepath.Join(dir, "rejected.txt")
	external := filepath.Join(dir, "external.txt")
	if err := os.WriteFile(input, []byte("||bad.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const sentinel = "preserve external data\n"
	if err := os.WriteFile(external, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(external, report); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	if _, err := ReadFileToSink(input, report, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(external); err != nil || string(got) != sentinel {
		t.Fatalf("external hard-link target changed: got=%q err=%v", got, err)
	}
	got, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "# line\treason\trule\n") || !strings.Contains(string(got), "1\tunterminated-host-rule\t||bad.example\n") {
		t.Fatalf("replacement report is incomplete: %q", got)
	}
}
