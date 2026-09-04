package util

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
)

const (
	releaseFile = "release.md"

	lineTemplate = "* {{if .Repo}}[`{{.ListName}}`](https://github.com/{{.Repo}}/releases/latest/download/{{.ListName}}.dat){{else}}`{{.ListName}}`{{end}}: updated {{.GotCount}}/{{.FullCount}} list{{if gt .ErrorCount 0}} ({{.ErrorCount}} failed){{end}}\n"
)

var (
	releaseTemplate = template.Must(
		template.New("releaseLine").Parse(lineTemplate),
	)

	// Protects concurrent calls within the same process.
	releaseFileMu sync.Mutex
)

type releaseInfo struct {
	ListName  string
	Repo      string
	GotCount  int
	FullCount int
	ErrorCount int
}

// AppendReleaseList appends one list-generation result to release.md.
//
// The file is opened with O_APPEND so each generated line is appended rather
// than replacing existing release notes.
func AppendReleaseList(fn string, gotCount, fullCount int) error {
	if gotCount < 0 {
		return fmt.Errorf("got count cannot be negative: %d", gotCount)
	}

	if fullCount < 0 {
		return fmt.Errorf("full count cannot be negative: %d", fullCount)
	}

	if gotCount > fullCount {
		return fmt.Errorf(
			"got count %d cannot exceed full count %d",
			gotCount,
			fullCount,
		)
	}

	listName := strings.TrimSuffix(filepath.Base(fn), ".txt")
	if listName == "" || listName == "." {
		return fmt.Errorf("invalid list filename %q", fn)
	}

	data := releaseInfo{
		ListName:   listName,
		Repo:       strings.TrimSpace(os.Getenv("GITHUB_REPOSITORY")),
		GotCount:   gotCount,
		FullCount:  fullCount,
		ErrorCount: fullCount - gotCount,
	}

	var builder strings.Builder
	if err := releaseTemplate.Execute(&builder, data); err != nil {
		return fmt.Errorf("render release line: %w", err)
	}

	releaseFileMu.Lock()
	defer releaseFileMu.Unlock()

	file, err := os.OpenFile(
		releaseFile,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o644,
	)
	if err != nil {
		return fmt.Errorf("open release file: %w", err)
	}

	line := builder.String()

	if _, err := file.WriteString(line); err != nil {
		_ = file.Close()
		return fmt.Errorf("write release file: %w", err)
	}

	// Ensure the written line is flushed before returning.
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync release file: %w", err)
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("close release file: %w", err)
	}

	return nil
}
