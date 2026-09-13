package download

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
	"strconv"
	"strings"
	"sync"
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
	URL   string
	Path  string
	Bytes int64
	Err   error
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
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	out := []string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(line, "\r"), "\uFEFF"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.ParseRequestURI(line)
		if err != nil || u.Hostname() == "" || !strings.EqualFold(u.Scheme, "https") {
			continue
		}
		u.Scheme = strings.ToLower(u.Scheme)
		s := u.String()
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
		if len(out) > MaxSources {
			return nil, fmt.Errorf("more than %d sources", MaxSources)
		}
	}
	return out, nil
}

func All(ctx context.Context, urls []string, dir string, workers, retries int, timeout time.Duration) ([]Result, error) {
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
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	c := newClient(timeout)
	if len(urls) == 0 {
		return nil, fmt.Errorf("no URLs")
	}
	if len(urls) > MaxSources {
		return nil, fmt.Errorf("too many sources")
	}
	jobs := make(chan string)
	results := make([]Result, 0, len(urls))
	var mu sync.Mutex
	var wg sync.WaitGroup
	if workers > len(urls) {
		workers = len(urls)
	}
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for u := range jobs {
				p, n, err := get(ctx, c, u, dir, retries)
				mu.Lock()
				results = append(results, Result{URL: u, Path: p, Bytes: n, Err: err})
				mu.Unlock()
			}
		}()
	}
	for _, u := range urls {
		select {
		case jobs <- u:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return results, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	var errs []error
	for _, r := range results {
		if r.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.URL, r.Err))
		}
	}
	if len(errs) > 0 {
		return results, errors.Join(errs...)
	}
	return results, nil
}

func get(ctx context.Context, c *client, rawURL, dir string, retries int) (string, int64, error) {
	var last error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(500*(1<<min(attempt-1, 5))) * time.Millisecond):
			case <-ctx.Done():
				return "", 0, ctx.Err()
			}
		}
		p, n, e := getOnce(ctx, c, rawURL, dir)
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

func getOnce(ctx context.Context, c *client, rawURL, dir string) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Accept", "text/plain, text/*;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", "legacy-bromite-filter-builder/3.0")
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
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, MaxSourceBytes+1))
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
	if strings.EqualFold(resp.Header.Get("Content-Type"), "text/html") { // do not reject only on header; inspect body prefix below would require reread
		// HTML is filtered by prefix sniff in inspectDownloaded.
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
func htmlError(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	s := strings.ToLower(string(buf[:n]))
	return strings.Contains(s, "<html") || strings.Contains(s, "<!doctype html") || strings.Contains(s, "<head") || strings.Contains(s, "<body")
}

type statusError struct {
	Code   int
	Status string
}

func (e *statusError) Error() string {
	return "unexpected HTTP status: " + strconv.Itoa(e.Code) + " " + e.Status
}
func retryable(err error) bool {
	var s *statusError
	if errors.As(err, &s) {
		return s.Code == 408 || s.Code == 429 || s.Code == 500 || s.Code == 502 || s.Code == 503 || s.Code == 504
	}
	return !errors.Is(err, ErrTooLarge)
}
func shaName(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
