## [Unreleased]

Modifications relative to the original upload. No version is bumped.

### Changed
- `internal/filter` (`ReadFileToSink`): each source line is inspected in the
  scanner's byte buffer. Comment and blank lines, which dominate real lists,
  are skipped without allocating; a string is created only for lines that are
  normalized. The rejected-report line number comes from the single physical
  line counter instead of a duplicate counter. Accepted rules, stats, and
  report contents are unchanged. On a comment-heavy 200k-line benchmark,
  allocations fell from 220,011 to 80,011 per run (about 64 %) and bytes
  allocated fell by about 77 %.
- `internal/download` (`htmlError`): the error-page check compares the first
  bytes of the response directly instead of lower-casing a copy of the 4 KiB
  buffer for every download and cache hit. Results are identical; a
  differential check over 200,000 random inputs found no difference.
- `scripts/build_preflight_test.sh`: removed an unused fixture directory.
- `.github/workflows/Keep-Alive.yml`: removed a redundant `mkdir -p .github`;
  the directory already exists after checkout.
- `internal/filter.ReadFileToSink`: write rejection reports to a temporary file
  beside the destination and atomically replace the report after flushing and
  closing it. This prevents an existing hard link from truncating an unrelated
  file, while retaining a usable partial report after a scan or sink failure.
- `internal/ruleset.Convert`: write converter output to a same-directory
  temporary log and atomically replace the requested log path after the process
  exits. Existing log paths must be regular files; symlinks and other file types
  are rejected before conversion.
- `internal/download.htmlError`: skip a file-start BOM and leading Unicode
  whitespace incrementally, so HTML error pages are still recognized when
  padded by more than the former 4 KiB prefix limit without buffering the body.

### Added
- `internal/filter/readfile_test.go`: regression test that mixes a BOM,
  comments, indented comments, blank lines, and CRLF endings, and checks that
  rejected-report entries carry the physical source line number.
- `internal/download/htmlerror_test.go`: table test for HTML error-page
  detection (BOM, whitespace, case, non-breaking space, more than 4 KiB of
  leading whitespace, truncated and mid-text tags, and ordinary filter content).
- `internal/filter/readfile_test.go` and `internal/ruleset/ruleset_test.go`: hard-link
  regression tests proving that report and log replacement leave the linked
  external file untouched; the ruleset tests also cover non-regular log paths.

### Fixed
- `README.md`: documented `scripts/build_preflight_test.sh`, which CI runs but
  the README did not describe, and corrected the CI step order to include it
  before `build.sh`.
- `README.md`: the HTML error-page rule now states exactly which prefixes are
  matched and how they are normalized.
- `CHANGELOG.md`: an earlier note said an `attempts` accumulator was removed
  from `internal/download` `get()`. The counter is still present and is
  required, because it reports the attempts actually issued after a
  non-retryable failure. Corrected; the code is unchanged.

### Verification
- Every Go file, both workflows, `build.sh`, all `scripts/*.sh`, the source
  manifest, and the docs were re-read before editing. Files not modified in
  this pass were confirmed identical to the original upload.
- `go vet ./...`, `go test ./...`, and `go test -race` on the download and
  filter packages pass. `gofmt -l` reports nothing. Both shell regression
  suites pass (`validate_test.sh`, and `build_preflight_test.sh` with 19 cases).
- Both fuzz targets (`FuzzNormalize`, `FuzzURLsFromFile`) ran for 60 seconds
  with no failures.
- A full `build.sh` run was checked end to end against a local TLS source
  server and a stand-in converter, including a file-start BOM.
- Verification ran with Go 1.22.2 and the `go` directive temporarily lowered in
  a scratch copy, because the project's Go 1.27.1 toolchain was unavailable.
  The delivered `go.mod` is unchanged. `golangci-lint` and `staticcheck` were
  not run.

### Latest review verification
- Re-applied the ten-file prior patch to the original archive and audited the
  complete merged project. All 34 project files (including every Go test,
  workflow, manifest, shell script, and documentation file) were read in full.
- Fixed converter-log publication so a temporary log is renamed into place
  only after permission-setting and close both succeed. A failed finalization
  now preserves the previous log and removes the incomplete temporary file.
- Updated `README.md` to document the log-finalization behavior. The existing
  Unreleased entry is retained; no release number or `go.mod` version changed.
- `go test ./...`, `go test -race ./...`, `go vet ./...`, and `gofmt` checks pass
  in a scratch copy with its Go directive set to the installed Go 1.23.2.
  The project declares Go 1.27.1, which is not installed in this environment;
  the delivered `go.mod` remains unchanged.
