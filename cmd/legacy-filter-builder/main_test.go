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

	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := filepath.Join(buildDir, "build-summary.env")
	if err := os.WriteFile(summary, []byte("stale=true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run(sources, custom, output, buildDir, cacheDir, summary, false, filter.DefaultSortChunkBytes)
	if err == nil || !strings.Contains(err.Error(), "refusing to publish a partial ruleset") {
		t.Fatalf("run error=%v, want release-fatal partial-download error", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("output exists after failed partial build: stat=%v", statErr)
	}
	if _, statErr := os.Stat(summary); !os.IsNotExist(statErr) {
		t.Fatalf("stale summary survived failed build: stat=%v", statErr)
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

func TestRunWithCacheDoesNotCreateRawDir(t *testing.T) {
	dir := t.TempDir()
	sources := filepath.Join(dir, "sources.txt")
	cacheDir := filepath.Join(dir, "cache")
	buildDir := filepath.Join(dir, "work")
	output := filepath.Join(dir, "filters", "adblock.txt")
	custom := filepath.Join(dir, "custom.txt")
	cachedURL := "https://cached.example/list.txt"
	if err := os.WriteFile(sources, []byte(cachedURL+"\n"), 0o644); err != nil {
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

	if err := run(sources, custom, output, buildDir, cacheDir, filepath.Join(buildDir, "build-summary.env"), false, filter.DefaultSortChunkBytes); err != nil {
		t.Fatal(err)
	}
	// Regression: a cache-enabled build must never create the raw/ staging
	// directory; snapshots live in the shared cache instead. This failed
	// before because download.AllWithCache created its download directory
	// unconditionally.
	if _, err := os.Stat(filepath.Join(buildDir, "raw")); !os.IsNotExist(err) {
		t.Fatalf("raw staging directory exists after cache-enabled build: stat=%v", err)
	}
	// The build directory itself and the summary must still exist.
	if _, err := os.Stat(filepath.Join(buildDir, "build-summary.env")); err != nil {
		t.Fatalf("build summary missing after cache-enabled build: %v", err)
	}
}

func TestRunRejectsOutputOrSummaryAliasingSourceManifest(t *testing.T) {
	dir := t.TempDir()
	sources := filepath.Join(dir, "sources.txt")
	manifest := "https://example.com/list.txt\n"
	if err := os.WriteFile(sources, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	custom := ""
	buildDir := filepath.Join(dir, "work")
	normalOutput := filepath.Join(dir, "filters.txt")

	tests := []struct {
		name    string
		output  string
		summary string
	}{
		{name: "output aliases manifest", output: sources},
		{name: "summary aliases manifest", output: normalOutput, summary: sources},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(sources, custom, tt.output, buildDir, "", tt.summary, false, filter.DefaultSortChunkBytes)
			if err == nil || !strings.Contains(err.Error(), "must not alias") {
				t.Fatalf("run error=%v, want path-alias rejection", err)
			}
			got, readErr := os.ReadFile(sources)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != manifest {
				t.Fatalf("source manifest was modified: %q", got)
			}
		})
	}
}

func TestRunRejectsSummaryCollidingWithRejectedReportWithoutDeletingIt(t *testing.T) {
	dir := t.TempDir()
	sources := filepath.Join(dir, "sources.txt")
	const sourceURL = "https://example.com/list.txt"
	if err := os.WriteFile(sources, []byte(sourceURL+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buildDir := filepath.Join(dir, "work")
	hash := sha256.Sum256([]byte(sourceURL))
	summary := filepath.Join(buildDir, "rejected-"+hex.EncodeToString(hash[:])[:12]+".txt")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const sentinel = "preserve this existing report\n"
	if err := os.WriteFile(summary, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run(sources, "", filepath.Join(dir, "filters.txt"), buildDir, "", summary, false, filter.DefaultSortChunkBytes)
	if err == nil || !strings.Contains(err.Error(), "must not alias or overlap") {
		t.Fatalf("run error=%v, want summary/report collision rejection", err)
	}
	got, readErr := os.ReadFile(summary)
	if readErr != nil {
		t.Fatalf("existing rejection report was deleted during preflight: %v", readErr)
	}
	if string(got) != sentinel {
		t.Fatalf("existing rejection report changed: %q", got)
	}
}

func TestRunRejectsOutputInsideSharedCacheDirectory(t *testing.T) {
	dir := t.TempDir()
	sources := filepath.Join(dir, "sources.txt")
	if err := os.WriteFile(sources, []byte("https://example.com/list.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(dir, "cache")
	output := filepath.Join(cacheDir, "filters.txt")
	err := run(sources, "", output, filepath.Join(dir, "work"), cacheDir, "", false, filter.DefaultSortChunkBytes)
	if err == nil || !strings.Contains(err.Error(), "must not alias or overlap") {
		t.Fatalf("run error=%v, want output/cache collision rejection", err)
	}
	if _, statErr := os.Stat(cacheDir); !os.IsNotExist(statErr) {
		t.Fatalf("cache directory was unexpectedly created during preflight: stat=%v", statErr)
	}
}

func TestRunRejectsInputsInsideDeletedStagingDirectories(t *testing.T) {
	dir := t.TempDir()
	buildDir := filepath.Join(dir, "work")
	rawDir := filepath.Join(buildDir, "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sources := filepath.Join(rawDir, "sources.txt")
	manifest := "https://example.com/list.txt\n"
	if err := os.WriteFile(sources, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run(sources, "", filepath.Join(dir, "filters.txt"), buildDir, "", "", false, filter.DefaultSortChunkBytes)
	if err == nil || !strings.Contains(err.Error(), "must not alias or overlap") {
		t.Fatalf("run error=%v, want staging-directory overlap rejection", err)
	}
	got, readErr := os.ReadFile(sources)
	if readErr != nil {
		t.Fatalf("source manifest was deleted by path validation: %v", readErr)
	}
	if string(got) != manifest {
		t.Fatalf("source manifest changed: %q", got)
	}
}

func TestRunRejectsInputAliasedThroughSymlinkedSortDirectory(t *testing.T) {
	root := t.TempDir()
	buildDir := filepath.Join(root, "work")
	sortDir := filepath.Join(buildDir, "sort")
	if err := os.MkdirAll(sortDir, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasDir := filepath.Join(root, "work-alias")
	if err := os.Symlink(buildDir, aliasDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	sources := filepath.Join(aliasDir, "sort", "sources.txt")
	const sourceURL = "https://cached.example/list.txt"
	const manifest = sourceURL + "\n"
	if err := os.WriteFile(sources, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(root, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(sourceURL))
	cacheEntry := filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".txt")
	if err := os.WriteFile(cacheEntry, []byte("||cached.example^\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run(sources, "", filepath.Join(root, "filters.txt"), buildDir, cacheDir, "", false, filter.DefaultSortChunkBytes)
	if err == nil || !strings.Contains(err.Error(), "must not alias or overlap") {
		t.Fatalf("run error=%v, want symlink-resolved staging overlap rejection", err)
	}
	got, readErr := os.ReadFile(sources)
	if readErr != nil {
		t.Fatalf("source manifest was removed during preflight: %v", readErr)
	}
	if string(got) != manifest {
		t.Fatalf("source manifest changed: %q", got)
	}
}
