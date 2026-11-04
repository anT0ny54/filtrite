#!/usr/bin/env bash
# Regression tests for build.sh checks that must complete before cleanup.
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fakebin="$work/bin"
mkdir -p "$fakebin"
cat >"$fakebin/go" <<'GO'
#!/usr/bin/env bash
if [[ "${1:-}" == env && "${2:-}" == GOVERSION ]]; then
  printf '%s\n' "${FAKE_GO_VERSION:-go1.27.1}"
  exit 0
fi
printf 'unexpected go invocation during rejected preflight: %s\n' "$*" >&2
exit 90
GO
chmod +x "$fakebin/go"

new_fixture() {
  local name="$1"
  repo="$work/$name"
  mkdir -p "$repo/lists" "$repo/filters" "$repo/dist" "$repo/build/work" "$repo/build/source-cache"
  cp "$root/build.sh" "$repo/build.sh"
  printf 'https://example.com/list.txt\n' >"$repo/lists/adblock.txt"
  printf '! custom rules\n' >"$repo/custom-rules.txt"
  printf 'keep filters\n' >"$repo/filters/sentinel.txt"
  printf 'keep dist\n' >"$repo/dist/sentinel.txt"
  printf 'keep work\n' >"$repo/build/work/sentinel.txt"
  printf 'keep cache\n' >"$repo/build/source-cache/sentinel.txt"
}

run_expect_failure() {
  local expected="$1"
  shift
  local log="$work/build.log"
  if (cd "$repo" && env PATH="$fakebin:$PATH" FAKE_GO_VERSION="${FAKE_GO_VERSION:-go1.27.1}" "$@" bash ./build.sh) >"$log" 2>&1; then
    echo "FAIL: build.sh unexpectedly succeeded (expected: $expected)" >&2
    cat "$log" >&2
    exit 1
  fi
  if ! grep -Fq "$expected" "$log"; then
    printf 'FAIL: build.sh did not report %q\n' "$expected" >&2
    cat "$log" >&2
    exit 1
  fi
}

assert_generated_sentinels_preserved() {
  for path in \
    filters/sentinel.txt \
    dist/sentinel.txt \
    build/work/sentinel.txt \
    build/source-cache/sentinel.txt; do
    [[ -f "$repo/$path" ]] || {
      printf 'FAIL: preflight removed %s\n' "$path" >&2
      exit 1
    }
  done
}

# A custom-rules symlink into a directory removed by build.sh must be rejected
# while its target and all old generated artifacts are still present.
new_fixture custom-under-work
printf '||custom.example^\n' >"$repo/build/work/custom-rules.txt"
rm "$repo/custom-rules.txt"
ln -s build/work/custom-rules.txt "$repo/custom-rules.txt"
run_expect_failure 'custom rules resolves inside a directory cleared by build.sh'
assert_generated_sentinels_preserved
[[ -f "$repo/build/work/custom-rules.txt" ]]

# The same guard applies to source-manifest symlinks into the shared cache.
new_fixture manifest-under-cache
mv "$repo/lists/adblock.txt" "$repo/build/source-cache/manifest.txt"
ln -s ../build/source-cache/manifest.txt "$repo/lists/adblock.txt"
run_expect_failure 'source manifest resolves inside a directory cleared by build.sh'
assert_generated_sentinels_preserved
[[ -f "$repo/build/source-cache/manifest.txt" ]]

# A symlinked release summary must never truncate the source manifest.
new_fixture summary-symlink
ln -s ../lists/adblock.txt "$repo/build/release-summary.md"
run_expect_failure 'managed build file must not be a symlink: build/release-summary.md'
assert_generated_sentinels_preserved
[[ "$(cat "$repo/lists/adblock.txt")" == 'https://example.com/list.txt' ]]

# Hard links to overwritten build files are also rejected before truncation.
new_fixture summary-hardlink
ln "$repo/lists/adblock.txt" "$repo/build/release-summary.md"
run_expect_failure 'source manifest is hard-linked to a file overwritten by build.sh'
assert_generated_sentinels_preserved
[[ "$(cat "$repo/lists/adblock.txt")" == 'https://example.com/list.txt' ]]

# A symlinked build root would make rm -rf build/work operate outside the repo.
new_fixture build-root-symlink
external="$work/external-build"
mkdir -p "$external/work" "$repo/build-parent"
printf 'keep external work\n' >"$external/work/sentinel.txt"
rm -rf "$repo/build"
ln -s "$external" "$repo/build"
run_expect_failure 'managed build directory must not be a symlink: build'
[[ -f "$external/work/sentinel.txt" ]]

