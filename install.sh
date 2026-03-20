#!/usr/bin/env bash
set -euo pipefail

REPO="isomorphx/pudding"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="pudding"

main() {
    detect_platform
    get_latest_version
    download_and_verify
    install_binary
    cleanup
    echo ""
    echo "Pudding ${VERSION} installed to ${INSTALL_DIR}/${BINARY_NAME}"
    echo ""
    echo "Run 'pudding doctor' to verify your setup."
}

detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "${OS}" in
        linux) OS="linux" ;;
        darwin) OS="darwin" ;;
        *) fatal "Unsupported OS: ${OS}" ;;
    esac

    case "${ARCH}" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *) fatal "Unsupported architecture: ${ARCH}" ;;
    esac
}

get_latest_version() {
    VERSION=$(fetch "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
    if [ -z "${VERSION}" ]; then
        fatal "Could not determine latest version"
    fi
}

download_and_verify() {
    TMPDIR=$(mktemp -d)
    trap 'rm -rf "${TMPDIR}"' EXIT

    ARCHIVE="pudding_${OS}_${ARCH}.tar.gz"
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
    CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

    echo "Downloading Pudding ${VERSION} for ${OS}/${ARCH}..."
    fetch "${DOWNLOAD_URL}" > "${TMPDIR}/${ARCHIVE}"
    fetch "${CHECKSUMS_URL}" > "${TMPDIR}/checksums.txt"

    EXPECTED=$(grep "${ARCHIVE}" "${TMPDIR}/checksums.txt" | awk '{print $1}')
    if [ -z "${EXPECTED}" ]; then
        fatal "Archive ${ARCHIVE} not found in checksums"
    fi

    ACTUAL=$(sha256sum "${TMPDIR}/${ARCHIVE}" 2>/dev/null || shasum -a 256 "${TMPDIR}/${ARCHIVE}" | awk '{print $1}')
    # Normalize: extract just the hash
    ACTUAL=$(echo "${ACTUAL}" | awk '{print $1}')

    if [ "${EXPECTED}" != "${ACTUAL}" ]; then
        fatal "Checksum verification failed. Expected ${EXPECTED}, got ${ACTUAL}"
    fi

    tar -xzf "${TMPDIR}/${ARCHIVE}" -C "${TMPDIR}" "${BINARY_NAME}"
}

install_binary() {
    if [ -w "${INSTALL_DIR}" ]; then
        mv "${TMPDIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
    else
        echo "Elevated permissions required to install to ${INSTALL_DIR}"
        sudo mv "${TMPDIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
    fi
    chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
}

cleanup() {
    rm -rf "${TMPDIR}" 2>/dev/null || true
}

fetch() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "$1"
    else
        fatal "curl or wget is required"
    fi
}

fatal() {
    echo "Error: $1" >&2
    exit 1
}

main

