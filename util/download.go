package util

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultDownloadWorkers = 6
	DefaultDownloadRetries = 3
	DefaultDownloadTimeout = 30 * time.Second
)

var defaultHTTPClient = &http.Client{
	Timeout: DefaultDownloadTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

// DownloadURLs downloads all valid HTTP(S) URLs into tempDir using a bounded
// worker pool, retries with exponential backoff, and returns the paths of
// successfully downloaded files.
//
// Behavior:
//   - Skips non-HTTP(S) URLs with a warning.
//   - Retries each URL up to DefaultDownloadRetries times on transient errors.
//   - Fails fast if the context is cancelled.
//   - Returns all successfully downloaded paths plus an error if any failed.
func DownloadURLs(inputURLs []string, tempDir string) (outputPaths []string, err error) {
	return DownloadURLsContext(context.Background(), inputURLs, tempDir, DefaultDownloadWorkers, DefaultDownloadRetries, DefaultDownloadTimeout)
}

// DownloadURLsContext is like DownloadURLs but with explicit concurrency and timeout controls.
func DownloadURLsContext(
	ctx context.Context,
	inputURLs []string,
	tempDir string,
	workers, retries int,
	timeout time.Duration,
) (outputPaths []string, err error) {
	if workers <= 0 {
		workers = DefaultDownloadWorkers
	}
	if retries <= 0 {
		retries = DefaultDownloadRetries
	}
	if timeout <= 0 {
		timeout = DefaultDownloadTimeout
	}

	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}

	// Validate and normalize URLs first.
	validURLs := make([]string, 0, len(inputURLs))
	for _, raw := range inputURLs {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}
		parsed, parseErr := url.Parse(u)
		if parseErr != nil || parsed.Host == "" {
			log.Printf("[Warning]: skipping invalid URL: %s", u)
			continue
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			log.Printf("[Warning]: skipping non-HTTP(S) URL: %s", u)
			continue
		}
		validURLs = append(validURLs, u)
	}

	if len(validURLs) == 0 {
		return nil, errors.New("no valid HTTP(S) URLs to download")
	}

	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating temp directory %q: %w", tempDir, err)
	}

	type job struct {
		url string
	}
	type result struct {
		url  string
		path string
		err  error
	}

	jobs := make(chan job, len(validURLs))
	results := make(chan result, len(validURLs))

	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := range jobs {
				path, err := downloadWithRetry(ctx, client, j.url, tempDir, retries)
				results <- result{
					url:  j.url,
					path: path,
					err:  err,
				}
			}
		}()
	}

	go func() {
		for _, u := range validURLs {
			jobs <- job{url: u}
		}
		close(jobs)
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	var (
		successes []result
		failures  []result
	)

	for r := range results {
		if r.err != nil {
			failures = append(failures, r)
			log.Printf("[Warning]: failed to download %s: %v", r.url, r.err)
		} else {
			successes = append(successes, r)
		}
	}

	// Sort for determinism (same order as input after filtering).
	sort.Slice(successes, func(i, j int) bool {
		return successes[i].url < successes[j].url
	})

	outputPaths = make([]string, 0, len(successes))
	for _, s := range successes {
		outputPaths = append(outputPaths, s.path)
	}

	if len(failures) > 0 {
		err = fmt.Errorf("%d/%d URLs failed to download", len(failures), len(validURLs))
	}

	return outputPaths, err
}

func downloadWithRetry(ctx context.Context, client *http.Client, rawURL, tempDir string, retries int) (string, error) {
	var lastErr error

	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt*attempt) * 500 * time.Millisecond
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}

		path, err := downloadOnce(ctx, client, rawURL, tempDir)
		if err == nil {
			return path, nil
		}

		lastErr = err
	}

	return "", lastErr
}

func downloadOnce(ctx context.Context, client *http.Client, rawURL, tempDir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "text/plain, text/*;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", "filtrite/optimized")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// Drain a bit to allow connection reuse.
		io.CopyN(io.Discard, resp.Body, 16*1024)
		return "", fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}

	filename := generateFilename(rawURL)
	path := filepath.Join(tempDir, filename)

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create file %q: %w", path, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, io.LimitReader(resp.Body, 100*1024*1024)); err != nil {
		return "", fmt.Errorf("write file %q: %w", path, err)
	}

	return path, nil
}

func generateFilename(urlStr string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(urlStr))
	return hex.EncodeToString(h.Sum(nil)) + ".txt"
}
