# ⚡ Filtrite

Filtrite builds a **legacy Chromium `subresource_filter`** network-filter list and converts it to an unindexed Chromium ruleset for browsers/builds that still use that engine.

> **Important:** modern Cromite uses a modified Adblock Plus engine for its normal ad blocker. The legacy Bromite engine is a separate, older `subresource_filter` path and is disabled by default in Cromite. This repository intentionally targets the legacy path only.

## What the build produces

The build has two stages, run once per source manifest (see "Multiple named lists" below):

1. `internal/filter` downloads a manifest's source lists, validates each line, canonicalizes supported network rules, rejects unsupported syntax, deduplicates exact duplicates, and writes `filters/<name>.txt`.
2. `internal/ruleset` passes that already-filtered list to Chromium's `ruleset_converter` with `--input_format=filter-list --output_format=unindexed-ruleset`, producing `dist/<name>.dat`.

Chromium documents this `ruleset_converter` flow for development/testing of custom legacy `subresource_filter` rulesets.

With only the default `lists/adblock.txt` present, `<name>` is `adblock`, so this is exactly the single `filters/adblock.txt` / `dist/adblock.dat` pair the project has always produced. `dist/adblock.dat`'s path is unchanged from earlier versions of this project.

`build.sh` also copies `filters/adblock.txt` to a root-level `filters.txt`. This copy exists purely so the existing `.github/workflows/build.yml` release step — intentionally left untouched here — keeps publishing that exact asset name without modification.

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
  - Resource-type (`ElementType`) filters — `script`, `image`, `stylesheet`, `object`, `xmlhttprequest`, `object-subrequest`, `subdocument`, `ping`, `media`, `font`, `websocket`, `other`, `popup` — each optionally negated with `~` (e.g. `$script,image` or `$~image,~stylesheet`). **A single rule must use one sign only**: Chromium's parser decides whether the unspecified types start out included or excluded based on the *first* type's sign, so mixing `script,~image` in one rule is order-dependent and is rejected rather than guessed at.
  - Whitelist-only (`ActivationType`) filters — `document`, `elemhide`, `generichide`, `genericblock` — only on `@@` exception rules, never negated (e.g. `@@||example.com^$document`). These can't be combined with resource-type filters in the same rule.

Modifier duplicates and conflicts are rejected rather than resolved implicitly. Domain lists are validated and canonicalized into Chromium's deterministic ordering — longest domain first, then lexicographically within equal-length groups. Because Chromium's parser stores first/third-party state, case sensitivity, resource type, activation type, and initiator-domain constraints as distinct rule metadata, none of these scopes are collapsed together.

### Rejected

Cosmetic selectors, scriptlets/procedural syntax, regex filters, metadata records, malformed hosts, malformed modifiers, non-ASCII/whitespace-bearing rules, and any other network syntax outside the supported subset. This includes `$` options Chromium's own rule parser marks as not implemented for this legacy engine — `$sitekey`, `$collapse`, `$donottrack` — plus deprecated element-type aliases; these are rejected the same way Chromium's parser itself would reject them, not silently dropped or approximated.

This is intentional: a rule is either safely representable in the target engine or it is rejected.

### Third-party guard

Bare domain blocks such as `||example.com^` are emitted as `||example.com^$third-party`.

This is not an arbitrary optimization. Chromium's own filter-list generation script applies the same transformation to prevent an unconditional domain rule from also matching a top-level navigation to that domain.

Other metadata-scoped rules are **not** removed merely because a broader host rule exists. For example, `$domain=...` and `$~third-party` can have different matching scope and therefore cannot safely be treated as redundant.

## Rejected-rule reports

Every build writes `build/<name>/rejected-*.txt` reports for each list `<name>` (see "Multiple named lists" below). Each report contains the original source line number, rejection reason, and sanitized original rule so unsupported syntax is auditable rather than silently discarded.

## 🌐 Source lists

Every file matching `lists/*.txt` is an independent source manifest; the default is [`lists/adblock.txt`](lists/adblock.txt). Each manifest is a simple URL-per-line file; blank lines and `#` comments are ignored.

Source URLs must be valid **HTTPS URLs without embedded credentials**. Invalid entries fail the build with a line-numbered error instead of being silently skipped.

The downloader also enforces per-source and combined download-size limits, follows a small bounded number of redirects, refuses HTTPS→HTTP downgrade redirects, rejects obvious HTML error pages, and preserves source result order for deterministic reporting.

A single source failing to download (a dead mirror, a transient 5xx, etc.) does not abort the build: the builder logs a warning and continues with whatever sources succeeded. The build only fails outright if the download run is cut short by cancellation/timeout or if every configured source failed.

## 🧾 Multiple named lists

