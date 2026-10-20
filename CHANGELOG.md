# Changelog

This file records only the latest modified patch.

## [Unreleased]

### Changed (optimization)
- `validDomain` (called for every candidate rule and `$domain=` entry) now
  validates hostnames in a single pass instead of `strings.Split`, removing
  one allocation per call. Accepted/rejected hostnames are unchanged.
- `canonicalDomainList` sorts with `slices.SortFunc` instead of
  `sort.Slice`, dropping the `sort` import from `internal/filter`.
- The rejected-report header is written with `WriteString` instead of
  `fmt.Fprintln`.

### Removed (dead / redundant code)
- `canonicalDomainList` kept two maps (`seen` and `excludedBase`) that
  rejected the same inputs; one set of base domains now covers duplicates and
  include/exclude conflicts, and the unreachable empty-result check is gone.
- Dropped repeated `filepath.Clean` calls on paths that `pathutil.Resolve`,
  `filepath.Abs` and `filepath.Join` already return cleaned
  (`pathutil`, `ruleset`, `legacy-filter-builder`).
- `legacy-filter-builder` no longer re-stats the output after
  `ExternalSorter.Finish`, which already guarantees a non-empty file.
- `build.sh` no longer re-checks that `deps/ruleset_converter` is executable
  right after the install block (it either exists or the script has already
  exited) or that `dist/<name>.dat` is non-empty (`filtrite` rejects empty
  converter output).

### Documentation
- README: documented repeated-domain rejection in `$domain=` lists and where
  the empty-output check lives.
- No functional bugs or conflicts were found: the Go normalizer,
  `scripts/validate.sh`, `build.sh`, the workflows and the README agree.

### Verification
- The Go toolchain was not available here, so `go build`, `go vet` and
  `go test ./...` were not run; CI runs them. `bash -n build.sh` was checked.
