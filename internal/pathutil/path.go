// Package pathutil provides filesystem path canonicalization for collision
// checks without requiring the final path component to exist yet.
package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// Resolve returns an absolute, cleaned path with every existing symlink
// component resolved. If the leaf does not exist, it resolves the nearest
// existing ancestor and appends the missing components. This lets callers
// detect aliases through symlinked parent directories before creating output
// files or deleting managed directories.
func Resolve(path string) (string, error) {
	candidate, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("make path absolute: %w", err)
	}

	var missing []string
	for {
		resolved, resolveErr := filepath.EvalSymlinks(candidate)
		if resolveErr == nil {
			resolved = filepath.Clean(resolved)
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(resolveErr) {
			return "", fmt.Errorf("resolve existing path components: %w", resolveErr)
		}

		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", fmt.Errorf("resolve path %q: no existing ancestor", path)
		}
		missing = append(missing, filepath.Base(candidate))
		candidate = parent
	}
}
