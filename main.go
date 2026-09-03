package main

import (
\t"context"
\t"fmt"
\t"log"
\t"os"
\t"path/filepath"
\t"strings"
\t"sync"
\t"time"
\t"unicode"

\t"ant0ny54/filtrite/util"
)

const (
\ttmpDir  = "tmp"
\tlistDir = "lists"
\tdistDir = "dist"
\tlogDir  = "logs"

\t// Chromium/Bromite kMaxBodySize.
\t// Bromite accepts filter files up to 20 MiB.
\tbromiteMaxFilterSize = 20 * 1024 * 1024
)

type generationError struct {
\tfile string
\terr  error
}

// safeListName produces a filesystem-safe name from a list file path.
func safeListName(path string) string {
\tname := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
\tname = strings.TrimSpace(name)

\tname = strings.Map(func(r rune) rune {
\t\tswitch {
\t\tcase unicode.IsSpace(r):
\t\t\treturn '_'
\t\tcase r == '/', r == '\\', r == ':', r == '*', r == '?', r == '"', r == '<', r == '>', r == '|':
\t\t\treturn '_'
\t\tdefault:
\t\t\treturn r
\t\t}
\t}, name)

\tif name == "" {
\t\treturn "filter-list"
\t}

\treturn name
}

func isListFile(path string, info os.FileInfo) bool {
\tif info == nil || info.IsDir() {
\t\treturn false
\t}

\treturn strings.EqualFold(filepath.Ext(path), ".txt")
}

func ensureDirectories() error {
\tdirectories := []string{
\t\tlistDir,
\t\tdistDir,
\t\tlogDir,
\t}

\tfor _, directory := range directories {
\t\tif err := os.MkdirAll(directory, 0o755); err != nil {
\t\t\treturn fmt.Errorf("creating directory %q: %w", directory, err)
\t\t}
\t}

\treturn nil
}

