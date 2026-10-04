#!/usr/bin/env bash

# LLGO_PACMAN_MAX_ATTEMPTS: total transaction attempts (default: 3).
# LLGO_PACMAN_RETRY_DELAY_SECONDS: delay between attempts (default: 5).
# Retries intentionally accept any pacman failure, including permanent errors;
# the bounded attempt count limits the delay before reporting exhaustion.
# Explicit package URLs bypass pacman's mirror lists. Rotate those from the
# MSYS2 primary server through its published HTTPS mirrors on retries, keeping
# the exact package versions and pacman's signature verification unchanged.
# https://www.msys2.org/dev/mirrors/

pacman_command() {
	command pacman "$@"
}

pacman_retry_sleep() {
	command sleep "$1"
}

pacman_with_retry() {
	local max_attempts="${LLGO_PACMAN_MAX_ATTEMPTS:-3}"
	local retry_delay="${LLGO_PACMAN_RETRY_DELAY_SECONDS:-5}"
	if ! [[ "${max_attempts}" =~ ^[1-9][0-9]*$ ]]; then
		echo "LLGO_PACMAN_MAX_ATTEMPTS must be a positive integer" >&2
		return 2
	fi
	if ! [[ "${retry_delay}" =~ ^[0-9]+$ ]]; then
		echo "LLGO_PACMAN_RETRY_DELAY_SECONDS must be a non-negative integer" >&2
		return 2
	fi
	if (( $# == 0 )); then
		echo "no pacman arguments specified" >&2
		return 2
	fi

	local mirrors=(
		https://repo.msys2.org
		https://mirror.umd.edu/msys2
		https://mirror.msys2.org
	)
	local attempt argument mirror
	local args=()
	for ((attempt = 1; attempt <= max_attempts; attempt++)); do
		mirror="${mirrors[$(((attempt - 1) % ${#mirrors[@]}))]}"
		args=()
		for argument in "$@"; do
			case "$argument" in
				https://repo.msys2.org/*) argument="$mirror/${argument#https://repo.msys2.org/}" ;;
			esac
			args+=("$argument")
		done
		if pacman_command "${args[@]}"; then
			return 0
		fi
		if (( attempt < max_attempts )); then
			echo "pacman failed (attempt ${attempt}/${max_attempts}); retrying the package transaction" >&2
			pacman_retry_sleep "${retry_delay}"
		fi
	done

	echo "pacman failed after ${max_attempts} attempts" >&2
	return 1
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	set -euo pipefail
	pacman_with_retry "$@"
fi
