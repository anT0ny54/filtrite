package main

import (
	"context"
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
	"filtrite/internal/pathutil"
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
	// run() and filter.NewExternalSorter share one policy: a non-positive
	// --sort-chunk-bytes is an error, never silently coerced to the default.
	if err := run(*sources, *custom, *output, *buildDir, *cacheDir, *summary, *allowPartial, *sortChunkBytes); err != nil {
		log.Fatal(err)
	}
}

func run(sources, custom, outFile, buildDir, cacheDir, summaryPath string, allowPartial bool, sortChunkBytes int64) error {
	if sortChunkBytes <= 0 {
		return fmt.Errorf("sort chunk size must be positive")
	}
	if strings.TrimSpace(sources) == "" || strings.TrimSpace(outFile) == "" || strings.TrimSpace(buildDir) == "" {
		return fmt.Errorf("source, output, and build directory paths must be non-empty")
	}
	if custom != "" && strings.TrimSpace(custom) == "" {
		return fmt.Errorf("custom rules path must be empty to disable custom rules")
	}
	if cacheDir != "" && strings.TrimSpace(cacheDir) == "" {
		return fmt.Errorf("cache directory path must be empty or a non-blank path")
	}
	if summaryPath != "" && strings.TrimSpace(summaryPath) == "" {
		return fmt.Errorf("summary path must be empty or a non-blank path")
	}
	// Validate the user-controlled input/output paths before touching an old
	// summary. This also prevents a summary path from truncating an input file.
	if err := rejectAliasedBuilderPaths(sources, custom, outFile, buildDir, cacheDir, summaryPath, nil); err != nil {
		return err
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return err
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
	// Include the deterministic report paths in the second check; otherwise a
	// caller could accidentally make a report overwrite a manifest or output.
	if err := rejectAliasedBuilderPaths(sources, custom, outFile, buildDir, cacheDir, summaryPath, urls); err != nil {
		return err
	}
	// Clear the previous success summary only after all input and generated
	// report paths are known and verified not to collide.
	if err := clearStaleSummary(summaryPath); err != nil {
		return err
	}
	// Snapshots land in the shared cache directory whenever one is
	// configured. Only a hand-run builder without --cache-dir uses the
	// per-list raw/ staging directory: it is wiped here so stale snapshots
	// never linger, and download.AllWithCache recreates it.
	downloadDir := filepath.Join(buildDir, "raw")
	if cacheDir == "" {
		if err := os.RemoveAll(downloadDir); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	results, downloadErr := download.AllWithCache(ctx, urls, downloadDir, cacheDir, download.DefaultWorkers, download.DefaultRetries, download.DefaultTimeout)
	successful, cached := 0, 0
	var totalBytes int64
	for _, result := range results {
		if result.Err != nil {
			continue
		}
		successful++
		totalBytes += result.Bytes
		if result.Cached {
			cached++
		}
	}
	fmt.Printf("Sources: %d configured, %d succeeded", len(urls), successful)
	if cached > 0 {
		fmt.Printf(" (%d cache hits, %d downloads, %d bytes)", cached, successful-cached, totalBytes)
	} else {
		fmt.Printf(" (%d bytes)", totalBytes)
	}
	fmt.Println()

	if downloadErr != nil {
		for _, result := range results {
			if result.Err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: source %s failed: %v\n", result.URL, result.Err)
			}
		}
		if ctx.Err() != nil {
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
		st, err := filter.ReadFileToSink(r.Path, rejectedReportPath(buildDir, r.URL), sorter.Add)
		if err != nil {
			return err
		}
		accumulate(st)
	}
	if custom != "" && customInfo.Size() > 0 {
		st, err := filter.ReadFileToSink(custom, filepath.Join(buildDir, "rejected-custom.txt"), sorter.Add)
		if err != nil {
			return err
		}
		accumulate(st)
	}
	result, err := sorter.Finish(outFile)
	if err != nil {
		return err
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

	// Machine-readable summary for build tooling (build.sh consumes this
	// instead of scraping the human-readable stdout line above). Written
	// only after a fully successful build so a failed build can never leave
	// stale counters behind. Besides the source counters it carries
	// rule-effectiveness totals for measuring list quality.
	if summaryPath != "" {
		var sb strings.Builder
		fmt.Fprintf(&sb, "sources_configured=%d\n", len(urls))
		fmt.Fprintf(&sb, "sources_succeeded=%d\n", successful)
		fmt.Fprintf(&sb, "sources_cached=%d\n", cached)
		fmt.Fprintf(&sb, "rules_read=%d\n", totalRead)
		fmt.Fprintf(&sb, "rules_rejected=%d\n", totalRejected)
		fmt.Fprintf(&sb, "rules_duplicates=%d\n", result.Duplicates)
		fmt.Fprintf(&sb, "rules_output=%d\n", result.Rules)
		if err := os.WriteFile(summaryPath, []byte(sb.String()), 0o644); err != nil {
			return fmt.Errorf("write build summary: %w", err)
		}
	}
	return nil
}

// rejectedReportPath is the per-source rejection report location: the first
// 12 hex characters of the URL hash keep names short but stable. It is shared
// by the writer and the alias preflight so the two can never drift apart.
func rejectedReportPath(buildDir, sourceURL string) string {
	return filepath.Join(buildDir, "rejected-"+download.SourceName(sourceURL)[:12]+".txt")
}

type builderPath struct {
	name      string
	path      string
	generated bool
	ownsTree  bool
}

// rejectAliasedBuilderPaths prevents generated files from overwriting inputs
// and prevents output, summary, and rejected-report paths from colliding.
// Lexical aliases are caught even when the destination does not exist yet;
// os.SameFile catches existing hard links and symlink targets.
func rejectAliasedBuilderPaths(sources, custom, outFile, buildDir, cacheDir, summaryPath string, urls []string) error {
	paths := []builderPath{{name: "source manifest", path: sources}}
	if custom != "" {
		paths = append(paths, builderPath{name: "custom rules", path: custom})
	}
	paths = append(paths, builderPath{name: "output", path: outFile, generated: true})
	// These directories are deleted during a normal builder run. Reject inputs
	// and outputs anywhere under them, not just paths equal to the directory.
	paths = append(paths, builderPath{
		name: "sort temporary directory", path: filepath.Join(buildDir, "sort"), generated: true, ownsTree: true,
	})
	if cacheDir != "" {
		// Cache entries are written under this directory. Keep all inputs and
		// final/generated paths outside it so cached files cannot overwrite them.
		paths = append(paths, builderPath{
			name: "shared cache directory", path: cacheDir, generated: true, ownsTree: true,
		})
	} else {
		paths = append(paths, builderPath{
			name: "raw staging directory", path: filepath.Join(buildDir, "raw"), generated: true, ownsTree: true,
		})
	}
	if summaryPath != "" {
		paths = append(paths, builderPath{name: "summary", path: summaryPath, generated: true})
	}
	for _, sourceURL := range urls {
		paths = append(paths, builderPath{
			name:      "source rejection report",
			path:      rejectedReportPath(buildDir, sourceURL),
			generated: true,
		})
	}
	if custom != "" {
		paths = append(paths, builderPath{
			name:      "custom rejection report",
			path:      filepath.Join(buildDir, "rejected-custom.txt"),
			generated: true,
		})
	}

	infos := make([]os.FileInfo, len(paths))
	statErrs := make([]error, len(paths))
	for i := range paths {
		resolvedPath, err := pathutil.Resolve(paths[i].path)
		if err != nil {
			return fmt.Errorf("resolve %s path %q: %w", paths[i].name, paths[i].path, err)
		}
		paths[i].path = resolvedPath
		infos[i], statErrs[i] = os.Stat(paths[i].path)
		if statErrs[i] != nil && !os.IsNotExist(statErrs[i]) {
			return fmt.Errorf("stat %s path %q: %w", paths[i].name, paths[i].path, statErrs[i])
		}
	}
	for i := 0; i < len(paths); i++ {
		for j := i + 1; j < len(paths); j++ {
			if !paths[i].generated && !paths[j].generated {
				continue
			}
			if paths[i].path == paths[j].path ||
				(paths[i].ownsTree && pathIsWithin(paths[j].path, paths[i].path)) ||
				(paths[j].ownsTree && pathIsWithin(paths[i].path, paths[j].path)) {
				return fmt.Errorf("%s and %s paths must not alias or overlap: %q and %q", paths[i].name, paths[j].name, paths[i].path, paths[j].path)
			}
			if statErrs[i] == nil && statErrs[j] == nil && os.SameFile(infos[i], infos[j]) {
				return fmt.Errorf("%s and %s paths must not alias: %q and %q refer to the same file", paths[i].name, paths[j].name, paths[i].path, paths[j].path)
			}
		}
	}
	return nil
}

func pathIsWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func clearStaleSummary(path string) error {
	if path == "" {
		return nil
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect stale build summary %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("summary path is not a regular file: %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale build summary %q: %w", path, err)
	}
	return nil
}
