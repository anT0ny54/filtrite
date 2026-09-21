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

command -v curl >/dev/null || {
  echo "ERROR: curl is required" >&2
  exit 1
}

command -v unzip >/dev/null || {
  echo "ERROR: unzip is required" >&2
  exit 1
}

command -v file >/dev/null || {
  echo "ERROR: file is required" >&2
  exit 1
}

mkdir -p deps

tmp_archive="$(mktemp "${CONVERTER_PATH}.zip.XXXXXX")"
tmp_extract="$(mktemp -d "${CONVERTER_PATH}.extract.XXXXXX")"

cleanup() {
  rm -f "$tmp_archive"
  rm -rf "$tmp_extract"
}
trap cleanup EXIT

echo "== Downloading ruleset converter =="

curl --fail --location --proto '=https' --tlsv1.2 \
  --retry 4 --retry-delay 2 \
  --connect-timeout 20 --max-time 180 \
  "$CONVERTER_URL" \
  --output "$tmp_archive"

test -s "$tmp_archive" || {
  echo "ERROR: downloaded ruleset_converter archive is empty" >&2
  exit 1
}

if [[ "$CONVERTER_URL" == *.zip ]]; then
  unzip -q "$tmp_archive" -d "$tmp_extract"

  candidate="$(
    find "$tmp_extract" -type f \
      \( -name 'ruleset_converter' -o -name 'subresource_filter_tools' \) \
      -print -quit
  )"

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

[[ -x "$CONVERTER_PATH" ]] || {
  echo "ERROR: ruleset_converter is not executable" >&2
  exit 1
}

echo "== Checking ruleset converter =="

file_output="$(file "$CONVERTER_PATH" 2>&1 || true)"
printf '%s\n' "$file_output"

# Accept normal Linux ELF executables, including PIE binaries:
#   ELF 64-bit LSB pie executable
#   ELF 64-bit LSB executable
#   ELF 32-bit LSB executable
#
# The previous regex was too restrictive and rejected valid PIE ELF files.
if ! grep -Eiq 'ELF (32-bit|64-bit).*executable' <<<"$file_output"; then
  echo "ERROR: ruleset_converter is not a Linux ELF executable" >&2
  printf '%s\n' "$file_output" >&2
  exit 1
fi

# This release URL is specifically linux-x64, so fail clearly on non-x86_64
# runners instead of producing a confusing executable-format failure later.
host_arch="$(uname -m)"

case "$host_arch" in
  x86_64|amd64)
    if ! grep -Eiq 'ELF 64-bit.*x86-64' <<<"$file_output"; then
      echo "ERROR: downloaded converter is not an x86-64 Linux binary" >&2
      echo "Host architecture: $host_arch" >&2
      printf '%s\n' "$file_output" >&2
      exit 1
    fi
    ;;
  *)
    echo "ERROR: this build downloads a Linux x64 converter, but the runner architecture is '$host_arch'" >&2
    echo "Use an architecture-specific converter for this runner." >&2
    exit 1
    ;;
esac

# Smoke-test the converter before the rest of the build.
"$CONVERTER_PATH" --help >/dev/null 2>&1 || {
  echo "ERROR: ruleset_converter is not runnable" >&2
  exit 1
}

echo "OK: ruleset_converter is a native Linux x86-64 executable"

go build -trimpath -ldflags='-s -w' \
  -o build/legacy-filter-builder \
  ./cmd/legacy-filter-builder

go build -trimpath -ldflags='-s -w' \
  -o build/filtrite \
  ./cmd/filtrite

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

mkdir -p filters dist

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
    --converter "$CONVERTER_PATH" \
    --log "$list_build_dir/ruleset-converter.log"

  test -s "$list_filters"
  test -s "$list_dist"

  list_bytes="$(wc -c < "$list_dist")"
  total_bytes=$((total_bytes + list_bytes))

  if (( list_bytes > MAX_RULESET_BYTES )); then
    printf \
      'ERROR: %s is %d bytes, over the configured %d-byte legacy ruleset limit; trim %s\n' \
      "$list_dist" \
      "$list_bytes" \
      "$MAX_RULESET_BYTES" \
      "$manifest" >&2
    exit 1
  fi

  printf \
    'OK: %s and %s generated (%d bytes)\n' \
    "$list_filters" \
    "$list_dist" \
    "$list_bytes"
done

printf \
  'OK: built %d list(s) from lists/*.txt (%d total bytes across dist/*.dat)\n' \
  "${#manifests[@]}" \
  "$total_bytes"
