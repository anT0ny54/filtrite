#!/usr/bin/env bash
# Regression tests for scripts/validate.sh: the validator must accept every
# rule shape the Go normalizer emits and reject known-malformed output. When
# a real ruleset_converter is available (after ./build.sh), each known-good
# fixture rule is additionally run through Chromium's parser. Rules the
# validator rejects are not sent to the converter; the full generated list is
# converted by build.sh itself. See README.md "Validation".
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Known-good generated rules: every one must pass the validator. These mirror
# the exact normalized shape the builder emits (bare domain blocks always
# carry the $third-party guard added by ExternalSorter.Add).
cat >"$work/good.txt" <<'RULES'
||example.com^$third-party
||tracker.example^$third-party
@@||cdn.example.com^$document
|https://example.com/ads.js|
|https://example.com/price%24sale.js|
||ads.example.com/path$script,image
RULES
bash "$root/scripts/validate.sh" "$work/good.txt" >/dev/null

# Known-malformed rules: each file must be rejected. Cases mirror the
# validator's diagnostic reasons one by one so a regression names itself.
cat >"$work/bad-guard.txt" <<'RULES'
||example.com^
RULES
cat >"$work/bad-prefix.txt" <<'RULES'
example.com^
RULES
cat >"$work/bad-case.txt" <<'RULES'
||UPPER.EXAMPLE^$third-party
RULES
cat >"$work/bad-mixed-sign.txt" <<'RULES'
||example.com^$script,~image
RULES
cat >"$work/bad-cosmetic.txt" <<'RULES'
example.com##.ad
RULES
cat >"$work/bad-activation.txt" <<'RULES'
||example.com^$document
RULES
cat >"$work/bad-whitespace.txt" <<'RULES'
|https://example.com/a b|
RULES
cat >"$work/bad-ip.txt" <<'RULES'
||1.2.3.4^$third-party
RULES
cat >"$work/bad-raw-dollar.txt" <<'RULES'
|https://example.com/price$sale.js|
RULES
cat >"$work/bad-unterminated-host.txt" <<'RULES'
||example.com
RULES

for bad in "$work"/bad-*.txt; do
  if bash "$root/scripts/validate.sh" "$bad" >/dev/null 2>&1; then
    echo "FAIL: validator accepted malformed rules in $bad" >&2
    exit 1
  fi
done

# The validator reads generated regular files only; it must reject a FIFO
# before awk attempts to read from it.
if command -v mkfifo >/dev/null 2>&1; then
  mkfifo "$work/filter-pipe"
  if bash "$root/scripts/validate.sh" "$work/filter-pipe" >/dev/null 2>&1; then
    echo "FAIL: validator accepted a FIFO input" >&2
    exit 1
  fi
fi

# Cross-check with Chromium's real parser when a converter is available.
converter="$root/deps/ruleset_converter"
if [[ -x "$converter" && -s "$converter" ]]; then
  while IFS= read -r rule; do
    [[ -n "$rule" ]] || continue
    printf '%s\n' "$rule" >"$work/single.txt"
    if ! "$converter" \
      --input_format=filter-list \
      --output_format=unindexed-ruleset \
      --input_files="$work/single.txt" \
      --output_file="$work/single.dat" >/dev/null 2>&1; then
      echo "FAIL: converter rejected validator-accepted rule: $rule" >&2
      exit 1
    fi
  done <"$work/good.txt"
  echo "OK: validator fixtures pass and converter cross-check passed"
else
  echo "OK: validator fixtures pass (converter cross-check skipped: no deps/ruleset_converter)"
fi
