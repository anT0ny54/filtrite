#!/usr/bin/env bash
set -euo pipefail

log () {
    echo "$(date +"%m/%d/%Y %H:%M:%S")" "$@"
}

cleanup() {
    rm -f filtrite >/dev/null 2>&1 || true
}

trap cleanup EXIT

echo "::group::Init"
log "Init"

# Install dependencies only if missing
if ! command -v unzip >/dev/null 2>&1 || ! command -v wget >/dev/null 2>&1; then
    log "Installing missing dependencies (unzip, wget)"
    sudo apt-get update
    sudo apt-get install -y unzip wget
fi

echo "::endgroup::"

echo "::group::Build executable"
log "Building"
go build -v -o filtrite
echo "::endgroup::"

echo "::group::Downloading latest ruleset_converter build"

install_bromite_ruleset_converter() {
    log "Downloading from latest Cromite release"
    rm -rf deps || true
    mkdir -p deps
    wget -O "deps/ruleset_converter" "https://github.com/uazo/cromite/releases/latest/download/ruleset_converter"
}

install_selfbuilt_ruleset_converter() {
    log "Downloading from latest self-built release"
    rm -rf deps || true
    mkdir -p deps

    wget -O "subresource_filter_tools_linux.zip" "https://github.com/xarantolus/subresource_filter_tools/releases/latest/download/subresource_filter_tools_linux-x64.zip"

    unzip -ou "subresource_filter_tools_linux.zip" -d deps

    rm "subresource_filter_tools_linux.zip"
}

# Prefer self-built; fall back to Bromite if it fails
if ! install_selfbuilt_ruleset_converter; then
    log "Self-built download failed; falling back to Bromite ruleset_converter"
    install_bromite_ruleset_converter
fi

echo "::endgroup::"

echo "::group::Other setup steps"
chmod +x filtrite
chmod +x deps/ruleset_converter
mkdir -p dist
mkdir -p logs
echo "::endgroup::"

# If the default list file exists, overwrite it with the official list
if [[ -f "lists/bromite-default.txt" ]]; then
    echo "::group::Downloading official list"
    wget -O "lists/bromite-default.txt" "https://raw.githubusercontent.com/bromite/filters/master/lists.txt"
    echo "::endgroup::"
fi

# Generate filter lists
./filtrite

echo "::group::Cleanup"
cleanup

# Restore the downloaded list to the previous text, in case this is run locally
if [[ -f "lists/bromite-default.txt" ]]; then
    git restore lists/bromite-default.txt || true
fi

echo "::endgroup::"
