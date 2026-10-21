# Changelog

This file records only the latest modified patch. No version is bumped here.

## [Unreleased]

### Changed (optimization)
- `cmd/legacy-filter-builder`: removed the duplicate path-collision preflight that ran before the source manifest was read. The single remaining preflight (after the manifest and custom rules are known) performs every check the removed one did, including the per-source rejected-report paths, so behavior is unchanged with less redundant path resolution, stat, and pairwise comparison work per build.
- `internal/download`: the oversize-`Content-Length` fast path in `getOnce` now drains a bounded amount of the response body, mirroring the non-2xx status path so keep-alive connections can return to the transport pool instead of being closed.

### Documentation
- README reviewed against the current sources (retry/backoff, redirect hop limit, worker/source/byte caps, keep-alive schedule, validator contract); all statements verified accurate, and the two changes above are internal-only, so no README edits were required.

### Verification
- Full-source review of all 41 files; diffs reviewed against every caller and test (`TestRunRejects*` preflight tests, `TestGetReportsActualAttemptsAndStatusOnce`, budget/robustness suites) for behavioral equivalence.
- Not re-executed in this environment: `go test ./...`, `go vet ./...`, `go test -race ./internal/download/`, and `bash scripts/validate_test.sh` should be run by CI to confirm.
