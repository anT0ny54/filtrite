#!/usr/bin/env bash
# Regression tests for scripts/validate.sh.
#
# Every ACCEPT case must pass the validator; every REJECT case must fail it.
# When deps/ruleset_converter exists (it does after ./build.sh), each ACCEPT
# case is also fed to the real converter, so the validator can never drift
# into accepting a rule that Chromium rejects. REJECT cases are not required
# to be rejected by the converter: the validator is intentionally stricter
# than Chromium where this project's policy demands it (e.g. mixed-sign
# resource types, see README "Legacy filter syntax policy").
#
# Usage: scripts/validate_test.sh [path/to/ruleset_converter]
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
validator="$root/scripts/validate.sh"
converter="${1:-$root/deps/ruleset_converter}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

accept=(
  '||example.com^$third-party'
  '||example.com/path$~third-party'
  '||example.com^$script,image,third-party'
  '||example.com^$~script,~image'
  '||example.com^$domain=a.com|~bb.com'
  '||example.com^$match-case,third-party'
  '@@||example.com^$document'
  '@@||example.com^$document,genericblock'
  '|https://example.com/ads'
)

reject=(
  # Missing third-party guard on a bare domain block.
  '||example.com^'
  # Duplicate or conflicting modifiers.
  '||example.com^$script,image,script'
  '||example.com^$third-party,~third-party'
  '||example.com^$match-case,match-case'
  '||example.com^$domain=a.com,domain=b.com'
  # Mixed-sign resource types are order-dependent in Chromium's parser.
  '||example.com^$script,~image'
  # Activation types: exception-only, never combined with resource types.
  '||example.com^$document'
  '@@||example.com^$document,script'
  # Malformed domain= lists.
  '||example.com^$domain=a.com||b.com'
  '||example.com^$domain=|a.com'
  '||example.com^$domain=a.com|a.com'
  '||example.com^$domain=a.com|~a.com'
  '||example.com^$domain=A.com'
  '||example.com^$domain=localhost'
  # Unsupported or empty modifiers.
  '||example.com^$popup'
  '||example.com^$third-party,'
  '||example.com^$'
  # Host shape.
  '||1.2.3.4^$third-party'
  '||Example.com^$third-party'
  '||exa_mple.com^$third-party'
  '||example.com:8080^$third-party'
  '||localhost^$third-party'
  # Unsupported syntax classes.
  'example.com##.ad'
  '/regex/'
  '||example.com^ $third-party'
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