func generateFilterList(ctx context.Context, listTextFile string) error {
\tlistName := safeListName(listTextFile)

\toutputFile := filepath.Join(distDir, listName+".dat")
\tlogFile := filepath.Join(logDir, listName+".log")

\tfmt.Printf("::group::List: %s
", listName)
\tdefer fmt.Println("::endgroup::")

\tfilterListURLs, err := util.ReadListFile(listTextFile)
\tif err != nil {
\t\treturn fmt.Errorf("reading list file: %w", err)
\t}

\tif len(filterListURLs) == 0 {
\t\treturn fmt.Errorf("no filter-list URLs found in %q", listTextFile)
\t}

\t// Create a fresh temporary directory for this specific list.
\tlistTmpDir := filepath.Join(tmpDir, listName)
\tif err := os.RemoveAll(listTmpDir); err != nil {
\t\treturn fmt.Errorf("removing temporary directory for %q: %w", listName, err)
\t}
\tif err := os.MkdirAll(listTmpDir, 0o755); err != nil {
\t\treturn fmt.Errorf("creating temporary directory for %q: %w", listName, err)
\t}

\tdefer func() {
\t\tif err := os.RemoveAll(listTmpDir); err != nil {
\t\t\tlog.Printf("warning: removing temporary directory %q: %v", listTmpDir, err)
\t\t}
\t}()

\tlog.Printf(
\t\t"Downloading %d filter lists for %s",
\t\tlen(filterListURLs),
\t\tlistName,
\t)

\t// util.DownloadURLs now handles concurrency, retries, and timeouts.
\tpaths, err := util.DownloadURLs(filterListURLs, listTmpDir)
\tif err != nil {
\t\t// We still proceed if at least some lists were downloaded.
\t\tlog.Printf("warning: some downloads failed: %v", err)
\t}

\tif len(paths) == 0 {
\t\treturn fmt.Errorf(
\t\t\t"all filter lists failed to download: %d attempted",
\t\t\tlen(filterListURLs),
\t\t)
\t}

\tlog.Printf(
\t\t"Downloaded %d/%d filter lists",
\t\tlen(paths),
\t\tlen(filterListURLs),
\t)

\tif err := os.MkdirAll(distDir, 0o755); err != nil {
\t\treturn fmt.Errorf("creating distribution directory: %w", err)
\t}

\tif err := os.MkdirAll(logDir, 0o755); err != nil {
\t\treturn fmt.Errorf("creating log directory: %w", err)
\t}

\t// Remove previous output before generating the new file.
\tif err := os.Remove(outputFile); err != nil && !os.IsNotExist(err) {
\t\treturn fmt.Errorf("removing previous output %q: %w", outputFile, err)
\t}

\tlog.Printf("Converting ruleset for legacy Bromite engine")

\tif err := util.GenerateDistributableList(paths, outputFile, logFile); err != nil {
\t\treturn fmt.Errorf("generating distributable list: %w", err)
\t}

\tfileInfo, err := os.Stat(outputFile)
\tif err != nil {
\t\treturn fmt.Errorf("checking generated output: %w", err)
\t}

\tif fileInfo.Size() == 0 {
\t\treturn fmt.Errorf("generated filter file is empty")
\t}

\tif fileInfo.Size() > bromiteMaxFilterSize {
\t\treturn fmt.Errorf(
\t\t\t"filter list is too large for Bromite: %d bytes > %d bytes",
\t\t\tfileInfo.Size(),
\t\t\tbromiteMaxFilterSize,
\t\t)
\t}

\tlog.Printf(
\t\t"Generated %s: %d bytes",
\t\toutputFile,
\t\tfileInfo.Size(),
\t)

\tif err := util.AppendReleaseList(
\t\tlistTextFile,
\t\tlen(paths),
\t\tlen(filterListURLs),
\t); err != nil {
\t\treturn fmt.Errorf("generating release list: %w", err)
\t}

\treturn nil
}

func processLists(ctx context.Context) []generationError {
\tvar failures []generationError
\tvar mu sync.Mutex

\tentries, err := os.ReadDir(listDir)
\tif err != nil {
\t\treturn []generationError{
\t\t\t{file: listDir, err: fmt.Errorf("reading list directory: %w", err)},
\t\t}
\t}

\t// Sequential per-list processing; safe and simple.
\tfor _, entry := range entries {
\t\tif entry.IsDir() {
\t\t\tcontinue
\t\t}

\t\tpath := filepath.Join(listDir, entry.Name())

\t\tinfo, err := entry.Info()
\t\tif err != nil {
\t\t\tfailures = append(failures, generationError{
\t\t\t\tfile: path,
\t\t\t\terr:  fmt.Errorf("reading file info: %w", err),
\t\t\t})
\t\t\tcontinue
\t\t}

\t\tif !isListFile(path, info) {
\t\t\tcontinue
\t\t}

\t\tif err := generateFilterList(ctx, path); err != nil {
\t\t\tmu.Lock()
\t\t\tfailures = append(failures, generationError{
\t\t\t\tfile: path,
\t\t\t\terr:  err,
\t\t\t})
\t\t\tmu.Unlock()

\t\t\tlog.Printf(
\t\t\t\t"ERROR: failed to generate %q: %v",
\t\t\t\tpath,
\t\t\t\terr,
\t\t\t)
\t\t}
\t}

\treturn failures
}

func main() {
\tlog.SetFlags(log.Ldate | log.Ltime)

\tif err := ensureDirectories(); err != nil {
\t\tlog.Fatalf("initialization failed: %v", err)
\t}

\t// Root context for the whole run; can be extended with timeouts or cancellation.
\tctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
\tdefer cancel()

\tfailures := processLists(ctx)

\tif len(failures) == 0 {
\t\tlog.Println("All filter lists generated successfully")
\t\treturn
\t}

\tlog.Printf(
\t\t"Generation completed with %d failure(s)",
\t\tlen(failures),
\t)

\tfor _, failure := range failures {
\t\tlog.Printf(
\t\t\t"FAILED %s: %v",
\t\t\tfailure.file,
\t\t\tfailure.err,
\t\t)
\t}

\tos.Exit(1)
}