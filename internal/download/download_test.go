package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

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
