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

  # -j: accept the binary at the archive root or inside a sub-directory.
  unzip -oqj "$tmp" '*ruleset_converter' -d deps
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
  # and prints "Sources: N configured, M succeeded" to stdout; capture that
  # here (while still streaming it to the console) instead of recomputing an
  # independent count from the manifest, which could only ever equal the
  # configured total given this script never passes --allow-partial.
  builder_log="build/work/$name.builder-stdout.log"
  ./build/legacy-filter-builder \
    --sources "$manifest" \
    --custom "$custom" \
    --output "filters/$name.txt" \
    --build-dir "$work" \
    --cache-dir build/source-cache \
    | tee "$builder_log"

  source_line="$(grep '^Sources:' "$builder_log" | tail -n1)"
  configured_count="$(sed -nE 's/^Sources: ([0-9]+) configured.*/\1/p' <<<"$source_line")"
  succeeded_count="$(sed -nE 's/^Sources: [0-9]+ configured, ([0-9]+) succeeded.*/\1/p' <<<"$source_line")"
  : "${configured_count:=0}"
  : "${succeeded_count:=0}"
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
