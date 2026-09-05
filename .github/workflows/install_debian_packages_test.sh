#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=.github/workflows/install_debian_packages.sh
source "${SCRIPT_DIR}/install_debian_packages.sh"

fail() {
	echo "$1" >&2
	exit 1
}

updates=0
installs=0
list_resets=0
delays=0
last_install=""
# Called indirectly by install_debian_packages.
# shellcheck disable=SC2329
apt_get() {
	if [[ " $* " == *" update "* ]]; then
		updates=$((updates + 1))
		return 0
	fi
	if [[ " $* " == *" install "* ]]; then
		installs=$((installs + 1))
		last_install="$*"
		(( installs >= 3 ))
		return
	fi
	return 2
}

# Called indirectly by install_debian_packages.
# shellcheck disable=SC2329
rm() {
	[[ $# -ge 2 && "$1" == -rf ]] || fail "unexpected cleanup arguments: $*"
	shift
	local path
	# Bash expands the glob before calling this stub when apt lists exist on
	# Linux. On macOS it normally stays literal; accept both forms per argument.
	for path in "$@"; do
		[[ "${path}" == /var/lib/apt/lists/* ]] || fail "unexpected cleanup path: ${path}"
	done
	list_resets=$((list_resets + 1))
}

# Called indirectly by install_debian_packages.
# shellcheck disable=SC2329
sleep() {
	delays=$((delays + 1))
}

# Exercise both forms without depending on the host's apt directory contents.
rm -rf '/var/lib/apt/lists/*'
rm -rf /var/lib/apt/lists/partial /var/lib/apt/lists/debian_Packages
[[ "${list_resets}" -eq 2 ]] || fail "cleanup stub did not accept both glob forms"
list_resets=0

LLGO_APT_MAX_ATTEMPTS=3 install_debian_packages build-essential zlib1g-dev rsync
[[ "${updates}" -eq 3 ]] || fail "update attempts = ${updates}, want 3"
[[ "${installs}" -eq 3 ]] || fail "install attempts = ${installs}, want 3"
[[ "${list_resets}" -eq 2 ]] || fail "list resets = ${list_resets}, want 2"
[[ "${delays}" -eq 2 ]] || fail "retry delays = ${delays}, want 2"
[[ " ${last_install} " == *" -o Acquire::Retries=3 install -y build-essential zlib1g-dev rsync "* ]] ||
	fail "unexpected install command: ${last_install}"

updates=0
list_resets=0
delays=0
# Called indirectly by install_debian_packages.
# shellcheck disable=SC2329
apt_get() {
	updates=$((updates + 1))
	return 1
}

if LLGO_APT_MAX_ATTEMPTS=2 install_debian_packages build-essential; then
	fail "exhausted retries unexpectedly succeeded"
fi
[[ "${updates}" -eq 2 ]] || fail "exhausted update attempts = ${updates}, want 2"
[[ "${list_resets}" -eq 1 ]] || fail "exhausted list resets = ${list_resets}, want 1"
[[ "${delays}" -eq 1 ]] || fail "exhausted retry delays = ${delays}, want 1"

if LLGO_APT_MAX_ATTEMPTS=0 install_debian_packages build-essential; then
	fail "invalid attempt count unexpectedly succeeded"
fi

echo 'Debian package installation retry checks passed'
