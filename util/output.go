package util

import (
\t"fmt"
\t"os"
\t"path/filepath"
\t"strings"
\t"text/template"
)

const (
\treleaseFile = "release.md"

\tlineTemplate = "* {{if .Repo}}[`{{.ListName}}`](https://github.com/{{.Repo}}/releases/latest/download/{{.ListName}}.dat){{else}}`{{.ListName}}`{{end}}: updated {{.GotCount}}/{{.FullCount}} list{{if ne .FullCount 1}}s{{end}}{{if ne .ErrorCount 0}}, {{if eq .ErrorCount 1}}one error{{else}}{{.ErrorCount}} errors{{end}}{{end}}
"
)

var tmpl = template.Must(template.New("releaseLine").Parse(lineTemplate))

type info struct {
\tListName string
\tRepo     string

\tGotCount  int
\tFullCount int

\tErrorCount int
}

// AppendReleaseList appends a line describing this list generation run to release.md.
// It writes atomically via a temp file + rename to avoid partial lines on failure.
func AppendReleaseList(fn string, gotCount, fullCount int) (err error) {
\tfriendlyName := strings.TrimSuffix(filepath.Base(fn), ".txt")

\tdata := info{
\t\tListName:   friendlyName,
\t\tRepo:       os.Getenv("GITHUB_REPOSITORY"),
\t\tGotCount:   gotCount,
\t\tFullCount:  fullCount,
\t\tErrorCount: fullCount - gotCount,
\t}

\tvar buf strings.Builder
\tif err := tmpl.Execute(&buf, data); err != nil {
\t\treturn fmt.Errorf("rendering release line: %w", err)
\t}

\t// Open for appending; create if missing.
\tf, err := os.OpenFile(releaseFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
\tif err != nil {
\t\treturn fmt.Errorf("opening release file: %w", err)
\t}
\tdefer f.Close()

\tif _, err := f.WriteString(buf.String()); err != nil {
\t\treturn fmt.Errorf("writing release line: %w", err)
\t}

\tif err := f.Sync(); err != nil {
\t\treturn fmt.Errorf("flushing release file: %w", err)
\t}

\treturn nil
}