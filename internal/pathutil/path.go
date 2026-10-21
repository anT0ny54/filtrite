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
			// EvalSymlinks already returns a cleaned path.
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(resolveErr) {
			return "", fmt.Errorf("resolve existing path components: %w", resolveErr)
		}

		// EvalSymlinks reports not-exist when a symlink points to a target
		// that has not been created yet. Expand that link before walking up
		// to the nearest existing ancestor, otherwise dangling aliases evade
		// path-collision checks for future output/log files.
		if info, lstatErr := os.Lstat(candidate); lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(candidate)
			if readErr != nil {
				return "", fmt.Errorf("read symlink %q: %w", candidate, readErr)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(candidate), target)
			}
			candidate = filepath.Clean(target)
			continue
		} else if lstatErr != nil && !os.IsNotExist(lstatErr) {
			return "", fmt.Errorf("inspect path component %q: %w", candidate, lstatErr)
		}

		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", fmt.Errorf("resolve path %q: no existing ancestor", path)
		}
		missing = append(missing, filepath.Base(candidate))
		candidate = parent
	}
}
