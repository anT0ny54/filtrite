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

        if ! command -v sudo >/dev/null 2>&1; then
            log "sudo is required to install missing dependencies"
            exit 1
        fi

        sudo apt-get update
        sudo apt-get install -y "$command"
    fi
done

mkdir -p dist logs tmp deps

rm -rf dist/*
rm -rf logs/*
rm -rf tmp/*
rm -rf deps/*

echo "::endgroup::"

echo "::group::Build executable"
log "Building Filtrite"

go build \
    -trimpath \
    -ldflags="-s -w" \
    -o filtrite \
    .

chmod +x filtrite

echo "::endgroup::"

echo "::group::Downloading ruleset converter"

install_ruleset_converter() {
    local archive="tmp/subresource_filter_tools_linux-x64.zip"
    local converter

    log "Downloading latest self-built ruleset_converter"

    wget \
        --https-only \
        --retry-connrefused \
        --tries=3 \
        --timeout=30 \
        -O "$archive" \
        "https://github.com/xarantolus/subresource_filter_tools/releases/latest/download/subresource_filter_tools_linux-x64.zip"

    if [[ ! -s "$archive" ]]; then
        log "Downloaded converter archive is empty"
        return 1
    fi

    if ! unzip -tq "$archive" >/dev/null; then
        log "Downloaded converter archive is invalid"
        return 1
    fi

    unzip -oq "$archive" -d deps

    converter="$(find deps -type f -name 'ruleset_converter' -print -quit)"

    if [[ -z "$converter" ]]; then
        log "ruleset_converter was not found in the downloaded archive"
        log "Archive contents:"
        unzip -l "$archive" >&2
        return 1
    fi

    if [[ "$converter" != "deps/ruleset_converter" ]]; then
        mv "$converter" deps/ruleset_converter
    fi

    chmod +x deps/ruleset_converter

    if [[ ! -s deps/ruleset_converter ]]; then
        log "ruleset_converter was not installed correctly"
        return 1
    fi
}

if ! install_ruleset_converter; then
    log "Unable to download or install ruleset_converter"
    exit 1
fi

log "Installed deps/ruleset_converter"

echo "::endgroup::"

# Keep generated release notes deterministic.
printf '%s\n\n' \
    'Automatic filter list generation for the configured filter lists.' \
    > release.md

echo "::group::Generate filter lists"

./filtrite

echo "::endgroup::"

echo "::group::Cleanup"

cleanup

echo "::endgroup::"
