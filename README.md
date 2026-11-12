# ⚡ Filtrite

Filtrite builds a **legacy Chromium `subresource_filter`** network-filter list and converts it to an unindexed Chromium ruleset for browsers/builds that still use that engine.

> **Important:** modern Cromite uses a modified Adblock Plus engine for its normal ad blocker. The legacy Bromite engine is a separate, older `subresource_filter` path and is disabled by default in Cromite. This repository intentionally targets the legacy path only.

## What the build produces

The build has two stages, run once per source manifest (see "Multiple named lists" below):

1. `legacy-filter-builder` downloads a manifest's source lists, validates each line, canonicalizes supported network rules, rejects unsupported syntax, and streams accepted rules through an external sort/merge so the complete rule set is not held in RAM. A shared per-build source cache avoids downloading the same URL again for another manifest.
2. `filtrite` passes the sorted legacy-compatible list to Chromium's `ruleset_converter` with `--input_format=filter-list --output_format=unindexed-ruleset`, producing `dist/<name>.dat`.

Chromium documents this `ruleset_converter` flow for development/testing of custom legacy `subresource_filter` rulesets.

With only the default `lists/adblock.txt` present, `<name>` is `adblock`, so the build produces `filters/adblock.txt` as an intermediate text artifact and `dist/adblock.dat` as the release artifact. `dist/adblock.dat`'s path is unchanged from earlier versions of this project.

After its preflight succeeds, `build.sh` deletes `filters/`, `dist/`, `build/work/`, and `build/source-cache/` and truncates `build/release-summary.md`; the compiled helper binaries in `build/` and the cached `deps/ruleset_converter` are kept (the binaries are simply rebuilt). The preflight rejects symlinked/non-directory managed output paths and prevents source manifests or custom rules from resolving into directories that will be cleared or aliasing build files that will be overwritten. The release workflow publishes the matching `dist/<name>.dat` files and no redundant root-level `filters.txt` copy. The per-list download links written to `build/release-summary.md` use `$GITHUB_REPOSITORY` (set automatically in GitHub Actions) and fall back to `anT0ny54/filtrite` for local runs, so set `GITHUB_REPOSITORY=<owner>/<repo>` when building a fork locally.

## 🧩 Legacy filter syntax policy

The generated `filters/<name>.txt` files are deliberately **not** the output of a general-purpose uBlock Origin, AdGuard, or modern Adblock Plus filter compiler. Every source rule must fit the smaller legacy syntax accepted by Chromium's `subresource_filter` rule parser. Unsupported rules are rejected rather than rewritten into an approximation.

### Accepted

- `||host^` and `||host/path...` network rules. One trailing `|` may be used as an end anchor; internal or repeated pipes are rejected.
- `@@` network exceptions using the same supported network syntax.
- Fully anchored `|http://...` and `|https://...` network rules, including `@@` exceptions. A single trailing `|` (end-of-URL anchor, e.g. `|https://example.com/ads.js|`) is accepted; the URL tail may only use the conservative character set described under "Validation". A literal dollar sign in the URL tail must be percent-encoded as `%24`, because `$` is reserved as the modifier delimiter.
- Hosts-file entries using `0.0.0.0`, `127.0.0.1`, or `::1` followed by **one ASCII space and exactly one hostname**. Tabs, multiple separators, and trailing fields/comments are rejected.
- These rule modifiers, matching exactly what Chromium's legacy `rule_parser` accepts ([`rule_parser.cc`](https://chromium.googlesource.com/chromium/src/+/1d0244ea5b870edebab547606aa19d4594ce1de5/components/subresource_filter/tools/rule_parser/rule_parser.cc)):
  - `$third-party` / `$~third-party`
  - `$match-case`
  - `$domain=example.com|~excluded.example`
  - Resource-type (`ElementType`) filters — `script`, `image`, `stylesheet`, `object`, `xmlhttprequest`, `object-subrequest`, `subdocument`, `ping`, `media`, `font`, `websocket`, `other` — each optionally negated with `~` (e.g. `$script,image` or `$~image,~stylesheet`). `popup` is rejected because the legacy indexed engine strips popup element types. **A single rule must use one sign only**: Chromium's parser decides whether the unspecified types start out included or excluded based on the *first* type's sign, so mixing `script,~image` in one rule is order-dependent and is rejected rather than guessed at.
  - Whitelist-only (`ActivationType`) filters — `document`, `genericblock` — only on `@@` exception rules, never negated (e.g. `@@||example.com^$document`). CSS-related `elemhide` and `generichide` are rejected because the legacy indexed engine removes those activation bits. These activation filters can't be combined with resource-type filters in the same rule.

