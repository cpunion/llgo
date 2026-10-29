#!/usr/bin/env bash
set -euxo pipefail

# Run outside the checkout without LLGO_ROOT or a prepopulated build cache.
unset LLGO_ROOT LLVM_CONFIG
export LLGO_BUILD_CACHE=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"

root=$(llgo env LLGO_ROOT)
test "$root" = /usr/lib/llgo
test "$(readlink /usr/bin/llgo)" = /usr/lib/llgo/bin/llgo
test -f "$root/runtime/go.mod"
test -f "$root/targets/esp32.json"
test ! -e "$root/crosscompile"
ldd "$root/bin/llgo" > loader.txt
cat loader.txt
! grep -q 'not found' loader.txt
grep -E 'libLLVM\.so\.22\.1 => /' loader.txt
# Debian installs versioned LLVM tools; Fedora provides unversioned commands.
if command -v llvm-config-22 >/dev/null; then
    LLVM_CONFIG=$(command -v llvm-config-22)
    export LLVM_CONFIG
fi
llvm_config=${LLVM_CONFIG:-llvm-config}
[[ "$("$llvm_config" --version)" == 22.* ]]
[[ "$(llgo env LLGO_LLVM_VERSION)" == 22.* ]]
"$("$llvm_config" --bindir)/clang++" --version
llgo version

cat > go.mod <<'MODULE'
module example.com/llgo-release-smoke

go 1.20
MODULE
cat > main.go <<'GO'
package main

import (
	"fmt"
	_ "unsafe"
	"github.com/goplus/lib/c"
	"github.com/goplus/lib/cpp/std"
)

//go:linkname abs C.abs
func abs(value int32) int32

func main() {
	done := make(chan string)
	go func() { done <- fmt.Sprintf("LLGo release: %d", abs(-42)) }()
	fmt.Println(<-done)
	c.Printf(std.Str("C++ dependency works\n").CStr())
}
GO
go mod edit -require=github.com/goplus/lib@v0.6.3
go mod tidy
llgo build -o hello .
test "$(./hello)" = $'LLGo release: 42\nC++ dependency works'
