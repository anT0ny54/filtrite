// Package pathutil provides filesystem path canonicalization for collision
// checks without requiring the final path component to exist yet.
package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve returns an absolute, cleaned path with every existing symlink
// component resolved. If the leaf does not exist, it resolves the nearest
// existing ancestor and appends the missing components. Components are
// processed from left to right so a ".." after a symlink is interpreted from
// the symlink target, matching filesystem path resolution rather than
// filepath.Clean's purely lexical behavior.
func Resolve(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	if !filepath.IsAbs(path) {
		if filepath.VolumeName(path) != "" {
			return "", fmt.Errorf("volume-relative path %q is ambiguous", path)
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		// Do not use filepath.Join here: it would clean away ".." before
		// symlinks earlier in the input have been resolved.
		path = cwd + string(filepath.Separator) + path
	}

	volume := filepath.VolumeName(path)
	rest := path[len(volume):]
	// No absolute-path re-validation is needed here: os.Getwd is documented to
	// return a rooted path and the separator was appended explicitly above.

	resolved := volume + string(filepath.Separator)
	unresolved := make([]string, 0, 4)
	parts := splitComponents(rest)
	symlinkCount := 0

	for i := 0; i < len(parts); i++ {
		part := parts[i]
		switch part {
		case "", ".":
			continue
		case "..":
			if len(unresolved) > 0 {
				unresolved = unresolved[:len(unresolved)-1]
			} else {
				parent := filepath.Dir(resolved)
				if parent != resolved {
					resolved = parent
				}
			}
			continue
		}

		// Once a component is missing, later components cannot be inspected
		// safely. Preserve them lexically, while still allowing a following
		// ".." to cancel a missing component.
		if len(unresolved) > 0 {
			unresolved = append(unresolved, part)
			continue
		}

		candidate := filepath.Join(resolved, part)
		info, err := os.Lstat(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				unresolved = append(unresolved, part)
				continue
			}
			return "", fmt.Errorf("inspect path component %q: %w", candidate, err)
		}

		if info.Mode()&os.ModeSymlink == 0 {
			resolved = candidate
			continue
		}

		symlinkCount++
		if symlinkCount > 255 {
			return "", fmt.Errorf("resolve path %q: too many symlink components", path)
		}
		target, err := os.Readlink(candidate)
		if err != nil {
			return "", fmt.Errorf("read symlink %q: %w", candidate, err)
		}

		remaining := append([]string(nil), parts[i+1:]...)
		// unresolved is necessarily empty here: a component is only inspected
		// while nothing before it is missing, so it never needs resetting.
		if filepath.IsAbs(target) {
			volume = filepath.VolumeName(target)
			targetRest := target[len(volume):]
			resolved = volume + string(filepath.Separator)
			parts = append(splitComponents(targetRest), remaining...)
		} else {
			if filepath.VolumeName(target) != "" {
				return "", fmt.Errorf("resolve symlink %q: volume-relative target %q is ambiguous", candidate, target)
			}
			// A relative link target is interpreted from the link's parent,
			// which is still held in resolved.
			parts = append(splitComponents(target), remaining...)
		}
		i = -1
	}

	for _, part := range unresolved {
		resolved = filepath.Join(resolved, part)
	}
	return resolved, nil
}

func splitComponents(path string) []string {
	return strings.Split(strings.TrimLeft(path, string(filepath.Separator)), string(filepath.Separator))
}
