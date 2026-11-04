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
command -v realpath >/dev/null || {
  echo "ERROR: realpath is required for safe build-path preflight" >&2
  exit 1
}
command -v stat >/dev/null || {
  echo "ERROR: stat is required for safe build-path preflight" >&2
  exit 1
}
command -v awk >/dev/null || {
  echo "ERROR: awk is required" >&2
  exit 1
}

# Use the installed toolchain for this check so an unavailable auto-download
# cannot fail after the previous generated output has already been removed.
go_version="$(GOTOOLCHAIN=local go env GOVERSION 2>/dev/null)" || {
  echo "ERROR: unable to determine the installed Go toolchain version" >&2
  exit 1
}
if [[ "$go_version" =~ ^go([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  go_major="${BASH_REMATCH[1]}"
  go_minor="${BASH_REMATCH[2]}"
  go_patch="${BASH_REMATCH[3]}"
else
  printf 'ERROR: unrecognized Go toolchain version: %s\n' "$go_version" >&2
  exit 1
fi
if (( go_major < 1 || (go_major == 1 && (go_minor < 27 || (go_minor == 27 && go_patch < 1))) )); then
  printf 'ERROR: Go 1.27.1 or newer is required (found %s)\n' "$go_version" >&2
  exit 1
fi

[[ "$MAX_RULESET_BYTES" =~ ^[1-9][0-9]{0,17}$ ]] || {
  echo "ERROR: MAX_RULESET_BYTES must be a positive decimal integer of at most 18 digits" >&2
  exit 1
}

# Refuse ambiguous managed paths before cleanup. In particular, rm -rf on a
# child of a symlinked `build/` parent would operate outside this checkout.
for managed_dir in build filters dist build/work build/source-cache; do
  if [[ -L "$managed_dir" ]]; then
    printf 'ERROR: managed build directory must not be a symlink: %s\n' "$managed_dir" >&2
    exit 1
  fi
  if [[ -e "$managed_dir" && ! -d "$managed_dir" ]]; then
    printf 'ERROR: managed build path is not a directory: %s\n' "$managed_dir" >&2
    exit 1
  fi
done

# These files are replaced/truncated by the script. Never follow a symlink or
# truncate a non-regular object at one of these paths.
for managed_file in build/legacy-filter-builder build/filtrite build/release-summary.md; do
  if [[ -L "$managed_file" ]]; then
    printf 'ERROR: managed build file must not be a symlink: %s\n' "$managed_file" >&2
    exit 1
  fi
  if [[ -e "$managed_file" && ! -f "$managed_file" ]]; then
    printf 'ERROR: managed build path is not a regular file: %s\n' "$managed_file" >&2
    exit 1
  fi
done
if [[ ( -e deps || -L deps ) && ! -d deps ]]; then
  echo "ERROR: deps must be a directory (or a symlink to a directory)" >&2
  exit 1
fi

# Validate source inputs before removing the previous generated artifacts.
shopt -s nullglob
manifests=(lists/*.txt)
(( ${#manifests[@]} > 0 )) || {
  echo "ERROR: no manifests found in lists/*.txt" >&2
  exit 1
}
for manifest in "${manifests[@]}"; do
  [[ -f "$manifest" && -r "$manifest" ]] || {
    printf 'ERROR: source manifest must be a readable regular file: %s\n' "$manifest" >&2
    exit 1
  }
done

# Custom rules are optional only when the path is truly absent. A dangling
# symlink, directory, or unreadable file is a configuration error, not a
# reason to silently publish a list without the requested rules.
custom=custom-rules.txt
if [[ -e "$custom" || -L "$custom" ]]; then
  [[ -f "$custom" && -r "$custom" ]] || {
    printf 'ERROR: custom rules must be a readable regular file: %s\n' "$custom" >&2
    exit 1
  }
else
  custom=""
fi

# build.sh removes these directories and overwrites the listed files before
# the Go builder's deeper path checks can run. Resolve symlinked input paths
# now so a manifest/custom-rules file cannot disappear or be truncated during
# this earlier cleanup stage. Existing hard links to a managed file are also
# detected by device/inode identity.
managed_dirs=()
for managed_dir in filters dist build/work build/source-cache; do
  managed_dirs+=("$(realpath -m -- "$managed_dir")")
done

# `deps` may be shared through a symlink, but it must not live inside a tree
# removed during this build or the cached converter would be deleted too.
if [[ -e deps || -L deps ]]; then
  deps_resolved="$(realpath -e -- deps)" || {
    echo "ERROR: unable to resolve deps directory" >&2
    exit 1
  }
  for managed in "${managed_dirs[@]}"; do
    if [[ "$deps_resolved" == "$managed" || "$deps_resolved" == "$managed/"* ]]; then
      printf 'ERROR: deps resolves inside a directory cleared by build.sh: %s\n' "$deps_resolved" >&2
      exit 1
    fi
  done
fi

managed_file_paths=(build/legacy-filter-builder build/filtrite build/release-summary.md deps/ruleset_converter)
managed_files=()
for managed_file in "${managed_file_paths[@]}"; do
  managed_files+=("$(realpath -m -- "$managed_file")")
done

check_input_path() {
  local input="$1"
  local label="$2"
  local resolved input_identity managed_identity managed

  if [[ "$input" == *$'\n'* ]]; then
    printf 'ERROR: %s path must not contain a newline: %q\n' "$label" "$input" >&2
    return 1
  fi
  resolved="$(realpath -e -- "$input")" || {
    printf 'ERROR: unable to resolve %s path: %s\n' "$label" "$input" >&2
    return 1
  }
  for managed in "${managed_dirs[@]}"; do
    if [[ "$resolved" == "$managed" || "$resolved" == "$managed/"* ]]; then
      printf 'ERROR: %s resolves inside a directory cleared by build.sh: %s\n' "$label" "$resolved" >&2
      return 1
    fi
  done
  input_identity="$(stat -Lc '%d:%i' -- "$input")" || {
    printf 'ERROR: unable to inspect %s file identity: %s\n' "$label" "$input" >&2
    return 1
  }
  for i in "${!managed_files[@]}"; do
    managed="${managed_files[$i]}"
    if [[ "$resolved" == "$managed" ]]; then
      printf 'ERROR: %s aliases a file overwritten by build.sh: %s\n' "$label" "$resolved" >&2
      return 1
    fi
    if [[ -e "${managed_file_paths[$i]}" ]]; then
      managed_identity="$(stat -Lc '%d:%i' -- "${managed_file_paths[$i]}")" || return 1
      if [[ "$input_identity" == "$managed_identity" ]]; then
        printf 'ERROR: %s is hard-linked to a file overwritten by build.sh: %s\n' "$label" "${managed_file_paths[$i]}" >&2
        return 1
      fi
    fi
  done
}

for manifest in "${manifests[@]}"; do
  check_input_path "$manifest" "source manifest" || exit 1
done
if [[ -n "$custom" ]]; then
  check_input_path "$custom" "custom rules" || exit 1
fi

# Managed outputs must not alias one another or unrelated files through hard
# links. The summary is truncated and the helper binaries are rebuilt in place.
for ((i = 0; i < ${#managed_file_paths[@]}; i++)); do
  managed_path="${managed_file_paths[$i]}"
  [[ -e "$managed_path" ]] || continue
  for ((j = i + 1; j < ${#managed_file_paths[@]}; j++)); do
    [[ -e "${managed_file_paths[$j]}" ]] || continue
    if [[ "${managed_files[$i]}" == "${managed_files[$j]}" ]]; then
      printf 'ERROR: managed build files resolve to the same path: %s and %s\n' \
        "$managed_path" "${managed_file_paths[$j]}" >&2
      exit 1
    fi
  done
  # Every managed file except the last one (the cached converter, whose
  # refresh path performs its own link check below) must be free of extra
  # hard links before it is truncated or rebuilt in place.
  if (( i < ${#managed_file_paths[@]} - 1 )); then
    managed_link_count="$(stat -Lc '%h' -- "$managed_path")" || {
      printf 'ERROR: unable to inspect managed build file links: %s\n' "$managed_path" >&2
      exit 1
    }
    if (( managed_link_count > 1 )); then
      printf 'ERROR: managed build file has multiple hard links: %s\n' "$managed_path" >&2
      exit 1
    fi
  fi
done

converter_bin=deps/ruleset_converter
if [[ -L "$converter_bin" ]]; then
  [[ -f "$converter_bin" && -x "$converter_bin" && -s "$converter_bin" ]] || {
    printf 'ERROR: cached converter symlink must target a non-empty executable file: %s\n' "$converter_bin" >&2
    exit 1
  }
elif [[ -e "$converter_bin" && ! -f "$converter_bin" ]]; then
  printf 'ERROR: cached converter path is not a regular file: %s\n' "$converter_bin" >&2
  exit 1
fi
if [[ -e "$converter_bin" ]]; then
  converter_resolved="$(realpath -e -- "$converter_bin")" || {
    printf 'ERROR: unable to resolve cached converter path: %s\n' "$converter_bin" >&2
    exit 1
  }
  for managed in "${managed_dirs[@]}"; do
    if [[ "$converter_resolved" == "$managed" || "$converter_resolved" == "$managed/"* ]]; then
      printf 'ERROR: cached converter resolves inside a directory cleared by build.sh: %s\n' "$converter_resolved" >&2
      exit 1
    fi
  done
fi

converter_needs_download=0
if [[ ! -f "$converter_bin" || ! -x "$converter_bin" || ! -s "$converter_bin" ]]; then
  converter_needs_download=1
  if [[ -e "$converter_bin" ]]; then
    converter_link_count="$(stat -Lc '%h' -- "$converter_bin")" || {
      printf 'ERROR: unable to inspect cached converter links: %s\n' "$converter_bin" >&2
      exit 1
    }
    if (( converter_link_count > 1 )); then
      printf 'ERROR: cached converter has multiple hard links and cannot be safely refreshed: %s\n' "$converter_bin" >&2
      exit 1
    fi
  fi
  [[ "$CONVERTER_URL" == https://* && "$CONVERTER_URL" != *[[:space:]]* ]] || {
    echo "ERROR: CONVERTER_URL must be a non-empty HTTPS URL" >&2
    exit 1
  }
  converter_authority="${CONVERTER_URL#https://}"
  converter_authority="${converter_authority%%[/?#]*}"
  converter_port=""
  if [[ "$converter_authority" =~ ^\[[[:xdigit:]:.]+\](:[0-9]{1,5})?$ ]]; then
    if [[ "$converter_authority" =~ :([0-9]{1,5})$ ]]; then
      converter_port="${BASH_REMATCH[1]}"
    fi
  # Validate hostnames as dot-separated DNS labels (1-63 chars, alphanumeric
  # with interior hyphens, or a single label such as localhost). A loose
  # character class would accept malformed names like "a..b" or "-bad" here
  # and only fail inside curl, after prior output has already been deleted.
  elif [[ "$converter_authority" =~ ^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(:[0-9]{1,5})?$ ]]; then
    if [[ "$converter_authority" =~ :([0-9]{1,5})$ ]]; then
      converter_port="${BASH_REMATCH[1]}"
    fi
  else
    echo "ERROR: CONVERTER_URL must be HTTPS with a valid host and optional port, without credentials" >&2
    exit 1
  fi
  if [[ -n "$converter_port" ]]; then
    converter_port_number=$((10#$converter_port))
    if (( converter_port_number < 1 || converter_port_number > 65535 )); then
      echo "ERROR: CONVERTER_URL port must be between 1 and 65535" >&2
      exit 1
    fi
  fi
  command -v curl >/dev/null || {
    echo "ERROR: curl is required to download ruleset_converter" >&2
    exit 1
  }
  command -v unzip >/dev/null || {
    echo "ERROR: unzip is required to extract ruleset_converter" >&2
    exit 1
  }
  command -v mktemp >/dev/null || {
    echo "ERROR: mktemp is required to download ruleset_converter" >&2
    exit 1
  }
  if [[ -n "${CONVERTER_SHA256:-}" ]]; then
    [[ "$CONVERTER_SHA256" =~ ^[[:xdigit:]]{64}$ ]] || {
      echo "ERROR: CONVERTER_SHA256 must contain exactly 64 hexadecimal characters" >&2
      exit 1
    }
    command -v sha256sum >/dev/null || {
      echo "ERROR: sha256sum is required when CONVERTER_SHA256 is set" >&2
      exit 1
    }
  fi
fi

# Generated output is rebuilt from scratch so removed lists/rules never linger.
rm -rf filters dist build/work build/source-cache
mkdir -p deps filters dist build/work build/source-cache

# Release description generated automatically from successful builds.
: > build/release-summary.md

if (( converter_needs_download )); then
  tmp="$(mktemp)"
  trap 'rm -f "$tmp"' EXIT

  curl \
    --fail \
    --location \
    --proto '=https' \
    --proto-redir '=https' \
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
  [[ -f "$converter_bin" && ! -L "$converter_bin" && -s "$converter_bin" ]] || {
    echo "ERROR: converter archive did not produce a non-empty regular file" >&2
    exit 1
  }
  chmod +x "$converter_bin"

  # The EXIT trap only covers this download block; disarm it here instead of
  # leaking it to the end of the script.
  trap - EXIT
  rm -f "$tmp"
fi

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
    --converter "$converter_bin" \
    --log "$work/ruleset-converter.log"

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
