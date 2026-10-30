#!/usr/bin/env bash
# Keep the build SDK intact; stage separately pinned executable payloads.
set -euo pipefail

source_root=$(cd "${1:-.sysroot}" && pwd -P)
destination=${2:-.release-toolchains}
mkdir -p "$destination"
destination=$(cd "$destination" && pwd -P)
case "$destination/" in
    "$source_root/"*) echo 'Release staging must be outside the build sysroot' >&2; exit 1 ;;
esac
case "$source_root/" in
    "$destination/"*) echo 'Release staging must not contain the build sysroot' >&2; exit 1 ;;
esac

runtime_root=$(cd "${3:-.esp-runtime}" && pwd -P)
case "$destination/" in
    "$runtime_root/"*) echo 'Release staging must be outside the runtime source' >&2; exit 1 ;;
esac
case "$runtime_root/" in
    "$destination/"*) echo 'Release staging must not contain the runtime source' >&2; exit 1 ;;
esac
stage=$(mktemp -d "$destination/.staging.XXXXXX")
trap 'rm -rf "$stage"' EXIT
for platform in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
    sdk="$source_root/$platform/crosscompile/clang"
    source="$runtime_root/$platform"
    target="$stage/$platform"
    test -f "$source/LICENSE-LLVM.txt"
    test -x "$source/bin/clang"
    mkdir -p "$target"
    tar -c -C "$source" . | tar -x -C "$target"
    mkdir -p "$stage/payloads/$platform"
    # DEB/RPM retain the xz archive. It is unpacked locally into the user cache
    # for embedded builds, keeping the system LLVM and native compiler separate.
    cp "$runtime_root/payloads/$platform/"*.tar.xz "$stage/payloads/$platform/"
    if [[ "$platform" == linux/* ]]; then
        # Fedora's LLVM 22 omits the experimental Xtensa backend. Package
        # compilers use the common target set; tarballs retain the ESP build.
        # Override only the generated target lists, leaving the SDK untouched.
        config="$stage/system-linux/${platform#linux/}/include/llvm/Config"
        mkdir -p "$config"
        for definition in Targets AsmParsers AsmPrinters; do
            sed '/^LLVM_[A-Z_]*(Xtensa)$/d' "$sdk/include/llvm/Config/$definition.def" \
                > "$config/$definition.def"
        done
    fi
done

for platform in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 system-linux/amd64 system-linux/arm64 payloads; do
    mkdir -p "$destination/${platform%/*}"
    rm -rf "${destination:?}/$platform"
    mv "$stage/$platform" "$destination/$platform"
    echo "Staged runtime toolchain: $platform"
done
