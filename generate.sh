#!/usr/bin/env bash

set -Eeuo pipefail

readonly BINARY="filtrite"
readonly DEP_DIR="deps"
readonly DIST_DIR="dist"
readonly LOG_DIR="logs"

readonly LIST_FILE="lists/adblock.txt"
readonly LIST_URL="https://raw.githubusercontent.com/bromite/filters/master/lists.txt"

readonly SELF_BUILT_URL="https://github.com/xarantolus/subresource_filter_tools/releases/latest/download/subresource_filter_tools_linux-x64.zip"
readonly CROMITE_URL="https://github.com/uazo/cromite/releases/latest/download/ruleset_converter"

WORK_DIR=""
LIST_BACKUP=""
RESTORE_LIST=0

log() {
    printf '[%s] %s\n' "$(date '+%m/%d/%Y %H:%M:%S')" "$*"
}

die() {
    log "ERROR: $*"
    exit 1
}

cleanup() {
    local status=$?

    if [[ "$RESTORE_LIST" -eq 1 &&
          -n "$LIST_BACKUP" &&
          -f "$LIST_BACKUP" ]]; then
        if ! cp -p "$LIST_BACKUP" "$LIST_FILE"; then
            log "WARNING: failed to restore $LIST_FILE"
        fi
    fi

    if [[ -n "$WORK_DIR" && -d "$WORK_DIR" ]]; then
        rm -rf "$WORK_DIR" || true
    fi

    rm -f "$BINARY" || true

    exit "$status"
}

trap cleanup EXIT

require_command() {
    local command_name="$1"

    command -v "$command_name" >/dev/null 2>&1
}

install_dependencies() {
    local missing=()

    require_command unzip || missing+=("unzip")
    require_command wget || missing+=("wget")

    if [[ "${#missing[@]}" -eq 0 ]]; then
        return
    fi

    log "Missing dependencies: ${missing[*]}"

    require_command apt-get || {
        die "apt-get is required to install: ${missing[*]}"
    }

    if [[ "$EUID" -eq 0 ]]; then
        apt-get update
        apt-get install -y "${missing[@]}"
    else
        require_command sudo || {
            die "sudo is required to install: ${missing[*]}"
        }

        sudo apt-get update
        sudo apt-get install -y "${missing[@]}"
    fi
}

download_file() {
    local url="$1"
    local destination="$2"

    wget \
        --fail \
        --location \
        --retry-connrefused \
        --tries=3 \
        --timeout=30 \
        --output-document="$destination" \
        "$url"
}

install_selfbuilt_ruleset_converter() {
    local archive="$WORK_DIR/subresource_filter_tools_linux.zip"
    local converter_path

    log "Downloading latest self-built ruleset_converter"

    rm -rf "$DEP_DIR"
    mkdir -p "$DEP_DIR"

    download_file "$SELF_BUILT_URL" "$archive"
    unzip -oq "$archive" -d "$DEP_DIR"

    converter_path="$(
        find "$DEP_DIR" \
            -type f \
            -name "ruleset_converter" \
            -print -quit
    )"

    if [[ -z "$converter_path" ]]; then
        log "Self-built archive does not contain ruleset_converter"
        return 1
    fi

    if [[ "$converter_path" != "$DEP_DIR/ruleset_converter" ]]; then
        cp -- "$converter_path" "$DEP_DIR/ruleset_converter"
    fi

    chmod +x "$DEP_DIR/ruleset_converter"

    [[ -s "$DEP_DIR/ruleset_converter" ]] || {
        log "Self-built ruleset_converter is empty"
        return 1
    }

    log "Using self-built ruleset_converter"
}

install_cromite_ruleset_converter() {
    local destination="$DEP_DIR/ruleset_converter"

    log "Downloading latest Cromite ruleset_converter"

    rm -rf "$DEP_DIR"
    mkdir -p "$DEP_DIR"

    download_file "$CROMITE_URL" "$destination"
    chmod +x "$destination"

    [[ -s "$destination" ]] || {
        die "Downloaded Cromite ruleset_converter is empty"
    }

    log "Using Cromite ruleset_converter"
}

install_ruleset_converter() {
    if install_selfbuilt_ruleset_converter; then
        return
    fi

    log "Self-built download failed; falling back to Cromite"
    install_cromite_ruleset_converter
}

build_filtrite() {
    log "Building filtrite"

    go build \
        -trimpath \
        -v \
        -o "$BINARY" \
        .

    chmod +x "$BINARY"

    [[ -x "$BINARY" ]] || {
        die "Build did not produce an executable: $BINARY"
    }
}

prepare_directories() {
    mkdir -p "$DIST_DIR" "$LOG_DIR"
}

download_list() {
    local temporary_list

    [[ -f "$LIST_FILE" ]] || {
        log "$LIST_FILE does not exist; skipping official list download"
        return
    }

    LIST_BACKUP="$WORK_DIR/adblock.txt.backup"

    log "Backing up $LIST_FILE"
    cp -p "$LIST_FILE" "$LIST_BACKUP"
    RESTORE_LIST=1

    temporary_list="$WORK_DIR/adblock.txt"

    log "Downloading official filter list"

    download_file "$LIST_URL" "$temporary_list"

    [[ -s "$temporary_list" ]] || {
        die "Downloaded filter list is empty"
    }

    # Replace only after the download has completed successfully.
    mv -- "$temporary_list" "$LIST_FILE"
}

main() {
    WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/filtrite-build.XXXXXX")"

    echo "::group::Init"
    log "Initializing build"
    install_dependencies
    echo "::endgroup::"

    echo "::group::Build executable"
    build_filtrite
    echo "::endgroup::"

    echo "::group::Download ruleset converter"
    install_ruleset_converter
    echo "::endgroup::"

    echo "::group::Prepare directories"
    prepare_directories
    echo "::endgroup::"

    echo "::group::Download adblock list"
    download_list
    echo "::endgroup::"

    echo "::group::Generate filter lists"
    "./$BINARY"
    echo "::endgroup::"

    echo "::group::Cleanup"
    log "Generation completed successfully"
    echo "::endgroup::"
}

main "$@"
