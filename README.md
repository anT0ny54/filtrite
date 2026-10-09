# ⚡ Filtrite

Filtrite builds a **legacy Chromium `subresource_filter`** network-filter list and converts it to an unindexed Chromium ruleset for browsers/builds that still use that engine.

> **Important:** modern Cromite uses a modified Adblock Plus engine for its normal ad blocker. The legacy Bromite engine is a separate, older `subresource_filter` path and is disabled by default in Cromite. This repository intentionally targets the legacy path only.

## What the build produces

The build has two stages, run once per source manifest (see "Multiple named lists" below):

1. `legacy-filter-builder` downloads a manifest's source lists, validates each line, canonicalizes supported network rules, rejects unsupported syntax, and streams accepted rules through an external sort/merge so the complete rule set is not held in RAM. A shared per-build source cache avoids downloading the same URL again for another manifest.
2. `filtrite` passes the sorted legacy-compatible list to Chromium's `ruleset_converter` with `--input_format=filter-list --output_format=unindexed-ruleset`, producing `dist/<name>.dat`.

Chromium documents this `ruleset_converter` flow for development/testing of custom legacy `subresource_filter` rulesets.

With only the default `lists/adblock.txt` present, `<name>` is `adblock`, so the build produces `filters/adblock.txt` as an intermediate text artifact and `dist/adblock.dat` as the release artifact. `dist/adblock.dat`'s path is unchanged from earlier versions of this project.

At the start of every build, `build.sh` deletes `filters/`, `dist/`, `build/work/`, and `build/source-cache/` and truncates `build/release-summary.md`; the compiled helper binaries in `build/` and the cached `deps/ruleset_converter` are kept (the binaries are simply rebuilt). The release workflow publishes the matching `dist/<name>.dat` files and no redundant root-level `filters.txt` copy.

## 🧩 Legacy filter syntax policy

The generated `filters/<name>.txt` files are deliberately **not** the output of a general-purpose uBlock Origin, AdGuard, or modern Adblock Plus filter compiler. Every source rule must fit the smaller legacy syntax accepted by Chromium's `subresource_filter` rule parser. Unsupported rules are rejected rather than rewritten into an approximation.

### Accepted

- `||host^` and `||host/path...` network rules.
- `@@` network exceptions using the same supported network syntax.
- Fully anchored `|http://...` and `|https://...` network rules, including `@@` exceptions.
- Hosts-file entries using `0.0.0.0`, `127.0.0.1`, or `::1` followed by **one ASCII space and exactly one hostname**. Tabs, multiple separators, and trailing fields/comments are rejected.
- These rule modifiers, matching exactly what Chromium's legacy `rule_parser` accepts ([`rule_parser.cc`](https://chromium.googlesource.com/chromium/src/+/1d0244ea5b870edebab547606aa19d4594ce1de5/components/subresource_filter/tools/rule_parser/rule_parser.cc)):
  - `$third-party` / `$~third-party`
  - `$match-case`
  - `$domain=example.com|~excluded.example`
  - Resource-type (`ElementType`) filters — `script`, `image`, `stylesheet`, `object`, `xmlhttprequest`, `object-subrequest`, `subdocument`, `ping`, `media`, `font`, `websocket`, `other` — each optionally negated with `~` (e.g. `$script,image` or `$~image,~stylesheet`). `popup` is rejected because the legacy indexed engine strips popup element types. **A single rule must use one sign only**: Chromium's parser decides whether the unspecified types start out included or excluded based on the *first* type's sign, so mixing `script,~image` in one rule is order-dependent and is rejected rather than guessed at.
  - Whitelist-only (`ActivationType`) filters — `document`, `genericblock` — only on `@@` exception rules, never negated (e.g. `@@||example.com^$document`). CSS-related `elemhide` and `generichide` are rejected because the legacy indexed engine removes those activation bits. These activation filters can't be combined with resource-type filters in the same rule.

Hostnames are ASCII-only and canonicalized to lower case. IP literals, single-label hosts, underscores, ports, and IDN/non-ASCII hosts are rejected; URL paths retain their original case. Modifier duplicates and conflicts are rejected rather than resolved implicitly. Domain lists are validated and canonicalized into Chromium's deterministic ordering — longest domain first, then lexicographically within equal-length groups. Because Chromium's parser stores first/third-party state, case sensitivity, resource type, activation type, and initiator-domain constraints as distinct rule metadata, none of these scopes are collapsed together.

