#!/usr/bin/env bash
set -Eeuo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

# build/, filters/, and dist/ are generated. Start every run from a clean
# artifact/work area so removed manifests cannot leave stale outputs behind.
rm -rf build filters dist filters.txt
mkdir -p build build/work build/source-cache

CONVERTER_URL="${CONVERTER_URL:-https://github.com/xarantolus/subresource_filter_tools/releases/latest/download/subresource_filter_tools_linux-x64.zip}"
CONVERTER_PATH="deps/ruleset_converter"

# Cromite publishes the converter as a standalone Linux binary in a release zip.
# The archive must be unpacked before the binary can be executed; downloading the
# zip directly and chmod'ing it produces the exec format error seen in CI.
command -v curl >/dev/null || { echo "ERROR: curl is required" >&2; exit 1; }
command -v unzip >/dev/null || { echo "ERROR: unzip is required" >&2; exit 1; }
mkdir -p deps

tmp_archive="$(mktemp "${CONVERTER_PATH}.zip.XXXXXX")"
tmp_extract="$(mktemp -d "${CONVERTER_PATH}.extract.XXXXXX")"
trap 'rm -f "$tmp_archive"; rm -rf "$tmp_extract"' EXIT

curl --fail --location --proto '=https' --tlsv1.2 --retry 4 --retry-delay 2 \
  --connect-timeout 20 --max-time 180 "$CONVERTER_URL" --output "$tmp_archive"
test -s "$tmp_archive" || { echo "ERROR: downloaded ruleset_converter archive is empty" >&2; exit 1; }

if [[ "$CONVERTER_URL" == *.zip ]]; then
  unzip -q "$tmp_archive" -d "$tmp_extract"
  candidate="$(find "$tmp_extract" -type f \( -name 'ruleset_converter' -o -name 'subresource_filter_tools' \) | head -n 1)"
  if [[ -z "$candidate" ]]; then
    echo "ERROR: ruleset_converter binary was not found in the downloaded archive" >&2
    unzip -l "$tmp_archive" >&2 || true
    exit 1
  fi
  mv -f "$candidate" "$CONVERTER_PATH"
else
  mv -f "$tmp_archive" "$CONVERTER_PATH"
fi

chmod +x "$CONVERTER_PATH"
trap - EXIT
[[ -x "$CONVERTER_PATH" ]] || { echo "ERROR: ruleset_converter is not executable" >&2; exit 1; }

file_output="$(file "$CONVERTER_PATH" 2>&1 || true)"
printf '%s\n' "$file_output"

grep -Eq 'ELF .* (64-bit|32-bit).*executable' <<<"$file_output" || {
  echo "ERROR: ruleset_converter is not a native Linux executable" >&2
  printf '%s\n' "$file_output" >&2 || true
  exit 1
}

# Smoke-test the converter before the rest of the build. This catches bad or
# partially-downloaded binaries even when the file format is accepted by the
# OS-level ELF detection logic.
"$CONVERTER_PATH" --help >/dev/null 2>&1 || {
  echo "ERROR: ruleset_converter is not runnable" >&2
  exit 1
}

go build -trimpath -ldflags='-s -w' -o build/legacy-filter-builder ./cmd/legacy-filter-builder
go build -trimpath -ldflags='-s -w' -o build/filtrite ./cmd/filtrite

# Build one legacy-compatible ruleset per source manifest under lists/:
# lists/<name>.txt -> filters/<name>.txt -> dist/<name>.dat.
# The shared build/source-cache directory lets later manifests reuse identical
# source URLs without a second network download during the same build.
shopt -s nullglob
manifests=(lists/*.txt)
shopt -u nullglob
if [[ ${#manifests[@]} -eq 0 ]]; then
  echo "ERROR: no source manifests found matching lists/*.txt" >&2
  exit 1
fi

mkdir -p filters

: "${MAX_RULESET_BYTES:=$((20 * 1024 * 1024))}"
[[ "$MAX_RULESET_BYTES" =~ ^[1-9][0-9]*$ ]] || {
  echo "ERROR: MAX_RULESET_BYTES must be a positive integer" >&2
  exit 1
}
total_bytes=0

for manifest in "${manifests[@]}"; do
  name="$(basename "$manifest" .txt)"
  list_build_dir="build/work/$name"
  list_filters="filters/$name.txt"
  list_dist="dist/$name.dat"

  echo "== $name: building from $manifest =="
  ./build/legacy-filter-builder \
    --sources "$manifest" \
    --custom custom-rules.txt \
    --output "$list_filters" \
    --build-dir "$list_build_dir" \
    --cache-dir build/source-cache
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
    printf 'ERROR: %s is %d bytes, over the configured %d-byte legacy ruleset limit; trim %s\n' \
      "$list_dist" "$list_bytes" "$MAX_RULESET_BYTES" "$manifest" >&2
    exit 1
  fi
  printf 'OK: %s and %s generated (%d bytes)\n' "$list_filters" "$list_dist" "$list_bytes"
done

printf 'OK: built %d list(s) from lists/*.txt (%d total bytes across dist/*.dat)\n' \
  "${#manifests[@]}" "$total_bytes"
