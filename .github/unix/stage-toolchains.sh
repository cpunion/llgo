#!/usr/bin/env bash
# Keep the build SDK intact; publish only its compiler/runtime payload.
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

stage=$(mktemp -d "$destination/.staging.XXXXXX")
trap 'rm -rf "$stage"' EXIT
for platform in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
    source="$source_root/$platform/crosscompile/clang"
    target="$stage/$platform"
    test -f "$source/LICENSE-LLVM.txt"
    test -x "$source/bin/clang"
    mkdir -p "$target"
    # Clang resource headers and target runtime libraries live under lib/clang;
    # the excluded include trees and top-level archives are SDK build inputs.
    tar -c -C "$source" \
        --exclude='./include/llvm' --exclude='./include/llvm-c' \
        --exclude='./include/clang' --exclude='./include/clang-c' \
        --exclude='./lib/libLLVM*.a' --exclude='./lib/libclang*.a' \
        --exclude='./bin/llvm-exegesis' . | tar -x -C "$target"
    # The macOS SDK ships two identical regular files. Retain both loader names
    # through a relative symlink, but never deduplicate different libraries.
    if [[ "$platform" == darwin/* && ! -L "$target/lib/libLLVM.dylib" &&
          ! -L "$target/lib/libLLVM-22.dylib" &&
          -f "$target/lib/libLLVM.dylib" && -f "$target/lib/libLLVM-22.dylib" ]] &&
        cmp -s "$target/lib/libLLVM.dylib" "$target/lib/libLLVM-22.dylib"; then
        rm "$target/lib/libLLVM-22.dylib"
        ln -s libLLVM.dylib "$target/lib/libLLVM-22.dylib"
    fi
done

for platform in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
    mkdir -p "$destination/${platform%/*}"
    rm -rf "$destination/$platform"
    mv "$stage/$platform" "$destination/$platform"
    echo "Staged runtime toolchain: $platform"
done
