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
	"syscall"
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
	MaxRetryAfter        = 2 * time.Minute
)

var ErrTooLarge = errors.New("download too large")

type Result struct {
	URL    string
	Path   string
	Bytes  int64
	Err    error
	Cached bool
}

func newClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConns: 32, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 8, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, ForceAttemptHTTP2: true}
	return &http.Client{
		Transport: tr, Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return fmt.Errorf("too many redirects")
			}
			if req.URL.User != nil {
				return fmt.Errorf("refusing redirect with embedded credentials")
			}
			if len(via) > 0 && strings.EqualFold(via[len(via)-1].URL.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "https") {
				return fmt.Errorf("refusing HTTPS downgrade")
			}
			return nil
		},
	}
}

func URLsFromFile(path string) ([]string, error) {
	// Inspect before opening: opening a named pipe for reading can block until
	// another process connects, but a source manifest must be a regular file.
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open source list: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("source list %q is not a regular file", path)
	}
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
		// A UTF-8 BOM is valid only at the start of the file. ScanLines already
		// strips a trailing CR from CRLF input; don't silently erase hidden
		// characters from the beginning of later manifest entries.
		rawLine := sc.Text()
		if lineNo == 1 {
			rawLine = strings.TrimPrefix(rawLine, "\uFEFF")
		}
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if rawLine != trimmed {
			return nil, fmt.Errorf("source list line %d: leading/trailing whitespace is not allowed", lineNo)
		}
		// A literal '#' starts a URL fragment, which is not sent in an HTTP
		// request. ParseRequestURI treats it as request-path/query data instead
		// of a fragment, so reject it explicitly; encode a literal hash as %23.
		if strings.Contains(rawLine, "#") {
			return nil, fmt.Errorf("source list line %d: URL fragments are not allowed; percent-encode a literal '#' as %%23", lineNo)
		}

		u, err := url.ParseRequestURI(rawLine)
		if err != nil || u.Hostname() == "" || !strings.EqualFold(u.Scheme, "https") || u.User != nil {
			if err == nil {
				err = fmt.Errorf("must be an HTTPS URL without embedded credentials")
			}
			return nil, fmt.Errorf("source list line %d: %q: %w", lineNo, rawLine, err)
		}
		if strings.HasSuffix(u.Host, ":") {
			return nil, fmt.Errorf("source list line %d: %q: port must be between 1 and 65535", lineNo, rawLine)
		}
		if port := u.Port(); port != "" {
			n, portErr := strconv.Atoi(port)
			if portErr != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("source list line %d: %q: port must be between 1 and 65535", lineNo, rawLine)
			}
		}
		// URL schemes and DNS hostnames are case-insensitive. Canonicalizing
		// both avoids duplicate downloads/cache entries for equivalent URLs.
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
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

