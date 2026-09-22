package download

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultWorkers       = 8
	DefaultRetries       = 4
	DefaultTimeout       = 2 * time.Minute
	MaxSourceBytes int64 = 50 << 20
	MaxTotalBytes  int64 = 500 << 20
	MaxSources           = 100
	MaxRedirects         = 5
)

var ErrTooLarge = errors.New("download too large")

type Result struct {
	URL    string
	Path   string
	Bytes  int64
	Err    error
	Cached bool
}

type client struct{ http *http.Client }

func newClient(timeout time.Duration) *client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConns: 32, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 8, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, ForceAttemptHTTP2: true}
	return &client{http: &http.Client{
		Transport: tr, Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return fmt.Errorf("too many redirects")
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && strings.EqualFold(req.URL.Scheme, "http") {
				return fmt.Errorf("refusing HTTPS downgrade")
			}
			return nil
		},
	}}
}

func URLsFromFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open source list: %w", err)
	}
	defer f.Close()

	seen := make(map[string]struct{})
	urls := make([]string, 0, 32)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 16<<10), 1<<20)
	for lineNo := 1; sc.Scan(); lineNo++ {
		rawLine := strings.TrimPrefix(strings.TrimSuffix(sc.Text(), "\r"), "\uFEFF")
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if rawLine != trimmed {
			return nil, fmt.Errorf("source list line %d: leading/trailing whitespace is not allowed", lineNo)
		}

		u, err := url.ParseRequestURI(rawLine)
		if err != nil || u.Hostname() == "" || !strings.EqualFold(u.Scheme, "https") || u.User != nil {
			if err == nil {
				err = fmt.Errorf("must be an HTTPS URL without embedded credentials")
			}
			return nil, fmt.Errorf("source list line %d: %q: %w", lineNo, rawLine, err)
		}
		u.Scheme = "https"
		canonical := u.String()
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		urls = append(urls, canonical)
		if len(urls) > MaxSources {
			return nil, fmt.Errorf("more than %d sources", MaxSources)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read source list: %w", err)
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("no HTTPS sources configured")
	}
	return urls, nil
}

func All(ctx context.Context, urls []string, dir string, workers, retries int, timeout time.Duration) ([]Result, error) {
	return all(ctx, urls, dir, "", workers, retries, timeout)
}

// AllWithCache shares successfully downloaded sources across multiple list
// manifests. The cache is deliberately keyed by the canonical URL and uses
// atomic replacement, so a later manifest in the same build can reuse a
// source without downloading it again.
func AllWithCache(ctx context.Context, urls []string, dir, cacheDir string, workers, retries int, timeout time.Duration) ([]Result, error) {
	return all(ctx, urls, dir, cacheDir, workers, retries, timeout)
}

func all(ctx context.Context, urls []string, dir, cacheDir string, workers, retries int, timeout time.Duration) ([]Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if workers <= 0 {
		workers = DefaultWorkers
	}
	if retries < 0 {
		retries = 0
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create download directory: %w", err)
	}
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return nil, fmt.Errorf("create source cache directory: %w", err)
		}
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("no URLs")
	}
	if len(urls) > MaxSources {
		return nil, fmt.Errorf("too many sources")
	}
	if workers > len(urls) {
		workers = len(urls)
	}

	c := newClient(timeout)
	type job struct {
		index int
		url   string
	}
	jobs := make(chan job)
	results := make([]Result, len(urls))
	budget := MaxTotalBytes

	// Cached files are returned immediately and do not consume the network
	// download budget because they were already accounted for when written.
	missing := make([]job, 0, len(urls))
	for i, rawURL := range urls {
		if cacheDir != "" {
			if path, n, ok := cachedFile(cacheDir, rawURL); ok {
				results[i] = Result{URL: rawURL, Path: path, Bytes: n, Cached: true}
				continue
			}
		}
		missing = append(missing, job{index: i, url: rawURL})
	}
	if len(missing) == 0 {
		return results, nil
	}
	// Re-cap after subtracting cache hits: a manifest that is mostly cached
	// (the common case on a second build.sh list) should not spin up a full
	// worker pool sized for every URL when only a handful still need a
	// network round trip.
	if workers > len(missing) {
		workers = len(missing)
	}

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := range jobs {
				destination := dir
				if cacheDir != "" {
					destination = cacheDir
				}
				path, n, err := get(ctx, c, j.url, destination, retries, &budget)
				results[j.index] = Result{URL: j.url, Path: path, Bytes: n, Err: err}
			}
		}()
	}

	for _, j := range missing {
		select {
		case jobs <- j:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return results, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()

	var errs []error
	for _, result := range results {
		if result.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", result.URL, result.Err))
		}
	}
	if len(errs) > 0 {
		return results, errors.Join(errs...)
	}
	return results, nil
}

