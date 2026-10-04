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

func rejectAliasedPaths(input, output, logPath string) error {
	paths := []struct {
		name string
		path string
	}{
		{name: "input", path: input},
		{name: "output", path: output},
	}
	if logPath != "" {
		paths = append(paths, struct {
			name string
			path string
		}{name: "log", path: logPath})
	}

	for i := range paths {
		a, err := filepath.Abs(paths[i].path)
		if err != nil {
			return fmt.Errorf("resolve %s path %q: %w", paths[i].name, paths[i].path, err)
		}
		paths[i].path = filepath.Clean(a)
	}

	// Stat each canonical path exactly once; the pairwise loop below only
	// reads the cached results.
	infos := make([]os.FileInfo, len(paths))
	statErrs := make([]error, len(paths))
	for i := range paths {
		infos[i], statErrs[i] = os.Stat(paths[i].path)
	}

	for i := 0; i < len(paths); i++ {
		if statErrs[i] != nil && !os.IsNotExist(statErrs[i]) {
			return fmt.Errorf("stat %s path %q: %w", paths[i].name, paths[i].path, statErrs[i])
		}
		for j := i + 1; j < len(paths); j++ {
			if paths[i].path == paths[j].path {
				return fmt.Errorf("%s and %s paths must not alias: %q", paths[i].name, paths[j].name, paths[i].path)
			}
			if statErrs[i] == nil && statErrs[j] == nil && os.SameFile(infos[i], infos[j]) {
				return fmt.Errorf("%s and %s paths must not alias: %q and %q refer to the same file", paths[i].name, paths[j].name, paths[i].path, paths[j].path)
			}
		}
	}
	return nil
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
	if err := rejectAliasedPaths(o.Input, o.Output, o.Log); err != nil {
		return err
	}
	outDir := filepath.Dir(o.Output) // never empty: "." for a bare filename
	if err := os.MkdirAll(outDir, 0o755); err != nil {
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
		if err := os.MkdirAll(filepath.Dir(o.Log), 0o755); err != nil {
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
