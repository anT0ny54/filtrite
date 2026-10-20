package ruleset

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertRejectsAliasedPaths(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	output := filepath.Join(dir, "output.dat")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		o    Options
		want string
	}{
		{name: "output equals input", o: Options{Converter: "/bin/true", Input: input, Output: input}, want: "input and output paths must not alias"},
		{name: "log equals input", o: Options{Converter: "/bin/true", Input: input, Output: output, Log: input}, want: "input and log paths must not alias"},
		{name: "log equals output", o: Options{Converter: "/bin/true", Input: input, Output: output, Log: output}, want: "output and log paths must not alias"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Convert(context.Background(), tt.o)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Convert error=%v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestConvertRejectsOutputAndLogAliasedThroughSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasDir := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	output := filepath.Join(realDir, "rules.dat")
	logPath := filepath.Join(aliasDir, "rules.dat")
	err := Convert(context.Background(), Options{Converter: "/bin/true", Input: input, Output: output, Log: logPath})
	if err == nil || !strings.Contains(err.Error(), "output and log paths must not alias") {
		t.Fatalf("Convert error=%v, want symlink-resolved output/log collision rejection", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("output was created despite alias rejection: stat=%v", statErr)
	}
}

func TestConvertRejectsWhitespaceOnlyLogPath(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Convert(context.Background(), Options{Converter: "/bin/true", Input: input, Output: filepath.Join(dir, "out.dat"), Log: "   "})
	if err == nil || !strings.Contains(err.Error(), "log path must be empty or a non-blank path") {
		t.Fatalf("Convert error=%v, want whitespace-only log-path rejection", err)
	}
}
