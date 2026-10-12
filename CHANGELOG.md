# Changelog

All notable changes to this project are documented in this file.

## [Unreleased]

### Fixed
- `scripts/validate.sh`: cosmetic and scriptlet markers (`#@%?#`, `#?#`,
  `+js(`, `:has-text(`, …) are now matched as literal strings instead of ERE
  regexes, removing implementation-dependent `\?` and lone-`(` escaping.
- `scripts/validate.sh`: the bare-domain `$third-party` guard check now uses
  the same hostname shape as `validDomain` in `internal/filter` (lower-case
  labels, proper label boundaries, at least two labels, not all-numeric).
- Anchored-URL tails are validated in `internal/filter` against the exact
  printable-ASCII subset `scripts/validate.sh` accepts
  (`anchoredURLChars`), and every `%` must start a well-formed two-digit hex
  escape. A rule the Go stage emits can no longer pass the Go parser and
  then fail validation, aborting a release build.
- `build.sh`: the `mktemp` EXIT trap is disarmed after the converter
  download block instead of leaking to the end of the script; the two Go
  helper binaries are compiled in parallel.
- `legacy-filter-builder`: `--sort-chunk-bytes <= 0` is rejected by both
  `run()` and `filter.NewExternalSorter` (the sorter previously coerced it
  silently to the default — a conflicting policy).

### Changed
- `legacy-filter-builder` no longer recreates the empty
  `build/work/<name>/raw/` directory when `--cache-dir` is set; `raw/` only
  exists for hand-run builds without a cache directory.
- `internal/download.Result.Bytes` is now surfaced in the builder's summary
  output (it was previously written but never read).
- The worker job channel in `AllWithCache` is buffered to the exact job
  count; the cached-error scan is skipped when no cache directory is used.
- README: removed off-topic sections (Free DNS Services, Bandwidth Hero
  Server, donations).

### Removed
- Dead `.gitattributes` rules for `*.py`, `*.ts`, `*.tsx`, `*.js`, `*.jsx`,
  and `*.json` — no such files exist in this repository.
- Duplicated URL-hashing logic in `cmd/legacy-filter-builder` (`shortHash`);
  it now reuses the `SourceName` helper exported by `internal/download`.
- Stale test comment referencing a removed package-level Optimize API.

### Notes
- `||host/path` patterns may contain a literal `#` (e.g.
  `||example.com/page#frag`). Chromium's legacy rule parser treats it as a
  literal pattern character, so it is converter-safe; such rules are
  preserved, not rewritten.
- `build.sh`, `scripts/validate.sh`, and `scripts/validate_test.sh` are
  committed with the executable bit; the CI `chmod` workaround was removed.

## Current release behavior (as built by `./build.sh`)

- Builds every `lists/<name>.txt` manifest independently into
  `filters/<name>.txt` and `dist/<name>.dat`, layering non-empty
  `custom-rules.txt` onto every list.
- Downloads share a per-build source cache (`build/source-cache/`): 50 MiB
  per-source / 500 MiB per-manifest budget, at most 100 sources, 8 workers,
  4 retries with exponential backoff plus `Retry-After`, a 4-hop redirect
  limit, and refused HTTPS downgrades and credential-bearing redirects.
  Source failures are release-fatal unless `--allow-partial` is passed.
- Rules are validated/canonicalized against the legacy Chromium
  `subresource_filter` syntax subset; unsupported lines are written to
  `build/work/<name>/rejected-*.txt` reports (`# line<TAB>reason<TAB>rule`
  header, one report per source URL by short URL hash, plus
  `rejected-custom.txt`).
- Accepted rules stream through an external merge sort (8 MiB default
  in-memory chunk, tunable via `--sort-chunk-bytes`); duplicates collapse
  during the merge. Bare `||host^` rules gain the `$third-party` guard at
  sorter ingestion.
- `scripts/validate.sh` sanity-checks every generated list before `filtrite`
  runs Chromium's `ruleset_converter`
  (`--input_format=filter-list --output_format=unindexed-ruleset`); a
  `dist/<name>.dat` over 20 MiB (override with `MAX_RULESET_BYTES`) prints a
  warning.
- `.github/workflows/build.yml` runs daily at 03:17 UTC, is serialized, and
  keeps the latest 2 releases; `Keep-Alive.yml` rewrites
  `.github/keep-alive.txt` on the 1st and 15th of each month so the schedule
  is not disabled after 60 days of inactivity.
