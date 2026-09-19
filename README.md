# ⚡ Filtrite

Filtrite builds a **legacy Chromium `subresource_filter`** network-filter list and converts it to an unindexed Chromium ruleset for browsers/builds that still use that engine.

> **Important:** modern Cromite uses a modified Adblock Plus engine for its normal ad blocker. The legacy Bromite engine is a separate, older `subresource_filter` path and is disabled by default in Cromite. This repository intentionally targets the legacy path only.

## What the build produces

The build has two stages, run once per source manifest (see "Multiple named lists" below):

1. `legacy-filter-builder` downloads a manifest's source lists, validates each line, canonicalizes supported network rules, rejects unsupported syntax, and streams accepted rules through an external sort/merge so the complete rule set is not held in RAM. A shared per-build source cache avoids downloading the same URL again for another manifest.
2. `filtrite` passes the sorted legacy-compatible list to Chromium's `ruleset_converter` with `--input_format=filter-list --output_format=unindexed-ruleset`, producing `dist/<name>.dat`.

Chromium documents this `ruleset_converter` flow for development/testing of custom legacy `subresource_filter` rulesets.

With only the default `lists/adblock.txt` present, `<name>` is `adblock`, so the build produces `filters/adblock.txt` as an intermediate text artifact and `dist/adblock.dat` as the release artifact. `dist/adblock.dat`'s path is unchanged from earlier versions of this project.

Generated `filters/`, `dist/`, and `build/` content is cleaned at the start of every build. The release workflow publishes the matching `dist/<name>.dat` files and no redundant root-level `filters.txt` copy.

## 🧩 Legacy filter syntax policy

The generated `filters/<name>.txt` files are deliberately **not** the output of a general-purpose uBlock Origin, AdGuard, or modern Adblock Plus filter compiler. Every source rule must fit the smaller legacy syntax accepted by Chromium's `subresource_filter` rule parser. Unsupported rules are rejected rather than rewritten into an approximation.

### Accepted

- `||host^` and `||host/path...` network rules.
- `@@` network exceptions using the same supported network syntax.
- Fully anchored `|http://...` and `|https://...` network rules, including `@@` exceptions.
- Hosts-file entries using `0.0.0.0`, `127.0.0.1`, or `::1` followed by exactly one hostname.
- These rule modifiers, matching exactly what Chromium's legacy `rule_parser` accepts ([`rule_parser.cc`](https://chromium.googlesource.com/chromium/src/+/1d0244ea5b870edebab547606aa19d4594ce1de5/components/subresource_filter/tools/rule_parser/rule_parser.cc)):
  - `$third-party` / `$~third-party`
  - `$match-case`
  - `$domain=example.com|~excluded.example`
  - Resource-type (`ElementType`) filters — `script`, `image`, `stylesheet`, `object`, `xmlhttprequest`, `object-subrequest`, `subdocument`, `ping`, `media`, `font`, `websocket`, `other` — each optionally negated with `~` (e.g. `$script,image` or `$~image,~stylesheet`). `popup` is rejected because the legacy indexed engine strips popup element types. **A single rule must use one sign only**: Chromium's parser decides whether the unspecified types start out included or excluded based on the *first* type's sign, so mixing `script,~image` in one rule is order-dependent and is rejected rather than guessed at.
  - Whitelist-only (`ActivationType`) filters — `document`, `genericblock` — only on `@@` exception rules, never negated (e.g. `@@||example.com^$document`). CSS-related `elemhide` and `generichide` are rejected because the legacy indexed engine removes those activation bits. These activation filters can't be combined with resource-type filters in the same rule.

Modifier duplicates and conflicts are rejected rather than resolved implicitly. Domain lists are validated and canonicalized into Chromium's deterministic ordering — longest domain first, then lexicographically within equal-length groups. Because Chromium's parser stores first/third-party state, case sensitivity, resource type, activation type, and initiator-domain constraints as distinct rule metadata, none of these scopes are collapsed together.

### Rejected

Cosmetic selectors, scriptlets/procedural syntax, regex filters, metadata records, malformed hosts, malformed modifiers, non-ASCII/whitespace-bearing rules, and any other network syntax outside the supported subset. This includes `$` options Chromium's own rule parser marks as not implemented for this legacy engine — `$sitekey`, `$collapse`, `$donottrack` — plus deprecated element-type aliases; these are rejected the same way Chromium's parser itself would reject them, not silently dropped or approximated.

This is intentional: a rule is either safely representable in the target engine or it is rejected.

### Third-party guard

Bare domain blocks such as `||example.com^` are emitted as `||example.com^$third-party`.

This is not an arbitrary optimization. Chromium's own filter-list generation script applies the same transformation to prevent an unconditional domain rule from also matching a top-level navigation to that domain.

Other metadata-scoped rules are **not** removed merely because a broader host rule exists. For example, `$domain=...` and `$~third-party` can have different matching scope and therefore cannot safely be treated as redundant.

## Rejected-rule reports

Every build writes `build/work/<name>/rejected-*.txt` reports for each list `<name>` (see "Multiple named lists" below). Each report contains the original source line number, rejection reason, and sanitized original rule so unsupported syntax is auditable rather than silently discarded.

## 🌐 Source lists

Every file matching `lists/*.txt` is an independent source manifest; the default is [`lists/adblock.txt`](lists/adblock.txt). Each manifest is a simple URL-per-line file; blank lines and `#` comments are ignored.

