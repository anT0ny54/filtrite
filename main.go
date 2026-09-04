package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
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
	job       listJob
	downloaded int
	attempted  int
}

type workerResult struct {
	result *generationResult
	err    *generationError
}

// safeListName converts a list filename into a filesystem-safe name.
func safeListName(path string) string {
	filename := filepath.Base(path)
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	name = strings.TrimSpace(name)

	name = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return '_'
		case r == '/', r == '\\', r == ':',
			r == '*', r == '?', r == '"',
			r == '<', r == '>', r == '|':
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
	for _, directory := range []string{
		listDir,
		distDir,
		logDir,
		tmpDir,
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("creating directory %q: %w", directory, err)
		}
	}

	return nil
}

// createJobs creates deterministic, collision-resistant names for every list.
func createJobs(paths []string) ([]listJob, error) {
	sort.Strings(paths)

	nameCounts := make(map[string]int)
	for _, path := range paths {
		nameCounts[safeListName(path)]++
	}

	jobs := make([]listJob, 0, len(paths))
	usedNames := make(map[string]string, len(paths))

	for _, path := range paths {
		baseName := safeListName(path)
		listName := baseName

		// Add a short hash if multiple files resolve to the same safe name.
		if nameCounts[baseName] > 1 {
			hash := sha256.Sum256([]byte(path))
			listName = fmt.Sprintf("%s-%x", baseName, hash[:6])
		}

		if previousPath, exists := usedNames[listName]; exists {
			return nil, fmt.Errorf(
				"list-name collision between %q and %q",
				previousPath,
				path,
			)
		}

		usedNames[listName] = path

		jobs = append(jobs, listJob{
			path: path,
			name: listName,
		})
	}

	return jobs, nil
}

