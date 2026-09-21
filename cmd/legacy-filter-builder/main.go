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
	allowPartial := flag.Bool("allow-partial", false, "continue when one or more source downloads fail")
	flag.Parse()
	if err := run(*sources, *custom, *output, *buildDir, *cacheDir, *allowPartial); err != nil {
		log.Fatal(err)
	}
}

func run(sources, custom, outFile, buildDir, cacheDir string, allowPartial bool) error {
	urls, err := download.URLsFromFile(sources)
	if err != nil {
		return err
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
	if err := os.RemoveAll(sortDir); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(sortDir) }()
	sorter, err := filter.NewExternalSorter(sortDir, filter.DefaultSortChunkBytes)
	if err != nil {
		return err
	}
	b := filter.Builder{}
	var totalRead, totalRejected int
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
		totalRead += st.Read
		totalRejected += st.Rejected
	}
	if custom != "" {
		info, err := os.Stat(custom)
		if err != nil {
			return fmt.Errorf("custom rules %q: %w", custom, err)
		}
		if info.IsDir() {
			return fmt.Errorf("custom rules %q is a directory", custom)
		}
		if info.Size() > 0 {
			st, err := b.ReadFileToSink(custom, filepath.Join(buildDir, "rejected-custom.txt"), sorter.Add)
			if err != nil {
				return err
			}
			totalRead += st.Read
			totalRejected += st.Rejected
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
	fmt.Printf("Read lines: %d; rejected: %d; duplicate rules removed: %d; output rules: %d\n", totalRead, totalRejected, result.Duplicates, result.Rules)
	return nil
}

func shortHash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:6]) }
