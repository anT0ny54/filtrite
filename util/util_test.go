package util

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadListFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "lists.txt")
	content := strings.Join([]string{
		"# comment",
		"",
		" https://example.com/a ",
		"http://example.com/b",
		"https://example.com/a",
		"ftp://example.com/c",
		"not-a-url",
	}, "\n")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadListFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"http://example.com/b", "https://example.com/a"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestDownloadURLsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("||example.com^"))
	}))
	defer server.Close()

	dir := t.TempDir()
	urls := []string{
		server.URL + "/ok",
		server.URL + "/ok",
		server.URL + "/fail",
		"ftp://invalid.example/file",
	}
	got, err := DownloadURLsContext(context.Background(), urls, dir, 2, 0, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "1/2 downloads failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d files, want 1", len(got))
	}
	data, err := os.ReadFile(got[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "||example.com^" {
		t.Fatalf("unexpected content %q", data)
	}
}
