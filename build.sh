#!/usr/bin/env bash
set -Eeuo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

# build/, filters/, and dist/ are generated. Start every run from a clean
# artifact/work area so removed manifests cannot leave stale outputs behind.
rm -rf build filters dist filters.txt
mkdir -p build build/work build/source-cache

: "${CONVERTER_TAG:=2026-07-24-05-28}"
: "${CONVERTER_URL:=https://github.com/xarantolus/subresource_filter_tools/releases/download/${CONVERTER_TAG}/subresource_filter_tools_linux-x64.zip}"
: "${CONVERTER_SHA256:=7dd1121c197cffaa8d6a83e26c668c43dbf812299dd959a9611562237a69b493}"

CONVERTER_LOCK="deps/ruleset_converter.lock"
CONVERTER_PATH="deps/ruleset_converter"

# The release tag, URL, and archive SHA-256 form the content pin. The lock
# additionally records the extracted binary hash so a changed or replaced
# local converter is never silently reused.
lock_tag=""
lock_url=""
lock_archive_sha256=""
lock_binary_sha256=""
if [[ -f "$CONVERTER_LOCK" ]]; then
  while IFS='=' read -r key value; do
    case "$key" in
      converter_tag)  lock_tag="$value" ;;
      converter_url)  lock_url="$value" ;;
      archive_sha256) lock_archive_sha256="$value" ;;
      binary_sha256)  lock_binary_sha256="$value" ;;
    esac
  done < "$CONVERTER_LOCK"
fi

mkdir -p deps
if [[ -x "$CONVERTER_PATH" ]]; then
  reusable=0
  if [[ "$lock_tag" == "$CONVERTER_TAG" && "$lock_url" == "$CONVERTER_URL" && "$lock_archive_sha256" == "$CONVERTER_SHA256" && "$lock_binary_sha256" =~ ^[[:xdigit:]]{64}$ ]]; then
    command -v sha256sum >/dev/null || { echo "sha256sum is required" >&2; exit 1; }
    actual_binary_sha256="$(sha256sum "$CONVERTER_PATH" | awk '{print $1}')"
    if [[ "$actual_binary_sha256" == "$lock_binary_sha256" ]]; then
      reusable=1
      echo "Using pinned ruleset_converter $CONVERTER_TAG ($actual_binary_sha256)"
    fi
  fi
  if (( ! reusable )); then
    echo "Refreshing ruleset_converter because its pin or verified hash changed" >&2
    rm -f "$CONVERTER_PATH" "$CONVERTER_LOCK"
  fi
fi

if [[ ! -x "$CONVERTER_PATH" ]]; then
  command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
  command -v unzip >/dev/null || { echo "unzip is required" >&2; exit 1; }
  command -v sha256sum >/dev/null || { echo "sha256sum is required" >&2; exit 1; }
  [[ "$CONVERTER_SHA256" =~ ^[[:xdigit:]]{64}$ ]] || {
    echo "ERROR: CONVERTER_SHA256 must contain the 64-hex SHA-256 of $CONVERTER_URL" >&2
    exit 1
  }
  tmp="$(mktemp)"
  extract_dir="$(mktemp -d)"
  trap 'rm -f "$tmp"; rm -rf "$extract_dir"' EXIT
  curl --fail --location --proto '=https' --tlsv1.2 --retry 4 --retry-delay 2 \
    --connect-timeout 20 --max-time 180 "$CONVERTER_URL" --output "$tmp"
  printf '%s  %s\n' "$CONVERTER_SHA256" "$tmp" | sha256sum --check --status - \
    || { echo "ERROR: converter archive checksum mismatch" >&2; exit 1; }
  unzip -oq "$tmp" 'ruleset_converter' -d "$extract_dir"
  chmod +x "$extract_dir/ruleset_converter"
  binary_sha256="$(sha256sum "$extract_dir/ruleset_converter" | awk '{print $1}')"
  mv -f "$extract_dir/ruleset_converter" "$CONVERTER_PATH"
  tmp_lock="${CONVERTER_LOCK}.tmp"
  {
    printf 'converter_tag=%s\n' "$CONVERTER_TAG"
    printf 'converter_url=%s\n' "$CONVERTER_URL"
    printf 'archive_sha256=%s\n' "$CONVERTER_SHA256"
    printf 'binary_sha256=%s\n' "$binary_sha256"
  } > "$tmp_lock"
  mv -f "$tmp_lock" "$CONVERTER_LOCK"
fi
[[ -x "$CONVERTER_PATH" ]] || { echo "ERROR: ruleset_converter not installed" >&2; exit 1; }

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
