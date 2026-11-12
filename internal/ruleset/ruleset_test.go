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

func TestConvertRejectsUnaliasedSymlinkedLogLeaf(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "outside.log")
	const sentinel = "do not truncate\n"
	if err := os.WriteFile(target, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "converter.log")
	if err := os.Symlink(target, logPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	output := filepath.Join(root, "output.dat")

	err := Convert(context.Background(), Options{Converter: "/bin/true", Input: input, Output: output, Log: logPath})
	if err == nil || !strings.Contains(err.Error(), "log path must not be a symlink") {
		t.Fatalf("Convert error=%v, want unaliased log-symlink rejection", err)
	}
	if got, readErr := os.ReadFile(target); readErr != nil || string(got) != sentinel {
		t.Fatalf("external log target changed: got=%q err=%v", got, readErr)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("output was created despite log-symlink rejection: stat=%v", statErr)
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

func TestConvertDoesNotTruncateExternalHardLinkToLog(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	output := filepath.Join(root, "output.dat")
	logPath := filepath.Join(root, "converter.log")
	external := filepath.Join(root, "external.txt")
	converter := filepath.Join(root, "converter.sh")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const sentinel = "preserve external data\n"
	if err := os.WriteFile(external, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho converter output\nfor arg in \"$@\"; do case \"$arg\" in --output_file=*) printf 'ruleset' > \"${arg#--output_file=}\";; esac; done\n"
	if err := os.WriteFile(converter, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(external, logPath); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	if err := Convert(context.Background(), Options{Converter: converter, Input: input, Output: output, Log: logPath}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(external); err != nil || string(got) != sentinel {
		t.Fatalf("external hard-link target changed: got=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(got), "converter output") {
		t.Fatalf("converter log missing captured output: got=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(output); err != nil || string(got) != "ruleset" {
		t.Fatalf("converter output=%q err=%v, want ruleset", got, err)
	}
}

func TestConvertRejectsNonRegularLogPath(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(root, "log-directory")
	if err := os.Mkdir(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := Convert(context.Background(), Options{Converter: "/bin/true", Input: input, Output: filepath.Join(root, "out.dat"), Log: logDir})
	if err == nil || !strings.Contains(err.Error(), "log path must be a regular file") {
		t.Fatalf("Convert error=%v, want non-regular log-path rejection", err)
	}
	if _, err := os.Stat(filepath.Join(root, "out.dat")); !os.IsNotExist(err) {
		t.Fatalf("output was created despite log-path rejection: stat=%v", err)
	}
}

func TestConvertRejectsSymlinkedOrNonRegularOutputBeforeCreatingTemps(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("symlink", func(t *testing.T) {
		target := filepath.Join(root, "outside.dat")
		const sentinel = "preserve external output\n"
		if err := os.WriteFile(target, []byte(sentinel), 0o644); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(root, "output.dat")
		if err := os.Symlink(target, output); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		err := Convert(context.Background(), Options{Converter: "/bin/true", Input: input, Output: output})
		if err == nil || !strings.Contains(err.Error(), "output path must not be a symlink") {
			t.Fatalf("Convert error=%v, want output-symlink rejection", err)
		}
		if got, readErr := os.ReadFile(target); readErr != nil || string(got) != sentinel {
			t.Fatalf("symlink target changed: got=%q err=%v", got, readErr)
		}
		if info, lstatErr := os.Lstat(output); lstatErr != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("output symlink was changed: info=%v err=%v", info, lstatErr)
		}
		entries, readErr := os.ReadDir(root)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), ".tmp-") {
				t.Fatalf("temporary file %q created before output preflight failed", entry.Name())
			}
		}
	})

	t.Run("directory", func(t *testing.T) {
		output := filepath.Join(root, "output-directory")
		if err := os.Mkdir(output, 0o755); err != nil {
			t.Fatal(err)
		}
		err := Convert(context.Background(), Options{Converter: "/bin/true", Input: input, Output: output})
		if err == nil || !strings.Contains(err.Error(), "output path must be a regular file") {
			t.Fatalf("Convert error=%v, want non-regular output rejection", err)
		}
		entries, readErr := os.ReadDir(output)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(entries) != 0 {
			t.Fatalf("output directory was modified during failed preflight: %v", entries)
		}
	})
}

func TestConvertRejectsConverterReplacingTemporaryOutputWithSymlink(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	output := filepath.Join(root, "output.dat")
	target := filepath.Join(root, "target.dat")
	converter := filepath.Join(root, "converter.sh")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const sentinel = "preserve converter target\n"
	if err := os.WriteFile(target, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	// Replace the wrapper-created temporary output with a symlink to an
	// unrelated file. The wrapper must reject it before Chmod or publication.
	script := "#!/bin/sh\n" +
		"for arg in \"$@\"; do\n" +
		"  case \"$arg\" in\n" +
		"    --output_file=*)\n" +
		"      temp=\"${arg#--output_file=}\"\n" +
		"      rm -f \"$temp\"\n" +
		"      ln -s \"$(dirname \"$temp\")/target.dat\" \"$temp\"\n" +
		"      ;;\n" +
		"  esac\n" +
		"done\n"
	if err := os.WriteFile(converter, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(converter, 0o755); err != nil {
		t.Fatal(err)
	}

	err := Convert(context.Background(), Options{Converter: converter, Input: input, Output: output})
	if err == nil || !strings.Contains(err.Error(), "converter output is not a regular file") {
		t.Fatalf("Convert error=%v, want replaced-temporary-output rejection", err)
	}
	if got, readErr := os.ReadFile(target); readErr != nil || string(got) != sentinel {
		t.Fatalf("converter target content changed: got=%q err=%v", got, readErr)
	}
	if info, statErr := os.Stat(target); statErr != nil {
		t.Fatal(statErr)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("converter target permissions=%#o, want %#o", info.Mode().Perm(), 0o600)
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("final output was published from a symlinked temporary path: stat=%v", statErr)
	}
}

func TestConvertRejectsConverterReplacingTemporaryOutputWithHardLink(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	output := filepath.Join(root, "output.dat")
	target := filepath.Join(root, "target.dat")
	converter := filepath.Join(root, "converter.sh")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const sentinel = "preserve hard-link target\n"
	if err := os.WriteFile(target, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"for arg in \"$@\"; do\n" +
		"  case \"$arg\" in\n" +
		"    --output_file=*)\n" +
		"      temp=\"${arg#--output_file=}\"\n" +
		"      rm -f \"$temp\"\n" +
		"      ln \"$(dirname \"$temp\")/target.dat\" \"$temp\"\n" +
		"      ;;\n" +
		"  esac\n" +
		"done\n"
	if err := os.WriteFile(converter, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(converter, 0o755); err != nil {
		t.Fatal(err)
	}

	err := Convert(context.Background(), Options{Converter: converter, Input: input, Output: output})
	if err == nil || !strings.Contains(err.Error(), "converter replaced its temporary output file") {
		t.Fatalf("Convert error=%v, want replaced-temporary-output hard-link rejection", err)
	}
	if got, readErr := os.ReadFile(target); readErr != nil || string(got) != sentinel {
		t.Fatalf("converter hard-link target content changed: got=%q err=%v", got, readErr)
	}
	if info, statErr := os.Stat(target); statErr != nil {
		t.Fatal(statErr)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("converter hard-link target permissions=%#o, want %#o", info.Mode().Perm(), 0o600)
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("final output was published from a replaced temporary path: stat=%v", statErr)
	}
}
