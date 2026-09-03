package util

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	rulesetExecutable = "deps/ruleset_converter"
)

// GenerateDistributableList runs ruleset_converter to produce a Bromite-compatible
// .dat file from the given input filter-list files.
func GenerateDistributableList(inputPaths []string, output string, logPath string) (err error) {
	if len(inputPaths) == 0 {
		return fmt.Errorf("no input paths provided")
	}
	if output == "" {
		return fmt.Errorf("output path is empty")
	}

	// Ensure output directory exists.
	outDir := filepath.Dir(output)
	if outDir != "" && outDir != "." {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return fmt.Errorf("creating output directory %q: %w", outDir, err)
		}
	}

	ctx := context.Background()

	args := []string{
		"--input_format=filter-list",
		"--output_format=unindexed-ruleset",
		"--input_files=" + strings.Join(inputPaths, ","),
		"--output_file=" + output,
	}

	cmd := exec.CommandContext(ctx, rulesetExecutable, args...)

	if logPath != "" {
		logDir := filepath.Dir(logPath)
		if logDir != "" && logDir != "." {
			if err := os.MkdirAll(logDir, 0o755); err != nil {
				return fmt.Errorf("creating log directory %q: %w", logDir, err)
			}
		}

		f, err := os.Create(logPath)
		if err != nil {
			return fmt.Errorf("creating log file %q: %w", logPath, err)
		}
		defer f.Close()

		cmd.Stdout = f
		cmd.Stderr = f
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ruleset_converter failed: %w", err)
	}

	// Basic sanity check: output must exist and be non-empty.
	info, statErr := os.Stat(output)
	if statErr != nil {
		return fmt.Errorf("output file %q not created: %w", output, statErr)
	}
	if info.Size() == 0 {
		return fmt.Errorf("output file %q is empty", output)
	}

	return nil
}
