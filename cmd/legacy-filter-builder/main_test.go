package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filtrite/internal/filter"
)

func TestRunFailsOnPartialSourceDownloadsByDefault(t *testing.T) {
	dir := t.TempDir()
	sources := filepath.Join(dir, "sources.txt")
	cacheDir := filepath.Join(dir, "cache")
	buildDir := filepath.Join(dir, "work")
	output := filepath.Join(dir, "filters", "adblock.txt")
	custom := filepath.Join(dir, "custom.txt")
	cachedURL := "https://cached.example/list.txt"
	failedURL := "https://127.0.0.1:1/missing.txt"
	if err := os.WriteFile(sources, []byte(cachedURL+"\n"+failedURL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(custom, []byte("||custom.example^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(cachedURL))
	cachedPath := filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".txt")
	if err := os.WriteFile(cachedPath, []byte("||cached.example^\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := run(sources, custom, output, buildDir, cacheDir, "", false, filter.DefaultSortChunkBytes)
	if err == nil || !strings.Contains(err.Error(), "refusing to publish a partial ruleset") {
		t.Fatalf("run error=%v, want release-fatal partial-download error", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("output exists after failed partial build: stat=%v", statErr)
	}
}

func TestRunAllowsExplicitPartialBuild(t *testing.T) {
	dir := t.TempDir()
	sources := filepath.Join(dir, "sources.txt")
	cacheDir := filepath.Join(dir, "cache")
	buildDir := filepath.Join(dir, "work")
	output := filepath.Join(dir, "filters", "adblock.txt")
	custom := filepath.Join(dir, "custom.txt")
	cachedURL := "https://cached.example/list.txt"
	failedURL := "https://127.0.0.1:1/missing.txt"
	if err := os.WriteFile(sources, []byte(cachedURL+"\n"+failedURL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(custom, []byte("||custom.example^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(cachedURL))
	cachedPath := filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".txt")
	if err := os.WriteFile(cachedPath, []byte("||cached.example^\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := run(sources, custom, output, buildDir, cacheDir, "", true, filter.DefaultSortChunkBytes); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{"||cached.example^$third-party\n", "||custom.example^$third-party\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output=%q missing %q", text, want)
		}
	}
}

func TestRunValidatesCustomBeforeDownload(t *testing.T) {
	dir := t.TempDir()
	sources := filepath.Join(dir, "sources.txt")
	custom := filepath.Join(dir, "missing-custom.txt")
	if err := os.WriteFile(sources, []byte("https://127.0.0.1:1/missing.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := run(sources, custom, filepath.Join(dir, "filters.txt"), filepath.Join(dir, "work"), "", "", false, filter.DefaultSortChunkBytes)
	if err == nil || !strings.Contains(err.Error(), "custom rules") {
		t.Fatalf("run error=%v, want custom-rule validation error before download", err)
	}
}
