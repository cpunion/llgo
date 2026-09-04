#!/bin/bash

apt_get() {
	# Keep apt invocation replaceable for the shell-level retry test below.
	command apt-get "$@"
}

install_debian_packages() {
	local max_attempts="${LLGO_APT_MAX_ATTEMPTS:-3}"
	if ! [[ "${max_attempts}" =~ ^[1-9][0-9]*$ ]]; then
		echo "LLGO_APT_MAX_ATTEMPTS must be a positive integer" >&2
		return 2
	fi
	if (( $# == 0 )); then
		echo "no Debian packages specified" >&2
		return 2
	fi

	local attempt
	for ((attempt = 1; attempt <= max_attempts; attempt++)); do
		if (( attempt > 1 )); then
			# A Debian mirror can publish a new Packages index before all package
			# objects have reached every CDN edge. Do not retain an index that named
			# a just-removed package when retrying after a 404 or a reset connection.
			rm -rf /var/lib/apt/lists/*
		fi
		if apt_get -o Acquire::Retries=3 update &&
			apt_get -o Acquire::Retries=3 install -y "$@"; then
			return 0
		fi
		if (( attempt < max_attempts )); then
			echo "apt-get failed (attempt ${attempt}/${max_attempts}); retrying with fresh package indexes" >&2
			sleep "$((attempt * 2))"
		fi
	done

	echo "apt-get failed after ${max_attempts} attempts" >&2
	return 1
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	set -euo pipefail
	install_debian_packages "$@"
fi