### Rejected

Cosmetic selectors, scriptlets/procedural syntax, regex filters, metadata records, malformed hosts, malformed modifiers, non-ASCII/whitespace-bearing rules, and any other network syntax outside the supported subset. This includes `$` options Chromium's own rule parser marks as not implemented for this legacy engine — `$sitekey`, `$collapse`, `$donottrack` — plus deprecated element-type aliases; these are rejected the same way Chromium's parser itself would reject them, not silently dropped or approximated.

This is intentional: a rule is either safely representable in the target engine or it is rejected. Exception-prefixed hosts-file records (e.g. `@@0.0.0.0 host.example`) are rejected as unsupported exception rules rather than silently converted into block rules.

### Third-party guard

Bare domain blocks such as `||example.com^` are emitted as `||example.com^$third-party`.

This is not an arbitrary optimization. Chromium's own filter-list generation script applies the same transformation to prevent an unconditional domain rule from also matching a top-level navigation to that domain. The builder applies the guard when streaming rules into the external sorter (`ExternalSorter.Add`), and `scripts/validate.sh` enforces that generated bare `||host^` rules are never emitted without `$third-party`.

Other metadata-scoped rules are **not** removed merely because a broader host rule exists. For example, `$domain=...` and `$~third-party` can have different matching scope and therefore cannot safely be treated as redundant.

## Rejected-rule reports

Every build writes `build/work/<name>/rejected-*.txt` reports for each list `<name>` (see "Multiple named lists" below). Each report contains the original source line number, rejection reason, and sanitized original rule so unsupported syntax is auditable rather than silently discarded.

Comment lines starting with `!` and blank lines are skipped without being reported. Lines starting with `#` (hosts-file comments) are not treated as comments and show up as rejections. A single source line longer than 2 MiB aborts the build with a scan error rather than being skipped.

Each report starts with a `# line<TAB>reason<TAB>rule` header. Report files are named `rejected-<hash>.txt` — one per source URL, where `<hash>` is the first 12 hex characters of the SHA-256 of that URL — plus `rejected-custom.txt` for `custom-rules.txt`.

## 🌐 Source lists

Every file matching `lists/*.txt` is an independent source manifest; the default is [`lists/adblock.txt`](lists/adblock.txt). Each manifest is a simple URL-per-line file; blank lines and `#` comments are ignored.

Source URLs must be valid **HTTPS URLs without embedded credentials**, with no leading/trailing whitespace. Invalid entries fail the build with a line-numbered error instead of being silently skipped. Duplicate URLs (compared after lower-casing the host) are collapsed into one.

The downloader uses a **2-minute per-request timeout** and up to **4 retries** (5 attempts per source) with exponential backoff of **0.5 s, 1 s, 2 s, and 4 s** (`Retry-After` is honored and capped at 2 minutes). All downloads for one list share an overall **15-minute deadline** hardcoded in `cmd/legacy-filter-builder`; a source still failing after its attempts is release-fatal unless `--allow-partial` is set, while the deadline expiring is always fatal. It retries transient HTTP failures (408, 429, 500, 502, 503, 504; other statuses such as 404 or 501 are not retried), request/network timeouts, unexpected EOF/connection closure, and `ECONNRESET`; caller cancellation and the overall deadline are not retried. Each source is capped at 50 MiB and each manifest at 500 MiB, including cached bytes. At most 100 sources are accepted and up to 8 workers are used (never more than the number of sources still to download). Redirects are limited to 4 hops (the redirect guard counts the initial request, so a 5th hop is refused), HTTPS→HTTP downgrades and credential-bearing redirects are refused, obvious HTML error pages are rejected, and source result order is preserved for deterministic reporting.

The production builder uses an external merge sort with an 8 MiB default in-memory chunk size. That trades some temporary disk I/O for substantially lower peak RAM when large filter collections are processed.

All configured source downloads are release-critical by default. If any source fails, `legacy-filter-builder` refuses to publish a partial ruleset. This prevents a transient mirror failure from silently reducing a release. For an explicitly intentional partial build, pass `--allow-partial`; cancellation and timeout remain fatal.

When multiple manifests contain the same source URL, a shared cache directory can reuse the already downloaded file so the URL is fetched only once during that build. Source-cache entries never expire automatically; `build.sh` removes the cache at the start of each build.

## 🧾 Multiple named lists

