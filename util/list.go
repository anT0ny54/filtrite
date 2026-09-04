package util

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
)

const (
	maxListLineBytes  = 2 * 1024 * 1024 // 2 MiB
	initialURLCapacity = 256
)

// ReadListFile returns unique, valid HTTP(S) URLs from the given file,
// sorted lexicographically.
//
// Empty lines and lines whose first non-whitespace character is '#'
// are ignored. Invalid and non-HTTP(S) entries are skipped.
func ReadListFile(fn string) (urls []string, err error) {
	f, err := os.Open(fn)
	if err != nil {
		return nil, fmt.Errorf("open list file %q: %w", fn, err)
	}

	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			urls = nil
			err = fmt.Errorf("close list file %q: %w", fn, closeErr)
		}
	}()

	urls = make([]string, 0, initialURLCapacity)
	seen := make(map[string]struct{}, initialURLCapacity)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(
		make([]byte, 64*1024),
		maxListLineBytes,
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Ignore blank lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parsed, parseErr := url.ParseRequestURI(line)
		if parseErr != nil {
			continue
		}

		// Require an absolute HTTP(S) URL with a valid hostname.
		if parsed.Hostname() == "" {
			continue
		}

		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			continue
		}

		// Ignore exact duplicates.
		if _, exists := seen[line]; exists {
			continue
		}

		seen[line] = struct{}{}
		urls = append(urls, line)
	}

	if scanErr := scanner.Err(); scanErr != nil {
		return nil, fmt.Errorf("read list file %q: %w", fn, scanErr)
	}

	sort.Strings(urls)

	return urls, nil
}
