package download

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

func TestAllDownloadsAndRetries(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("||example.com^\n"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	res, err := All(context.Background(), []string{srv.URL + "/ok", srv.URL + "/missing"}, dir, 2, 0, 5*time.Second)
	if err == nil || len(res) != 2 || calls != 2 {
		t.Fatalf("unexpected result err=%v results=%d calls=%d", err, len(res), calls)
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
