#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

: "${CONVERTER_URL:=https://github.com/xarantolus/subresource_filter_tools/releases/latest/download/subresource_filter_tools_linux-x64.zip}"
: "${MAX_RULESET_BYTES:=$((20 * 1024 * 1024))}"

command -v go >/dev/null || {
  echo "ERROR: go is required" >&2
  exit 1
}

# Generated output is rebuilt from scratch so removed lists/rules never linger.
rm -rf filters dist build/work build/source-cache
mkdir -p deps filters dist build/work build/source-cache

# Release description generated automatically from successful builds.
: > build/release-summary.md

if [[ ! -x deps/ruleset_converter ]]; then
  command -v curl >/dev/null || {
    echo "ERROR: curl is required" >&2
    exit 1
  }

  command -v unzip >/dev/null || {
    echo "ERROR: unzip is required" >&2
    exit 1
  }

  tmp="$(mktemp)"
  trap 'rm -f "$tmp"' EXIT

  curl \
    --fail \
    --location \
    --proto '=https' \
    --tlsv1.2 \
    --retry 4 \
    --retry-delay 2 \
    --connect-timeout 20 \
    --max-time 180 \
    "$CONVERTER_URL" \
    --output "$tmp"

  if [[ -n "${CONVERTER_SHA256:-}" ]]; then
    printf '%s  %s\n' "$CONVERTER_SHA256" "$tmp" \
      | sha256sum --check --status - \
      || {
        echo "ERROR: converter archive checksum mismatch" >&2
        exit 1
      }
  fi

  # -j: accept the binary at the archive root or inside a sub-directory, but
  # fail loudly if the archive ever ships more than one matching binary.
  mapfile -t converter_entries < <(unzip -Z1 "$tmp" '*ruleset_converter')
  (( ${#converter_entries[@]} == 1 )) || {
    echo "ERROR: expected exactly one ruleset_converter in converter archive, found ${#converter_entries[@]}" >&2
    exit 1
  }
  unzip -oqj "$tmp" "${converter_entries[0]}" -d deps
  chmod +x deps/ruleset_converter
fi

[[ -x deps/ruleset_converter ]] || {
  echo "ERROR: ruleset_converter not installed" >&2
  exit 1
}

go build \
  -trimpath \
  -ldflags='-s -w' \
  -o build/legacy-filter-builder \
  ./cmd/legacy-filter-builder

go build \
  -trimpath \
  -ldflags='-s -w' \
  -o build/filtrite \
  ./cmd/filtrite

custom=custom-rules.txt
[[ -f "$custom" ]] || custom=""

shopt -s nullglob
manifests=(lists/*.txt)

(( ${#manifests[@]} > 0 )) || {
  echo "ERROR: no manifests found in lists/*.txt" >&2
  exit 1
}

# One independent build per manifest:
# lists/<name>.txt -> filters/<name>.txt -> dist/<name>.dat.
# Identical source URLs are downloaded once via the shared build/source-cache.
for manifest in "${manifests[@]}"; do
  name="$(basename "$manifest" .txt)"
  work="build/work/$name"

  echo "==> Building list: $name"

  # legacy-filter-builder already counts configured/succeeded sources itself
  # and writes them to a machine-readable key=value summary file; stream its
  # human-readable stdout to the console and read the counts from the summary
  # instead of scraping stdout with grep/sed.
  builder_log="build/work/$name.builder-stdout.log"
  summary_file="$work/build-summary.env"
  ./build/legacy-filter-builder \
    --sources "$manifest" \
    --custom "$custom" \
    --output "filters/$name.txt" \
    --build-dir "$work" \
    --cache-dir build/source-cache \
    --summary "$summary_file" \
    | tee "$builder_log"

  # shellcheck disable=SC1090
  source "$summary_file"
  configured_count="${sources_configured:-0}"
  succeeded_count="${sources_succeeded:-0}"
  rm -f "$builder_log"

  bash ./scripts/validate.sh "filters/$name.txt"

  ./build/filtrite \
    --input "filters/$name.txt" \
    --output "dist/$name.dat" \
    --converter deps/ruleset_converter \
    --log "$work/ruleset-converter.log"

  test -s "filters/$name.txt"
  test -s "dist/$name.dat"

  ruleset_bytes="$(wc -c < "dist/$name.dat")"

  if (( ruleset_bytes > MAX_RULESET_BYTES )); then
    printf \
      'WARNING: dist/%s.dat is %d bytes, over the %d-byte limit; trim lists/%s.txt\n' \
      "$name" \
      "$ruleset_bytes" \
      "$MAX_RULESET_BYTES" \
      "$name" >&2
  fi

  printf \
    'OK: filters/%s.txt and dist/%s.dat generated (%d bytes)\n' \
    "$name" \
    "$name" \
    "$ruleset_bytes"

  # Generate a clickable stable latest-release download link.
  printf \
    '• [%s](https://github.com/anT0ny54/filtrite/releases/latest/download/%s.dat) : updated %d/%d sources\n' \
    "$name" \
    "$name" \
    "$succeeded_count" \
    "$configured_count" \
    >> build/release-summary.md
done

echo
echo "==> Release summary"
cat build/release-summary.md
