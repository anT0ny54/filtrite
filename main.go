package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"ant0ny54/filtrite/util"
)

const (
	tmpDir  = "tmp"
	listDir = "lists"
	distDir = "dist"
	logDir  = "logs"

	bromiteMaxFilterSize = 20 * 1024 * 1024
	maxWorkers           = 4
	listTimeout          = 5 * time.Minute
)

type generationError struct {
	file string
	err  error
}

type listJob struct {
	path string
	name string
}

type generationResult struct {
	job        listJob
	downloaded int
	attempted  int
}

type workerResult struct {
	result *generationResult
	err    *generationError
}

func safeListName(path string) string {
	filename := filepath.Base(path)
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	name = strings.TrimSpace(name)

	name = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return '_'
		case r == '/', r == '\\', r == ':', r == '*', r == '?',
			r == '"', r == '<', r == '>', r == '|':
			return '_'
		default:
			return r
		}
	}, name)

	if name == "" || name == "." || name == ".." {
		return "filter-list"
	}
	return name
}

func isListFile(path string, info os.FileInfo) bool {
	return info != nil && !info.IsDir() &&
		strings.EqualFold(filepath.Ext(path), ".txt")
}

func ensureDirectories() error {
	for _, directory := range []string{listDir, distDir, logDir, tmpDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", directory, err)
		}
	}
	return nil
}

func createJobs(paths []string) ([]listJob, error) {
	sort.Strings(paths)

	nameCounts := make(map[string]int, len(paths))
	for _, path := range paths {
		nameCounts[safeListName(path)]++
	}

	jobs := make([]listJob, 0, len(paths))
	usedNames := make(map[string]string, len(paths))
	for _, path := range paths {
		baseName := safeListName(path)
		listName := baseName
		if nameCounts[baseName] > 1 {
			sum := sha256.Sum256([]byte(path))
			listName = fmt.Sprintf("%s-%x", baseName, sum[:6])
		}

		if previous, exists := usedNames[listName]; exists {
			return nil, fmt.Errorf("list-name collision between %q and %q", previous, path)
		}
		usedNames[listName] = path
		jobs = append(jobs, listJob{path: path, name: listName})
	}
	return jobs, nil
}

func generateFilterList(ctx context.Context, job listJob) (*generationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	outputFile := filepath.Join(distDir, job.name+".dat")
	logFile := filepath.Join(logDir, job.name+".log")
	listTmpDir := filepath.Join(tmpDir, job.name)

	log.Printf("Starting generation for %s", job.path)

	filterListURLs, err := util.ReadListFile(job.path)
	if err != nil {
		return nil, fmt.Errorf("read list file: %w", err)
	}
	if len(filterListURLs) == 0 {
		return nil, fmt.Errorf("no filter-list URLs found in %q", job.path)
	}

	if err := os.RemoveAll(listTmpDir); err != nil {
		return nil, fmt.Errorf("remove temporary directory for %q: %w", job.path, err)
	}
	if err := os.MkdirAll(listTmpDir, 0o755); err != nil {
		return nil, fmt.Errorf("create temporary directory for %q: %w", job.path, err)
	}
	defer func() {
		if err := os.RemoveAll(listTmpDir); err != nil {
			log.Printf("warning: remove temporary directory %q: %v", listTmpDir, err)
		}
	}()

	log.Printf("Downloading %d filter lists for %s", len(filterListURLs), job.name)
	paths, downloadErr := util.DownloadURLsContext(ctx, filterListURLs, listTmpDir,
		util.DefaultDownloadWorkers, util.DefaultDownloadRetries, util.DefaultDownloadTimeout)
	if downloadErr != nil {
		log.Printf("warning: some downloads failed for %s: %v", job.name, downloadErr)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("all filter lists failed to download: %d attempted", len(filterListURLs))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	log.Printf("Downloaded %d/%d filter lists for %s", len(paths), len(filterListURLs), job.name)

	tempOutputFile := filepath.Join(listTmpDir, job.name+".dat")
	if err := util.GenerateDistributableList(ctx, paths, tempOutputFile, logFile); err != nil {
		return nil, fmt.Errorf("generate distributable list: %w", err)
	}

	info, err := os.Stat(tempOutputFile)
	if err != nil {
		return nil, fmt.Errorf("check generated output: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("generated output is not a regular file")
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("generated filter file is empty")
	}
	if info.Size() > bromiteMaxFilterSize {
		return nil, fmt.Errorf("filter list is too large for Bromite: %d bytes > %d bytes",
			info.Size(), bromiteMaxFilterSize)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Rename is atomic when source and destination are on the same filesystem.
	// Remove the old file first for portability with filesystems that reject
	// replacing an existing destination.
	if err := os.Remove(outputFile); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove previous output %q: %w", outputFile, err)
	}
	if err := os.Rename(tempOutputFile, outputFile); err != nil {
		return nil, fmt.Errorf("install generated output %q: %w", outputFile, err)
	}

	log.Printf("Generated %s: %d bytes", outputFile, info.Size())
	return &generationResult{job: job, downloaded: len(paths), attempted: len(filterListURLs)}, nil
}

func discoverJobs() ([]listJob, []generationError) {
	entries, err := os.ReadDir(listDir)
	if err != nil {
		return nil, []generationError{{file: listDir, err: fmt.Errorf("read list directory: %w", err)}}
	}

	paths := make([]string, 0, len(entries))
	var failures []generationError
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".txt") {
			continue
		}
		path := filepath.Join(listDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			failures = append(failures, generationError{file: path, err: fmt.Errorf("read file info: %w", err)})
			continue
		}
		if isListFile(path, info) {
			paths = append(paths, path)
		}
	}

	jobs, err := createJobs(paths)
	if err != nil {
		failures = append(failures, generationError{file: listDir, err: err})
	}
	return jobs, failures
}

