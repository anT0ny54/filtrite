package util

import (
\t"context"
\t"fmt"
\t"os"
\t"os/exec"
\t"path/filepath"
\t"strings"
)

const (
\trulesetExecutable = "deps/ruleset_converter"
)

// GenerateDistributableList runs ruleset_converter to produce a Bromite-compatible
// .dat file from the given input filter-list files.
func GenerateDistributableList(inputPaths []string, output string, logPath string) (err error) {
\tif len(inputPaths) == 0 {
\t\treturn fmt.Errorf("no input paths provided")
\t}
\tif output == "" {
\t\treturn fmt.Errorf("output path is empty")
\t}

\t// Ensure output directory exists.
\toutDir := filepath.Dir(output)
\tif outDir != "" && outDir != "." {
\t\tif err := os.MkdirAll(outDir, 0o755); err != nil {
\t\t\treturn fmt.Errorf("creating output directory %q: %w", outDir, err)
\t\t}
\t}

\tctx := context.Background()

\targs := []string{
\t\t"--input_format=filter-list",
\t\t"--output_format=unindexed-ruleset",
\t\t"--input_files=" + strings.Join(inputPaths, ","),
\t\t"--output_file=" + output,
\t}

\tcmd := exec.CommandContext(ctx, rulesetExecutable, args...)

\tif logPath != "" {
\t\tlogDir := filepath.Dir(logPath)
\t\tif logDir != "" && logDir != "." {
\t\t\tif err := os.MkdirAll(logDir, 0o755); err != nil {
\t\t\t\treturn fmt.Errorf("creating log directory %q: %w", logDir, err)
\t\t\t}
\t\t}

\t\tf, err := os.Create(logPath)
\t\tif err != nil {
\t\t\treturn fmt.Errorf("creating log file %q: %w", logPath, err)
\t\t}
\t\tdefer f.Close()

\t\tcmd.Stdout = f
\t\tcmd.Stderr = f
\t}

\tif err := cmd.Run(); err != nil {
\t\treturn fmt.Errorf("ruleset_converter failed: %w", err)
\t}

\t// Basic sanity check: output must exist and be non-empty.
\tinfo, statErr := os.Stat(output)
\tif statErr != nil {
\t\treturn fmt.Errorf("output file %q not created: %w", output, statErr)
\t}
\tif info.Size() == 0 {
\t\treturn fmt.Errorf("output file %q is empty", output)
\t}

\treturn nil
}