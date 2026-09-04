package util

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	DefaultDownloadWorkers = 6
	DefaultDownloadRetries = 3
	DefaultDownloadTimeout = 30 * time.Second

	MaxDownloadSize int64 = 100 * 1024 * 1024 // 100 MiB
	MaxRedirects           = 5
)

var (
	ErrNoValidURLs = errors.New("no valid HTTP(S) URLs")

	ErrContentTooLarge = errors.New("download exceeds maximum size")
)

type httpStatusError struct {
	Code   int
	Status string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP status: %s", e.Status)
}

type downloadJob struct {
	index int
	url   string
}

type downloadResult struct {
	index int
	path  string
	err   error
}

// DownloadURLs downloads all valid HTTP(S) URLs into tempDir.
//
// Successful paths are returned in the same order as the first occurrence
// of each input URL. Duplicate URLs are downloaded only once.
func DownloadURLs(
	inputURLs []string,
	tempDir string,
) ([]string, error) {
	return DownloadURLsContext(
		context.Background(),
		inputURLs,
		tempDir,
		DefaultDownloadWorkers,
		DefaultDownloadRetries,
		DefaultDownloadTimeout,
	)
}

// DownloadURLsContext is the configurable version of DownloadURLs.
func DownloadURLsContext(
	ctx context.Context,
	inputURLs []string,
	tempDir string,
	workers int,
	retries int,
	timeout time.Duration,
) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if workers <= 0 {
		workers = DefaultDownloadWorkers
	}
	if retries < 0 {
		retries = 0
	}
	if timeout <= 0 {
		timeout = DefaultDownloadTimeout
	}

	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return nil, fmt.Errorf("create temporary directory %q: %w", tempDir, err)
	}

	jobsList := make([]downloadJob, 0, len(inputURLs))
	seen := make(map[string]struct{}, len(inputURLs))

	for _, rawURL := range inputURLs {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			continue
		}

		if !isHTTPURL(rawURL) {
			continue
		}

		// Prevent duplicate URLs from concurrently writing the same file.
		if _, exists := seen[rawURL]; exists {
			continue
		}
		seen[rawURL] = struct{}{}

		jobsList = append(jobsList, downloadJob{
			index: len(jobsList),
			url:   rawURL,
		})
	}

	if len(jobsList) == 0 {
		return nil, ErrNoValidURLs
	}

	if workers > len(jobsList) {
		workers = len(jobsList)
	}

	client := newHTTPClient(timeout)

	jobs := make(chan downloadJob, len(jobsList))
	results := make(chan downloadResult, len(jobsList))

	for _, job := range jobsList {
		jobs <- job
	}
	close(jobs)

	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()

			for job := range jobs {
				path, err := downloadWithRetry(
					ctx,
					client,
					job.url,
					tempDir,
					retries,
				)

				results <- downloadResult{
					index: job.index,
					path:  path,
					err:   err,
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	resultByIndex := make([]downloadResult, len(jobsList))
	for result := range results {
		resultByIndex[result.index] = result
	}

	outputPaths := make([]string, 0, len(jobsList))
	var failures []error

	for _, result := range resultByIndex {
		if result.err != nil {
			failures = append(
				failures,
				fmt.Errorf("download failed: %w", result.err),
			)
			continue
		}

		outputPaths = append(outputPaths, result.path)
	}

	if len(failures) > 0 {
		return outputPaths, fmt.Errorf(
			"%d/%d downloads failed: %w",
			len(failures),
			len(jobsList),
			errors.Join(failures...),
		)
	}

	return outputPaths, nil
}

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

func isHTTPURL(rawURL string) bool {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return false
	}

	if parsed.Host == "" {
		return false
	}

	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func downloadWithRetry(
	ctx context.Context,
	client *http.Client,
	rawURL string,
	tempDir string,
	retries int,
) (string, error) {
	var lastErr error

	for attempt := 0; attempt <= retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		if attempt > 0 {
			if err := waitBeforeRetry(ctx, attempt); err != nil {
				return "", err
			}
		}

		path, err := downloadOnce(ctx, client, rawURL, tempDir)
		if err == nil {
			return path, nil
		}

		lastErr = err

		if !isRetryable(err) {
			break
		}
	}

	return "", fmt.Errorf(
		"%s after %d attempt(s): %w",
		rawURL,
		retries+1,
		lastErr,
	)
}

func waitBeforeRetry(ctx context.Context, attempt int) error {
	// 500 ms, 1 s, 2 s, 4 s...
	delay := 500 * time.Millisecond * time.Duration(1<<(attempt-1))

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	if errors.Is(err, ErrContentTooLarge) {
		return false
	}

	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.Code {
		case http.StatusRequestTimeout,
			http.StatusTooManyRequests,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		default:
			// Do not retry most 4xx responses, such as 404 or 403.
			return false
		}
	}

	// Network errors and temporary filesystem-independent failures
	// are generally worth retrying.
	return true
}

func downloadOnce(
	ctx context.Context,
	client *http.Client,
	rawURL string,
	tempDir string,
) (string, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		rawURL,
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "text/plain, text/*;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", "filtrite-downloader/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK ||
		resp.StatusCode >= http.StatusMultipleChoices {
		return "", &httpStatusError{
			Code:   resp.StatusCode,
			Status: resp.Status,
		}
	}

	if resp.ContentLength > MaxDownloadSize {
		return "", fmt.Errorf(
			"%w: %d bytes",
			ErrContentTooLarge,
			resp.ContentLength,
		)
	}

	filename := SHA256Filename(rawURL)
	finalPath := filepath.Join(tempDir, filename)

	// CreateTemp prevents collisions between concurrent downloads.
	tmpFile, err := os.CreateTemp(
		tempDir,
		"."+filename+".tmp-*",
	)
	if err != nil {
		return "", fmt.Errorf("create temporary file: %w", err)
	}

	tmpPath := tmpFile.Name()

	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}

	// Read one byte beyond the limit so oversized chunked responses
	// are detected even when Content-Length is unavailable.
	limitedBody := io.LimitReader(resp.Body, MaxDownloadSize+1)

	n, err := io.Copy(tmpFile, limitedBody)
	if err != nil {
		cleanup()
		return "", fmt.Errorf("write download: %w", err)
	}

	if n > MaxDownloadSize {
		cleanup()
		return "", fmt.Errorf(
			"%w: more than %d bytes",
			ErrContentTooLarge,
			MaxDownloadSize,
		)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temporary file: %w", err)
	}

	// Avoid committing a file after cancellation.
	if err := ctx.Err(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("rename temporary file: %w", err)
	}

	return finalPath, nil
}

// SHA256Filename returns a deterministic filename for a URL.
func SHA256Filename(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:]) + ".txt"
}
