#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
tmp_root="$(mktemp -d)"
workdir="$tmp_root/llgo-work"

cleanup() {
	rm -rf "$tmp_root"
}
trap cleanup EXIT

rsync -a --delete --exclude='.git/' "$repo_root/" "$workdir/"

log_section() {
	printf "\n==== %s ====\n" "$1"
}

run_hello() {
	local mod_version="$1"
	local hello_dir="$workdir/_test/helloworld-$mod_version"
	rm -rf "$hello_dir"
	mkdir -p "$hello_dir"
	cat >"$hello_dir/go.mod" <<EOF
module hello
go $mod_version
EOF
	cat >"$hello_dir/main.go" <<'EOF'
package main

import (
	"fmt"
	"github.com/goplus/lib/c"
	"github.com/goplus/lib/cpp/std"
)

func main() {
	fmt.Println("Hello, LLGo!")
	println("Hello, LLGo!")
	c.Printf(c.Str("Hello, LLGo!\n"))
	c.Printf(std.Str("Hello LLGo by cpp/std.Str\n").CStr())
}
EOF
	(cd "$hello_dir" && go mod tidy)
	local output
	set +e
	output=$(cd "$hello_dir" && llgo run . 2>&1)
	local status=$?
	set -e
	if [[ $status -ne 0 ]]; then
		printf "%s\n" "$output"
		exit $status
	fi
	if ! grep -q "Hello LLGo by cpp/std.Str" <<<"$output"; then
		printf "%s\n" "$output"
		exit 1
	fi
}

log_section "Format"
for dir in . runtime; do
	pushd "$workdir/$dir" >/dev/null
	fmt_output=$(go fmt ./... | grep -v xgo_autogen.go || true)
	if [ -n "$fmt_output" ]; then
		printf "Detected formatting differences in %s:\n%s\n" "$dir" "$fmt_output"
		exit 1
	fi
	popd >/dev/null
done

log_section "Go Build"
(cd "$workdir" && go build ./...)

# log_section "Go Test"
# (cd "$workdir" && go test ./...)

log_section "Install llgo"
(cd "$workdir" && go install ./...)
export LLGO_ROOT="$workdir"

log_section "llgo test"
(cd "$workdir" && llgo test ./...)

log_section "Demo Tests"
(cd "$workdir" && bash .github/workflows/test_demo.sh)

log_section "Build targets"
(cd "$workdir/_demo/embed/targetsbuild" && bash build.sh)

log_section "Hello World"
for mod in 1.21 1.22 1.23 1.24; do
	run_hello "$mod"
done

log_section "Done (workspace: $workdir)"
