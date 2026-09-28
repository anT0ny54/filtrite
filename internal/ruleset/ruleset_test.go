package ruleset

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertRejectsAliasedPaths(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	output := filepath.Join(dir, "output.dat")
	if err := os.WriteFile(input, []byte("||example.com^\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		o    Options
		want string
	}{
		{name: "output equals input", o: Options{Converter: "/bin/true", Input: input, Output: input}, want: "input and output paths must not alias"},
		{name: "log equals input", o: Options{Converter: "/bin/true", Input: input, Output: output, Log: input}, want: "input and log paths must not alias"},
		{name: "log equals output", o: Options{Converter: "/bin/true", Input: input, Output: output, Log: output}, want: "output and log paths must not alias"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Convert(context.Background(), tt.o)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Convert error=%v, want substring %q", err, tt.want)
			}
		})
	}
}
