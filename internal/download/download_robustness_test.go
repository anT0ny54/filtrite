package download

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// seedCache writes a small valid cache entry for every URL so AllWithCache
// treats them all as cache hits.
func seedCache(t *testing.T, cacheDir string, urls []string) {
	t.Helper()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, u := range urls {
		path := filepath.Join(cacheDir, shaName(u)+".txt")
		if err := os.WriteFile(path, []byte("||cached.example^\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAllWithCacheFullyCachedSucceedsWithLiveContext(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	urls := []string{"https://a.example/list.txt", "https://b.example/list.txt"}
	seedCache(t, cacheDir, urls)

	res, err := AllWithCache(context.Background(), urls, filepath.Join(root, "out"), cacheDir, 1, 0, time.Second)
	if err != nil {
		t.Fatalf("err=%v, want success for a live context", err)
	}
	if len(res) != len(urls) {
		t.Fatalf("results=%d, want %d", len(res), len(urls))
	}
	for _, r := range res {
		if r.Err != nil || !r.Cached {
			t.Fatalf("result=%#v, want successful cache hit", r)
		}
	}
}

func TestAllWithCacheFullyCachedHonorsCanceledContext(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	urls := []string{"https://a.example/list.txt", "https://b.example/list.txt"}
	seedCache(t, cacheDir, urls)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := AllWithCache(ctx, urls, filepath.Join(root, "out"), cacheDir, 1, 0, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled even though every source is cached", err)
	}
	for _, r := range res {
		if r.Err == nil {
			t.Fatalf("result=%#v, canceled run must not report successful sources", r)
		}
	}
}

func TestAllWithCacheFullyCachedHonorsExpiredDeadline(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	urls := []string{"https://a.example/list.txt"}
	seedCache(t, cacheDir, urls)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	res, err := AllWithCache(ctx, urls, filepath.Join(root, "out"), cacheDir, 1, 0, time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want context.DeadlineExceeded even though every source is cached", err)
	}
	for _, r := range res {
		if r.Err == nil {
			t.Fatalf("result=%#v, expired run must not report successful sources", r)
		}
	}
}

// chunkReader hands out data in small fixed-size pieces to force many
// interleavings between concurrent budgetReaders.
type chunkReader struct {
	data  []byte
	chunk int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.chunk
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

// failAfterReader returns its data, then a fixed error.
type failAfterReader struct {
	data []byte
	err  error
}

func (r *failAfterReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// zeroReader never makes progress.
type zeroReader struct{ calls int }

func (r *zeroReader) Read([]byte) (int, error) {
	r.calls++
	return 0, nil
}

func TestBudgetReaderConcurrentReadersNeverExceedBudget(t *testing.T) {
	const (
		readers = 16
		size    = 1000
		limit   = int64(5000) // far less than readers*size, so they fight over the tail
	)
	budget := limit
	accepted := make([]int64, readers)
	errs := make([]error, readers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r := &budgetReader{r: &chunkReader{data: make([]byte, size), chunk: 7}, remaining: &budget}
			accepted[i], errs[i] = io.Copy(io.Discard, r)
		}(i)
	}
	close(start)
	wg.Wait()

	var total int64
	failed := 0
	for i := range accepted {
		total += accepted[i]
		if errs[i] != nil {
			if !errors.Is(errs[i], ErrTooLarge) {
				t.Fatalf("reader %d err=%v, want ErrTooLarge", i, errs[i])
			}
			failed++
		}
	}
	if total > limit {
		t.Fatalf("accepted %d bytes, exceeds budget %d", total, limit)
	}
	left := atomic.LoadInt64(&budget)
	if left < 0 {
		t.Fatalf("budget went negative: %d", left)
	}
	if total+left != limit {
		t.Fatalf("accounting leak: accepted=%d remaining=%d budget=%d", total, left, limit)
	}
	if failed == 0 {
		t.Fatal("expected at least one reader to be rejected when demand exceeds the budget")
	}
}

func TestBudgetReaderInterruptedReadKeepsAccurateAccounting(t *testing.T) {
	budget := int64(100)
	r := &budgetReader{r: &failAfterReader{data: []byte("0123456789"), err: io.ErrUnexpectedEOF}, remaining: &budget}
	n, err := io.Copy(io.Discard, r)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err=%v, want io.ErrUnexpectedEOF", err)
	}
	if n != 10 {
		t.Fatalf("n=%d, want 10", n)
	}
	// Only the bytes actually delivered stay charged; the caller (getOnce)
	// refunds them when it discards the failed attempt.
	if got := atomic.LoadInt64(&budget); got != 90 {
		t.Fatalf("budget=%d, want 90 (unused reservation refunded)", got)
	}
	atomic.AddInt64(&budget, n)
	if got := atomic.LoadInt64(&budget); got != 100 {
		t.Fatalf("budget after refund=%d, want 100", got)
	}
}

func TestBudgetReaderZeroByteReadsDoNotConsumeBudget(t *testing.T) {
	budget := int64(10)
	zr := &zeroReader{}
	r := &budgetReader{r: zr, remaining: &budget}
	buf := make([]byte, 8)
	for i := 0; i < 1000; i++ {
		n, err := r.Read(buf)
		if n != 0 || err != nil {
			t.Fatalf("read %d: n=%d err=%v, want 0,nil", i, n, err)
		}
	}
	if got := atomic.LoadInt64(&budget); got != 10 {
		t.Fatalf("budget=%d after zero-byte reads, want 10", got)
	}
}

func TestBudgetReaderStalledReaderAtExhaustedBudgetFails(t *testing.T) {
	budget := int64(0)
	zr := &zeroReader{}
	r := &budgetReader{r: zr, remaining: &budget}
	_, err := r.Read(make([]byte, 8))
	if err == nil || errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("err=%v, want a stalled-reader error", err)
	}
	if zr.calls == 0 || zr.calls > 200 {
		t.Fatalf("probe calls=%d, want a small bounded number", zr.calls)
	}
	if got := atomic.LoadInt64(&budget); got != 0 {
		t.Fatalf("budget=%d, want 0", got)
	}
}

func TestGetOnceBudgetBoundary(t *testing.T) {
	body := strings.Repeat("||example.com^\n", 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := newClient(5 * time.Second)
	defer c.CloseIdleConnections()

	t.Run("exact budget is accepted", func(t *testing.T) {
		budget := ptrInt64(int64(len(body)))
		path, n, err := get(context.Background(), c, srv.URL, t.TempDir(), 0, budget)
		if err != nil || path == "" || n != int64(len(body)) {
			t.Fatalf("path=%q n=%d err=%v, want success", path, n, err)
		}
		if got := atomic.LoadInt64(budget); got != 0 {
			t.Fatalf("budget=%d, want 0", got)
		}
	})

	t.Run("one byte short is rejected and refunded", func(t *testing.T) {
		start := int64(len(body) - 1)
		budget := ptrInt64(start)
		dir := t.TempDir()
		_, _, err := get(context.Background(), c, srv.URL, dir, 0, budget)
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("err=%v, want ErrTooLarge", err)
		}
		if got := atomic.LoadInt64(budget); got != start {
			t.Fatalf("budget=%d, want full refund to %d", got, start)
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(entries) != 0 {
			t.Fatalf("leftover files after rejected download: %v", entries)
		}
	})
}

// partialHandler promises the full body but sends only a prefix on the
// first failFirst requests, so the client sees io.ErrUnexpectedEOF.
func partialHandler(body string, failFirst int64, calls *atomic.Int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if n <= failFirst {
			_, _ = w.Write([]byte(body[:len(body)/3]))
			return
		}
		_, _ = w.Write([]byte(body))
	}
}

func TestGetRetriesAfterPartialResponseWithoutLeakingBudget(t *testing.T) {
	body := strings.Repeat("||example.com^\n", 20)
	var calls atomic.Int64
	srv := httptest.NewServer(partialHandler(body, 1, &calls))
	defer srv.Close()
	c := newClient(5 * time.Second)
	defer c.CloseIdleConnections()

	budget := ptrInt64(MaxTotalBytes)
	path, n, err := get(context.Background(), c, srv.URL, t.TempDir(), 1, budget)
	if err != nil {
		t.Fatalf("err=%v, want success on the retry", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("server calls=%d, want 2", calls.Load())
	}
	if n != int64(len(body)) {
		t.Fatalf("n=%d, want %d", n, len(body))
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != body {
		t.Fatalf("published file mismatch: err=%v len=%d", readErr, len(got))
	}
	// Only the successful attempt stays charged.
	if want := MaxTotalBytes - int64(len(body)); atomic.LoadInt64(budget) != want {
		t.Fatalf("budget=%d, want %d", atomic.LoadInt64(budget), want)
	}
}

func TestGetFailedPartialAttemptsDoNotConsumeBudget(t *testing.T) {
	body := strings.Repeat("||example.com^\n", 20)
	var calls atomic.Int64
	srv := httptest.NewServer(partialHandler(body, 1<<30, &calls))
	defer srv.Close()
	c := newClient(5 * time.Second)
	defer c.CloseIdleConnections()

	dir := t.TempDir()
	budget := ptrInt64(MaxTotalBytes)
	_, _, err := get(context.Background(), c, srv.URL, dir, 1, budget)
	if err == nil {
		t.Fatal("expected failure when every attempt is truncated")
	}
	if calls.Load() != 2 {
		t.Fatalf("server calls=%d, want 2 attempts", calls.Load())
	}
	if got := atomic.LoadInt64(budget); got != MaxTotalBytes {
		t.Fatalf("budget=%d, want full refund to %d", got, MaxTotalBytes)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("leftover temp files after failed attempts: %v", entries)
	}
}
