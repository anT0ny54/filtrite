#!/usr/bin/env bash
set -Eeuo pipefail

log() {
    printf '[%s] %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"
}

cleanup() {
    rm -f filtrite 2>/dev/null || true
}

trap cleanup EXIT

echo "::group::Init"
log "Initializing build"

for command in go wget unzip; do
    if ! command -v "$command" >/dev/null 2>&1; then
        log "Missing dependency: $command"
        sudo apt-get update
        sudo apt-get install -y "$command"
    fi
done

mkdir -p dist logs tmp deps
rm -rf dist/* logs/* tmp/* deps/*

echo "::endgroup::"

echo "::group::Build executable"
log "Building Filtrite"
go build -trimpath -ldflags="-s -w" -o filtrite .
chmod +x filtrite
echo "::endgroup::"

echo "::group::Downloading ruleset converter"

install_bromite_ruleset_converter() {
    log "Downloading latest Cromite ruleset_converter"
    wget --https-only --retry-connrefused --tries=3 --timeout=30 \
        -O deps/ruleset_converter \
        "https://github.com/uazo/cromite/releases/latest/download/ruleset_converter"
}

install_selfbuilt_ruleset_converter() {
    log "Downloading latest self-built ruleset_converter"
    local archive="subresource_filter_tools_linux.zip"

    wget --https-only --retry-connrefused --tries=3 --timeout=30 \
        -O "$archive" \
        "https://github.com/xarantolus/subresource_filter_tools/releases/latest/download/subresource_filter_tools_linux-x64.zip"

    unzip -oq "$archive" -d deps
    rm -f "$archive"

    if [[ ! -f deps/ruleset_converter ]]; then
        local found
        found="$(find deps -type f -name ruleset_converter -print -quit)"
        [[ -n "$found" ]] || return 1
        mv "$found" deps/ruleset_converter
    fi
}

if ! install_selfbuilt_ruleset_converter; then
    log "Self-built converter unavailable; using Cromite release"
    rm -rf deps/*
    install_bromite_ruleset_converter
fi

test -s deps/ruleset_converter
chmod +x deps/ruleset_converter

echo "::endgroup::"

# Keep generated release notes deterministic. The repository's release.md is
# only a template; each run should contain results from this run.
printf '%s\n\n' 'Automatic filter list generation for Bromite and Cromite.' > release.md

# If the default list file exists, refresh it from the official source.
if [[ -f lists/bromite-default.txt ]]; then
    echo "::group::Downloading official list"
    wget --https-only --retry-connrefused --tries=3 --timeout=30 \
        -O lists/bromite-default.txt \
        "https://raw.githubusercontent.com/bromite/filters/master/lists.txt"
    echo "::endgroup::"
fi

echo "::group::Generate filter lists"
./filtrite
echo "::endgroup::"

echo "::group::Cleanup"
cleanup

# Restore the source list if it is tracked in the repository.
if git ls-files --error-unmatch lists/bromite-default.txt >/dev/null 2>&1; then
    git restore -- lists/bromite-default.txt || true
fi
echo "::endgroup::"
