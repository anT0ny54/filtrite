package util

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const rulesetExecutable = "deps/ruleset_converter"

// GenerateDistributableList runs ruleset_converter to generate a
// Bromite-compatible .dat file from the supplied filter-list files.
//
// The converter writes to a temporary file in the output directory.
// The temporary file is moved into place only after successful execution
// and validation, preventing partial or stale output files.
func GenerateDistributableList(
	ctx context.Context,
	inputPaths []string,
	output string,
	logPath string,
) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if len(inputPaths) == 0 {
		return fmt.Errorf("no input paths provided")
	}

	for i, inputPath := range inputPaths {
		if strings.TrimSpace(inputPath) == "" {
			return fmt.Errorf("input path %d is empty", i)
		}
	}

	if strings.TrimSpace(output) == "" {
		return fmt.Errorf("output path is empty")
	}

	outputDir := filepath.Dir(output)
	if outputDir == "" {
		outputDir = "."
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf(
			"create output directory %q: %w",
			outputDir,
			err,
		)
	}

	// Create a unique temporary filename in the same directory as the
	// final output. Keeping both files in the same directory allows rename
	// to be atomic on supported filesystems.
	tempFile, err := os.CreateTemp(
		outputDir,
		"."+filepath.Base(output)+".tmp-*",
	)
	if err != nil {
		return fmt.Errorf("create temporary output file: %w", err)
	}

	tempOutput := tempFile.Name()

	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempOutput)
		return fmt.Errorf("close temporary output file: %w", err)
	}

	// The converter should create and write the output itself.
	if err := os.Remove(tempOutput); err != nil {
		return fmt.Errorf("prepare temporary output file: %w", err)
	}

	defer func() {
		// The temporary file may still exist after a failed conversion.
		_ = os.Remove(tempOutput)
	}()

	args := []string{
		"--input_format=filter-list",
		"--output_format=unindexed-ruleset",
		"--input_files=" + strings.Join(inputPaths, ","),
		"--output_file=" + tempOutput,
	}

	cmd := exec.CommandContext(ctx, rulesetExecutable, args...)

	var logFile *os.File
	if strings.TrimSpace(logPath) != "" {
		logDir := filepath.Dir(logPath)
		if logDir == "" {
			logDir = "."
		}

		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return fmt.Errorf(
				"create log directory %q: %w",
				logDir,
				err,
			)
		}

		logFile, err = os.Create(logPath)
		if err != nil {
			return fmt.Errorf("create log file %q: %w", logPath, err)
		}

		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	runErr := cmd.Run()

	var closeLogErr error
	if logFile != nil {
		closeLogErr = logFile.Close()
	}

	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf(
				"ruleset_converter canceled: %w",
				ctxErr,
			)
		}

		if closeLogErr != nil {
			return fmt.Errorf(
				"ruleset_converter failed: %w; close log file: %v",
				runErr,
				closeLogErr,
			)
		}

		return fmt.Errorf("ruleset_converter failed: %w", runErr)
	}

	if closeLogErr != nil {
		return fmt.Errorf("close log file %q: %w", logPath, closeLogErr)
	}

	info, err := os.Stat(tempOutput)
	if err != nil {
		return fmt.Errorf(
			"converter did not create output file %q: %w",
			tempOutput,
			err,
		)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf(
			"converter output %q is not a regular file",
			tempOutput,
		)
	}

	if info.Size() == 0 {
		return fmt.Errorf(
			"converter output %q is empty",
			tempOutput,
		)
	}

	if err := os.Rename(tempOutput, output); err != nil {
		return fmt.Errorf(
			"install generated output %q: %w",
			output,
			err,
		)
	}

	return nil
}
