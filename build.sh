#!/usr/bin/env bash
set -Eeuo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

: "${CONVERTER_TAG:=2026-07-24-05-28}"
: "${CONVERTER_URL:=https://github.com/xarantolus/subresource_filter_tools/releases/download/${CONVERTER_TAG}/subresource_filter_tools_linux-x64.zip}"

mkdir -p deps dist build
if [[ ! -x deps/ruleset_converter ]]; then
  command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
  command -v unzip >/dev/null || { echo "unzip is required" >&2; exit 1; }
  tmp="$(mktemp)"
  trap 'rm -f "$tmp"' EXIT
  curl --fail --location --proto '=https' --tlsv1.2 --retry 4 --retry-delay 2 \
    --connect-timeout 20 --max-time 180 "$CONVERTER_URL" --output "$tmp"
  if [[ -n "${CONVERTER_SHA256:-}" ]]; then
    printf '%s  %s\n' "$CONVERTER_SHA256" "$tmp" | sha256sum --check --status - \
      || { echo "ERROR: converter archive checksum mismatch" >&2; exit 1; }
  fi
  unzip -oq "$tmp" 'ruleset_converter' -d deps
  chmod +x deps/ruleset_converter
fi
[[ -x deps/ruleset_converter ]] || { echo "ERROR: ruleset_converter not installed" >&2; exit 1; }

go build -trimpath -ldflags='-s -w' -o build/legacy-filter-builder ./cmd/legacy-filter-builder
go build -trimpath -ldflags='-s -w' -o build/filtrite ./cmd/filtrite

# One legacy-compatible ruleset is built per source manifest under lists/,
# named after the manifest file: lists/<name>.txt -> filters/<name>.txt ->
# dist/<name>.dat. With only the default lists/adblock.txt present, this is
# exactly the single dist/adblock.dat produced before; dropping in another
# manifest (e.g. lists/german.txt) gets you a second, independent
# dist/german.dat for free, using the same custom-rules.txt for both.
#
# This mirrors the upstream filtrite project's one-file-per-list convention
# (https://github.com/xarantolus/filtrite), which the filterlists.010.one /
# filtrite-lists fork crawler (https://github.com/xarantolus/filtrite-lists)
# expects: it reads a fork's lists/*.txt manifests and looks for a matching
# <name>.dat release asset per list. See "Publishing to filterlists.010.one"
# in README.md for the one remaining step (a release-workflow change) this
# script intentionally does not make on its own.
shopt -s nullglob
manifests=(lists/*.txt)
shopt -u nullglob
if [[ ${#manifests[@]} -eq 0 ]]; then
  echo "ERROR: no source manifests found matching lists/*.txt" >&2
  exit 1
fi

mkdir -p filters

: "${MAX_RULESET_BYTES:=$((20 * 1024 * 1024))}"
total_bytes=0
over_limit=0

for manifest in "${manifests[@]}"; do
  name="$(basename "$manifest" .txt)"
  list_build_dir="build/$name"
  list_filters="filters/$name.txt"
  list_dist="dist/$name.dat"

  echo "== $name: building from $manifest =="
  ./build/legacy-filter-builder \
    --sources "$manifest" \
    --custom custom-rules.txt \
    --output "$list_filters" \
    --build-dir "$list_build_dir"
  ./scripts/validate.sh "$list_filters"
  ./build/filtrite \
    --input "$list_filters" \
    --output "$list_dist" \
    --converter deps/ruleset_converter \
    --log "$list_build_dir/ruleset-converter.log"

  test -s "$list_filters"
  test -s "$list_dist"

  list_bytes="$(wc -c < "$list_dist")"
  total_bytes=$((total_bytes + list_bytes))
  if (( list_bytes > MAX_RULESET_BYTES )); then
    printf 'WARNING: %s is %d bytes, over Bromite'\''s %d-byte filters-file limit; trim %s\n' \
      "$list_dist" "$list_bytes" "$MAX_RULESET_BYTES" "$manifest" >&2
    over_limit=1
  fi
  printf 'OK: %s and %s generated (%d bytes)\n' "$list_filters" "$list_dist" "$list_bytes"
done

if (( over_limit )); then
  echo "WARNING: one or more generated rulesets exceed the 20 MiB legacy-updater limit (see above)" >&2
fi

# .github/workflows/build.yml (intentionally left untouched here) still
# publishes a root-level "filters.txt" release asset by that exact fixed
# name. Keep that path alive as a copy of the default list's filters file so
# the existing release step keeps working unmodified even though the
# per-list layout above moved it to filters/<name>.txt.
if [[ -f filters/adblock.txt ]]; then
  cp filters/adblock.txt filters.txt
fi

printf 'OK: built %d list(s) from lists/*.txt (%d total bytes across dist/*.dat)\n' \
  "${#manifests[@]}" "$total_bytes"
