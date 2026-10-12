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

  # The EXIT trap only covers this download block; disarm it here instead of
  # leaking it to the end of the script.
  trap - EXIT
  rm -f "$tmp"
fi

[[ -x deps/ruleset_converter ]] || {
  echo "ERROR: ruleset_converter not installed" >&2
  exit 1
}

# Build both helpers concurrently. Go's build cache still compiles the
# shared internal packages only once, and linking two small binaries in
# parallel beats building them back to back. Both exit statuses are
# collected: the first failure must not leave the other build uncollected
# (a bare `wait` under `set -e` would exit immediately), so wait for every
# PID and only then fail.
build_pids=()
go build \
  -trimpath \
  -ldflags='-s -w' \
  -o build/legacy-filter-builder \
  ./cmd/legacy-filter-builder &
build_pids+=($!)
go build \
  -trimpath \
  -ldflags='-s -w' \
  -o build/filtrite \
  ./cmd/filtrite &
build_pids+=($!)
build_status=0
for build_pid in "${build_pids[@]}"; do
  if ! wait "$build_pid"; then
    build_status=1
  fi
done
if (( build_status != 0 )); then
  echo "ERROR: one or both Go helper builds failed" >&2
  exit 1
fi

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

  # legacy-filter-builder counts configured/succeeded sources itself and
  # writes them to a machine-readable key=value summary file; its
  # human-readable stdout goes straight to the console. An empty --custom
  # disables custom rules (custom-rules.txt missing).
  summary_file="$work/build-summary.env"
  ./build/legacy-filter-builder \
    --sources "$manifest" \
    --custom "$custom" \
    --output "filters/$name.txt" \
    --build-dir "$work" \
    --cache-dir build/source-cache \
    --summary "$summary_file"

  # Clear values from the previous list so a missing key can never reuse them.
  unset sources_configured sources_succeeded
  # shellcheck disable=SC1090
  source "$summary_file"
  configured_count="${sources_configured:-0}"
  succeeded_count="${sources_succeeded:-0}"

  bash ./scripts/validate.sh "filters/$name.txt"

  ./build/filtrite \
    --input "filters/$name.txt" \
    --output "dist/$name.dat" \
    --converter deps/ruleset_converter \
    --log "$work/ruleset-converter.log"

  test -s "dist/$name.dat"

  ruleset_bytes="$(wc -c < "dist/$name.dat")"

  # The 20 MiB ceiling is a release requirement, not a recommendation:
  # remove the oversized artifact and abort so an over-limit ruleset can
  # never be published, even by a caller that ignores the exit status.
  if (( ruleset_bytes > MAX_RULESET_BYTES )); then
    rm -f "dist/$name.dat"
    printf \
      'ERROR: dist/%s.dat is %d bytes, over the %d-byte release limit; trim lists/%s.txt or raise MAX_RULESET_BYTES\n' \
      "$name" \
      "$ruleset_bytes" \
      "$MAX_RULESET_BYTES" \
      "$name" >&2
    exit 1
  fi

  printf \
    'OK: filters/%s.txt and dist/%s.dat generated (%d bytes)\n' \
    "$name" \
    "$name" \
    "$ruleset_bytes"

  # Generate a clickable stable latest-release download link.
  printf \
    '• [%s](https://github.com/%s/releases/latest/download/%s.dat) : updated %d/%d sources\n' \
    "$name" \
    "${GITHUB_REPOSITORY:-anT0ny54/filtrite}" \
    "$name" \
    "$succeeded_count" \
    "$configured_count" \
    >> build/release-summary.md
done

echo
echo "==> Release summary"
cat build/release-summary.md