// AllWithCache shares successfully downloaded sources across multiple list
// manifests. The cache is deliberately keyed by the canonical URL and uses
// atomic replacement, so a later manifest in the same build can reuse a
// source without downloading it again.
func AllWithCache(ctx context.Context, urls []string, dir, cacheDir string, workers, retries int, timeout time.Duration) ([]Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Honor cancellation and expired deadlines up front so behavior does not
	// depend on whether the sources happen to be cached.
	if err := ctx.Err(); err != nil {
		return nil, err
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
	// Reject invalid input before creating any directory.
	if len(urls) == 0 {
		return nil, fmt.Errorf("no URLs")
	}
	if len(urls) > MaxSources {
		return nil, fmt.Errorf("too many sources")
	}
	// Downloads go straight into the shared cache when one is configured, so
	// the per-caller staging directory is only created without a cache;
	// creating it unconditionally would resurrect directories such as
	// build/work/<name>/raw/ that cache-enabled builds never use.
	destination := dir
	if cacheDir != "" {
		destination = cacheDir
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return nil, fmt.Errorf("create download directory: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		// Each attempt can take up to `timeout`; cap retries so the
		// worst-case total attempt time fits inside the caller's deadline
		// instead of silently degrading into context cancellation.
		maxAttempts := int(time.Until(deadline) / timeout)
		if maxAttempts < 1 {
			maxAttempts = 1
		}
		if retries+1 > maxAttempts {
			retries = maxAttempts - 1
		}
	}

	c := newClient(timeout)
	defer c.CloseIdleConnections()
	type job struct {
		index int
		url   string
	}
	results := make([]Result, len(urls))
	budget := newByteBudget(MaxTotalBytes)

	// Cached files count toward the manifest budget just like fresh downloads.
	missing := make([]job, 0, len(urls))
	overflow := -1 // index of the first cached source that busts the budget
	for i, rawURL := range urls {
		if cacheDir != "" {
			if path, n, ok := cachedFile(cacheDir, rawURL); ok {
				// Cache accounting is single-threaded here; worker goroutines
				// are started only after this scan completes.
				results[i] = Result{URL: rawURL, Path: path, Bytes: n, Cached: true}
				if budget.chargeCached(n) < 0 {
					results[i].Err = ErrTooLarge
					if overflow < 0 {
						overflow = i
					}
				}
				continue
			}
		}
		missing = append(missing, job{index: i, url: rawURL})
	}
	if overflow >= 0 {
		// Entries that were not cached are never fetched on this path; mark
		// them failed so a caller using a partial build cannot count the
		// zero-valued results as successes.
		for i := range results {
			if results[i].Err == nil && results[i].Path == "" {
				results[i].URL = urls[i]
				results[i].Err = fmt.Errorf("not downloaded: manifest byte budget exhausted by cached sources")
			}
		}
		return results, fmt.Errorf("%s: %w", results[overflow].URL, ErrTooLarge)
	}
	if len(missing) == 0 {
		// An all-cached build never reaches the worker loop, so re-check the
		// context here; otherwise a cancellation or deadline that fired while
		// the cache was being scanned would be silently ignored.
		if err := ctx.Err(); err != nil {
			for i := range results {
				if results[i].Err == nil {
					results[i].Err = err
				}
			}
			return results, err
		}
		return results, nil
	}
	// The channel holds every job, so dispatch never blocks; workers check
	// the context before each job instead, which makes cancellation
	// deterministic (no random select between a ready send and Done).
	jobs := make(chan job, len(missing))
	for _, j := range missing {
		jobs <- j
	}
	close(jobs)
	// Never start more workers than there are downloads left to do.
	if workers > len(missing) {
		workers = len(missing)
	}
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := range jobs {
				if err := ctx.Err(); err != nil {
					// Never dispatched: fail it so callers cannot count a
					// zero-valued Result as a successful download.
					results[j.index] = Result{URL: j.url, Err: err}
					continue
				}
				path, n, err := get(ctx, c, j.url, destination, retries, budget)
				results[j.index] = Result{URL: j.url, Path: path, Bytes: n, Err: err}
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		// Context cancellation/deadline failure aborts the whole manifest,
		// including cache hits discovered before the workers started. Keep the
		// per-source results consistent with the all-cached cancellation path.
		for i := range results {
			results[i].Err = err
		}
		return results, err
	}

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
	path := filepath.Join(cacheDir, SourceName(rawURL)+".txt")
	// Cache entries are created as regular files via atomic rename. Use Lstat
	// so a pre-existing symlink is never followed outside the configured cache.
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxSourceBytes {
		if err == nil {
			// Cache names are owned files. Remove invalid objects recursively so
			// a stale non-empty directory at a cache filename cannot block the
			// fresh download's atomic rename. RemoveAll removes symlinks itself
			// rather than following their targets.
			_ = os.RemoveAll(path)
		}
		return "", 0, false
	}
	if htmlError(path) {
		_ = os.Remove(path)
		return "", 0, false
	}
	return path, info.Size(), true
}

func get(ctx context.Context, c *http.Client, rawURL, dir string, retries int, budget *byteBudget) (string, int64, error) {
	var last error
	attempts := 0
	for attempt := 0; attempt <= retries; attempt++ {
		attempts = attempt + 1
		if attempt > 0 {
			delay := time.Duration(500*(1<<min(attempt-1, 5))) * time.Millisecond
			var status *statusError
			if errors.As(last, &status) && status.RetryAfter > delay {
				delay = status.RetryAfter
			}
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return "", 0, ctx.Err()
			}
		}
		p, n, e := getOnce(ctx, c, rawURL, dir, budget)
		if e == nil {
			return p, n, nil
		}
		last = e
		// Only the caller's context cancellation/deadline is fatal. A
		// per-request Client.Timeout also wraps DeadlineExceeded, but is a
		// transient mirror/network failure and must remain retryable.
		if ctx.Err() != nil {
			return "", 0, ctx.Err()
		}
		if !retryable(e) {
			break
		}
	}
	return "", 0, fmt.Errorf("after %d attempts: %w", attempts, last)
}

func getOnce(ctx context.Context, c *http.Client, rawURL, dir string, budget *byteBudget) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Accept", "text/plain, text/*;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", "filtrite/4.0 (legacy-subresource-filter-builder)")
	resp, err := c.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		// Drain a bounded amount of the response body so reusable connections
		// can stay in the transport pool before this request is retried.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return "", 0, &statusError{Code: resp.StatusCode, Status: resp.Status, RetryAfter: retryAfter}
	}
	if resp.ContentLength > MaxSourceBytes {
		// Drain a bounded amount of the response body so reusable connections
		// can stay in the transport pool, mirroring the error-status path.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return "", 0, ErrTooLarge
	}
	name := SourceName(rawURL) + ".txt"
	final := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	reader := &budgetReader{r: io.LimitReader(resp.Body, MaxSourceBytes+1), budget: budget}
	n, err := io.Copy(tmp, reader)
	// The shared budget accounts for bytes that end up in published
	// artifacts; anything discarded below is refunded so a failed or
	// rejected download cannot silently consume the build's budget.
	refund := func() { budget.refund(n) }
	if err != nil {
		refund()
		return "", 0, err
	}
	if n > MaxSourceBytes {
		refund()
		return "", n, ErrTooLarge
	}
	if n == 0 {
		return "", 0, fmt.Errorf("empty response")
	}
	if err := tmp.Close(); err != nil {
		refund()
		return "", n, err
	}
	// Check the temp file before the rename so an HTML error page is never
	// visible (or cached) under its final name.
	if htmlError(tmpPath) {
		refund()
		return "", 0, fmt.Errorf("HTML error page")
	}
	if err := os.Rename(tmpPath, final); err != nil {
		refund()
		return "", n, err
	}
	return final, n, nil
}

// byteBudget coordinates reservations for concurrent source readers. Reserving
// before an underlying Read prevents concurrent streams from exceeding the
// aggregate limit; waiting when reservations are temporarily in flight avoids
// treating a temporary zero balance as a definitive over-limit result.
type byteBudget struct {
	mu          sync.Mutex
	cond        *sync.Cond
	remaining   int64
	activeReads int
}

func newByteBudget(limit int64) *byteBudget {
	b := &byteBudget{remaining: limit}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *byteBudget) chargeCached(n int64) int64 {
	b.mu.Lock()
	b.remaining -= n
	b.cond.Broadcast()
	remaining := b.remaining
	b.mu.Unlock()
	return remaining
}

func (b *byteBudget) refund(n int64) {
	b.mu.Lock()
	b.remaining += n
	b.cond.Broadcast()
	b.mu.Unlock()
}

// reserve waits only while another underlying Read is actively holding bytes
// out of the shared budget. The read's result either consumes those bytes or
// refunds its unused reservation and wakes waiters.
func (b *byteBudget) reserve(size int64) int64 {
	b.mu.Lock()
	for {
		if b.remaining > 0 {
			want := size
			if want > b.remaining {
				want = b.remaining
			}
			b.remaining -= want
			b.activeReads++
			b.mu.Unlock()
			return want
		}
		if b.activeReads == 0 {
			b.mu.Unlock()
			return 0
		}
		b.cond.Wait()
	}
}

func (b *byteBudget) settle(want, n int64) {
	b.mu.Lock()
	b.remaining += want - n
	b.activeReads--
	b.cond.Broadcast()
	b.mu.Unlock()
}

type budgetReader struct {
	r      io.Reader
	budget *byteBudget
}

func (r *budgetReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	want := r.budget.reserve(int64(len(p)))
	if want <= 0 {
		// We may have consumed the final permitted byte exactly. Probe the
		// underlying reader so an actual EOF is accepted, while any additional
		// source byte still fails the global budget. A reader that keeps
		// returning (0, nil) without progress is treated as stalled instead of
		// spinning forever.
		var probe [1]byte
		for stalled := 0; ; stalled++ {
			n, err := r.r.Read(probe[:])
			if n > 0 {
				return 0, ErrTooLarge
			}
			if err != nil {
				return 0, err
			}
			if stalled >= 100 {
				return 0, fmt.Errorf("download stalled: reader returned no data without error")
			}
		}
	}

	n, err := r.r.Read(p[:want])
	r.budget.settle(want, int64(n))
	return n, err
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
	Code       int
	Status     string
	RetryAfter time.Duration
}

func (e *statusError) Error() string {
	return "unexpected HTTP status: " + e.Status
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		if seconds > int64(MaxRetryAfter/time.Second) {
			return MaxRetryAfter
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		delay := when.Sub(now)
		if delay > MaxRetryAfter {
			return MaxRetryAfter
		}
		return delay
	}
	return 0
}

func retryable(err error) bool {
	if errors.Is(err, ErrTooLarge) {
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
	// Server closed the connection before/without a response.
	if errors.Is(err, io.EOF) {
		return true
	}
	// Connection resets are transient and commonly happen with overloaded
	// mirrors or reused connections.
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// SourceName returns the stable file name stem (full SHA-256 hex of the URL)
// used for source-cache entries. Callers that only need a short prefix may
// slice the returned string; this is the single URL-hashing implementation,
// shared with cmd/legacy-filter-builder.
func SourceName(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
