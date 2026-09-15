# ⚡ Filtrite

Filtrite builds a **legacy Chromium `subresource_filter`** network-filter list and converts it to an unindexed Chromium ruleset for browsers/builds that still use that engine.

> **Important:** modern Cromite uses a modified Adblock Plus engine for its normal ad blocker. The legacy Bromite engine is a separate, older `subresource_filter` path and is disabled by default in Cromite. This repository intentionally targets the legacy path only.

## What the build produces

The build has two stages:

1. `internal/filter` downloads source lists, validates each line, canonicalizes supported network rules, rejects unsupported syntax, deduplicates exact duplicates, and writes `filters.txt`.
2. `internal/ruleset` passes that already-filtered list to Chromium's `ruleset_converter` with `--input_format=filter-list --output_format=unindexed-ruleset`, producing `dist/adblock.dat`.

Chromium documents this `ruleset_converter` flow for development/testing of custom legacy `subresource_filter` rulesets.

## 🧩 Legacy filter syntax policy

`filters.txt` is deliberately **not** a general-purpose uBlock Origin, AdGuard, or modern Adblock Plus filter compiler. Every source rule must fit the smaller legacy syntax accepted by Chromium's `subresource_filter` rule parser. Unsupported rules are rejected rather than rewritten into an approximation.

### Accepted

- `||host^` and `||host/path...` network rules.
- `@@` network exceptions using the same supported network syntax.
- Fully anchored `|http://...` and `|https://...` network rules, including `@@` exceptions.
- Hosts-file entries using `0.0.0.0`, `127.0.0.1`, or `::1` followed by exactly one hostname.
- Only these rule modifiers:
  - `$third-party`
  - `$~third-party`
  - `$match-case`
  - `$domain=example.com|~excluded.example`

Modifier duplicates/conflicts are rejected. Domain lists are validated and canonicalized into Chromium's deterministic ordering. Chromium's parser stores first/third-party state, case sensitivity, and initiator-domain constraints as distinct rule metadata, so these scopes must not be collapsed together.

### Rejected

Cosmetic selectors, scriptlets/procedural syntax, regex filters, metadata records, unsupported `$` options, malformed hosts, malformed modifiers, non-ASCII/whitespace-bearing rules, and any other network syntax outside the supported subset.

This is intentional: a rule is either safely representable in the target engine or it is rejected.

### Third-party guard

Bare domain blocks such as `||example.com^` are emitted as `||example.com^$third-party`.

This is not an arbitrary optimization. Chromium's own filter-list generation script applies the same transformation to prevent an unconditional domain rule from also matching a top-level navigation to that domain.

Other metadata-scoped rules are **not** removed merely because a broader host rule exists. For example, `$domain=...` and `$~third-party` can have different matching scope and therefore cannot safely be treated as redundant.

## Rejected-rule reports

Every build writes `build/rejected-*.txt` reports. Each report contains the original source line number, rejection reason, and sanitized original rule so unsupported syntax is auditable rather than silently discarded.

## 🌐 Source lists

The default source manifest is [`lists/adblock.txt`](lists/adblock.txt). It is a simple URL-per-line file; blank lines and `#` comments are ignored.

Source URLs must be valid **HTTPS URLs without embedded credentials**. Invalid entries fail the build with a line-numbered error instead of being silently skipped.

The downloader also enforces per-source and combined download-size limits, follows a small bounded number of redirects, refuses HTTPS→HTTP downgrade redirects, rejects obvious HTML error pages, and preserves source result order for deterministic reporting.

## 🛠️ Build

Requirements:

- Go 1.23+ for this source tree.
- `curl` and `unzip` when `deps/ruleset_converter` is not already present.
- A Linux-compatible Chromium `ruleset_converter` binary.

Run:

```sh
./build.sh
```

Outputs:

```text
filters.txt
build/rejected-*.txt
dist/adblock.dat
build/ruleset-converter.log
```

The converter archive is pinned by tag in `build.sh`. For supply-chain hardening, `CONVERTER_SHA256` may be set to the expected SHA-256 of the archive; the build will then fail on mismatch.

## ✅ Validation

Run the text-level sanity check with:

```sh
./scripts/validate.sh filters.txt
```

The final authority remains Chromium's `ruleset_converter`, which is executed by `build.sh` after the text builder completes. Chromium's documented conversion command is the same `filter-list` → `unindexed-ruleset` path used here.

Unit tests:

```sh
go test ./...
```

## 📦 Bromite / legacy-engine usage

Bromite documents that its legacy ad-blocking implementation uses Chromium's `subresource_filter` and that its legacy distribution uses an unindexed filter file.

For Cromite, use this output only when the specific build/configuration you are running exposes or consumes the legacy engine. Cromite's current normal ad blocker is a modified Adblock Plus implementation, so this repository should not be described as a drop-in compiler for Cromite's modern engine.

## Create a custom build

1. Fork the repository.
2. Edit `lists/adblock.txt` to add/remove source URLs.
3. Edit `custom-rules.txt` for local legacy-compatible network rules.
4. Run the test suite.
5. Run `./build.sh`.
6. Publish `dist/adblock.dat` and/or `filters.txt` from your own release process.

Keep custom rules within the supported syntax policy above. A rule that is useful in uBlock Origin may still be invalid for this legacy engine.

## 🔐 Supply-chain and maintenance notes

Third-party filter sources keep their own licenses and terms. Do not assume that the MIT license covering this repository also licenses the downloaded filter content.

The GitHub Actions workflow runs tests before building and validating the generated list. Releases contain the generated `dist/adblock.dat` and `filters.txt` artifacts.

## 📄 License

See [`LICENSE`](LICENSE).


## 🌐 Free DNS Services

High-performance DNS utilizing HaGeZi Blocklists (Multi Pro + TIF).

| Blocklist | DNS-over-HTTPS (DoH) |
| :--- | :--- |
| Multi Pro + TIF | `https://freedns.koyeb.app/dns-query` (Recommended) |
| Multi Pro + TIF | `https://freedns-six.vercel.app/api/doh/dns-query` (Recommended) |
| Multi Pro + TIF | `https://dnssix.netlify.app/api/doh/dns-query` |

---

# ⚡ Bandwidth Hero Server

A lightweight image optimization proxy designed to slash bandwidth usage and accelerate web browsing.

Bandwidth Hero Server fetches remote images, compresses them on the fly, and delivers optimized versions to the client. This significantly reduces data consumption while improving page load performance.

🖥️ **Live Demo:** [Bandwidth Hero](https://bhserv.netlify.app/).

## Supporting the Project

If you find this project useful, donations are appreciated:
- **Bitcoin**: `1HntwKxyqGCfnSGvGLMUTRAqLnTvLarAQP`

  
  