## [Unreleased]

### Latest review and maintenance update
- Re-read all 36 files in the original project combined with the supplied second-, fifth-, and sixth-review patches before evaluating behavior. No other confirmed dead or redundant Go application code was found; no speculative cleanup was made.
- Fixed converter-log publication in `internal/ruleset.Convert`. The wrapper now snapshots the temporary log file's identity when it creates it and, after the converter exits, uses `Lstat`, a regular-file check, and `os.SameFile` to reject a temporary path replaced with a symlink, special object, or hard link to an unrelated file. This prevents a converter from substituting an external file for the captured log before the wrapper publishes it.
- Added regression coverage for both symlink and hard-link replacement of the temporary log. The tests confirm the external target remains unchanged and neither the log nor output is published after replacement. The test uses Linux procfs to locate the inherited stdout descriptor and skips on systems without `/proc/self/fd`.
- Updated `README.md` to describe the converter-log identity check and its regression coverage. The existing release-retention fix and per-attempt downloader deadline checks from the supplied patches remain intact.

### Verification
- `bash -n build.sh scripts/*.sh` passed.
- `bash scripts/prune_releases_test.sh` passed; `bash scripts/build_preflight_test.sh` passed all 34 cases; `bash scripts/validate_test.sh` passed (the optional converter cross-check was skipped because `deps/ruleset_converter` is not present).
- Both GitHub Actions workflow YAML files parsed successfully; all Go files pass `gofmt`.
- `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...` passed in an isolated compatibility copy using the installed Go 1.23.2 toolchain; only that copy's `go.mod` directive was adjusted. The submitted project still requires Go 1.27.4.
- Three-second fuzz smoke runs passed for `FuzzNormalize` (28,342 executions) and `FuzzURLsFromFile` (11,618 executions).
- Native verification with Go 1.27.4 could not run because the toolchain was not installed and network access prevented downloading it. The live GitHub release API/delete operations were not run against the user's repository; release cleanup was verified offline with a fake GitHub CLI and the installed `jq`.