Hostnames are ASCII-only and canonicalized to lower case. IP literals, single-label hosts, underscores, ports, and IDN/non-ASCII hosts are rejected; URL paths retain their original case. Modifier duplicates and conflicts are rejected rather than resolved implicitly. A rule may contain only one `$` modifier delimiter; additional delimiters are rejected instead of being preserved inside the URL pattern. Domain lists are validated and canonicalized into Chromium's deterministic ordering — longest domain first, then lexicographically by bare domain string within equal-length groups (the `~` exclude marker is not part of the sort key, matching Chromium's `DomainListParser::Canonicalize`); a domain that appears twice in one `$domain=` list (as a duplicate or as an include/exclude conflict) rejects the rule. Because Chromium's parser stores first/third-party state, case sensitivity, resource type, activation type, and initiator-domain constraints as distinct rule metadata, none of these scopes are collapsed together.

### Rejected

Cosmetic selectors, scriptlets/procedural syntax, regex filters, metadata records, malformed hosts, malformed modifiers, non-ASCII/whitespace-bearing rules, and any other network syntax outside the supported subset. This includes `$` options Chromium's own rule parser marks as not implemented for this legacy engine — `$sitekey`, `$collapse`, `$donottrack` — plus deprecated element-type aliases; these are rejected the same way Chromium's parser itself would reject them, not silently dropped or approximated.

This is intentional: a rule is either safely representable in the target engine or it is rejected. Exception-prefixed hosts-file records (e.g. `@@0.0.0.0 host.example`) are rejected as unsupported exception rules rather than silently converted into block rules.

### Third-party guard

Bare domain blocks such as `||example.com^` are emitted as `||example.com^$third-party`.

This is not an arbitrary optimization. Chromium's own filter-list generation script applies the same transformation to prevent an unconditional domain rule from also matching a top-level navigation to that domain. The builder applies the guard when streaming rules into the external sorter (`ExternalSorter.Add`), and `scripts/validate.sh` enforces that generated bare `||host^` rules are never emitted without `$third-party`.

Other metadata-scoped rules are **not** removed merely because a broader host rule exists. For example, `$domain=...` and `$~third-party` can have different matching scope and therefore cannot safely be treated as redundant.

## Rejected-rule reports

Every build writes `build/work/<name>/rejected-*.txt` reports for each list `<name>` (see "Multiple named lists" below). Each report contains the original source line number, rejection reason, and sanitized original rule so unsupported syntax is auditable rather than silently discarded.

Comment lines starting with `!` and blank lines are skipped without being reported. Lines starting with `#` (hosts-file comments) are not treated as comments and show up as rejections. A single source line with more than 2 MiB of content (excluding its line ending) aborts the build with a scan error rather than being skipped; a line exactly at the limit is accepted.

Each report starts with a `# line<TAB>reason<TAB>rule` header. Reports are written to a temporary file in the destination directory and atomically renamed into place, so an existing hard link at the report path cannot cause an unrelated file to be truncated. A partially generated report is still committed on a scan or sink error when it can be flushed and closed; a failure to write, flush, close, or replace a report file fails the build. Rejected source text is sanitized: malformed UTF-8 is replaced, and non-printing characters (including terminal controls, bidirectional formatting, and zero-width formatting) become spaces. Report files are named `rejected-<hash>.txt` — one per successfully downloaded source URL, where `<hash>` is the first 12 hex characters of the SHA-256 of that URL (the writer and the path-collision preflight share one helper, so the two cannot disagree on the name) — plus `rejected-custom.txt` for `custom-rules.txt`, which is only written when that file exists and is non-empty.

## 🌐 Source lists