# A managed output hard-linked to an unrelated file would corrupt that file.
new_fixture summary-hardlink-external
printf 'preserve this external file\n' >"$work/external-summary-data"
ln "$work/external-summary-data" "$repo/build/release-summary.md"
run_expect_failure 'managed build file has multiple hard links: build/release-summary.md'
assert_generated_sentinels_preserved
[[ "$(cat "$work/external-summary-data")" == 'preserve this external file' ]]

# A converter symlink to another managed output would be truncated by the build.
new_fixture converter-alias-summary
printf 'summary content\n' >"$repo/build/release-summary.md"
chmod +x "$repo/build/release-summary.md"
mkdir -p "$repo/deps"
ln -s ../build/release-summary.md "$repo/deps/ruleset_converter"
run_expect_failure 'managed build files resolve to the same path'
assert_generated_sentinels_preserved
[[ -s "$repo/build/release-summary.md" ]]

# A converter hard-linked to external data is unsafe when it needs refresh.
new_fixture converter-hardlink-external
mkdir -p "$repo/deps"
printf 'external converter data\n' >"$work/external-converter-data"
ln "$work/external-converter-data" "$repo/deps/ruleset_converter"
run_expect_failure 'cached converter has multiple hard links and cannot be safely refreshed'
assert_generated_sentinels_preserved
[[ "$(cat "$work/external-converter-data")" == 'external converter data' ]]

# A deps symlink into a generated directory would erase the converter cache.
new_fixture deps-under-filters
rm -rf "$repo/deps"
ln -s filters "$repo/deps"
printf 'converter\n' >"$repo/filters/ruleset_converter"
chmod +x "$repo/filters/ruleset_converter"
run_expect_failure 'deps resolves inside a directory cleared by build.sh'
assert_generated_sentinels_preserved
[[ -x "$repo/filters/ruleset_converter" ]]

# A cached converter symlink must not target a directory cleared by the build.
new_fixture converter-under-filters
mkdir -p "$repo/deps"
printf 'converter\n' >"$repo/filters/ruleset_converter"
chmod +x "$repo/filters/ruleset_converter"
ln -s ../filters/ruleset_converter "$repo/deps/ruleset_converter"
run_expect_failure 'cached converter resolves inside a directory cleared by build.sh'
assert_generated_sentinels_preserved
[[ -x "$repo/filters/ruleset_converter" ]]

# Reject malformed HTTPS URLs before downloading or deleting prior outputs.
new_fixture bad-converter-url
run_expect_failure 'CONVERTER_URL must be HTTPS with a valid host' env CONVERTER_URL=https:///download.zip
assert_generated_sentinels_preserved

new_fixture converter-url-empty-host
run_expect_failure 'CONVERTER_URL must be HTTPS with a valid host' env CONVERTER_URL=https://:443/download.zip
assert_generated_sentinels_preserved

new_fixture converter-url-invalid-port
run_expect_failure 'CONVERTER_URL port must be between 1 and 65535' env CONVERTER_URL=https://example.com:65536/download.zip
assert_generated_sentinels_preserved

# Malformed DNS names (empty labels, labels with bad boundaries) must fail
# preflight, before cleanup, instead of surfacing later inside curl.
new_fixture converter-url-malformed-host
run_expect_failure 'CONVERTER_URL must be HTTPS with a valid host' env CONVERTER_URL=https://bad..host.example/download.zip
assert_generated_sentinels_preserved

new_fixture converter-url-bad-label-boundary
run_expect_failure 'CONVERTER_URL must be HTTPS with a valid host' env CONVERTER_URL=https://-bad.example.com/download.zip
assert_generated_sentinels_preserved

# A prerelease or decorated GOVERSION string is not a stable Go 1.27.1+ toolchain.
new_fixture go-prerelease
FAKE_GO_VERSION=go1.27.1rc1 run_expect_failure 'unrecognized Go toolchain version'
assert_generated_sentinels_preserved

# Invalid user configuration is caught before removing prior output as well.
new_fixture bad-checksum
run_expect_failure 'CONVERTER_SHA256 must contain exactly 64 hexadecimal characters' env CONVERTER_SHA256=invalid
assert_generated_sentinels_preserved

new_fixture bad-size-limit
run_expect_failure 'MAX_RULESET_BYTES must be a positive decimal integer' env MAX_RULESET_BYTES=0
assert_generated_sentinels_preserved

new_fixture old-go
FAKE_GO_VERSION=go1.26.0 run_expect_failure 'Go 1.27.1 or newer is required'
assert_generated_sentinels_preserved

echo 'OK: build preflight regression tests passed (19 cases)'
