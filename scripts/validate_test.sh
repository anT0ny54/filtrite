#!/usr/bin/env bash
# Regression tests for scripts/validate.sh.
# Every ACCEPT case must pass; every REJECT case must fail.
# If deps/ruleset_converter exists, accepted cases are also checked by it.
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
validator="$root/scripts/validate.sh"
converter="${1:-$root/deps/ruleset_converter}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

too_long_label="$(printf 'a%.0s' {1..64})"
too_long_host="$(printf 'a%.0s' {1..63}).$(printf 'b%.0s' {1..63}).$(printf 'c%.0s' {1..63}).$(printf 'd%.0s' {1..62})"

accept=(
  '||example.com^$third-party'
  '||example.com/path$~third-party'
  '||example.com^$script,image,third-party'
  '||example.com^$~script,~image'
  '||example.com^$domain=a.com|~bb.com'
  '||example.com^$domain=a.example|~bb.example'
  '||example.com^$match-case,third-party'
  '@@||example.com^$document'
  '@@||example.com^$document,genericblock'
  '|https://example.com/ads'
  '|http://example.com'
  '@@|https://example.com/ads?x=1&y=two'

  # End-anchored URLs: filter.go emits one trailing "|" for these.
  '|https://example.com/ads.js|'
  '@@|https://example.com/allowed.js|'
  '|https://example.com/track.js|$third-party'
)

reject=(
  '||example.com^'
  '||example.com/path|middle'
  '||example.com/path||'
  '||example.com^$script,image,script'
  '||example.com^$third-party,~third-party'
  '||example.com/path$unknown$third-party'
  '||example.com^$third-party$match-case'
  '||example.com^$match-case,match-case'
  '||example.com^$domain=a.com,domain=b.com'
  '||example.com^$script,~image'
  '||example.com^$document'
  '@@||example.com^$document,script'
  '||example.com^$domain=a.com||b.com'
  '||example.com^$domain=|a.com'
  '||example.com^$domain=a.com|a.com'
  '||example.com^$domain=a.com|~a.com'
  '||example.com^$domain=A.com'
  '||example.com^$domain=localhost'
  '||example.com^$popup'
  '||example.com^$third-party,'
  '||example.com^$'
  '||1.2.3.4^$third-party'
  '||Example.com^$third-party'
  '||exa_mple.com^$third-party'
  '||example.com:8080^$third-party'
  '||localhost^$third-party'
  '||-bad.example^$third-party'
  '||bad-.example^$third-party'
  '||sub.-bad.example^$third-party'
  '||good.example^$domain=-bad.example'
  '||good.example^$domain=bad-.example'
  "||good.example^\$domain=${too_long_label}.example"
  "||${too_long_label}.example^\$third-party"
  "||${too_long_host}^\$third-party"
  'example.com##.ad'
  '/regex/'
  '||example.com^ $third-party'

  # Fully anchored URL regressions: hostname, port, and syntax must be checked
  # for both blocking rules and exception rules.
  '|https://bad_host.example/x'
  '@@|https://bad_host.example/x'
  '|https://example.com:443/x'
  '@@|https://example.com:443/x'
  '|https:///missing-host'
  '@@|https:///missing-host'
  '|ftp://example.com/x'
  '@@|ftp://example.com/x'
  '|https://example.com/%ZZ'
  '@@|https://example.com/%2G'
  '|https://-bad.example/x'
  '@@|https://bad-.example/x'
  '|https://127.0.0.1/x'
  '@@|https://127.0.0.1/x'

  # Anchored-URL tail character set: the validator must reject exactly the
  # characters internal/filter/filter.go (anchoredURLChars) rejects, so a
  # rule the Go stage emits can never fail validation and abort a release.
  '|https://example.com/a"b'
  '@@|https://example.com/a"b'
  "|https://example.com/a'b"
  '|https://example.com/page[1].html'
  '|https://example.com/page{1}.html'
  '|https://example.com/a^b'
  '|https://example.com/a|b'
  '|https://example.com/a\\b'
  '|https://example.com/a%2'
  '|https://example.com/a%zz'
  '@@|https://example.com/a%2G'

  # Only one trailing "|" is the end anchor; anything else is a stray pipe.
  '|https://example.com/a||'
  '@@|https://example.com/a||'
  '|https://example.com/a|b|'
)

fail=0
run_validator() {
  printf '%s\n' "$1" >"$work/case.txt"
  bash "$validator" "$work/case.txt" >/dev/null 2>&1
}

for rule in "${accept[@]}"; do
  if ! run_validator "$rule"; then
    echo "FAIL (validator rejected a valid rule): $rule"
    fail=1
  fi
done

for rule in "${reject[@]}"; do
  if run_validator "$rule"; then
    echo "FAIL (validator accepted a malformed rule): $rule"
    fail=1
  fi
done

if [[ -x "$converter" ]]; then
  for rule in "${accept[@]}"; do
    printf '%s\n' "$rule" >"$work/case.txt"
    if ! "$converter" --input_format=filter-list --output_format=unindexed-ruleset \
      --input_files="$work/case.txt" --output_file="$work/case.dat" >"$work/converter.log" 2>&1; then
      echo "FAIL (validator accepts a rule the converter rejects): $rule"
      sed 's/^/    /' "$work/converter.log"
      fail=1
    fi
  done
  echo "converter cross-check ran against $converter"
else
  echo "SKIP converter cross-check: $converter not found (run ./build.sh first)"
fi

if ((fail)); then
  exit 1
fi
echo "OK: validator regression tests passed (${#accept[@]} accept, ${#reject[@]} reject)"
