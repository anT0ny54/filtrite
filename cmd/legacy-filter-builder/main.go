package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	output := flag.String("output", "filters.txt", "generated legacy-compatible filter-list")
	buildDir := flag.String("build-dir", "build", "build/report directory")
	flag.Parse()
	if err := run(*sources, *custom, *output, *buildDir); err != nil {
		log.Fatal(err)
	}
}

func run(sources, custom, outFile, buildDir string) error {
	urls, err := download.URLsFromFile(sources)
	if err != nil {
		return err
	}
	raw := filepath.Join(buildDir, "raw")
	if err := os.RemoveAll(raw); err != nil {
		return err
	}
	if err := os.MkdirAll(raw, 0755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	results, downloadErr := download.All(ctx, urls, raw, download.DefaultWorkers, download.DefaultRetries, download.DefaultTimeout)
	successful := 0
	for _, result := range results {
		if result.Err == nil {
			successful++
		}
	}
	fmt.Printf("Sources: %d configured, %d downloaded\n", len(urls), successful)

	// A single flaky third-party mirror should not abort the whole build:
	// each source is independent, and internal/download already preserves
	// a per-URL result specifically so the build can proceed with whatever
	// succeeded. Only treat this as fatal if the run was cut short by
	// cancellation/timeout (results may be incomplete) or if nothing at
	// all was downloaded.
	if downloadErr != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("source download failed: %w", downloadErr)
		}
		for _, result := range results {
			if result.Err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: skipping source %s: %v\n", result.URL, result.Err)
			}
		}
	}
	if successful == 0 {
		return fmt.Errorf("source download failed: %w", downloadErr)
	}

	b := filter.Builder{}
	var allRules []string
	var totalRead, totalRejected int
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		name := shortHash(r.URL)
		rej := filepath.Join(buildDir, "rejected-"+name+".txt")
		rules, st, err := b.ReadFile(r.Path, rej)
		if err != nil {
			return err
		}
		allRules = append(allRules, rules...)
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
			rules, st, err := b.ReadFile(custom, filepath.Join(buildDir, "rejected-custom.txt"))
			if err != nil {
				return err
			}
			allRules = append(allRules, rules...)
			totalRead += st.Read
			totalRejected += st.Rejected
		}
	}
	final, red := filter.Optimize(allRules)
	if len(final) == 0 {
		return fmt.Errorf("no compatible rules generated")
	}
	if err := filter.Write(outFile, final); err != nil {
		return err
	}
	fmt.Printf("Read rules: %d; rejected: %d; duplicate rules removed: %d; output rules: %d\n", totalRead, totalRejected, red, len(final))
	return nil
}
func shortHash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:6]) }
