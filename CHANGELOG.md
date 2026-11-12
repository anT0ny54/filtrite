## [Unreleased]

### Latest review and maintenance update
- Re-reviewed the merged 34-file project from `filtrite-main.zip` plus `filtrite-re-review-patch.zip`; every file was read in full before selecting changes.
- Simplified converter URL percent-escape validation to a single pattern scan, avoiding repeated suffix copies while rejecting malformed and incomplete escapes before build cleanup.
- Added regression cases for one-digit and non-hex percent escapes. Valid percent-encoded path bytes remain accepted.
- Updated `README.md` to specify the exact percent-escape rule. No further dead code, redundant code, correctness defects, conflicts, or safe optimization opportunities were confirmed; no speculative Go changes were made.

### Verification
- `bash -n` passed for `build.sh` and all shell scripts.
- `bash scripts/build_preflight_test.sh` passed all 34 regression cases; the reported count is now derived from the fixture setup so it cannot silently drift.
- `bash scripts/validate_test.sh` passed all validator fixtures; the optional real-converter cross-check was skipped because `deps/ruleset_converter` is unavailable.
- `gofmt` reported no unformatted Go files.
- Go tests, race-enabled Go tests, and `go vet` passed in an isolated compatibility copy using the installed Go 1.23.2 toolchain; only that copy's `go.mod` was adjusted. The submitted project still requires Go 1.27.4, which is unavailable in this environment.
- Native Go 1.27.4 verification could not run because the required toolchain is not installed and toolchain download access is unavailable. No fuzz-test result is claimed for this review.
