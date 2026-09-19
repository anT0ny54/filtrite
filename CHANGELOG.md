# Changelog

All notable changes to this project are documented here.

## [Unreleased]

### Changed
- Replaced the pinned third-party converter archive workflow with Cromite's rolling latest `ruleset_converter` binary URL. `build.sh` no longer uses `CONVERTER_LOCK`, `CONVERTER_SHA256`, a converter tag, or a converter checksum lock file; each build retrieves the converter currently published by Cromite.

### Fixed
- Fixed the exact `MaxTotalBytes` boundary case: a source whose final permitted byte is also the global budget boundary now terminates cleanly at EOF instead of being reported as oversized.
- Source-download failures are release-fatal by default, preventing accidental publication of a partially populated ruleset. Intentional partial builds remain available with `--allow-partial`; cancellation and timeouts are always fatal.
- Rulesets over the configured `MAX_RULESET_BYTES` ceiling now fail the build instead of only emitting a warning.
- Expanded the GitHub Actions manifest trigger from only `lists/adblock.txt` to every `lists/*.txt` file and changed release publishing to include every `dist/*.dat` artifact.
- Build output is now cleaned at the start of every run, removing stale `build/`, `filters/`, `dist/`, and compatibility `filters.txt` artifacts before generating new results.
- Rejected `popup`, `elemhide`, and `generichide` modifiers that are stripped by the legacy indexed engine; `genericblock` remains supported.
- Preserved executable mode for `build.sh` and `scripts/validate.sh` in the repository/archive packaging.

### Improved
- Replaced the production builder's process-wide rule slice and global deduplication map with an external sort/merge pipeline. Rules are normalized into bounded chunks, sorted on disk, and merged/deduplicated without retaining the full ruleset in RAM.
- Added a shared per-build source cache so the same URL referenced by multiple manifests is downloaded once and reused for later manifests.
- Added regression tests for the exact download-budget EOF boundary, over-budget detection, cache reuse, and multi-chunk external sorting.
- Kept `ReadFile`/`ReadFileWithSeen` available for small in-memory callers/tests while moving the release build path to the streaming sink API.
- Added generated `filters/` and `filters.txt` to `.gitignore` and removed the redundant root-level release copy.
- Updated README and CI documentation to reflect multi-manifest publishing, release-fatal source failures, external sorting, source caching, and hard output-size enforcement.

### Resource considerations
- Production sorting uses an 8 MiB default chunk budget (`filter.DefaultSortChunkBytes`), leaving substantially more RAM available than the previous full-rule in-memory representation.
- The shared source cache is scoped to a build's generated `build/source-cache` directory and is cleared with the rest of the generated build area at the start of each run, avoiding stale source-list reuse across builds.

### CI / maintenance notes
- The release workflow now watches all `lists/*.txt` manifests and publishes all matching `dist/*.dat` files.
- The generated text filters remain useful as auditable intermediate artifacts, but they are no longer duplicated to a root-level `filters.txt` release asset.
