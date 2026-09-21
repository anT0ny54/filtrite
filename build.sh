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

# Cromite publishes the converter as a standalone Linux binary at its
# rolling latest-release URL. Do not keep a tag, archive checksum, or lock
# file here: a fresh build intentionally retrieves the currently published
# converter. Download to a temporary file and replace the local copy only
# after a successful transfer.
command -v curl >/dev/null || { echo "ERROR: curl is required" >&2; exit 1; }
mkdir -p deps
tmp_converter="$(mktemp "${CONVERTER_PATH}.tmp.XXXXXX")"
trap 'rm -f "$tmp_converter"' EXIT
curl --fail --location --proto '=https' --tlsv1.2 --retry 4 --retry-delay 2 \
  --connect-timeout 20 --max-time 180 "$CONVERTER_URL" --output "$tmp_converter"
test -s "$tmp_converter" || { echo "ERROR: downloaded ruleset_converter is empty" >&2; exit 1; }
chmod +x "$tmp_converter"
mv -f "$tmp_converter" "$CONVERTER_PATH"
trap - EXIT
[[ -x "$CONVERTER_PATH" ]] || { echo "ERROR: ruleset_converter is not executable" >&2; exit 1; }

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
