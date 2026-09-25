package download

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryableClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "http 408", err: &statusError{Code: 408, Status: "408 Request Timeout"}, want: true},
		{name: "http 429", err: &statusError{Code: 429, Status: "429 Too Many Requests"}, want: true},
		{name: "http 503", err: &statusError{Code: 503, Status: "503 Service Unavailable"}, want: true},
		{name: "http 404", err: &statusError{Code: 404, Status: "404 Not Found"}, want: false},
		{name: "too large", err: ErrTooLarge, want: false},
		{name: "plain error", err: errors.New("disk full"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryable(tt.err); got != tt.want {
				t.Fatalf("retryable(%v)=%v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRepositorySourceManifestIsValid(t *testing.T) {
	path := filepath.Join("..", "..", "lists", "adblock.txt")
	urls, err := URLsFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) < 10 {
		t.Fatalf("unexpectedly small source manifest: %d URLs", len(urls))
	}
}

func TestURLsFromFileRejectsInvalidSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.txt")
	input := "# comment\nhttps://example.com/list.txt\nhttps://user:pass@example.com/private.txt\n"
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := URLsFromFile(path)
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("expected line-numbered validation error, got %v", err)
	}
}

func TestURLsFromFileRejectsWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.txt")
	if err := os.WriteFile(path, []byte(" https://example.com/list.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := URLsFromFile(path); err == nil || !strings.Contains(err.Error(), "whitespace") {
		t.Fatalf("expected whitespace validation error, got %v", err)
	}
}

func TestURLsFromFileCanonicalizesHostCase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.txt")
	input := "https://EXAMPLE.com/list.txt\nhttps://example.com/list.txt\n"
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	urls, err := URLsFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || urls[0] != "https://example.com/list.txt" {
		t.Fatalf("urls=%v, want one canonical URL", urls)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, time.September, 25, 13, 0, 0, 0, time.UTC)
	if got := parseRetryAfter("3", now); got != 3*time.Second {
		t.Fatalf("delta retry-after=%s, want 3s", got)
	}
	if got := parseRetryAfter("999999", now); got != MaxRetryAfter {
		t.Fatalf("large retry-after=%s, want cap %s", got, MaxRetryAfter)
	}
	if got := parseRetryAfter("invalid", now); got != 0 {
		t.Fatalf("invalid retry-after=%s, want 0", got)
	}
	future := now.Add(5 * time.Second).Format(http.TimeFormat)
	if got := parseRetryAfter(future, now); got != 5*time.Second {
		t.Fatalf("date retry-after=%s, want 5s", got)
	}
}

func TestClientRedirectGuardsHTTPSSources(t *testing.T) {
	t.Run("downgrade", func(t *testing.T) {
		var target string
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				http.Redirect(w, r, target, http.StatusFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		target = "http://example.com/plain.txt"

		c := newClient(5 * time.Second)
		c.http.Transport = srv.Client().Transport
		_, err := c.http.Get(srv.URL + "/")
		if err == nil || !strings.Contains(err.Error(), "HTTPS downgrade") {
			t.Fatalf("redirect error=%v, want HTTPS downgrade rejection", err)
		}
	})

	t.Run("credentials", func(t *testing.T) {
		var target string
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				http.Redirect(w, r, target, http.StatusFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		target = srv.URL + "/private.txt"
		u, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword("user", "pass")
		target = u.String()

		c := newClient(5 * time.Second)
		c.http.Transport = srv.Client().Transport
		_, err = c.http.Get(srv.URL + "/")
		if err == nil || !strings.Contains(err.Error(), "embedded credentials") {
			t.Fatalf("redirect error=%v, want embedded-credentials rejection", err)
		}
	})
}

func TestAllDownloadsAndRetries(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("||example.com^\n"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	res, err := All(context.Background(), []string{srv.URL + "/ok", srv.URL + "/missing"}, dir, 2, 0, 5*time.Second)
	if err == nil || len(res) != 2 || calls.Load() != 2 {
		t.Fatalf("unexpected result err=%v results=%d calls=%d", err, len(res), calls.Load())
	}
	if res[0].URL != srv.URL+"/ok" || res[1].URL != srv.URL+"/missing" {
		t.Fatalf("results are not stable: %#v", res)
	}
	var okPath string
	for _, r := range res {
		if r.Err == nil {
			okPath = r.Path
		}
	}
	if okPath == "" {
		t.Fatal("successful download missing")
	}
	b, err := os.ReadFile(filepath.Clean(okPath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "||example.com^" {
		t.Fatalf("unexpected body %q", b)
	}
}

func TestBudgetReaderAllowsExactEOF(t *testing.T) {
	var remaining int64 = 3
	reader := &budgetReader{r: strings.NewReader("abc"), remaining: &remaining}
	buf := make([]byte, 8)
	n, err := reader.Read(buf)
	if n != 3 || err != nil {
		t.Fatalf("first read: n=%d err=%v, want n=3 err=nil", n, err)
	}
	n, err = reader.Read(buf)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("exact-boundary EOF: n=%d err=%v, want n=0 EOF", n, err)
	}
}

func TestBudgetReaderRejectsBytePastLimit(t *testing.T) {
	var remaining int64 = 3
	reader := &budgetReader{r: strings.NewReader("abcd"), remaining: &remaining}
	buf := make([]byte, 8)
	n, err := reader.Read(buf)
	if n != 3 || err != nil {
		t.Fatalf("first read: n=%d err=%v, want n=3 err=nil", n, err)
	}
	n, err = reader.Read(buf)
	if n != 0 || !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over-limit read: n=%d err=%v, want n=0 ErrTooLarge", n, err)
	}
}

func TestAllWithCacheReusesSource(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("||cached.example^\n"))
	}))
	defer srv.Close()

	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	firstDir := filepath.Join(root, "first")
	secondDir := filepath.Join(root, "second")
	urls := []string{srv.URL + "/list.txt"}

	first, err := AllWithCache(context.Background(), urls, firstDir, cacheDir, 1, 0, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Err != nil || first[0].Cached {
		t.Fatalf("first result=%#v, want fresh download", first)
	}
	second, err := AllWithCache(context.Background(), urls, secondDir, cacheDir, 1, 0, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Err != nil || !second[0].Cached {
		t.Fatalf("second result=%#v, want cache hit", second)
	}
	if calls.Load() != 1 {
		t.Fatalf("server calls=%d, want exactly 1", calls.Load())
	}
}
