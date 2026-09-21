package filter

import (
	"bufio"
	"container/heap"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultSortChunkBytes bounds the in-memory rule set used by the production
// builder before an external sort/merge. Keeping this small leaves most RAM
// available for the converter and the rest of the CI/build environment.
const DefaultSortChunkBytes int64 = 8 << 20

type SortResult struct {
	Rules      int
	Duplicates int
}

type ExternalSorter struct {
	dir        string
	chunkBytes int64
	rules      []string
	bytes      int64
	duplicates int
	chunks     []string
}

func NewExternalSorter(dir string, chunkBytes int64) (*ExternalSorter, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("sort directory is empty")
	}
	if chunkBytes <= 0 {
		chunkBytes = DefaultSortChunkBytes
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create sort directory: %w", err)
	}
	return &ExternalSorter{
		dir:        dir,
		chunkBytes: chunkBytes,
		rules:      make([]string, 0, 4096),
	}, nil
}

func (s *ExternalSorter) Add(rule string) error {
	if s == nil {
		return fmt.Errorf("external sorter is nil")
	}
	if rule == "" {
		return nil
	}

	// Apply Chromium's required bare-domain guard before sorting/deduplication.
	// Doing this at ingestion time means a bare-domain rule and an already
	// guarded equivalent collapse into the same canonical string without a
	// second global in-memory map.
	rule = optimizeRule(rule)
	ruleBytes := int64(len(rule)) + 1 // include the newline stored in chunks
	if len(s.rules) > 0 && s.bytes+ruleBytes > s.chunkBytes {
		if err := s.flushChunk(); err != nil {
			return err
		}
	}
	s.rules = append(s.rules, rule)
	s.bytes += ruleBytes
	return nil
}

func (s *ExternalSorter) Finish(output string) (result SortResult, err error) {
	if s == nil {
		return SortResult{}, fmt.Errorf("external sorter is nil")
	}
	defer func() {
		for _, path := range s.chunks {
			_ = os.Remove(path)
		}
	}()

	if err := s.flushChunk(); err != nil {
		return SortResult{}, err
	}
	if len(s.chunks) == 0 {
		return SortResult{}, fmt.Errorf("no compatible rules generated")
	}
	result.Duplicates = s.duplicates

	outDir := filepath.Dir(output)
	if outDir == "" {
		outDir = "."
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return SortResult{}, fmt.Errorf("create output directory: %w", err)
	}

	tmp, err := os.CreateTemp(outDir, ".filters.tmp-*")
	if err != nil {
		return SortResult{}, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	defer tmp.Close() // no-op after the explicit Close below; covers early error returns

	files := make([]*os.File, len(s.chunks))
	readers := make([]*bufio.Reader, len(s.chunks))
	defer func() {
		for _, f := range files {
			if f != nil {
				_ = f.Close()
			}
		}
	}()

	h := make(mergeHeap, 0, len(s.chunks))
	for i, path := range s.chunks {
		f, err := os.Open(path)
		if err != nil {
			return SortResult{}, fmt.Errorf("open sort chunk %s: %w", path, err)
		}
		files[i] = f
		readers[i] = bufio.NewReaderSize(f, 64<<10)
		line, err := nextChunkLine(readers[i])
		if err == io.EOF {
			continue
		}
		if err != nil {
			return SortResult{}, fmt.Errorf("read sort chunk %s: %w", path, err)
		}
		heap.Push(&h, mergeItem{line: line, reader: readers[i]})
	}

	w := bufio.NewWriterSize(tmp, 1<<20)
	last := ""
	haveLast := false
	for h.Len() > 0 {
		item := heap.Pop(&h).(mergeItem)
		if haveLast && item.line == last {
			result.Duplicates++
		} else {
			if err := writeLine(w, item.line); err != nil {
				return SortResult{}, fmt.Errorf("write generated filter list: %w", err)
			}
			result.Rules++
			last = item.line
			haveLast = true
		}

		next, err := nextChunkLine(item.reader)
		if err == nil {
			heap.Push(&h, mergeItem{line: next, reader: item.reader})
		} else if err != io.EOF {
			return SortResult{}, fmt.Errorf("read merged sort chunk: %w", err)
		}
	}

	if result.Rules == 0 {
		return SortResult{}, fmt.Errorf("no compatible rules generated")
	}
	if err := w.Flush(); err != nil {
		return SortResult{}, fmt.Errorf("flush generated filter list: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return SortResult{}, fmt.Errorf("sync generated filter list: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return SortResult{}, fmt.Errorf("close generated filter list: %w", err)
	}
	// CreateTemp uses 0600; published artifacts should be world-readable.
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return SortResult{}, fmt.Errorf("set generated filter list permissions: %w", err)
	}
	if err := os.Rename(tmpPath, output); err != nil {
		return SortResult{}, fmt.Errorf("replace %s: %w", output, err)
	}
	return result, nil
}

func (s *ExternalSorter) flushChunk() error {
	if len(s.rules) == 0 {
		return nil
	}
	sort.Strings(s.rules)
	unique := s.rules[:0]
	for _, rule := range s.rules {
		if len(unique) == 0 || unique[len(unique)-1] != rule {
			unique = append(unique, rule)
			continue
		}
		s.duplicates++
	}
	s.rules = unique

	path := filepath.Join(s.dir, fmt.Sprintf(".chunk-%06d.txt", len(s.chunks)))
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create sort chunk: %w", err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	for _, rule := range s.rules {
		if err := writeLine(w, rule); err != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return fmt.Errorf("write sort chunk: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("flush sort chunk: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close sort chunk: %w", err)
	}
	s.chunks = append(s.chunks, path)
	s.rules = s.rules[:0]
	s.bytes = 0
	return nil
}

type mergeItem struct {
	line   string
	reader *bufio.Reader
}

type mergeHeap []mergeItem

func (h mergeHeap) Len() int            { return len(h) }
func (h mergeHeap) Less(i, j int) bool  { return h[i].line < h[j].line }
func (h mergeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *mergeHeap) Push(x interface{}) { *h = append(*h, x.(mergeItem)) }
func (h *mergeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func writeLine(w *bufio.Writer, s string) error {
	if _, err := w.WriteString(s); err != nil {
		return err
	}
	return w.WriteByte('\n')
}

func nextChunkLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if len(line) > 0 {
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if err == io.EOF {
			return line, nil
		}
	}
	if err != nil {
		return "", err
	}
	return line, nil
}