Every file matching `lists/*.txt` is an independent source manifest; the default is [`lists/adblock.txt`](lists/adblock.txt). Each manifest is a simple URL-per-line file; a UTF-8 BOM is accepted only at the very beginning, and blank lines and `#` comments are ignored.

Source URLs must be valid **HTTPS URLs without embedded credentials**, with no leading/trailing whitespace or literal `#` fragment delimiter. Explicit ports must be in the range 1–65535, and a source manifest must be a regular file rather than a pipe or directory. Fragments are not sent in HTTP requests; encode a literal hash as `%23`. Invalid entries fail the build with a line-numbered error instead of being silently skipped. Duplicate URLs are collapsed after canonicalizing the case-insensitive scheme and host.

The downloader uses a **2-minute per-request timeout** and up to **4 retries** (5 attempts per source) with exponential backoff of **0.5 s, 1 s, 2 s, and 4 s** (`Retry-After` is honored and capped at 2 minutes). All downloads for one list share an overall **15-minute deadline** hardcoded in `cmd/legacy-filter-builder`; the number of attempts per source is reduced if needed so that worst-case attempt time (attempts × the per-request timeout) fits inside the remaining deadline. A source still failing after its attempts is release-fatal unless `--allow-partial` is set, while the deadline expiring is always fatal. It retries transient HTTP failures (408, 429, 500, 502, 503, 504; other statuses such as 404 or 501 are not retried), request/network timeouts, unexpected EOF/connection closure, and `ECONNRESET`; caller cancellation and the overall deadline are not retried. Each source is capped at 50 MiB and each manifest at 500 MiB, including cached bytes; concurrent readers reserve the shared budget before reading and wait for in-flight reservations to settle before declaring exhaustion. Bytes from failed or rejected attempts are refunded, including bytes already read when a local destination write fails partway through. A response reader that returns `(0, nil)` 100 times consecutively is treated as stalled (using `io.ErrNoProgress`) even when budget remains; any delivered bytes or read error resets that counter. If cached sources alone exhaust the manifest budget, the sources that were never fetched are reported as failed (so `--allow-partial` cannot count them as successes). At most 100 sources are accepted (an empty or oversized list is rejected before any download or cache directory is created) and up to 8 workers are used (never more than the number of sources still to download). Redirects are limited to 4 hops (the redirect guard counts the initial request, so a 5th hop is refused), HTTPS→HTTP downgrades and credential-bearing redirects are refused, HTML error pages are rejected (a response whose body begins, after an optional BOM and surrounding whitespace, with `<!doctype html`, `<html`, `<head`, or `<body` is treated as an error, compared case-insensitively), and source result order is preserved for deterministic reporting.

The production builder uses an external merge sort with an 8 MiB default in-memory chunk size. That trades some temporary disk I/O for substantially lower peak RAM when large filter collections are processed.

All configured source downloads are release-critical by default. If any source fails, `legacy-filter-builder` refuses to publish a partial ruleset. This prevents a transient mirror failure from silently reducing a release. For an explicitly intentional partial build, pass `--allow-partial`; cancellation and timeout remain fatal.

When multiple manifests contain the same source URL, a shared cache directory can reuse the already downloaded file so the URL is fetched only once during that build. Source-cache entries must be regular files; symlink and other invalid entries are discarded rather than followed, then fetched again. Cache entries never expire automatically; `build.sh` removes the cache at the start of each build.

## 🧾 Multiple named lists