`build.sh` builds **every** manifest under `lists/*.txt`, independently, into its own `filters/<name>.txt` intermediate and `dist/<name>.dat` release artifact (`<name>` is the manifest's filename without `.txt`). `custom-rules.txt` is layered onto every list the same way, while identical source URLs are reused from the shared per-build download cache. `custom-rules.txt` is only layered on when it exists **and is non-empty**: when the file is missing, `build.sh` passes an empty `--custom ""` (which disables custom rules), and the builder skips a 0-byte file.

To add another list (e.g. a smaller or region-specific one), drop a new manifest next to the default one:

```sh
lists/adblock.txt   -> filters/adblock.txt , dist/adblock.dat   (default, shipped with the repo)
lists/german.txt    -> filters/german.txt  , dist/german.dat
lists/minimal.txt   -> filters/minimal.txt , dist/minimal.dat
```

Each manifest is otherwise identical in format to `lists/adblock.txt`: one HTTPS URL per line, `#` comments and blank lines ignored. This mirrors the one-file-per-list convention used by the original [xarantolus/filtrite](https://github.com/xarantolus/filtrite) project.

### Keep-alive workflow

GitHub disables scheduled workflows after 60 days without repository activity, which would silently stop the daily 03:17 UTC build described under "Supply-chain and maintenance notes". To prevent that, `.github/workflows/Keep-Alive.yml` ("Keep Fork Alive") runs at 03:17 UTC on the **1st and 15th of each month** (and on manual dispatch). It rewrites `.github/keep-alive.txt` with a UTC timestamp and pushes a `chore: update repository keep-alive [skip ci]` commit to the branch it ran on; the `[skip ci]` marker keeps these commits from triggering extra builds.

The workflow needs the `contents: write` permission (declared in the workflow itself) so it can push that commit. Runs are serialized (`cancel-in-progress: false`) and time out after 5 minutes. If you fork this repository and your organization restricts the default `GITHUB_TOKEN` to read-only, allow workflow write access under *Settings -> Actions -> General -> Workflow permissions*, or the keep-alive push will fail and scheduled builds can still be disabled after 60 days of inactivity.

## 🛠️ Build

Requirements:

- Go 1.27.1+ for this source tree.
- Bash 4+, `awk`, and `mktemp`.
- `curl` and `unzip` only when `deps/ruleset_converter` is absent.
- `sha256sum` only when `CONVERTER_SHA256` is set.
- A Linux-compatible `ruleset_converter` binary is downloaded automatically on the first `build.sh` run and cached in `deps/`.

Run:

```sh
./build.sh
```

Outputs (per list `<name>`, see "Multiple named lists" below):

```text
build/legacy-filter-builder
build/filtrite
deps/ruleset_converter            (downloaded once, then cached)
filters/<name>.txt
build/source-cache/                 (shared downloaded source snapshots, one file per URL)
build/work/<name>/raw/              (created empty; snapshots live in build/source-cache/ whenever --cache-dir is set)
build/work/<name>/build-summary.env
build/work/<name>/rejected-*.txt
build/work/<name>/ruleset-converter.log
dist/<name>.dat
build/release-summary.md
```

`build.sh` always passes `--cache-dir build/source-cache`, so downloads are written there and `build/work/<name>/raw/` stays empty. Only when `legacy-filter-builder` is run by hand without `--cache-dir` are the snapshots written into `raw/`. The sorter's temporary `build/work/<name>/sort/` directory is removed when the builder exits.

With only the default `lists/adblock.txt`, that's `filters/adblock.txt`, `build/work/adblock/rejected-*.txt`, `build/work/adblock/ruleset-converter.log`, and `dist/adblock.dat`.

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

`build.sh` downloads the prebuilt `ruleset_converter` (Chromium `subresource_filter_tools`) from the rolling latest-release archive below and caches it as `deps/ruleset_converter`; delete that file to force a refresh:

```text
https://github.com/xarantolus/subresource_filter_tools/releases/latest/download/subresource_filter_tools_linux-x64.zip
```

Override the location with `CONVERTER_URL=<https url>`, and pin the archive with `CONVERTER_SHA256=<hex>`; when set, a checksum mismatch aborts the build. Without a pin, each fresh download uses whatever is currently published, so reproducible builds require preserving a known converter binary separately.

`build.sh` checks every generated `dist/<name>.dat` against a **20 MiB default limit** and prints a warning (without failing the build) when it is exceeded. Override the ceiling with `MAX_RULESET_BYTES=<bytes>`.

`filtrite` (the converter wrapper) takes `--input`, `--output`, `--converter`, `--log`, and `--timeout` (default 5 minutes). `legacy-filter-builder` takes `--sources`, `--custom`, `--output`, `--build-dir`, `--cache-dir`, `--summary`, `--allow-partial`, and `--sort-chunk-bytes`. The summary is a key=value file containing `sources_configured`, `sources_succeeded`, and `sources_cached`; `build.sh` consumes only the first two, while `sources_cached` is available to other tooling.

The legacy-filter builder uses an **8 MiB default in-memory sort chunk**, measured as the bytes of rule text (plus one newline per rule); Go string headers and slice overhead are not counted, so real memory use is somewhat higher. For larger-memory environments, increase it with `--sort-chunk-bytes <bytes>` to reduce temporary chunk-file I/O; the default is retained for low-memory CI/build hosts.

## ✅ Validation

Run the text-level sanity check on a generated list with:

```sh
./scripts/validate.sh filters/adblock.txt
```

`build.sh` runs this on every generated list before invoking the converter. The validator checks whitespace, cosmetic/scriptlet/regex syntax, the supported modifier subset, hostname shape, and the required `$third-party` guard on bare `||host^` rules. It does not fully emulate Chromium's parser; the final authority remains Chromium's `ruleset_converter`, which is executed by `build.sh` after the text builder completes.

The shell validator is deliberately a line-local sanity check rather than a second copy of the Go parser (`internal/filter/filter.go` remains the single source of normalization logic). Regression tests keep it honest:

```sh
./scripts/validate_test.sh
```

This feeds known-good and known-malformed generated rules through `validate.sh`. When `deps/ruleset_converter` exists (after `./build.sh`), every rule the validator accepts is also run through the real converter, so the validator cannot drift into accepting rules Chromium rejects. The CI workflow runs it after the build.

Unit tests:

```sh
go test ./...
```

The download tests cover cancellation and deadline handling for fully cached manifests, and concurrent/failure-path accounting of the shared byte budget (`go test -race ./internal/download/` is recommended when touching that code).

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

The build workflow (`.github/workflows/build.yml`) runs `go test ./...` before building (Go module caching is disabled because the module has no dependencies and no `go.sum`) and validates every generated list. It runs daily at 03:17 UTC, on manual dispatch, and on pushes to `main` that touch `lists/*.txt`, `custom-rules.txt`, `cmd/**`, `internal/**`, `build.sh`, `scripts/**`, `go.mod`, or the workflow itself. Runs are serialized (`cancel-in-progress: false`) so a release in progress is never cancelled. Each run publishes every `dist/*.dat` as a new timestamp-tagged release (body from `build/release-summary.md`) and keeps only the latest 2 releases. Source-download failures are release-fatal unless a caller explicitly uses the builder's `--allow-partial` option outside the release workflow.

## 🌐 Free DNS Services

High-performance DNS utilizing HaGeZi Blocklists (Multi Pro + TIF).

| Blocklist | DNS-over-HTTPS (DoH) |
| :--- | :--- |
| Multi Pro + TIF | `https://freedns.koyeb.app/dns-query` (Recommended) |
| Multi Pro + TIF | `https://dns.mydoh.workers.dev/dns-query` (Recommended) |
| Multi Pro + TIF | `https://dns-pi.vercel.app/api/doh/dns-query` (Recommended) |
| Multi Pro + TIF | `https://dnssix.netlify.app/api/doh/dns-query` |
| Multi Pro + TIF | `https://dns-93aca.containers.snapdeploy.app/dns-query` |
| Multi Pro + TIF | `https://doh-93aca.containers.snapdeploy.app/dns-query` |

## ⚡ Bandwidth Hero Server

A lightweight image optimization proxy designed to slash bandwidth usage and accelerate web browsing.

Bandwidth Hero Server fetches remote images, compresses them on the fly, and delivers optimized versions to the client. This significantly reduces data consumption while improving page load performance.

🖥️ **Live Demo:** [Bandwidth Hero](https://bhserv.netlify.app/).

## Supporting the Project

If you find this project useful, donations are appreciated:

- **Bitcoin**: `1HntwKxyqGCfnSGvGLMUTRAqLnTvLarAQP`

## License

See [`LICENSE`](LICENSE).
