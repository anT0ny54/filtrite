## [Unreleased]

### Added
- Fuzz regression targets: `FuzzNormalize` (`internal/filter/fuzz_test.go`)
  asserts that accepted rules are non-empty, reason-free, and idempotent
  under `normalize`; `FuzzURLsFromFile` (`internal/download/fuzz_test.go`)
  asserts accepted manifests never contain duplicate URLs after canonical
  (scheme/host lower-cased) deduplication. Run with
  `go test -fuzz=FuzzNormalize -fuzztime=60s ./internal/filter/` and
  `go test -fuzz=FuzzURLsFromFile -fuzztime=60s ./internal/download/`.
- `.golangci.yml`: opt-in stricter static analysis (govet, staticcheck,
  ineffassign, unused, misspell, gofmt) for local linting and future CI
  adoption; the existing workflow still runs `go vet` only.

### Changed
- `internal/ruleset`: replaced the bare `defer os.Remove(tmpPath)` with the
  explicit `defer func() { _ = os.Remove(tmpPath) }()` form used everywhere
  else in the codebase, so errcheck-style linters stay clean. Behavior is
  unchanged.
- `internal/download`: removed the redundant `attempts` accumulator in `get()`;
  a loop that only reaches its final statement after running to completion
  always executed exactly `retries+1` attempts, so the counter duplicated the
  loop bound. The error text and count are unchanged.
- `internal/download`: `AllWithCache` now constructs its HTTP client/transport
  only when at least one source actually needs downloading; all-cached builds
  no longer allocate a throwaway transport.
- `build.sh`: the managed-file hard-link preflight no longer hard-codes the
  index of the cached-converter entry (`(( i < 3 ))`); the bound is derived
  from the managed-file list length, so extending or reordering the list
  cannot exempt the wrong file from the link check. Behavior is unchanged.

### Fixed
- `CHANGELOG.md`: merged the two duplicate `### Changed` sections under
  `[Unreleased]` into a single section and removed a stray double blank
  line. Documentation-only fix; no code or build behavior changed.
- No functional bugs were found in this pass. Path-alias preflight ordering,
  the download byte-budget reserve/settle/refund protocol, retry
  classification, the normalizer/validator rule-subset sync, and the
  workflow/README documentation were re-verified line by line against the
  implementation and left untouched.

### Verification
- 100 % of the repository was read before patching: all Go source (every
  `cmd/` and `internal/` package, including all `*_test.go` files),
  `build.sh`, all `scripts/*.sh`, both GitHub workflows, `README.md`,
  `CHANGELOG.md`, `go.mod`, `lists/adblock.txt`, and `custom-rules.txt`.
- `bash -n` passes for `build.sh` and every `scripts/*.sh`, and both shell
  regression suites pass in the review environment:
  `scripts/build_preflight_test.sh` (19 cases) and
  `scripts/validate_test.sh` (fixture pass; the converter cross-check was
  skipped because no cached `deps/ruleset_converter` exists). `go vet ./...`
  / `go test ./...` were not run (no Go toolchain in the review
  environment). The only edit is documentation, and the modified file list
  is exactly `CHANGELOG.md`.
