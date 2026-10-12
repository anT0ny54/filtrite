# Changelog

All notable changes to this project are documented in this file.

## [Unreleased]

### Fixed
- `internal/download.AllWithCache`: when cached sources alone exhausted the
  500 MiB manifest budget, the early return left every not-yet-fetched source
  as a zero-valued `Result` (empty URL/path, nil error). With
  `--allow-partial` the builder counted those as successful downloads and
  then failed trying to open path `""`. Unfetched sources are now returned
  with their URL and a non-nil error. Regression test:
  `TestCachedBudgetOverflowMarksUnfetchedSourcesFailed`.
- `cmd/filtrite`: `log.Fatal` was called from `main` after `defer stop()` /
  `defer cancel()`, so those defers never ran on failure. The work now lives
  in `run()`, which returns the error to `main`.

### Changed
- `internal/download.AllWithCache` dispatches work deterministically: all jobs
  are queued up front (the channel was already sized to hold every job, so the
  old `select` against `ctx.Done()` could never block and chose randomly when
  the context was already canceled), and each worker checks the context before
  starting a job, marking jobs it skips with the context error. On
  cancellation the function still returns `ctx.Err()` and no result is left
  zero-valued.
- `internal/filter`: `parseModifiers` no longer allocates three maps per rule;
  `third-party`/`domain` use boolean flags and element/activation type
  duplicates are found by scanning the (at most 12-entry) name slices.
  Output is unchanged (verified against the previous implementation on a
  161-case modifier corpus). The cosmetic/scriptlet marker list in `normalize`
  is a package-level array instead of a slice rebuilt per call.
- `build.sh` no longer unsets `sources_cached`, which it never read (the key is
  still written to the summary file for other tooling).

### Documentation
- README: the Build section's "Multiple named lists" cross-reference pointed
  "below" although that section is above it.
- README: `rules_read` is documented as an input-line count (it includes `!`
  comments and blank lines), matching `Stats.Read`.
- README: documented that cached-budget exhaustion reports unfetched sources
  as failed, that release-summary links use `GITHUB_REPOSITORY` with an
  `anT0ny54/filtrite` fallback for local builds, and that `filtrite` stops the
  converter on `SIGINT`/`SIGTERM`.

### Review notes (no change required)
- Static review found no unused functions in non-test code; `go vet` and
  `go test -race ./...` pass.
- `actions/checkout@v7` and `softprops/action-gh-release@v3.0.3` exist as
  published releases; the workflows were left unchanged.
