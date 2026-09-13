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
  curl --fail --location --retry 4 --retry-delay 2 --connect-timeout 20 --max-time 180 \
    "$CONVERTER_URL" --output "$tmp"
  unzip -oq "$tmp" 'ruleset_converter' -d deps
  chmod +x deps/ruleset_converter
fi

go build -trimpath -ldflags='-s -w' -o build/legacy-filter-builder ./cmd/legacy-filter-builder
go build -trimpath -ldflags='-s -w' -o build/filtrite ./cmd/filtrite

./build/legacy-filter-builder --sources sources.txt --custom custom-rules.txt --output filters.txt --build-dir build
./build/filtrite --input filters.txt --output dist/adblock.dat --converter deps/ruleset_converter

test -s filters.txt
test -s dist/adblock.dat
printf 'OK: filters.txt and dist/adblock.dat generated\n'
