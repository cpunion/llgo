//go:build llgo && wasm && !(wasip1 && llgo.wasi_threads)

package runtime

import "unsafe"

// A LocalContext can live on a suspended Fiber outside the compiler's current
// root-chain range. Publish its block head through the always-registered system
// context so package-local pointer storage remains visible while another G
// triggers collection. Store the value, not the slot address: rooting the slot
// would conservatively retain and scan the complete Fiber stack allocation.
func activateLocalBlocks(ctx *LocalContext) {
	setWasmGCRoot(&wasmSystemGCRoot, ctx.blocks)
}

func publishLocalBlocks(ctx *LocalContext) {
	setWasmGCRoot(&wasmSystemGCRoot, ctx.blocks)
}

func deactivateLocalBlocks(ctx *LocalContext) {
	setWasmGCRoot(&wasmSystemGCRoot, nil)
}

// GoroutineLocalPackage returns the package-local block owned by the current
// logical G. The process-global key identifies the package but never caches a
// G-specific address, because one wasm worker runs many goroutines.
//
//go:noinline
func GoroutineLocalPackage(key *uintptr, size, align uintptr) unsafe.Pointer {
	gp := getg()
	if gp == nil || gp.context == nil {
		panic("runtime: goroutine-local variable accessed without a current G")
	}
	if key == nil {
		panic("runtime: nil goroutine-local package key")
	}
	ctx := &gp.context.platform.glsContext
	for data := ctx.blocks; data != nil; data = localBlockHeader(data).next {
		if localBlockHeader(data).cacheSlot == key {
			return data
		}
	}
	if align == 0 || align&(align-1) != 0 {
		panic("runtime: invalid goroutine-local package alignment")
	}
	data := newLocalBlock(key, size, align)
	localBlockHeader(data).next = ctx.blocks
	ctx.blocks = data
	return data
}

// releaseGoroutineLocalBlocks drops one G's ownership links. The block header
// uses the process-global package key only as an identity, so it must not clear
// that shared key when a G exits.
func releaseGoroutineLocalBlocks(ctx *LocalContext) {
	data := ctx.blocks
	ctx.blocks = nil
	for data != nil {
		block := localBlockHeader(data)
		next := block.next
		block.next = nil
		block.cacheSlot = nil
		data = next
	}
}