Source URLs must be valid **HTTPS URLs without embedded credentials**. Invalid entries fail the build with a line-numbered error instead of being silently skipped.

The downloader also enforces per-source and combined download-size limits, follows a small bounded number of redirects, refuses HTTPS→HTTP downgrade redirects, rejects obvious HTML error pages, and preserves source result order for deterministic reporting.

The production builder uses an external merge sort with an 8 MiB default in-memory chunk size. That trades some temporary disk I/O for substantially lower peak RAM when large filter collections are processed.

All configured source downloads are release-critical by default. If any source fails, `legacy-filter-builder` refuses to publish a partial ruleset. This prevents a transient mirror failure from silently reducing a release. For an explicitly intentional partial build, pass `--allow-partial`; cancellation and timeout remain fatal.

When multiple manifests contain the same source URL, a shared cache directory can reuse the already downloaded file so the URL is fetched only once during that build.

## 🧾 Multiple named lists

`build.sh` builds **every** manifest under `lists/*.txt`, independently, into its own `filters/<name>.txt` intermediate and `dist/<name>.dat` release artifact (`<name>` is the manifest's filename without `.txt`). `custom-rules.txt` is layered onto every list the same way, while identical source URLs are reused from the shared per-build download cache.

To add another list (e.g. a smaller or region-specific one), drop a new manifest next to the default one:

```sh
lists/adblock.txt   -> filters/adblock.txt , dist/adblock.dat   (default, always present)
lists/german.txt    -> filters/german.txt  , dist/german.dat
lists/minimal.txt   -> filters/minimal.txt , dist/minimal.dat
```

Each manifest is otherwise identical in format to `lists/adblock.txt`: one HTTPS URL per line, `#` comments and blank lines ignored. This mirrors the one-file-per-list convention used by the original [xarantolus/filtrite](https://github.com/xarantolus/filtrite) project.

Scheduled GitHub Actions workflows are disabled after 60 days without a commit to the repository, so an inactive fork eventually stops publishing new releases and drops out of search results until something is pushed again.

## 🛠️ Build

Requirements:

- Go 1.23+ for this source tree.
- `curl`.
- A Linux-compatible `ruleset_converter` binary is downloaded automatically from Cromite's latest release during each `build.sh` run.

Run:

```sh
./build.sh
```

Outputs (per list `<name>`, see "Multiple named lists" below):

```text
filters/<name>.txt
build/work/<name>/rejected-*.txt
build/work/<name>/ruleset-converter.log
dist/<name>.dat
```

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

`build.sh` downloads the standalone `ruleset_converter` binary from Cromite's rolling latest-release URL:

```text
https://github.com/uazo/cromite/releases/latest/download/ruleset_converter
```

The converter is intentionally not pinned or checksum-locked; each build retrieves the version currently published at that URL. This keeps the build aligned with the latest Cromite converter, while also meaning reproducible builds require you to preserve a known converter binary separately.

`build.sh` enforces a **20 MiB default maximum** for every generated `dist/<name>.dat` ruleset and fails the build immediately if a ruleset exceeds it. Override the ceiling explicitly with `MAX_RULESET_BYTES=<bytes>` when a different target limit is required.

## ✅ Validation

Run the text-level sanity check on a generated list with:

```sh
./scripts/validate.sh filters/adblock.txt
```

`build.sh` already runs this on every list it builds; the direct invocation is for checking a single `filters/<name>.txt` on its own.

The final authority remains Chromium's `ruleset_converter`, which is executed by `build.sh` after the text builder completes. Chromium's documented conversion command is the same `filter-list` → `unindexed-ruleset` path used here.

Unit tests:

```sh
go test ./...
```

## 📦 Bromite / legacy-engine usage

Bromite documents that its legacy ad-blocking implementation uses Chromium's `subresource_filter` and that its legacy distribution uses an unindexed filter file.

For Cromite, use this output only when the specific build/configuration you are running exposes or consumes the legacy engine. Cromite's current normal ad blocker is a modified Adblock Plus implementation, so this repository should not be described as a drop-in compiler for Cromite's modern engine.

## Create a custom build

1. Fork the repository if you want your own independently maintained build.
2. Edit `lists/adblock.txt` to add/remove source URLs, and/or add another `lists/<name>.txt` manifest for a separate named list.
3. Edit `custom-rules.txt` for local legacy-compatible network rules; it's applied to every list.
4. Run the test suite (`go test ./...`).
5. Run `./build.sh`.
6. Publish the generated `dist/<name>.dat` assets from your release process.

Keep custom rules within the supported syntax policy above. A rule that is useful in uBlock Origin may still be invalid for this legacy engine.

## 🔐 Supply-chain and maintenance notes

Third-party filter sources keep their own licenses and terms. Do not assume that the MIT license covering this repository also licenses the downloaded filter content.

The GitHub Actions workflow runs tests before building and validates every generated list. Pushes touching any `lists/*.txt` manifest trigger the build, and releases publish every generated `dist/*.dat` asset. Source-download failures are release-fatal unless a caller explicitly uses the builder's `--allow-partial` option outside the release workflow.

## 📄 License

See [`LICENSE`](LICENSE).

## 💜 Support this project

If you'd like to support development, consider donating:

**Bitcoin:** `1HntwKxyGCfnSGvGLMUTRAqLnTvLarAQP`
