package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"xarantolus/filtrite/util"
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

func safeListName(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	name = strings.TrimSpace(name)

	name = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return '_'
		case r == '/', r == '\\':
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
		if err := os.MkdirAll(directory, 0755); err != nil {
			return fmt.Errorf("creating directory %q: %w", directory, err)
		}
	}

	return nil
}

func generateFilterList(listTextFile string) error {
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
		return fmt.Errorf("no filter-list URLs found")
	}

	// Always start with a clean temporary directory. This prevents stale
	// downloaded lists from being included in a later build.
	if err := os.RemoveAll(tmpDir); err != nil {
		return fmt.Errorf("removing temporary directory: %w", err)
	}

	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("creating temporary directory: %w", err)
	}

	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			log.Printf("warning: removing temporary directory: %v", err)
		}
	}()

	log.Printf(
		"Downloading %d filter lists for %s",
		len(filterListURLs),
		listName,
	)

	paths, err := util.DownloadURLs(filterListURLs, tmpDir)
	if err != nil {
		return fmt.Errorf("downloading filter lists: %w", err)
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

	if err := os.MkdirAll(distDir, 0755); err != nil {
		return fmt.Errorf("creating distribution directory: %w", err)
	}

	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}

	// Remove a previous output before generating the new file. This avoids
	// accidentally serving an old valid-looking .dat file after a failed build.
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

func processLists() []generationError {
	var failures []generationError

	err := filepath.Walk(
		listDir,
		func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				failures = append(failures, generationError{
					file: path,
					err:  walkErr,
				})
				return nil
			}

			if !isListFile(path, info) {
				return nil
			}

			if err := generateFilterList(path); err != nil {
				failures = append(failures, generationError{
					file: path,
					err:  err,
				})

				log.Printf(
					"ERROR: failed to generate %q: %v",
					path,
					err,
				)
			}

			return nil
		},
	)

	if err != nil {
		failures = append(failures, generationError{
			file: listDir,
			err:  err,
		})
	}

	return failures
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime)

	if err := ensureDirectories(); err != nil {
		log.Fatalf("initialization failed: %v", err)
	}

	failures := processLists()

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
