#!/bin/bash
# Install the pinned LLGo Wasmer fork used by local and CI WASI tests.
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "${SCRIPT_DIR}/llgo_cache_dir.sh"
WASMER_BIN_DIR="$(llgo_cache_dir)/bin"
WASMER_REPOSITORY=xgo-dev/wasmer
WASMER_VERSION=llgo-v7.5.0.1
WASMER_NAME=wasmer

case "$(uname -s)-$(uname -m)" in
    Darwin-arm64)
        asset=wasmer-darwin-arm64.tar.gz
        digest=d76adbb9da9caff280404efba88f2a863e525c4c476a9c63543fd8650a35cc4f ;;
    Linux-x86_64)
        asset=wasmer-linux-amd64.tar.gz
        digest=2d7da51fe5b86fcad5268a716d3ca5a257cd0b50efdd23fac1232ee32e029950 ;;
    Linux-aarch64|Linux-arm64)
        asset=wasmer-linux-aarch64.tar.gz
        digest=cd229e0fe14fa358bac52bc26f5149ca2744e72864a2946e34ce3b361f9c6dfd ;;
    Linux-riscv64)
        asset=wasmer-linux-riscv64.tar.gz
        digest=28d42d8a80ac6423495127001af609e8310ec3c1c29abef7e54cba7ed344ab6c ;;
    MINGW*-x86_64|MSYS*-x86_64|CYGWIN*-x86_64)
        # This standalone host executable also runs LLGo's MinGW-built guests.
        asset=wasmer-windows-amd64.tar.gz
        digest=db380e5259f0de8720918dea4012377ec2408aa106015e120fa1ea6e6fe8bef5
        WASMER_NAME=wasmer.exe ;;
    *)
        echo "Wasmer ${WASMER_VERSION} has no supported prebuilt CLI for $(uname -s)-$(uname -m); install it from source and put wasmer on PATH." >&2
        exit 1 ;;
esac

build_id="${WASMER_REPOSITORY}-${WASMER_VERSION}-${digest}"
id_file="${WASMER_BIN_DIR}/${WASMER_NAME}.llgo-build-id"
if [ -x "${WASMER_BIN_DIR}/${WASMER_NAME}" ] && [ -f "${id_file}" ] && \
    [ "$(cat "${id_file}")" = "${build_id}" ]; then
    echo "Using cached Wasmer at ${WASMER_BIN_DIR}/${WASMER_NAME}"
else
    staging=$(mktemp -d)
    trap 'rm -rf "${staging}"' EXIT
    curl --fail --location --retry 3 \
        "https://github.com/${WASMER_REPOSITORY}/releases/download/${WASMER_VERSION}/${asset}" \
        --output "${staging}/${asset}"
    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "${staging}/${asset}")
    else
        actual=$(shasum -a 256 "${staging}/${asset}")
    fi
    if [ "${actual%% *}" != "${digest}" ]; then
        echo "Wasmer archive checksum mismatch: ${asset}" >&2
        exit 1
    fi
    tar -xzf "${staging}/${asset}" -C "${staging}" "bin/${WASMER_NAME}"
    chmod +x "${staging}/bin/${WASMER_NAME}"
    "${staging}/bin/${WASMER_NAME}" --version
    mkdir -p "${WASMER_BIN_DIR}"
    cp "${staging}/bin/${WASMER_NAME}" "${WASMER_BIN_DIR}/${WASMER_NAME}"
    printf '%s\n' "${build_id}" > "${id_file}"
fi

if [ -n "${GITHUB_PATH:-}" ]; then
    printf '%s\n' "${WASMER_BIN_DIR}" >> "${GITHUB_PATH}"
fi
echo "Wasmer ${WASMER_REPOSITORY}@${WASMER_VERSION}"
echo "Add ${WASMER_BIN_DIR} to PATH to run WASI programs with Wasmer."
