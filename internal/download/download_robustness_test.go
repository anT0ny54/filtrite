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

// remainingBytes reports the unreserved balance; only tests need to inspect it.
func (b *byteBudget) remainingBytes() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.remaining
}

// seedCache writes a small valid cache entry for every URL so AllWithCache
// treats them all as cache hits.
func seedCache(t *testing.T, cacheDir string, urls []string) {
	t.Helper()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, u := range urls {
		path := filepath.Join(cacheDir, SourceName(u)+".txt")
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

type gatedReader struct {
	entered chan struct{}
	release chan struct{}
	read    bool
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	close(r.entered)
	<-r.release
	p[0] = 'a'
	return 1, nil
}

func TestBudgetReaderWaitsForInFlightReservation(t *testing.T) {
	budget := newByteBudget(100)
	gate := &gatedReader{entered: make(chan struct{}), release: make(chan struct{})}
	first := &budgetReader{r: gate, budget: budget}
	firstDone := make(chan struct {
		data string
		err  error
	}, 1)
	go func() {
		data, err := io.ReadAll(first)
		firstDone <- struct {
			data string
			err  error
		}{string(data), err}
	}()
	<-gate.entered // The first reader has reserved the entire budget.

	secondReadStarted := make(chan struct{})
	secondBody := strings.NewReader("b")
	secondSource := readerFunc(func(p []byte) (int, error) {
		select {
		case <-secondReadStarted:
		default:
			close(secondReadStarted)
		}
		return secondBody.Read(p)
	})
	second := &budgetReader{r: secondSource, budget: budget}
	secondDone := make(chan struct {
		data string
		err  error
	}, 1)
	go func() {
		data, err := io.ReadAll(second)
		secondDone <- struct {
			data string
			err  error
		}{string(data), err}
	}()
	select {
	case <-secondReadStarted:
		t.Fatal("second source was read while the first reader still held its reservation")
	case <-time.After(25 * time.Millisecond):
	}
	close(gate.release)

	gotFirst := <-firstDone
	gotSecond := <-secondDone
	if gotFirst.err != nil || gotFirst.data != "a" {
		t.Fatalf("first read=%q err=%v, want a successful one-byte read", gotFirst.data, gotFirst.err)
	}
	if gotSecond.err != nil || gotSecond.data != "b" {
		t.Fatalf("second read=%q err=%v, want a successful one-byte read after refund", gotSecond.data, gotSecond.err)
	}
	if got := budget.remainingBytes(); got != 98 {
		t.Fatalf("remaining budget=%d, want 98", got)
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestBudgetReaderConcurrentReadersNeverExceedBudget(t *testing.T) {
	const (
		readers = 16
		size    = 1000
		limit   = int64(5000) // far less than readers*size, so they fight over the tail
	)
	budget := newByteBudget(limit)
	accepted := make([]int64, readers)
	errs := make([]error, readers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r := &budgetReader{r: &chunkReader{data: make([]byte, size), chunk: 7}, budget: budget}
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
	left := budget.remainingBytes()
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
	budget := newByteBudget(100)
	r := &budgetReader{r: &failAfterReader{data: []byte("0123456789"), err: io.ErrUnexpectedEOF}, budget: budget}
	n, err := io.Copy(io.Discard, r)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err=%v, want io.ErrUnexpectedEOF", err)
	}
	if n != 10 {
		t.Fatalf("n=%d, want 10", n)
	}
	// Only the bytes actually delivered stay charged; the caller (getOnce)
	// refunds them when it discards the failed attempt.
	if got := budget.remainingBytes(); got != 90 {
		t.Fatalf("budget=%d, want 90 (unused reservation refunded)", got)
	}
	budget.refund(n)
	if got := budget.remainingBytes(); got != 100 {
		t.Fatalf("budget after refund=%d, want 100", got)
	}
}

func TestBudgetReaderZeroByteReadsDoNotConsumeBudget(t *testing.T) {
	budget := newByteBudget(10)
	zr := &zeroReader{}
	r := &budgetReader{r: zr, budget: budget}
	buf := make([]byte, 8)
	for i := 0; i < 1000; i++ {
		n, err := r.Read(buf)
		if n != 0 || err != nil {
			t.Fatalf("read %d: n=%d err=%v, want 0,nil", i, n, err)
		}
	}
	if got := budget.remainingBytes(); got != 10 {
		t.Fatalf("budget=%d after zero-byte reads, want 10", got)
	}
}

func TestBudgetReaderStalledReaderAtExhaustedBudgetFails(t *testing.T) {
	budget := newByteBudget(0)
	zr := &zeroReader{}
	r := &budgetReader{r: zr, budget: budget}
	_, err := r.Read(make([]byte, 8))
	if err == nil || errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("err=%v, want a stalled-reader error", err)
	}
	if zr.calls == 0 || zr.calls > 200 {
		t.Fatalf("probe calls=%d, want a small bounded number", zr.calls)
	}
	if got := budget.remainingBytes(); got != 0 {
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
		budget := newByteBudget(int64(len(body)))
		path, n, err := get(context.Background(), c, srv.URL, t.TempDir(), 0, budget)
		if err != nil || path == "" || n != int64(len(body)) {
			t.Fatalf("path=%q n=%d err=%v, want success", path, n, err)
		}
		if got := budget.remainingBytes(); got != 0 {
			t.Fatalf("budget=%d, want 0", got)
		}
	})

	t.Run("one byte short is rejected and refunded", func(t *testing.T) {
		start := int64(len(body) - 1)
		budget := newByteBudget(start)
		dir := t.TempDir()
		_, _, err := get(context.Background(), c, srv.URL, dir, 0, budget)
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("err=%v, want ErrTooLarge", err)
		}
		if got := budget.remainingBytes(); got != start {
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

	budget := newByteBudget(MaxTotalBytes)
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
	if want := MaxTotalBytes - int64(len(body)); budget.remainingBytes() != want {
		t.Fatalf("budget=%d, want %d", budget.remainingBytes(), want)
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
	budget := newByteBudget(MaxTotalBytes)
	_, _, err := get(context.Background(), c, srv.URL, dir, 1, budget)
	if err == nil {
		t.Fatal("expected failure when every attempt is truncated")
	}
	if calls.Load() != 2 {
		t.Fatalf("server calls=%d, want 2 attempts", calls.Load())
	}
	if got := budget.remainingBytes(); got != MaxTotalBytes {
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

func TestCachedFileRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const sourceURL = "https://cache.example/list.txt"
	target := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(target, []byte("||outside.example^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(cacheDir, SourceName(sourceURL)+".txt")
	if err := os.Symlink(target, entry); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, _, ok := cachedFile(cacheDir, sourceURL); ok {
		t.Fatal("symlink cache entry was accepted")
	}
	if _, err := os.Lstat(entry); !os.IsNotExist(err) {
		t.Fatalf("invalid symlink cache entry was not removed: stat=%v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "||outside.example^\n" {
		t.Fatalf("target was modified while discarding cache symlink: got=%q err=%v", got, err)
	}
}

func TestAllWithCacheRejectsInvalidInputBeforeCreatingDirectories(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "out")
	cacheDir := filepath.Join(root, "cache")
	if _, err := AllWithCache(context.Background(), nil, dir, cacheDir, 1, 0, time.Second); err == nil {
		t.Fatal("expected an error for an empty URL list")
	}
	for _, p := range []string{dir, cacheDir} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s was created despite invalid input: stat=%v", p, err)
		}
	}
}