func generateFilterList(
	ctx context.Context,
	job listJob,
) (*generationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	outputFile := filepath.Join(distDir, job.name+".dat")
	logFile := filepath.Join(logDir, job.name+".log")
	listTmpDir := filepath.Join(tmpDir, job.name)

	log.Printf("Starting generation for %s", job.path)

	filterListURLs, err := util.ReadListFile(job.path)
	if err != nil {
		return nil, fmt.Errorf("reading list file: %w", err)
	}

	if len(filterListURLs) == 0 {
		return nil, fmt.Errorf("no filter-list URLs found in %q", job.path)
	}

	if err := os.RemoveAll(listTmpDir); err != nil {
		return nil, fmt.Errorf(
			"removing temporary directory for %q: %w",
			job.path,
			err,
		)
	}

	if err := os.MkdirAll(listTmpDir, 0o755); err != nil {
		return nil, fmt.Errorf(
			"creating temporary directory for %q: %w",
			job.path,
			err,
		)
	}

	defer func() {
		if err := os.RemoveAll(listTmpDir); err != nil {
			log.Printf(
				"warning: removing temporary directory %q: %v",
				listTmpDir,
				err,
			)
		}
	}()

	log.Printf(
		"Downloading %d filter lists for %s",
		len(filterListURLs),
		job.name,
	)

	paths, downloadErr := util.DownloadURLs(filterListURLs, listTmpDir)
	if downloadErr != nil {
		log.Printf(
			"warning: some downloads failed for %s: %v",
			job.name,
			downloadErr,
		)
	}

	if len(paths) == 0 {
		return nil, fmt.Errorf(
			"all filter lists failed to download: %d attempted",
			len(filterListURLs),
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	log.Printf(
		"Downloaded %d/%d filter lists for %s",
		len(paths),
		len(filterListURLs),
		job.name,
	)

	// Generate into the list-specific temporary directory first.
	// The final output is installed only after successful validation.
	tempOutputFile := filepath.Join(listTmpDir, job.name+".dat")

	log.Printf(
		"Converting ruleset for legacy Bromite engine: %s",
		job.name,
	)

	if err := util.GenerateDistributableList(
		ctx,
		paths,
		tempOutputFile,
		logFile,
	); err != nil {
		return nil, fmt.Errorf("generating distributable list: %w", err)
	}

	fileInfo, err := os.Stat(tempOutputFile)
	if err != nil {
		return nil, fmt.Errorf("checking generated output: %w", err)
	}

	if fileInfo.Size() == 0 {
		return nil, fmt.Errorf("generated filter file is empty")
	}

	if fileInfo.Size() > bromiteMaxFilterSize {
		return nil, fmt.Errorf(
			"filter list is too large for Bromite: %d bytes > %d bytes",
			fileInfo.Size(),
			bromiteMaxFilterSize,
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Install the completed file only after generation and validation succeed.
	// This prevents partially generated output from remaining in dist/.
	if err := os.Remove(outputFile); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf(
			"removing previous output %q: %w",
			outputFile,
			err,
		)
	}

	if err := os.Rename(tempOutputFile, outputFile); err != nil {
		return nil, fmt.Errorf(
			"installing generated output %q: %w",
			outputFile,
			err,
		)
	}

	log.Printf(
		"Generated %s: %d bytes",
		outputFile,
		fileInfo.Size(),
	)

	return &generationResult{
		job:        job,
		downloaded: len(paths),
		attempted:  len(filterListURLs),
	}, nil
}

func discoverJobs() ([]listJob, []generationError) {
	entries, err := os.ReadDir(listDir)
	if err != nil {
		return nil, []generationError{
			{
				file: listDir,
				err:  fmt.Errorf("reading list directory: %w", err),
			},
		}
	}

	var (
		paths    []string
		failures []generationError
	)

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

		if isListFile(path, info) {
			paths = append(paths, path)
		}
	}

	jobs, err := createJobs(paths)
	if err != nil {
		failures = append(failures, generationError{
			file: listDir,
			err:  err,
		})
	}

	return jobs, failures
}

func processLists(ctx context.Context) []generationError {
	jobs, failures := discoverJobs()
	if len(jobs) == 0 {
		return failures
	}

	workerCount := maxWorkers
	if workerCount > len(jobs) {
		workerCount = len(jobs)
	}

	jobCh := make(chan listJob)
	resultCh := make(chan workerResult, len(jobs))

	var wg sync.WaitGroup
	wg.Add(workerCount)

	worker := func() {
		defer wg.Done()

		for job := range jobCh {
			if err := ctx.Err(); err != nil {
				resultCh <- workerResult{
					err: &generationError{
						file: job.path,
						err:  err,
					},
				}
				continue
			}

			listCtx, cancel := context.WithTimeout(ctx, listTimeout)
			result, err := generateFilterList(listCtx, job)
			cancel()

			if err != nil {
				resultCh <- workerResult{
					err: &generationError{
						file: job.path,
						err:  err,
					},
				}
				continue
			}

			resultCh <- workerResult{
				result: result,
			}
		}
	}

	for i := 0; i < workerCount; i++ {
		go worker()
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

	var successful []generationResult

	for result := range resultCh {
		if result.err != nil {
			log.Printf(
				"ERROR: failed to generate %q: %v",
				result.err.file,
				result.err.err,
			)
			failures = append(failures, *result.err)
			continue
		}

		if result.result != nil {
			successful = append(successful, *result.result)
		}
	}

	// Update the shared release list sequentially and deterministically.
	// This avoids concurrent read/modify/write operations inside
	// util.AppendReleaseList.
	sort.Slice(successful, func(i, j int) bool {
		return successful[i].job.path < successful[j].job.path
	})

	for _, result := range successful {
		if err := util.AppendReleaseList(
			result.job.path,
			result.downloaded,
			result.attempted,
		); err != nil {
			failures = append(failures, generationError{
				file: result.job.path,
				err:  fmt.Errorf("generating release list: %w", err),
			})

			log.Printf(
				"ERROR: failed to update release list for %q: %v",
				result.job.path,
				err,
			)
		}
	}

	sort.Slice(failures, func(i, j int) bool {
		return failures[i].file < failures[j].file
	})

	return failures
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime)

	if err := ensureDirectories(); err != nil {
		log.Fatalf("initialization failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
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
		log.Printf("FAILED %s: %v", failure.file, failure.err)
	}

	os.Exit(1)
}
