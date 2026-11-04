# Changelog

This file records only the latest modified patch. No project version is bumped here.

## [Unreleased]

### Fixed
- `internal/download`: `MaxRedirects` is renamed to match its meaning and the README: it is the number of redirect hops allowed (4). The guard is now `len(via) > MaxRedirects`, which refuses exactly the same requests as the previous `len(via) >= 5`; the old name and value suggested five hops.
- `cmd/legacy-filter-builder`: the `--custom` help text called the file optional, but the default `custom-rules.txt` must exist and a missing file is a fatal error. The help text now states that requirement.
- `custom-rules.txt`: corrected the "Cromium" typo in the header comment to "Chromium".

### Removed
- `internal/pathutil`: removed a no-op `unresolved` reset in the absolute-symlink branch. The slice is always empty at that point, because a component is only inspected while nothing before it is missing.
- `internal/filter`: collapsed a `switch` in the `||host` normalizer whose `^`/`|` case duplicated the fall-through return. Behavior is unchanged.

### Optimized
- `internal/filter`: rejected-rule report records are written field by field through `writeRejectedLine`, which formats the line number into a stack buffer. Each rejected line no longer allocates a concatenated string.

### Documentation
- README: documents that `--custom` defaults to a required `custom-rules.txt`, and how to disable custom rules with `--custom ""`.

### Verification
- Every file in the project was read and cross-checked against the README and the tests. The review found no functional defect in the download, sorting, normalization, or path-resolution logic. The changes above are conflicts, redundancy, and allocation fixes.
- This environment had no Go toolchain and no network access, so nothing was compiled or executed. Before release, run `go vet ./...`, `go test ./...`, `go test -race ./internal/download/`, and `bash scripts/validate_test.sh`.