func cachedFile(cacheDir, rawURL string) (string, int64, bool) {
	path := filepath.Join(cacheDir, shaName(rawURL)+".txt")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxSourceBytes {
		if err == nil {
			_ = os.Remove(path)
		}
		return "", 0, false
	}
	if htmlError(path) {
		_ = os.Remove(path)
		return "", 0, false
	}
	return path, info.Size(), true
}

func get(ctx context.Context, c *client, rawURL, dir string, retries int, budget *int64) (string, int64, error) {
	var last error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(500*(1<<min(attempt-1, 5))) * time.Millisecond):
			case <-ctx.Done():
				return "", 0, ctx.Err()
			}
		}
		p, n, e := getOnce(ctx, c, rawURL, dir, budget)
		if e == nil {
			return p, n, nil
		}
		last = e
		if !retryable(e) {
			break
		}
	}
	return "", 0, fmt.Errorf("after %d attempts: %w", retries+1, last)
}

func getOnce(ctx context.Context, c *client, rawURL, dir string, budget *int64) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Accept", "text/plain, text/*;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", "filtrite/4.0 (legacy-subresource-filter-builder)")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, &statusError{resp.StatusCode, resp.Status}
	}
	if resp.ContentLength > MaxSourceBytes {
		return "", 0, ErrTooLarge
	}
	name := shaName(rawURL) + ".txt"
	final := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(tmpPath) }
	defer func() { os.Remove(tmpPath) }()
	reader := &budgetReader{r: io.LimitReader(resp.Body, MaxSourceBytes+1), remaining: budget}
	n, err := io.Copy(tmp, reader)
	if err != nil {
		cleanup()
		return "", 0, err
	}
	if n > MaxSourceBytes {
		cleanup()
		return "", n, ErrTooLarge
	}
	if n == 0 {
		cleanup()
		return "", 0, fmt.Errorf("empty response")
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", n, err
	}
	if err := os.Rename(tmpPath, final); err != nil {
		return "", n, err
	}
	if htmlError(final) {
		os.Remove(final)
		return "", 0, fmt.Errorf("HTML error page")
	}
	return final, n, nil
}

type budgetReader struct {
	r         io.Reader
	remaining *int64
}

func (r *budgetReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	for {
		remaining := atomic.LoadInt64(r.remaining)
		if remaining <= 0 {
			// We may have consumed the final permitted byte exactly. Probe the
			// underlying reader once so an actual EOF is accepted, while any
			// additional source byte still fails the global budget.
			var probe [1]byte
			n, err := r.r.Read(probe[:])
			if n > 0 {
				return 0, ErrTooLarge
			}
			if err == nil {
				continue
			}
			return 0, err
		}

		want := int64(len(p))
		if want > remaining {
			want = remaining
		}
		if !atomic.CompareAndSwapInt64(r.remaining, remaining, remaining-want) {
			continue
		}

		n, err := r.r.Read(p[:want])
		if unused := want - int64(n); unused > 0 {
			atomic.AddInt64(r.remaining, unused)
		}
		return n, err
	}
}

func htmlError(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := io.ReadFull(f, buf) // short files return ErrUnexpectedEOF with n valid
	s := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(string(buf[:n])), "\ufeff"))
	return strings.HasPrefix(s, "<!doctype html") || strings.HasPrefix(s, "<html") || strings.HasPrefix(s, "<head") || strings.HasPrefix(s, "<body")
}

type statusError struct {
	Code   int
	Status string
}

func (e *statusError) Error() string {
	return "unexpected HTTP status: " + strconv.Itoa(e.Code) + " " + e.Status
}
func retryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTooLarge) {
		return false
	}
	var s *statusError
	if errors.As(err, &s) {
		return s.Code == 408 || s.Code == 429 || s.Code == 500 || s.Code == 502 || s.Code == 503 || s.Code == 504
	}
	// A body cut off mid-transfer is a transient network failure.
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}
func shaName(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
