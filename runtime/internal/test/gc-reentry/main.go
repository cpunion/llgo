package main

import (
	_ "unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/runtime/tinygogc"
)

// This isolated negative fixture deliberately simulates a host callback
// entering while the allocator owns its single-mutator guard. It must trap,
// never allocate successfully or spin waiting for itself.
//
//go:linkname collectorActive github.com/xgo-dev/llgo/runtime/internal/runtime/tinygogc.gcMutex
var collectorActive bool

func main() {
	tinygogc.Alloc(1)
	collectorActive = true
	tinygogc.Alloc(1)
	println("unexpected GC reentry success")
}
