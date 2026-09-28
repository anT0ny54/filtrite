package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filtrite/internal/download"
	"filtrite/internal/filter"
)

func main() {
	sources := flag.String("sources", "lists/adblock.txt", "HTTPS source list")
	custom := flag.String("custom", "custom-rules.txt", "optional local custom rules; empty disables")
	output := flag.String("output", "filters/adblock.txt", "generated legacy-compatible filter-list")
	buildDir := flag.String("build-dir", "build/work/adblock", "build/report directory")
	cacheDir := flag.String("cache-dir", "", "optional shared source-download cache directory")
	summary := flag.String("summary", "", "optional key=value build summary file for tooling")
	allowPartial := flag.Bool("allow-partial", false, "continue when one or more source downloads fail")
	sortChunkBytes := flag.Int64("sort-chunk-bytes", filter.DefaultSortChunkBytes, "maximum in-memory sorter chunk size in bytes")
	flag.Parse()
	if *sortChunkBytes <= 0 {
		log.Fatalf("sort-chunk-bytes must be positive")
	}
	if err := run(*sources, *custom, *output, *buildDir, *cacheDir, *summary, *allowPartial, *sortChunkBytes); err != nil {
		log.Fatal(err)
	}
}

func run(sources, custom, outFile, buildDir, cacheDir, summaryPath string, allowPartial bool, sortChunkBytes int64) error {
	if sortChunkBytes <= 0 {
		return fmt.Errorf("sort chunk size must be positive")
	}
	urls, err := download.URLsFromFile(sources)
	if err != nil {
		return err
	}
	var customInfo os.FileInfo
	if custom != "" {
		customInfo, err = os.Stat(custom)
		if err != nil {
			return fmt.Errorf("custom rules %q: %w", custom, err)
		}
		if !customInfo.Mode().IsRegular() {
			return fmt.Errorf("custom rules %q is not a regular file", custom)
		}
	}
	workDir := filepath.Join(buildDir, "raw")
	if err := os.RemoveAll(workDir); err != nil {
		return err
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	results, downloadErr := download.AllWithCache(ctx, urls, workDir, cacheDir, download.DefaultWorkers, download.DefaultRetries, download.DefaultTimeout)
	successful, cached := 0, 0
	for _, result := range results {
		if result.Err != nil {
			continue
		}
		successful++
		if result.Cached {
			cached++
		}
	}
	fmt.Printf("Sources: %d configured, %d succeeded", len(urls), successful)
	if cached > 0 {
		fmt.Printf(" (%d cache hits, %d downloads)", cached, successful-cached)
	}
	fmt.Println()

	// Machine-readable summary for build tooling (build.sh consumes this
	// instead of scraping the human-readable stdout line above).
	if summaryPath != "" {
		var sb strings.Builder
		fmt.Fprintf(&sb, "sources_configured=%d\n", len(urls))
		fmt.Fprintf(&sb, "sources_succeeded=%d\n", successful)
		fmt.Fprintf(&sb, "sources_cached=%d\n", cached)
		if err := os.WriteFile(summaryPath, []byte(sb.String()), 0o644); err != nil {
			return fmt.Errorf("write build summary: %w", err)
		}
	}

	if downloadErr != nil {
		for _, result := range results {
			if result.Err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: source %s failed: %v\n", result.URL, result.Err)
			}
		}
		if errors.Is(downloadErr, context.Canceled) || errors.Is(downloadErr, context.DeadlineExceeded) {
			return fmt.Errorf("source download canceled: %w", downloadErr)
		}
		if !allowPartial {
			return fmt.Errorf("source download failed; refusing to publish a partial ruleset: %w", downloadErr)
		}
		fmt.Fprintln(os.Stderr, "WARNING: continuing with a partial ruleset because --allow-partial was set")
	}
	if successful == 0 {
		if downloadErr != nil {
			return fmt.Errorf("source download failed: no sources succeeded: %w", downloadErr)
		}
		return fmt.Errorf("source download failed: no sources succeeded")
	}

	sortDir := filepath.Join(buildDir, "sort")
	// The deferred RemoveAll is the only cleanup needed: NewExternalSorter
	// creates the directory, and Finish removes the chunks it created.
	defer func() { _ = os.RemoveAll(sortDir) }()
	sorter, err := filter.NewExternalSorter(sortDir, sortChunkBytes)
	if err != nil {
		return err
	}
	b := filter.Builder{}
	var totalRead, totalRejected int
	reasons := make(map[string]int)
	accumulate := func(st filter.Stats) {
		totalRead += st.Read
		totalRejected += st.Rejected
		for reason, n := range st.ByReason {
			reasons[reason] += n
		}
	}
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		name := shortHash(r.URL)
		rej := filepath.Join(buildDir, "rejected-"+name+".txt")
		st, err := b.ReadFileToSink(r.Path, rej, sorter.Add)
		if err != nil {
			return err
		}
		accumulate(st)
	}
	if custom != "" {
		if customInfo.Size() > 0 {
			st, err := b.ReadFileToSink(custom, filepath.Join(buildDir, "rejected-custom.txt"), sorter.Add)
			if err != nil {
				return err
			}
			accumulate(st)
		}
	}
	result, err := sorter.Finish(outFile)
	if err != nil {
		return err
	}
	info, err := os.Stat(outFile)
	if err != nil {
		return fmt.Errorf("generated filter list: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("generated filter list is empty")
	}
	fmt.Printf("Read lines: %d; rejected: %d; duplicate rules removed: %d; output rules: %d\n",
		totalRead, totalRejected, result.Duplicates, result.Rules)
	if len(reasons) > 0 {
		keys := make([]string, 0, len(reasons))
		for k := range reasons {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if reasons[keys[i]] != reasons[keys[j]] {
				return reasons[keys[i]] > reasons[keys[j]]
			}
			return keys[i] < keys[j]
		})
		if len(keys) > 5 {
			keys = keys[:5]
		}
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%d", k, reasons[k]))
		}
		fmt.Printf("Top rejection reasons: %s\n", strings.Join(parts, ", "))
	}
	return nil
}

func shortHash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:6]) }
