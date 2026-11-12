package download

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTMLErrorDetection(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "doctype", body: "<!DOCTYPE html>\n<title>403</title>", want: true},
		{name: "lower-case html tag", body: "<html><body>x</body></html>", want: true},
		{name: "mixed-case body tag", body: "  <BoDy>error</BoDy>", want: true},
		{name: "head tag", body: "<head><title>x</title>", want: true},
		{name: "BOM then whitespace", body: "\uFEFF \t<!doctype HTML>", want: true},
		{name: "leading non-breaking space", body: "\u00a0<html>", want: true},
		{name: "more than 4 KiB of leading whitespace", body: strings.Repeat(" \t\r\n", 1200) + "<!doctype html>", want: true},
		{name: "filter rules", body: "||ads.example^\n", want: false},
		{name: "comment mentioning html", body: "! <html> in a comment later\n", want: false},
		{name: "truncated prefix", body: "<!doctype htm", want: false},
		{name: "tag inside text", body: "text <html>", want: false},
	}
	dir := t.TempDir()
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, "body"+string(rune('a'+i)))
			if err := os.WriteFile(path, []byte(tt.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := htmlError(path); got != tt.want {
				t.Fatalf("htmlError(%q)=%v, want %v", tt.body, got, tt.want)
			}
		})
	}
	if htmlError(filepath.Join(dir, "missing")) {
		t.Fatal("missing file reported as HTML error")
	}
}
