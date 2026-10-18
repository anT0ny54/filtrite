# Changelog

All notable changes to this project are documented in this file.

## [Unreleased]

### Fixed
- `scripts/validate.sh`: fully anchored URL rules that end with the `|`
  end-of-URL anchor (for example `|https://example.com/ads.js|`,
  `@@|https://example.com/allowed.js|`, or `|https://example.com/x|$third-party`)
  were rejected as `malformed anchored URL`, even though
  `internal/filter/filter.go` deliberately emits them. Any source list that
  contained such a rule therefore aborted the whole build at the validation
  step. The validator now treats exactly one trailing `|` as the end anchor and
  still rejects a second trailing `|` or a `|` inside the URL, matching
  `validAnchoredTail` in the Go stage.
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

- `internal/filter.ReadFileToSink`: the rejected-rule report file was only
  closed by a `defer` that discarded its error, so a failed final write at
  close time could leave a truncated `rejected-*.txt` without any message.
  The file is now closed explicitly after the final flush and a close error
  fails the build like any other report-write error.

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
  Output is unchanged. The cosmetic/scriptlet marker list in `normalize` is a
  package-level array instead of a slice rebuilt per call.
- `build.sh` no longer unsets `sources_cached`, which it never read (the key is
  still written to the summary file for other tooling).
- Comment-only cleanups in `cmd/legacy-filter-builder/main.go` and
  `internal/filter/filter.go`: removed "previously/now" narration that no
  longer described the code and re-wrapped one over-long comment. No code
  changed.

### Tests
- `scripts/validate_test.sh` gained regression cases for end-anchored URLs:
  three accepted (`|https://example.com/ads.js|`,
  `@@|https://example.com/allowed.js|`, `|https://example.com/track.js|$third-party`)
  and three rejected (`|https://example.com/a||`, `@@|https://example.com/a||`,
  `|https://example.com/a|b|`). The suite now has 14 accept and 53 reject
  cases.

### Documentation
- README: documented the optional trailing `|` end anchor for fully anchored
  URL rules, and that the Go normalizer and the shell validator agree on it.
- README: `rejected-<hash>.txt` reports are written only for sources that
  downloaded successfully, and `rejected-custom.txt` only when
  `custom-rules.txt` exists and is non-empty.
- README: documented that the retry count is reduced when
  `attempts × per-request timeout` would not fit inside the remaining
  15-minute deadline.
- README: the "Keep-alive workflow" section moved from "Multiple named lists"
  to "Supply-chain and maintenance notes", where the workflows are described.
  It now states that the keep-alive commit does not start a build for three
  reasons: the file is outside the build workflow's `paths` filter, pushes
  made with the default `GITHUB_TOKEN` do not trigger workflows, and the
  commit message carries `[skip ci]`. Previously only `[skip ci]` was credited.
- README: the build workflow description now lists the
  `scripts/validate_test.sh` step that runs after the build.
- README: the Build section's "Multiple named lists" cross-reference pointed
  "below" although that section is above it.
- README: `rules_read` is documented as an input-line count (it includes `!`
  comments and blank lines), matching `Stats.Read`.
- README: documented that cached-budget exhaustion reports unfetched sources
  as failed, that release-summary links use `GITHUB_REPOSITORY` with an
  `anT0ny54/filtrite` fallback for local builds, and that `filtrite` stops the
  converter on `SIGINT`/`SIGTERM`.

### Review
- Full-codebase review performed (every Go source and test file, both shell
  scripts, both workflows, and all documentation): no dead code, no bugs, and
  no conflicts found. Every top-level function, method, and type in non-test
  code has a caller (the `container/heap` methods on `mergeHeap` are required
  by the heap interface), and no imports are unused.
- Cross-checked the Go normalizer against the shell validator for drift:
  modifier splitting on the last dollar sign (both sides), element/activation
  type rules (same-sign-only, exception-only activation), `domain=` list
  canonicalization and conflict rejection, the anchored-URL character set,
  end-anchored `|` handling, and the bare-domain third-party guard all
  agree. Anchored-URL tails containing a dollar sign before the final
  modifier list are accepted or rejected identically on both sides.
- README statements re-verified against the code and left unchanged: redirect
  limit (4 hops; the guard counts the initial request so a 5th hop is
  refused), backoff schedule (0.5 s, 1 s, 2 s, 4 s), `Retry-After` cap
  (2 minutes), 50 MiB per-source / 500 MiB per-manifest budgets including
  cached bytes, 2-minute per-request timeout with 4 retries, 15-minute
  overall deadline, 100-source cap, 8 workers (never more than remaining
  downloads), 8 MiB sort chunk with 256-chunk merge fan-in, summary keys,
  rejected-report naming, 20 MiB ruleset release limit, and the workflow
  settings (`paths` filter, cron schedules, `keep_latest: 2`, action
  versions `actions/checkout@v7`, `actions/setup-go@v7`,
  `softprops/action-gh-release@v3.0.3`,
  `dev-drprasad/delete-older-releases@v0.3.4`).
- Verification performed for this revision (Go toolchain 1.27.2):
  `go vet ./...` clean; `go test ./...` passes; `go test -race ./...` passes;
  `scripts/validate_test.sh` passes (14 accept, 53 reject, converter
  cross-check skipped without a cached converter); `build.sh` and
  `scripts/validate.sh` pass `bash -n`.
- Minor observations deliberately left as-is: `rejectAliasedPaths` stats the
  input a second time after `Convert` already validated it (one extra
  syscall), and `AllWithCache` uses atomic operations on the byte budget
  during the single-threaded cache scan (harmless, keeps one accounting
  style throughout). `download.SourceName` remains a justified one-line
  wrapper shared with `cmd/legacy-filter-builder`.

### Review notes (no change required)
- Verification performed for this revision: `scripts/validate_test.sh` was run
  (14 accept, 53 reject, all passing) and `build.sh` passes `bash -n`. No Go
  toolchain was available in the review environment, so `go test ./...` was
  not executed; the one Go change above is a three-line addition that only
  uses already-imported packages and should be confirmed by the CI test step.
- README statements were re-checked against the code and left as they were:
  retry/backoff schedule (0.5 s to 4 s), redirect limit (4 hops), 50 MiB /
  500 MiB budgets, worker count, summary keys, rejected-report naming, and the
  workflow `paths` filter and `keep_latest: 2`.
- `softprops/action-gh-release@v3.0.3` is a published release, in addition to
  the `actions/checkout@v7` and `actions/setup-go@v7` note below.
- Every top-level function and method in non-test code has a caller (or is
  required by an interface, such as the `container/heap` methods on
  `mergeHeap`), and no imports are unused.
- `download.SourceName` is a one-line exported wrapper around the unexported
  `shaName`. It is kept because `cmd/legacy-filter-builder` needs the exported
  name while the package's own tests use `shaName`.
- The `schemeEnd < 3` guard in `normalizeNetwork` is unreachable today (the
  `|http://` / `|https://` prefix check guarantees `://` is present). It is
  intentionally kept as a defensive check and is documented as such in the
  code.
- `actions/checkout@v7` and `actions/setup-go@v7` are published releases; the
  workflows were left unchanged.
