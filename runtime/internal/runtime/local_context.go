//go:build llgo

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package runtime

import "unsafe"

// LocalContext owns the package-local blocks of one active Go context. Native
// entries root it on their outer stack frame; a stackless logical G embeds it
// in its scanned runtime sidecar.
type LocalContext struct {
	// blocks owns the most recently allocated local block. The list keeps every
	// block reachable from the outer Go entry stack frame or logical G sidecar.
	blocks *localBlock
}

type localBlock struct {
	next      *localBlock
	data      unsafe.Pointer
	cacheSlot *unsafe.Pointer
	cached    bool
}

// EnterLocalContext installs ctx when the current runtime G has no local owner.
// A non-nil result means this is a nested Go entry that inherited the returned
// context; in that case ctx is not installed.
func EnterLocalContext(ctx *LocalContext) *LocalContext {
	gp := getg()
	previous := gp.localContext
	if previous == nil {
		if ctx == nil {
			panic("runtime: nil local context")
		}
		gp.localContext = ctx
	}
	return previous
}

// LeaveLocalContext finishes an entry paired with EnterLocalContext. A nested
// entry verifies and retains its inherited context. An outer entry clears ctx
// and releases its package-block roots.
func LeaveLocalContext(ctx, previous *LocalContext) {
	gp := getg()
	if previous != nil {
		if gp.localContext != previous {
			panic("runtime: local context changed by nested entry")
		}
		return
	}
	if gp.localContext != ctx {
		panic("runtime: leaving inactive local context")
	}
	gp.localContext = nil
	releaseLocalBlocks(ctx)
}

func leaveCurrentLocalContext() {
	gp := getg()
	ctx := gp.localContext
	if ctx == nil {
		return
	}
	gp.localContext = nil
	releaseLocalBlocks(ctx)
}

func releaseLocalBlocks(ctx *LocalContext) {
	block := ctx.blocks
	ctx.blocks = nil
	for block != nil {
		next := block.next
		// Do not free block here: an address of a local variable may outlive its
		// owner. Breaking the links lets the GC retain only escaped blocks.
		if block.cached {
			*block.cacheSlot = nil
		}
		block.next = nil
		block.data = nil
		block.cacheSlot = nil
		block.cached = false
		block = next
	}
}

// LocalPackage creates stable, zeroed storage for one generated cache slot in
// the current physical owner. Generated accessors load the slot directly after
// first touch; the block list is retained only as a GC root and teardown list.
//
//go:noinline
func LocalPackage(cacheSlot *unsafe.Pointer, size, align uintptr) unsafe.Pointer {
	ctx := getg().localContext
	if ctx == nil {
		panic("runtime: local variable accessed outside a Go entry context")
	}
	if cacheSlot == nil {
		panic("runtime: nil local cache slot")
	}
	if data := *cacheSlot; data != nil {
		return data
	}
	if align == 0 || align&(align-1) != 0 {
		panic("runtime: invalid local package alignment")
	}
	block := newLocalBlock(cacheSlot, size, align, true)
	block.next = ctx.blocks
	ctx.blocks = block
	*cacheSlot = block.data
	return block.data
}

// LocalPackageLogical returns one package block from the current stackless
// logical G. cacheSlot is a stable process-global identity only; no
// thread-local address cache may survive a task migration.
//
//go:noinline
func LocalPackageLogical(cacheSlot *unsafe.Pointer, size, align uintptr) unsafe.Pointer {
	ctx := getg().localContext
	if ctx == nil {
		coroRuntimeAbort("local variable accessed outside a logical Go context")
		return nil
	}
	if cacheSlot == nil {
		coroRuntimeAbort("nil logical local package key")
		return nil
	}
	for block := ctx.blocks; block != nil; block = block.next {
		if block.cacheSlot == cacheSlot {
			return block.data
		}
	}
	if align == 0 || align&(align-1) != 0 {
		coroRuntimeAbort("invalid logical local package alignment")
		return nil
	}
	block := newLocalBlock(cacheSlot, size, align, false)
	block.next = ctx.blocks
	ctx.blocks = block
	return block.data
}

func newLocalBlock(cacheSlot *unsafe.Pointer, size, align uintptr, cached bool) *localBlock {
	header := unsafe.Sizeof(localBlock{})
	padding := align - 1
	if size == 0 {
		size = 1
	}
	if header > ^uintptr(0)-padding || header+padding > ^uintptr(0)-size {
		coroRuntimeAbort("local package size overflow")
		return nil
	}
	dataOffset := (header + padding) &^ padding
	allocation := AllocZ(dataOffset + size)
	if allocation == nil {
		coroRuntimeAbort("failed to allocate local package")
		return nil
	}
	// AllocZ returns storage aligned for every ordinary Go value. Rounding the
	// header size, rather than the pointer address, keeps the derived payload
	// inside the same exact pointer-provenance chain under stackless lowering.
	block := (*localBlock)(allocation)
	block.data = unsafe.Add(allocation, dataOffset)
	block.cacheSlot = cacheSlot
	block.cached = cached
	return block
}