- Both shell regression suites pass, and `bash -n` passes for all shell
  scripts. The validator's optional converter cross-check was skipped because
  `deps/ruleset_converter` is absent. A full `build.sh` run was not performed.
- `golangci-lint`, `staticcheck`, and `shellcheck` are not installed, so those
  optional checks could not be run.
- The converter wrapper now rejects an output leaf that is a symlink or
  non-regular file before creating temporary files; regression tests verify
  that existing symlink targets and directories remain untouched.
- The Go normalizer and shell validator now consistently reject a raw `$` in
  fully anchored URL tails; literal dollar signs must use `%24`, since `$`
  delimits rule modifiers.
- Removed the redundant reader slice from the external sort merge; active heap
  entries already retain their readers. Added a 257-chunk regression test for
  multi-pass merging, sorted output, and deduplication across merge groups.
- Re-ran Go tests (including the race detector), `go vet`, formatting, shell
  syntax checks, and both shell regression suites successfully in the Go
  1.23.2 scratch copy. The project's Go 1.27.1 directive and next version were
  not changed.
- `internal/ruleset.Convert` now uses `Lstat` to require the converter's
  temporary output to remain a regular file before permission changes and
  publication. This prevents following a symlink substituted by the external
  converter; a regression test verifies that its target content and mode stay
  unchanged and no symlink is published as the final ruleset.
- The shell validator now matches the Go normalizer for unterminated `||host`
  rules and accepts only readable, non-empty regular input files. Regression
  fixtures cover the missing terminator and FIFO inputs.
- Raised the minimum Go toolchain directive and build preflight to Go 1.27.4,
  the current patch release as of 2026-11-12, so builds use the security fixes
  in the Go toolchain and `net/http`. This is a toolchain requirement update,
  not an application release/version bump.
- Third-pass checks: Go tests, race tests, vet, formatting, shell syntax, both
  shell regression suites, and filter/download fuzz smoke runs pass. The local
  environment only has Go 1.23.2, so Go tests ran in a scratch copy with its
  Go directive lowered; the delivered `go.mod` retains Go 1.27.4. The optional
  converter cross-check remains unavailable because its binary is absent.
- Fourth-pass review: re-applied the 13-file cumulative patch to the original
  archive and re-read all 34 files in the merged project before judging it.
  The downloader's `budgetReader` previously bounded repeated `(0, nil)` reads
  only after the shared byte budget reached zero; with budget still available,
  a non-progressing response reader could make `io.Copy` spin indefinitely.
  It now returns `io.ErrNoProgress` after 100 consecutive empty reads, with
  the counter reset by delivered bytes or any read error. Added regression
  tests for the positive-budget stall case, budget preservation, and counter
  reset after progress. No additional dead code or safe removable redundancy
  was found in this pass.
- Fourth-pass verification: `go test ./...`, `go test -race ./...`,
  `go vet ./...`, `gofmt`, `bash -n` for all shell scripts, and both shell
  regression suites pass. Verification used Go 1.23.2 in a scratch copy with
  only its `go` directive lowered; the delivered `go.mod` remains at Go
  1.27.4. Short fuzz smoke tests also pass. The optional Chromium converter
  cross-check was skipped because `deps/ruleset_converter` is not present.
  No application version was bumped.
- Fifth-pass review: re-applied the 15-file v4 patch to a fresh extraction of
  the original archive and re-read every file in the 34-file merged project.
  `internal/ruleset.Convert` previously checked that converter output was a
  regular file but did not verify it was the same file created before the
  converter ran; a converter could replace the path with a hard link and cause
  the wrapper to change permissions on an unrelated file. The wrapper now
  compares the final file identity with the original temporary file before
  chmod or publication. Added a regression test that verifies a hard-linked
  target's contents and permissions stay unchanged and no final output is
  published.
- Tightened `build.sh` converter URL preflight: bracketed IPv6 hosts are now
  structurally validated (single `::` compression, correct group count, and
  valid optional IPv4 tail), and DNS hostnames are capped at 253 characters.
  These errors now fail before prior build artifacts are deleted. Added seven
  preflight cases for malformed IPv6 forms, an invalid IPv4 tail, an oversized
  DNS name, valid IPv6 and IPv4-mapped IPv6 hosts, and the inclusive
  253-character DNS boundary. Updated the README to document these checks and
  the converter output identity guard.
- Fifth-pass verification: `go test ./...`, `go test -race ./...`,
  `go vet ./...`, formatting, shell syntax checks, both shell regression
  suites, and short fuzz runs pass in a scratch copy with the Go directive
  lowered only for the locally installed Go 1.23.2 toolchain. The delivered
  `go.mod` remains at Go 1.27.4; no application version was bumped. The
  optional Chromium converter cross-check remains unavailable because
  `deps/ruleset_converter` is not present.
