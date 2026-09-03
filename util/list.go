package util

import (
\t"bufio"
\t"fmt"
\t"log"
\t"net/url"
\t"os"
\t"sort"
\t"strings"
)

// ReadListFile returns all unique, valid HTTP(S) URLs from the given file,
// sorted lexicographically. Lines starting with '#' are treated as comments.
func ReadListFile(fn string) (entries []string, err error) {
\tf, err := os.Open(fn)
\tif err != nil {
\t\treturn nil, fmt.Errorf("open list file %q: %w", fn, err)
\t}
\tdefer f.Close()

\tseen := make(map[string]struct{})
\tvar list []string

\tscanner := bufio.NewScanner(f)
\t// Allow reasonably long lines; adjust if needed.
\tconst maxLineBytes = 2 * 1024 * 1024
\tbuf := make([]byte, 0, 64*1024)
\tscanner.Buffer(buf, maxLineBytes)

\tlineNum := 0
\tfor scanner.Scan() {
\t\tlineNum++
\t\tt := strings.TrimSpace(scanner.Text())

\t\tif t == "" || strings.HasPrefix(t, "#") {
\t\t\tcontinue
\t\t}

\t\tu, parseErr := url.ParseRequestURI(t)
\t\tif parseErr != nil || u.Host == "" {
\t\t\tlog.Printf("Invalid URL at %s:%d: %q", fn, lineNum, t)
\t\t\tcontinue
\t\t}
\t\tif u.Scheme != "http" && u.Scheme != "https" {
\t\t\tlog.Printf("Non-HTTP(S) URL at %s:%d: %q", fn, lineNum, t)
\t\t\tcontinue
\t\t}

\t\tif _, exists := seen[t]; exists {
\t\t\tlog.Printf("Duplicate URL %q will only be downloaded once", t)
\t\t\tcontinue
\t\t}
\t\tseen[t] = struct{}{}
\t\tlist = append(list, t)
\t}

\tif err := scanner.Err(); err != nil {
\t\treturn nil, fmt.Errorf("reading list file %q: %w", fn, err)
\t}

\tsort.Strings(list)
\treturn list, nil
}