`build.sh` builds **every** manifest under `lists/*.txt`, independently, into its own `filters/<name>.txt` intermediate and `dist/<name>.dat` release artifact (`<name>` is the manifest's filename without `.txt`). `custom-rules.txt` is layered onto every list the same way, while identical source URLs are reused from the shared per-build download cache. `custom-rules.txt` is only layered on when it exists **and is non-empty**: when the file is missing, `build.sh` passes an empty `--custom ""` (which disables custom rules), and the builder skips a 0-byte file.

To add another list (e.g. a smaller or region-specific one), drop a new manifest next to the default one:

```sh
lists/adblock.txt   -> filters/adblock.txt , dist/adblock.dat   (default, shipped with the repo)
lists/german.txt    -> filters/german.txt  , dist/german.dat
lists/minimal.txt   -> filters/minimal.txt , dist/minimal.dat
```

Each manifest is otherwise identical in format to `lists/adblock.txt`: one HTTPS URL per line, `#` comments and blank lines ignored. This mirrors the one-file-per-list convention used by the original [xarantolus/filtrite](https://github.com/xarantolus/filtrite) project.

## 🛠️ Build

Requirements:

- Stable Go 1.27.4+ for this source tree; `build.sh` validates the complete installed `goX.Y.Z` version before cleanup and does not rely on an automatic toolchain download succeeding mid-build.
- Bash 4+, `awk`, and GNU coreutils (`realpath`, `stat`, plus standard file utilities used by the script).
- `mktemp`, `curl`, and `unzip` only when the cached `deps/ruleset_converter` is missing or needs to be refreshed.
- `sha256sum` only when `CONVERTER_SHA256` is set.
- A Linux-compatible `ruleset_converter` binary is downloaded automatically on the first `build.sh` run and cached in `deps/`. A cached converter must be a non-empty executable file; invalid directories and broken/non-executable converter symlinks are rejected.

Run:

```sh
./build.sh
```

Outputs (per list `<name>`, see "Multiple named lists" above):

```text
build/legacy-filter-builder
build/filtrite
deps/ruleset_converter            (downloaded once, then cached)
filters/<name>.txt
build/source-cache/                 (shared downloaded source snapshots, one file per URL)
build/work/<name>/raw/              (only when the builder runs without --cache-dir)
build/work/<name>/build-summary.env
build/work/<name>/rejected-*.txt
build/work/<name>/ruleset-converter.log
dist/<name>.dat
build/release-summary.md
```

`build.sh` always passes `--cache-dir build/source-cache`, so downloads are written there and `build/work/<name>/raw/` is never created. Only when `legacy-filter-builder` is run by hand without `--cache-dir` is `raw/` created and the snapshots written into it. The sorter's temporary `build/work/<name>/sort/` directory is removed when the builder exits.

With only the default `lists/adblock.txt`, that's `filters/adblock.txt`, `build/work/adblock/rejected-*.txt`, `build/work/adblock/ruleset-converter.log`, and `dist/adblock.dat`.

`--custom` defaults to `custom-rules.txt`, which must exist; a missing or mistyped path fails the direct builder run rather than silently dropping custom rules. Pass `--custom ""` to disable custom rules. `build.sh` does this automatically only when `custom-rules.txt` is truly absent. When the path exists, it must resolve to a readable regular file; a directory, unreadable file, or dangling symlink is an error. Every `lists/*.txt` manifest must also be a readable regular file. Before cleanup, `build.sh` resolves both kinds of input and rejects symlinks into `filters/`, `dist/`, `build/work/`, or `build/source-cache/`, as well as aliases (including hard links) to the helper binaries, release summary, or converter path that the build may overwrite. It rejects hard-linked managed output files and collisions between managed output paths so truncating one output cannot corrupt another file. The `deps/` directory may point to an external shared cache, but neither its resolved root nor a cached converter symlink target may be inside a directory cleared by the build. It also validates the stable Go version, output-directory/file types, `MAX_RULESET_BYTES`, and any converter-download/checksum prerequisites before deleting old artifacts.

For direct builder use, partial source failures are disabled by default. Use `--allow-partial` only when an intentionally incomplete build is acceptable:

```sh
./build/legacy-filter-builder \
  --sources lists/adblock.txt \
  --custom custom-rules.txt \
  --output filters/adblock.txt \
  --build-dir build/work/adblock \
  --cache-dir build/source-cache \
  --allow-partial
```

`build.sh` does not pass `--allow-partial`, so release builds remain complete-or-fail.

`build.sh` downloads the prebuilt `ruleset_converter` (Chromium `subresource_filter_tools`) from the rolling latest-release archive below and caches it as `deps/ruleset_converter`; both the initial request and every redirect are restricted to HTTPS. Delete the binary to force a refresh:

```text
https://github.com/xarantolus/subresource_filter_tools/releases/latest/download/subresource_filter_tools_linux-x64.zip
```

Override the location with `CONVERTER_URL=<https url>` — the host is validated before cleanup as a DNS name (dot-separated labels of 1–63 alphanumeric characters with optional interior hyphens and a 253-character total limit, or a single label such as `localhost`), a strict dotted-quad IPv4 literal when the host is entirely numeric (exactly four decimal octets, each 0–255, with no leading zeros or trailing dot), or a bracketed IPv6 literal (including an optional final strict IPv4 dotted-quad tail, with at most one `::` compression and valid group/octet counts); an optional port must be 1–65535. A malformed URL therefore fails before any prior output is deleted. Pin the archive with `CONVERTER_SHA256=<hex>`; when set, a checksum mismatch aborts the build. Without a pin, each fresh download uses whatever is currently published, so reproducible builds require preserving a known converter binary separately.

`filtrite` itself fails on a missing or empty converter output, so `build.sh` does not re-check for an empty file. `build.sh` checks every generated `dist/<name>.dat` against a **20 MiB default release limit** and fails the build when it is exceeded: the oversized artifact is deleted and the script exits with a nonzero status, so an over-limit ruleset can never be published. Override the ceiling with `MAX_RULESET_BYTES=<bytes>` when intentionally shipping a larger ruleset.

`filtrite` (the converter wrapper) takes `--input`, `--output`, `--converter`, `--log`, and `--timeout` (default 5 minutes; must be positive, otherwise it exits with an error instead of expiring immediately); it also stops the converter on `SIGINT`/`SIGTERM`. An already-canceled conversion exits before touching the log or output. The wrapper resolves the executable before touching the log or output and rejects path aliases among the input, converter, output, and log so a misconfigured path cannot truncate the source or replace the converter binary. The output and log leaves themselves must not be symlinks and, when they exist, must be regular files. The wrapper writes logs to a same-directory temporary file and atomically replaces the requested path after the converter exits and the log has been finalized successfully, so a hard-linked external file is not truncated and a failed log finalization leaves the previous log intact. After conversion, it uses a non-following file-type check and compares the temporary output's file identity with the file initially created by the wrapper before changing permissions or publishing it; a converter that replaces that path with a symlink, special file, or hard link to an unrelated file is rejected. A converter failure still publishes its captured log when log finalization succeeds, preserving diagnostics. Unaliased dangling or external symlink targets are rejected before output or temporary files are created. `legacy-filter-builder` takes `--sources`, `--custom`, `--output`, `--build-dir`, `--cache-dir`, `--summary`, `--allow-partial`, and `--sort-chunk-bytes`. The summary is a key=value file containing `sources_configured`, `sources_succeeded`, `sources_cached`, and the rule-effectiveness counters `rules_read`, `rules_rejected`, `rules_duplicates`, and `rules_output` (`rules_read` counts every source line, including `!` comments and blank lines, so it is an input-line count rather than a rule count); an old summary is removed after source and path preflight succeeds, and a new one is written only after success. The builder rejects collisions between input files and generated output, summary, rejected-report, and managed staging/cache paths. Collision checks resolve existing symlinked path components in filesystem order—including `..` components that follow a symlink—and handle dangling symlinks to future paths before creating the work directory or cleaning scratch paths. This prevents lexical path cleaning from hiding an alias to an input or generated file. Generated output/report files and builder-owned sort/raw directories cannot be symlinks and must have the expected file/directory type; the explicitly configured shared cache directory may be a symlink to a directory. The sort scratch directory is cleared before sorting starts so each run has an isolated scratch tree, without leftover files from an interrupted build. Chunk files are also created with exclusive, unpredictable temporary names rather than truncating predictable paths, so a stale symlink or hard link in a directly reused sorter directory cannot redirect writes outside the workspace. `build.sh` consumes only the first two source keys, while the rest are available to other tooling.

The legacy-filter builder uses an **8 MiB default in-memory sort chunk**, measured as the bytes of rule text (plus one newline per rule); Go string headers and slice overhead are not counted, so real memory use is somewhat higher. Flushed rule strings are cleared from the chunk buffer so completed batches do not remain referenced in memory. A single rule larger than a configured chunk is written immediately as a standalone chunk. For larger-memory environments, increase the limit with `--sort-chunk-bytes <bytes>` to reduce temporary chunk-file I/O; the default is retained for low-memory CI/build hosts. For unusually large rule sets the merge runs with a bounded fan-in (256 chunk files per pass); inputs above that merge in multi-pass rounds so peak file-descriptor usage stays constant, with deduplication still performed only in the final pass.

## ✅ Validation

Run the text-level sanity check on a generated list with:

```sh
./scripts/validate.sh filters/adblock.txt
```

`build.sh` runs this on every generated list before invoking the converter. The validator checks whitespace, cosmetic/scriptlet/regex syntax, the supported modifier subset, hostname label boundaries and length limits (matching the Go normalizer), and the required `$third-party` guard on bare `||host^` rules. It does not fully emulate Chromium's parser; the final authority remains Chromium's `ruleset_converter`, which is executed by `build.sh` after the text builder completes.

The anchored-URL character set the validator enforces is the same conservative printable-ASCII subset (`anchoredURLChars` in `internal/filter/filter.go`) the Go normalizer applies, so the two stages cannot drift apart on which anchored rules are legal. `$` is deliberately excluded from the raw URL tail because it separates modifiers; encode a literal dollar as `%24`. Both stages treat exactly one trailing `|` as the end-of-URL anchor (not part of the URL); a second trailing `|` or a `|` anywhere inside the URL is rejected.

The shell validator is deliberately a line-local sanity check rather than a second copy of the Go parser (`internal/filter/filter.go` remains the single source of normalization logic). It rejects unterminated `||host` rules and only reads a readable, non-empty regular file. Regression tests keep it honest:

```sh
./scripts/validate_test.sh
```

This feeds known-good and known-malformed generated rules through `validate.sh`. When `deps/ruleset_converter` exists (after `./build.sh`), every rule the validator accepts is also run through the real converter, so the validator cannot drift into accepting rules Chromium rejects. The CI workflow runs it after the build.

`scripts/build_preflight_test.sh` covers the path-safety preflight that `build.sh` runs before it deletes any previous output: symlink, hard-link, and alias rejection, plus converter URL (including IPv6 structure and DNS length), checksum, size-limit, and Go-version validation. It uses a fake `go` binary, so it needs neither network access nor a Go 1.27.4 toolchain:

```sh
./scripts/build_preflight_test.sh
```

Static analysis (`go vet ./...`) alongside the unit tests:

```sh
go vet ./...
go test ./...
```

Longer fuzzing runs are available for the two most security-relevant parsers;
use them when touching normalization or source-manifest parsing:

```sh
go test -fuzz=FuzzNormalize -fuzztime=60s ./internal/filter/
go test -fuzz=FuzzURLsFromFile -fuzztime=60s ./internal/download/
```

`FuzzNormalize` checks that every accepted rule is non-empty, carries no
rejection reason, and is idempotent (re-normalizing an emitted rule yields the
identical text). `FuzzURLsFromFile` checks that accepted manifests never
contain duplicate URLs after canonical deduplication.

The download tests cover cancellation and deadline handling for fully cached manifests, concurrent/failure-path accounting of the shared byte budget, and partial destination writes. In particular, a failed copy refunds the bytes actually consumed by the response reader, not merely the smaller number of bytes the destination reports writing; this avoids leaking manifest budget when a local write fails partway through. Tests also cover a reader temporarily reserving all remaining bytes and repeated empty reads (`go test -race ./internal/download/` is recommended when touching that code).

Generated directories (`build/`, `deps/`, `dist/`, `filters/`) are listed in `.gitignore`, so they are not committed by accident. See [`CHANGELOG.md`](CHANGELOG.md) for notable changes.

## 📦 Bromite / legacy-engine usage

Bromite documents that its legacy ad-blocking implementation uses Chromium's `subresource_filter` and that its legacy distribution uses an unindexed filter file.

For Cromite, use this output only when the specific build/configuration you are running exposes or consumes the legacy engine. Cromite's current normal ad blocker is a modified Adblock Plus implementation, so this repository should not be described as a drop-in compiler for Cromite's modern engine.

## Create a custom build

1. Fork the repository if you want your own independently maintained build.
2. Edit `lists/adblock.txt` to add/remove source URLs, and/or add another `lists/<name>.txt` manifest for a separate named list.
3. Edit `custom-rules.txt` for local legacy-compatible network rules; it's applied to every list when non-empty (an empty file is skipped).
4. Run the test suite (`go test ./...`).
5. Run `./build.sh`.
6. Publish the generated `dist/<name>.dat` assets from your release process.

Keep custom rules within the supported syntax policy above. A rule that is useful in uBlock Origin may still be invalid for this legacy engine.

## 🔐 Supply-chain and maintenance notes

Third-party filter sources keep their own licenses and terms. Do not assume that the MIT license covering this repository also licenses the downloaded filter content.

The build workflow (`.github/workflows/build.yml`) runs `go test ./...`, `go vet ./...`, and `scripts/build_preflight_test.sh` before building (Go module caching is disabled because the module has no dependencies and no `go.sum`), runs `build.sh` (which validates every generated list), and then runs `scripts/validate_test.sh` against the freshly downloaded converter. It runs daily at 03:17 UTC, on manual dispatch, and on pushes to `main` that touch `lists/*.txt`, `custom-rules.txt`, `cmd/**`, `internal/**`, `build.sh`, `scripts/**`, `go.mod`, or the workflow itself. Runs are serialized (`cancel-in-progress: false`) so a release in progress is never cancelled. Each run publishes every `dist/*.dat` as a new timestamp-tagged release (body from `build/release-summary.md`) and keeps only the latest 2 releases. Source-download failures are release-fatal unless a caller explicitly uses the builder's `--allow-partial` option outside the release workflow.

### Keep-alive workflow

GitHub disables scheduled workflows after 60 days without repository activity, which would silently stop the daily 03:17 UTC build described above. To prevent that, `.github/workflows/Keep-Alive.yml` ("Keep Fork Alive") runs at 03:17 UTC on the **1st and 15th of each month** (and on manual dispatch). It rewrites `.github/keep-alive.txt` with a UTC timestamp and pushes a `chore: update repository keep-alive [skip ci]` commit to the branch it ran on. That file is not in the build workflow's `paths` filter, and pushes made with the default `GITHUB_TOKEN` do not start new workflow runs anyway, so these commits do not trigger extra builds (the `[skip ci]` marker is an additional safeguard).

The workflow needs the `contents: write` permission (declared in the workflow itself) so it can push that commit. Runs are serialized (`cancel-in-progress: false`) and time out after 5 minutes. If you fork this repository and your organization restricts the default `GITHUB_TOKEN` to read-only, allow workflow write access under *Settings -> Actions -> General -> Workflow permissions*, or the keep-alive push will fail and scheduled builds can still be disabled after 60 days of inactivity.

## 🔗 Related projects (not part of this repository)

The services below are maintained separately and are **not** part of this repository, its build pipeline, or its release cycle. They are listed here for convenience only.

### 🌐 Free DNS Services

High-performance DNS utilizing HaGeZi Blocklists (Multi Pro + TIF).

| Blocklist | DNS-over-HTTPS (DoH) |
| :--- | :--- |
| Multi Pro + TIF | `https://freedns.koyeb.app/dns-query` (Recommended) |
| Multi Pro + TIF | `https://dns.mydoh.workers.dev/dns-query` (Recommended) |
| Multi Pro + TIF | `https://dns-pi.vercel.app/api/doh/dns-query` (Recommended) |
| Multi Pro + TIF | `https://dnssix.netlify.app/api/doh/dns-query` |
| Multi Pro + TIF | `https://dns-93aca.containers.snapdeploy.app/dns-query` |
| Multi Pro + TIF | `https://doh-93aca.containers.snapdeploy.app/dns-query` |

### ⚡ Bandwidth Hero Server

A lightweight image optimization proxy designed to slash bandwidth usage and accelerate web browsing.

Bandwidth Hero Server fetches remote images, compresses them on the fly, and delivers optimized versions to the client. This significantly reduces data consumption while improving page load performance.

🖥️ **Live Demo:** [Bandwidth Hero](https://bhserv.netlify.app/).

## Supporting the Project

If you find this project useful, donations are appreciated:

- **Bitcoin**: `1HntwKxyqGCfnSGvGLMUTRAqLnTvLarAQP`

## License

See [`LICENSE`](LICENSE).
