package util

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	releaseFile = "release.md"

	lineTemplate = "* {{if .Repo}}[`{{.ListName}}`](https://github.com/{{.Repo}}/releases/latest/download/{{.ListName}}.dat){{else}}`{{.ListName}}`{{end}}: updated {{.GotCount}}/{{.FullCount}} list{{if gt .ErrorCount 0}} ({{.ErrorCount}} failed){{end}}\n"
)

var tmpl = template.Must(template.New("releaseLine").Parse(lineTemplate))

type info struct {
	ListName string
	Repo     string

	GotCount  int
	FullCount int

	ErrorCount int
}

// AppendReleaseList appends a line describing this list generation run to release.md.
// It writes atomically via a temp file + rename to avoid partial lines on failure.
func AppendReleaseList(fn string, gotCount, fullCount int) (err error) {
	friendlyName := strings.TrimSuffix(filepath.Base(fn), ".txt")

	data := info{
		ListName:   friendlyName,
		Repo:       os.Getenv("GITHUB_REPOSITORY"),
		GotCount:   gotCount,
		FullCount:  fullCount,
		ErrorCount: fullCount - gotCount,
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("rendering release line: %w", err)
	}

	// Open for appending; create if missing.
	f, err := os.OpenFile(releaseFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening release file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(buf.String()); err != nil {
		return fmt.Errorf("writing release line: %w", err)
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("flushing release file: %w", err)
	}

	return nil
}
