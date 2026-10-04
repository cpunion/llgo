#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=.github/actions/setup-deps/pacman_retry.sh
source "${script_dir}/pacman_retry.sh"

fail() {
	echo "$1" >&2
	exit 1
}

attempts=0
sleeps=0
last_sleep=""
last_args=()

# Called indirectly by pacman_with_retry.
# shellcheck disable=SC2329
pacman_command() {
	attempts=$((attempts + 1))
	last_args=("$@")
	(( attempts >= 3 ))
}

# Called indirectly by pacman_with_retry.
# shellcheck disable=SC2329
pacman_retry_sleep() {
	sleeps=$((sleeps + 1))
	last_sleep="$1"
}

LLGO_PACMAN_MAX_ATTEMPTS=3 LLGO_PACMAN_RETRY_DELAY_SECONDS=7 \
	pacman_with_retry --noconfirm -S --needed example-package 2>/dev/null
[[ "${attempts}" -eq 3 ]] || fail "pacman attempts = ${attempts}, want 3"
[[ "${sleeps}" -eq 2 ]] || fail "retry sleeps = ${sleeps}, want 2"
[[ "${last_sleep}" == 7 ]] || fail "retry delay = ${last_sleep}, want 7"
[[ " ${last_args[*]} " == *" --noconfirm -S --needed example-package "* ]] ||
	fail "unexpected pacman arguments: ${last_args[*]}"

attempts=0
sleeps=0
# Called indirectly by pacman_with_retry.
# shellcheck disable=SC2329
pacman_command() {
	attempts=$((attempts + 1))
	local mirror
	case "$attempts" in
		1) mirror=https://repo.msys2.org ;;
		2) mirror=https://mirror.umd.edu/msys2 ;;
		3) mirror=https://mirror.msys2.org ;;
		*) fail "unexpected mirror attempt $attempts" ;;
	esac
	local expected=(
		--noconfirm -U --assume-installed mingw-w64-clang-x86_64-cc-libs=22.1.8
		"$mirror/mingw/clang64/llvm-22.1.8-2-any.pkg.tar.zst"
		"$mirror/mingw/clangarm64/lldb-22.1.8-1-any.pkg.tar.zst"
		'https://example.com/package.pkg.tar.zst' 'local package.pkg.tar.zst'
	)
	[[ $# -eq ${#expected[@]} ]] || fail "mirror changed argument count"
	local argument index=0
	for argument in "$@"; do
		[[ "$argument" == "${expected[index]}" ]] || fail "mirror changed argument $index: $argument"
		index=$((index + 1))
	done
	(( attempts >= 3 ))
}
LLGO_PACMAN_MAX_ATTEMPTS=3 LLGO_PACMAN_RETRY_DELAY_SECONDS=0 \
	pacman_with_retry --noconfirm -U --assume-installed mingw-w64-clang-x86_64-cc-libs=22.1.8 \
	'https://repo.msys2.org/mingw/clang64/llvm-22.1.8-2-any.pkg.tar.zst' \
	'https://repo.msys2.org/mingw/clangarm64/lldb-22.1.8-1-any.pkg.tar.zst' \
	'https://example.com/package.pkg.tar.zst' 'local package.pkg.tar.zst' 2>/dev/null
[[ "$attempts" -eq 3 ]] || fail "mirror attempts = $attempts, want 3"

attempts=0
sleeps=0
# Called indirectly by pacman_with_retry.
# shellcheck disable=SC2329
pacman_command() {
	attempts=$((attempts + 1))
	return 1
}

if LLGO_PACMAN_MAX_ATTEMPTS=2 LLGO_PACMAN_RETRY_DELAY_SECONDS=0 \
	pacman_with_retry -U package.pkg.tar.zst 2>/dev/null; then
	fail "exhausted pacman retries unexpectedly succeeded"
fi
[[ "${attempts}" -eq 2 ]] || fail "exhausted attempts = ${attempts}, want 2"
[[ "${sleeps}" -eq 1 ]] || fail "exhausted retry sleeps = ${sleeps}, want 1"

if LLGO_PACMAN_MAX_ATTEMPTS=0 pacman_with_retry -S package 2>/dev/null; then
	fail "invalid attempt count unexpectedly succeeded"
fi
if LLGO_PACMAN_RETRY_DELAY_SECONDS=invalid pacman_with_retry -S package 2>/dev/null; then
	fail "invalid retry delay unexpectedly succeeded"
fi
if pacman_with_retry 2>/dev/null; then
	fail "empty pacman command unexpectedly succeeded"
fi

echo "MSYS2 pacman retry checks passed"
