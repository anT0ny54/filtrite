package ruleset

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Options struct {
	Converter string
	Input     string
	Output    string
	Log       string
}

func Convert(ctx context.Context, o Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(o.Converter) == "" {
		return fmt.Errorf("converter is empty")
	}
	if strings.TrimSpace(o.Input) == "" {
		return fmt.Errorf("input is empty")
	}
	if strings.TrimSpace(o.Output) == "" {
		return fmt.Errorf("output is empty")
	}
	inputInfo, err := os.Stat(o.Input)
	if err != nil {
		return fmt.Errorf("input: %w", err)
	}
	if !inputInfo.Mode().IsRegular() {
		return fmt.Errorf("input is not a regular file: %s", o.Input)
	}
	outDir := filepath.Dir(o.Output)
	if outDir == "" {
		outDir = "."
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(outDir, "."+filepath.Base(o.Output)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temporary output: %w", err)
	}
	defer os.Remove(tmpPath)
	args := []string{"--input_format=filter-list", "--output_format=unindexed-ruleset", "--input_files=" + o.Input, "--output_file=" + tmpPath}
	cmd := exec.CommandContext(ctx, o.Converter, args...)
	var logf *os.File
	if o.Log != "" {
		logDir := filepath.Dir(o.Log)
		if logDir == "" {
			logDir = "."
		}
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return fmt.Errorf("create log directory: %w", err)
		}
		logf, err = os.Create(o.Log)
		if err != nil {
			return fmt.Errorf("create converter log: %w", err)
		}
		cmd.Stdout = logf
		cmd.Stderr = logf
	}
	runErr := cmd.Run()
	var closeErr error
	if logf != nil {
		closeErr = logf.Close()
	}
	if runErr != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("converter canceled: %w", ctx.Err())
		}
		if o.Log != "" {
			return fmt.Errorf("converter failed (see %s): %w", o.Log, runErr)
		}
		return fmt.Errorf("converter failed: %w", runErr)
	}
	if closeErr != nil {
		return closeErr
	}
	info, err := os.Stat(tmpPath)
	if err != nil {
		return fmt.Errorf("converter did not create output: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("converter created empty output")
	}
	// CreateTemp uses 0600; published artifacts should be world-readable.
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return fmt.Errorf("set output permissions: %w", err)
	}
	if err := os.Rename(tmpPath, o.Output); err != nil {
		return fmt.Errorf("replace output %s: %w", o.Output, err)
	}
	return nil
}
