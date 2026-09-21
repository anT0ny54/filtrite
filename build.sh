#!/usr/bin/env bash
set -Eeuo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

: "${CONVERTER_URL:=https://github.com/xarantolus/subresource_filter_tools/releases/latest/download/subresource_filter_tools_linux-x64.zip}"

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

./build/legacy-filter-builder --sources lists/adblock.txt --custom custom-rules.txt --output filters.txt --build-dir build
./scripts/validate.sh filters.txt
./build/filtrite --input filters.txt --output dist/adblock.dat --converter deps/ruleset_converter

test -s filters.txt
test -s dist/adblock.dat

: "${MAX_RULESET_BYTES:=$((20 * 1024 * 1024))}"
ruleset_bytes="$(wc -c < dist/adblock.dat)"
if (( ruleset_bytes > MAX_RULESET_BYTES )); then
  printf 'WARNING: dist/adblock.dat is %d bytes, over Bromite'\''s %d-byte filters-file limit; trim lists/adblock.txt\n' \
    "$ruleset_bytes" "$MAX_RULESET_BYTES" >&2
fi

printf 'OK: filters.txt and dist/adblock.dat generated (%d bytes)\n' "$ruleset_bytes"