func processLists(ctx context.Context) []generationError {
	jobs, failures := discoverJobs()
	if len(jobs) == 0 {
		return failures
	}

	workerCount := min(maxWorkers, len(jobs))
	jobCh := make(chan listJob)
	resultCh := make(chan workerResult, len(jobs))

	var wg sync.WaitGroup
	wg.Add(workerCount)
	for range workerCount {
		go func() {
			defer wg.Done()
			for job := range jobCh {
				if err := ctx.Err(); err != nil {
					resultCh <- workerResult{err: &generationError{file: job.path, err: err}}
					continue
				}

				listCtx, cancel := context.WithTimeout(ctx, listTimeout)
				result, err := generateFilterList(listCtx, job)
				cancel()
				if err != nil {
					resultCh <- workerResult{err: &generationError{file: job.path, err: err}}
					continue
				}
				resultCh <- workerResult{result: result}
			}
		}()
	}

	go func() {
		defer close(jobCh)
		for _, job := range jobs {
			select {
			case jobCh <- job:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	successful := make([]generationResult, 0, len(jobs))
	for result := range resultCh {
		if result.err != nil {
			log.Printf("ERROR: failed to generate %q: %v", result.err.file, result.err.err)
			failures = append(failures, *result.err)
			continue
		}
		if result.result != nil {
			successful = append(successful, *result.result)
		}
	}

	sort.Slice(successful, func(i, j int) bool { return successful[i].job.path < successful[j].job.path })
	for _, result := range successful {
		if err := util.AppendReleaseList(result.job.path, result.downloaded, result.attempted); err != nil {
			failures = append(failures, generationError{
				file: result.job.path,
				err:  fmt.Errorf("update release list: %w", err),
			})
			log.Printf("ERROR: failed to update release list for %q: %v", result.job.path, err)
		}
	}

	sort.Slice(failures, func(i, j int) bool { return failures[i].file < failures[j].file })
	return failures
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime)

	if err := ensureDirectories(); err != nil {
		log.Fatalf("initialization failed: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	failures := processLists(ctx)
	if len(failures) == 0 {
		log.Println("All filter lists generated successfully")
		return
	}

	log.Printf("Generation completed with %d failure(s)", len(failures))
	for _, failure := range failures {
		log.Printf("FAILED %s: %v", failure.file, failure.err)
	}

	if errors.Is(ctx.Err(), context.Canceled) {
		log.Println("Generation canceled")
	}
	os.Exit(1)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