`build.sh` builds **every** manifest under `lists/*.txt`, independently, into its own `filters/<name>.txt` and `dist/<name>.dat` (`<name>` is the manifest's filename without `.txt`). `custom-rules.txt` is layered onto every list the same way.

To add another list (e.g. a smaller or region-specific one), drop a new manifest next to the default one:

```sh
lists/adblock.txt   -> filters/adblock.txt , dist/adblock.dat   (default, always present)
lists/german.txt    -> filters/german.txt  , dist/german.dat
lists/minimal.txt   -> filters/minimal.txt , dist/minimal.dat
```

Each manifest is otherwise identical in format to `lists/adblock.txt`: one HTTPS URL per line, `#` comments and blank lines ignored. This mirrors the one-file-per-list convention used by the original [xarantolus/filtrite](https://github.com/xarantolus/filtrite) project, which is what makes fork discovery via filterlists.010.one (see next section) possible.

## 🔎 Publishing to filterlists.010.one

[filterlists.010.one](https://filterlists.010.one/) is a search UI, built by [filtrite-lists](https://github.com/xarantolus/filtrite-lists), that walks the GitHub fork network of [xarantolus/filtrite](https://github.com/xarantolus/filtrite) once a day and, for every fork it finds, matches that fork's `lists/*.txt` manifest names against the release assets of its latest GitHub Release. A fork shows up there once **all** of the following are true:

1. **The repository is an actual GitHub fork of `xarantolus/filtrite`** (created with GitHub's "Fork" button, so it appears in that repository's fork network) — not a copy pushed to a brand-new repository. This tool can't create or verify that relationship for you; it's a one-time decision made when the repository is created on GitHub.
2. **`lists/*.txt` manifests exist with the names you want listed**, which `build.sh` now builds automatically into matching `dist/<name>.dat` files (see "Multiple named lists" above) — this part is handled.
3. **The latest GitHub Release publishes one asset per list, named `<name>.dat`.** This repository's own `.github/workflows/build.yml` currently publishes only `dist/adblock.dat` and `filters.txt` as fixed asset names (see its `files:` list). Changing that is out of scope here, since workflow files were intentionally left untouched. If you add more lists and want them discoverable, update that one `files:` entry yourself to `dist/*.dat` (optionally also `filters/*.txt`) so every generated list is published as its own asset.

Scheduled GitHub Actions workflows are disabled after 60 days without a commit to the repository, so an inactive fork eventually stops publishing new releases and drops out of search results until something is pushed again.

## 🛠️ Build

Requirements:

- Go 1.23+ for this source tree.
- `curl` and `unzip` when `deps/ruleset_converter` is not already present.
- A Linux-compatible Chromium `ruleset_converter` binary.

Run:

```sh
./build.sh
```

Outputs (per list `<name>`, see "Multiple named lists" below):

```text
filters/<name>.txt
build/<name>/rejected-*.txt
dist/<name>.dat
build/<name>/ruleset-converter.log
```

With only the default `lists/adblock.txt`, that's `filters/adblock.txt`, `build/adblock/rejected-*.txt`, `dist/adblock.dat`, and `build/adblock/ruleset-converter.log`.

The converter archive is pinned by tag in `build.sh`. For supply-chain hardening, `CONVERTER_SHA256` may be set to the expected SHA-256 of the archive; the build will then fail on mismatch.

Bromite's updater rejects a filters file larger than **20 MiB** (`kMaxBodySize` in `Bromite-subresource-adblocker.patch`). `build.sh` checks `dist/adblock.dat` against this limit and prints a warning if it is exceeded; trim `lists/adblock.txt` if that happens.

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

1. Fork the repository (use GitHub's "Fork" button if you want the result to show up on filterlists.010.one — see "Publishing to filterlists.010.one" above).
2. Edit `lists/adblock.txt` to add/remove source URLs, and/or add another `lists/<name>.txt` manifest for a separate named list.
3. Edit `custom-rules.txt` for local legacy-compatible network rules; it's applied to every list.
4. Run the test suite (`go test ./...`).
5. Run `./build.sh`.
6. Publish `dist/<name>.dat` and/or `filters/<name>.txt` from your own release process.

Keep custom rules within the supported syntax policy above. A rule that is useful in uBlock Origin may still be invalid for this legacy engine.

## 🔐 Supply-chain and maintenance notes

Third-party filter sources keep their own licenses and terms. Do not assume that the MIT license covering this repository also licenses the downloaded filter content.

The GitHub Actions workflow runs tests before building and validating the generated list. Releases currently contain the generated `dist/adblock.dat` and `filters.txt` artifacts (see "What the build produces" above for why `filters.txt` exists as a compatibility copy). If you add more lists under `lists/*.txt` and want each published as its own release asset — required for filterlists.010.one to discover them, see "Publishing to filterlists.010.one" above — widen the workflow's `files:` list to `dist/*.dat`.

## 📄 License

See [`LICENSE`](LICENSE).

## 🔗 Other projects by the maintainer

These are unrelated to the projects above but are run by the same maintainer.

**My Free DNS** — DNS-over-HTTPS resolvers using HaGeZi Blocklists Multi Pro + TIF:

| Service | DNS-over-HTTPS URL |
| --- | --- |
| Multi Pro + TIF (Recommended) | `https://freedns.koyeb.app/dns-query` |
| Multi Pro + TIF (Recommended) | `https://freedns-six.vercel.app/api/doh/dns-query` |
| Multi Pro + TIF (Backup) | `https://dnssix.netlify.app/api/doh/dns-query` |
| Multi Pro + TIF (Recommended, but will sleep if not used within 15 minutes) | `https://dns-93aca.containers.snapdeploy.app/dns-query` |

**Bandwidth Hero Server** — a lightweight image proxy that fetches remote images, compresses them, and returns optimized versions for faster loading and lower data use: https://bhserv.netlify.app/

## 💜 Support this project

If you'd like to support development, consider donating:

**Bitcoin:** `1HntwKxyGCfnSGvGLMUTRAqLnTvLarAQP`