- Sixth-pass review: `ExternalSorter.flushChunk` previously used `os.Create` on
  predictable `.chunk-000000.txt`-style paths. Reusing a scratch directory with
  a stale symlink or hard link at that name could follow or truncate an
  unrelated file. Chunk creation now uses `os.CreateTemp` (exclusive creation
  with a unique name), and regression tests verify that pre-existing symlink
  and hard-link targets remain unchanged. Updated the README with this safety
  guarantee; no application version or Go toolchain requirement changed.
- Seventh-pass review: re-applied the 15-file v6 patch to the original archive
  and read all 34 files in the merged project before making changes. Numeric
  dotted converter hosts are URL-parser IPv4 literals, not DNS names; the
  preflight now requires a valid strict dotted quad, preventing impossible
  addresses such as `999.999.999.999` from passing validation and failing only
  after cleanup. Updated the DNS length-boundary fixtures to use alphabetic
  labels so they exercise DNS limits rather than IPv4 parsing. Kept the
  validator's `end_anchor` variable because the unterminated-host guard uses
  it; removing it would weaken validation. No additional safely removable
  dead code or other correctness issue was found.
- Seventh-pass verification: all 28 build-preflight cases, validator
  regression fixtures, shell syntax checks, Go tests, race tests, vet, build,
  formatting, and short filter/download fuzz runs pass in a scratch copy with
  the Go directive lowered only for the installed Go 1.23.2 toolchain. The
  delivered `go.mod` remains at Go 1.27.4. The optional converter cross-check
  was skipped because `deps/ruleset_converter` is unavailable. No application
  version was bumped.
- Eighth-pass review: re-applied the 15-file v7 patch to a fresh extraction of
  the original archive and read all 34 files in the merged project. The
  converter URL validator's IPv4-tail check relied on Bash `read -a` splitting,
  which discards a trailing empty field; consequently an invalid IPv4-mapped
  IPv6 host such as `[::ffff:192.0.2.1.]` could pass host validation and reach
  `curl`. The validator now checks the complete strict dotted-quad shape before
  splitting and then checks each octet's numeric range. Added a regression
  case for the trailing-dot form, and updated README's IPv4/IPv6 description.
  No other safely removable dead code or justified optimization was identified.
- Ninth-pass review: re-applied the 15-file v8 patch to a fresh extraction of
  the original archive and read the complete 34-file merged project. In
  `internal/download.getOnce`, failed-attempt budget refunds used `io.Copy`'s
  returned byte count, which measures bytes written rather than bytes already
  read. A partial destination write could therefore leave discarded response
  bytes charged against the shared manifest budget and incorrectly exhaust
  later downloads. `budgetReader` now tracks bytes actually returned by its
  underlying reader, and failed attempts refund that amount. Added a
  regression for a short destination write and confirmed the full reservation
  is restored. Updated the README's download-budget and test-coverage notes.
- Ninth-pass verification: Go unit tests, race tests, vet, build, formatting,
  the short-write refund regression (100 runs), shell syntax, all 29 build
  preflight cases, validator fixtures, and short filter/download fuzz runs
  pass using the installed Go 1.23.2 toolchain with a temporary alternate
  module file. The delivered `go.mod` remains at Go 1.27.4; no application
  version was bumped. The optional converter cross-check remains unavailable
  because `deps/ruleset_converter` is not present.
- Tenth-pass review: re-applied the 15-file v9 patch to a fresh extraction of
  the original archive and read all 34 merged project files, including the
  complete Go sources and tests, workflows, scripts, manifests, README, and
  changelog. Re-verified the downloader short-write budget refund: the
  implementation tracks bytes consumed by `budgetReader` and refunds that
  amount when a failed attempt is discarded, rather than relying on
  `io.Copy`'s smaller destination-write count. The existing regression covers
  the partial-write case. No additional dead code, safe removals, patch
  conflicts, or justified behavioral optimizations were identified.
- Tenth-pass verification: Go tests, race tests, vet, build, formatting, shell
  syntax, all 29 build-preflight cases, validator regressions, and short
  filter/download fuzz runs pass. Go checks used the installed Go 1.23.2
  toolchain in a scratch copy with only its `go` directive lowered; the
  delivered `go.mod` remains at Go 1.27.4. The optional converter cross-check
  is unavailable because `deps/ruleset_converter` is absent. No application
  version or toolchain requirement was changed.
