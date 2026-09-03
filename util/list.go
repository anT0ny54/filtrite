package util

import (
	"bufio"
	"fmt"
	"log"
	"net/url"
	"os"
	"sort"
	"strings"
)

// ReadListFile returns all unique, valid HTTP(S) URLs from the given file,
// sorted lexicographically. Lines starting with '#' are treated as comments.
func ReadListFile(fn string) (entries []string, err error) {
	f, err := os.Open(fn)
	if err != nil {
		return nil, fmt.Errorf("open list file %q: %w", fn, err)
	}
	defer f.Close()

	seen := make(map[string]struct{})
	var list []string

	scanner := bufio.NewScanner(f)
	// Allow reasonably long lines; adjust if needed.
	const maxLineBytes = 2 * 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxLineBytes)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		t := strings.TrimSpace(scanner.Text())

		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}

		u, parseErr := url.ParseRequestURI(t)
		if parseErr != nil || u.Host == "" {
			log.Printf("Invalid URL at %s:%d: %q", fn, lineNum, t)
			continue
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			log.Printf("Non-HTTP(S) URL at %s:%d: %q", fn, lineNum, t)
			continue
		}

		if _, exists := seen[t]; exists {
			log.Printf("Duplicate URL %q will only be downloaded once", t)
			continue
		}
		seen[t] = struct{}{}
		list = append(list, t)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading list file %q: %w", fn, err)
	}

	sort.Strings(list)
	return list, nil
}
