package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"ant0ny54/filtrite/util"
)

const (
	tmpDir  = "tmp"
	listDir = "lists"
	distDir = "dist"
	logDir  = "logs"

	// Chromium/Bromite kMaxBodySize.
	// Bromite accepts filter files up to 20 MiB.
	bromiteMaxFilterSize = 20 * 1024 * 1024
)

type generationError struct {
	file string
	err  error
}

// safeListName produces a filesystem-safe name from a list file path.
func safeListName(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	name = strings.TrimSpace(name)

	name = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return '_'
		case r == '/', r == '\\', r == ':', r == '*', r == '?', r == '"', r == '<', r == '>', r == '|':
			return '_'
		default:
			return r
		}
	}, name)

	if name == "" {
		return "filter-list"
	}

	return name
}

func isListFile(path string, info os.FileInfo) bool {
	if info == nil || info.IsDir() {
		return false
	}

	return strings.EqualFold(filepath.Ext(path), ".txt")
}

func ensureDirectories() error {
	directories := []string{
		listDir,
		distDir,
		logDir,
	}

	for _, directory := range directories {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("creating directory %q: %w", directory, err)
		}
	}

	return nil
}

func generateFilterList(ctx context.Context, listTextFile string) error {
	listName := safeListName(listTextFile)

	outputFile := filepath.Join(distDir, listName+".dat")
	logFile := filepath.Join(logDir, listName+".log")

	fmt.Printf("::group::List: %s\n", listName)
	defer fmt.Println("::endgroup::")

	filterListURLs, err := util.ReadListFile(listTextFile)
	if err != nil {
		return fmt.Errorf("reading list file: %w", err)
	}

	if len(filterListURLs) == 0 {
		return fmt.Errorf("no filter-list URLs found in %q", listTextFile)
	}

	// Create a fresh temporary directory for this specific list.
	listTmpDir := filepath.Join(tmpDir, listName)
	if err := os.RemoveAll(listTmpDir); err != nil {
		return fmt.Errorf("removing temporary directory for %q: %w", listName, err)
	}
	if err := os.MkdirAll(listTmpDir, 0o755); err != nil {
		return fmt.Errorf("creating temporary directory for %q: %w", listName, err)
	}

	defer func() {
		if err := os.RemoveAll(listTmpDir); err != nil {
			log.Printf("warning: removing temporary directory %q: %v", listTmpDir, err)
		}
	}()

	log.Printf(
		"Downloading %d filter lists for %s",
		len(filterListURLs),
		listName,
	)

	// util.DownloadURLs now handles concurrency, retries, and timeouts.
	paths, err := util.DownloadURLs(filterListURLs, listTmpDir)
	if err != nil {
		// We still proceed if at least some lists were downloaded.
		log.Printf("warning: some downloads failed: %v", err)
	}

	if len(paths) == 0 {
		return fmt.Errorf(
			"all filter lists failed to download: %d attempted",
			len(filterListURLs),
		)
	}

	log.Printf(
		"Downloaded %d/%d filter lists",
		len(paths),
		len(filterListURLs),
	)

	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return fmt.Errorf("creating distribution directory: %w", err)
	}

	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}

	// Remove previous output before generating the new file.
	if err := os.Remove(outputFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing previous output %q: %w", outputFile, err)
	}

	log.Printf("Converting ruleset for legacy Bromite engine")

	if err := util.GenerateDistributableList(paths, outputFile, logFile); err != nil {
		return fmt.Errorf("generating distributable list: %w", err)
	}

	fileInfo, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("checking generated output: %w", err)
	}

	if fileInfo.Size() == 0 {
		return fmt.Errorf("generated filter file is empty")
	}

	if fileInfo.Size() > bromiteMaxFilterSize {
		return fmt.Errorf(
			"filter list is too large for Bromite: %d bytes > %d bytes",
			fileInfo.Size(),
			bromiteMaxFilterSize,
		)
	}

	log.Printf(
		"Generated %s: %d bytes",
		outputFile,
		fileInfo.Size(),
	)

	if err := util.AppendReleaseList(
		listTextFile,
		len(paths),
		len(filterListURLs),
	); err != nil {
		return fmt.Errorf("generating release list: %w", err)
	}

	return nil
}

func processLists(ctx context.Context) []generationError {
	var failures []generationError
	var mu sync.Mutex

	entries, err := os.ReadDir(listDir)
	if err != nil {
		return []generationError{
			{file: listDir, err: fmt.Errorf("reading list directory: %w", err)},
		}
	}

	// Sequential per-list processing; safe and simple.
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := filepath.Join(listDir, entry.Name())

		info, err := entry.Info()
		if err != nil {
			failures = append(failures, generationError{
				file: path,
				err:  fmt.Errorf("reading file info: %w", err),
			})
			continue
		}

		if !isListFile(path, info) {
			continue
		}

		if err := generateFilterList(ctx, path); err != nil {
			mu.Lock()
			failures = append(failures, generationError{
				file: path,
				err:  err,
			})
			mu.Unlock()

			log.Printf(
				"ERROR: failed to generate %q: %v",
				path,
				err,
			)
		}
	}

	return failures
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime)

	if err := ensureDirectories(); err != nil {
		log.Fatalf("initialization failed: %v", err)
	}

	// Root context for the whole run; can be extended with timeouts or cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	failures := processLists(ctx)

	if len(failures) == 0 {
		log.Println("All filter lists generated successfully")
		return
	}

	log.Printf(
		"Generation completed with %d failure(s)",
		len(failures),
	)

	for _, failure := range failures {
		log.Printf(
			"FAILED %s: %v",
			failure.file,
			failure.err,
		)
	}

	os.Exit(1)
}
