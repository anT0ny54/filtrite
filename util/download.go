package util

import (
\t"context"
\t"crypto/sha256"
\t"encoding/hex"
\t"errors"
\t"fmt"
\t"io"
\t"log"
\t"net/http"
\t"net/url"
\t"os"
\t"path/filepath"
\t"sort"
\t"strings"
\t"sync"
\t"time"
)

const (
\tDefaultDownloadWorkers = 6
\tDefaultDownloadRetries = 3
\tDefaultDownloadTimeout = 30 * time.Second
)

var defaultHTTPClient = &http.Client{
\tTimeout: DefaultDownloadTimeout,
\tCheckRedirect: func(req *http.Request, via []*http.Request) error {
\t\tif len(via) >= 5 {
\t\t\treturn errors.New("too many redirects")
\t\t}
\t\treturn nil
\t},
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
\treturn DownloadURLsContext(context.Background(), inputURLs, tempDir, DefaultDownloadWorkers, DefaultDownloadRetries, DefaultDownloadTimeout)
}

// DownloadURLsContext is like DownloadURLs but with explicit concurrency and timeout controls.
func DownloadURLsContext(
\tctx context.Context,
\tinputURLs []string,
\ttempDir string,
\tworkers, retries int,
\ttimeout time.Duration,
) (outputPaths []string, err error) {
\tif workers <= 0 {
\t\tworkers = DefaultDownloadWorkers
\t}
\tif retries <= 0 {
\t\tretries = DefaultDownloadRetries
\t}
\tif timeout <= 0 {
\t\ttimeout = DefaultDownloadTimeout
\t}

\tclient := &http.Client{
\t\tTimeout: timeout,
\t\tCheckRedirect: func(req *http.Request, via []*http.Request) error {
\t\t\tif len(via) >= 5 {
\t\t\t\treturn errors.New("too many redirects")
\t\t\t}
\t\t\treturn nil
\t\t},
\t}

\t// Validate and normalize URLs first.
\tvalidURLs := make([]string, 0, len(inputURLs))
\tfor _, raw := range inputURLs {
\t\tu := strings.TrimSpace(raw)
\t\tif u == "" {
\t\t\tcontinue
\t\t}
\t\tparsed, parseErr := url.Parse(u)
\t\tif parseErr != nil || parsed.Host == "" {
\t\t\tlog.Printf("[Warning]: skipping invalid URL: %s", u)
\t\t\tcontinue
\t\t}
\t\tif parsed.Scheme != "http" && parsed.Scheme != "https" {
\t\t\tlog.Printf("[Warning]: skipping non-HTTP(S) URL: %s", u)
\t\t\tcontinue
\t\t}
\t\tvalidURLs = append(validURLs, u)
\t}

\tif len(validURLs) == 0 {
\t\treturn nil, errors.New("no valid HTTP(S) URLs to download")
\t}

\tif err := os.MkdirAll(tempDir, 0o755); err != nil {
\t\treturn nil, fmt.Errorf("creating temp directory %q: %w", tempDir, err)
\t}

\ttype job struct {
\t\turl string
\t}
\ttype result struct {
\t\turl  string
\t\tpath string
\t\terr  error
\t}

\tjobs := make(chan job, len(validURLs))
\tresults := make(chan result, len(validURLs))

\tvar wg sync.WaitGroup
\twg.Add(workers)

\tfor i := 0; i < workers; i++ {
\t\tgo func() {
\t\t\tdefer wg.Done()
\t\t\tfor j := range jobs {
\t\t\t\tpath, err := downloadWithRetry(ctx, client, j.url, tempDir, retries)
\t\t\t\tresults <- result{
\t\t\t\t\turl:  j.url,
\t\t\t\t\tpath: path,
\t\t\t\t\terr:  err,
\t\t\t\t}
\t\t\t}
\t\t}()
\t}

\tgo func() {
\t\tfor _, u := range validURLs {
\t\t\tjobs <- job{url: u}
\t\t}
\t\tclose(jobs)
\t}()

\tgo func() {
\t\twg.Wait()
\t\tclose(results)
\t}()

\tvar (
\t\tsuccesses []result
\t\tfailures  []result
\t)

\tfor r := range results {
\t\tif r.err != nil {
\t\t\tfailures = append(failures, r)
\t\t\tlog.Printf("[Warning]: failed to download %s: %v", r.url, r.err)
\t\t} else {
\t\t\tsuccesses = append(successes, r)
\t\t}
\t}

\t// Sort for determinism (same order as input after filtering).
\tsort.Slice(successes, func(i, j int) bool {
\t\treturn successes[i].url < successes[j].url
\t})

\toutputPaths = make([]string, 0, len(successes))
\tfor _, s := range successes {
\t\toutputPaths = append(outputPaths, s.path)
\t}

\tif len(failures) > 0 {
\t\terr = fmt.Errorf("%d/%d URLs failed to download", len(failures), len(validURLs))
\t}

\treturn outputPaths, err
}

func downloadWithRetry(ctx context.Context, client *http.Client, rawURL, tempDir string, retries int) (string, error) {
\tvar lastErr error

\tfor attempt := 0; attempt <= retries; attempt++ {
\t\tif attempt > 0 {
\t\t\tdelay := time.Duration(attempt*attempt) * 500 * time.Millisecond
\t\t\tselect {
\t\t\tcase <-ctx.Done():
\t\t\t\treturn "", ctx.Err()
\t\t\tcase <-time.After(delay):
\t\t\t}
\t\t}

\t\tpath, err := downloadOnce(ctx, client, rawURL, tempDir)
\t\tif err == nil {
\t\t\treturn path, nil
\t\t}

\t\tlastErr = err
\t}

\treturn "", lastErr
}

func downloadOnce(ctx context.Context, client *http.Client, rawURL, tempDir string) (string, error) {
\treq, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
\tif err != nil {
\t\treturn "", fmt.Errorf("create request: %w", err)
\t}
\treq.Header.Set("Accept", "text/plain, text/*;q=0.9, */*;q=0.1")
\treq.Header.Set("User-Agent", "filtrite/optimized")

\tresp, err := client.Do(req)
\tif err != nil {
\t\treturn "", err
\t}
\tdefer resp.Body.Close()

\tif resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
\t\t// Drain a bit to allow connection reuse.
\t\tio.CopyN(io.Discard, resp.Body, 16*1024)
\t\treturn "", fmt.Errorf("unexpected HTTP status %s", resp.Status)
\t}

\tfilename := generateFilename(rawURL)
\tpath := filepath.Join(tempDir, filename)

\tf, err := os.Create(path)
\tif err != nil {
\t\treturn "", fmt.Errorf("create file %q: %w", path, err)
\t}
\tdefer f.Close()

\tif _, err := io.Copy(f, io.LimitReader(resp.Body, 100*1024*1024)); err != nil {
\t\treturn "", fmt.Errorf("write file %q: %w", path, err)
\t}

\treturn path, nil
}

func generateFilename(urlStr string) string {
\th := sha256.New()
\t_, _ = h.Write([]byte(urlStr))
\treturn hex.EncodeToString(h.Sum(nil)) + ".txt"
}