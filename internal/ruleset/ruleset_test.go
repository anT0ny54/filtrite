package ruleset

import (
	"context"
	"errors"
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

func makeTestConverter(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestConvertRejectsOutputOrLogAliasingConverter(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	converter := filepath.Join(root, "converter")
	makeTestConverter(t, converter)

	tests := []struct {
		name   string
		output string
		log    string
		want   string
	}{
		{name: "output aliases converter", output: converter, want: "output and converter paths must not alias"},
		{name: "log aliases converter", output: filepath.Join(root, "output.dat"), log: converter, want: "converter and log paths must not alias"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Convert(context.Background(), Options{Converter: converter, Input: input, Output: tt.output, Log: tt.log})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Convert error=%v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestConvertRejectsOutputAndLogAliasedThroughDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(realDir, "rules.dat")
	logPath := filepath.Join(root, "rules.log")
	if err := os.Symlink(output, logPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := Convert(context.Background(), Options{Converter: "/bin/true", Input: input, Output: output, Log: logPath})
	if err == nil || !strings.Contains(err.Error(), "output and log paths must not alias") {
		t.Fatalf("Convert error=%v, want dangling-symlink alias rejection", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("output target was created before alias rejection: stat=%v", statErr)
	}
}

func TestConvertMissingConverterLeavesLogAndOutputUntouched(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	converter := filepath.Join(root, "missing-converter")
	output := filepath.Join(root, "output.dat")
	logPath := filepath.Join(root, "converter.log")
	const sentinel = "keep existing log contents\n"
	if err := os.WriteFile(logPath, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Convert(context.Background(), Options{Converter: converter, Input: input, Output: output, Log: logPath})
	if err == nil || !strings.Contains(err.Error(), "find converter") {
		t.Fatalf("Convert error=%v, want missing-converter preflight error", err)
	}
	if got, readErr := os.ReadFile(logPath); readErr != nil || string(got) != sentinel {
		t.Fatalf("log changed before converter validation: got=%q err=%v", got, readErr)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("output was created before converter validation: stat=%v", statErr)
	}
}

func TestConvertCanceledContextLeavesLogAndOutputUntouched(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	output := filepath.Join(root, "output.dat")
	logPath := filepath.Join(root, "converter.log")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const outputSentinel = "previous output\n"
	const logSentinel = "previous log\n"
	if err := os.WriteFile(output, []byte(outputSentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte(logSentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Convert(ctx, Options{Converter: "/bin/true", Input: input, Output: output, Log: logPath})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Convert error=%v, want context.Canceled", err)
	}
	if got, readErr := os.ReadFile(output); readErr != nil || string(got) != outputSentinel {
		t.Fatalf("output changed for canceled conversion: got=%q err=%v", got, readErr)
	}
	if got, readErr := os.ReadFile(logPath); readErr != nil || string(got) != logSentinel {
		t.Fatalf("log changed for canceled conversion: got=%q err=%v", got, readErr)
	}
}
