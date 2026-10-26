//go:build llgo && coro_context_layout_test

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package runtime

import "unsafe"

// CoroContextLayoutForTesting uses the linked runtime's actual types and
// storage predicate. The fixture does not publish these synthetic tasks or
// register any logical G; production builds exclude the entire adapter.
func CoroContextLayoutForTesting() ([9]uintptr, string) {
	var allocation coroTaskAllocation
	task, ctx := &allocation.task, &allocation.context
	layout := [9]uintptr{
		unsafe.Sizeof(allocation.task), unsafe.Sizeof(ctx.g), unsafe.Sizeof(*ctx),
		unsafe.Sizeof(allocation), runtimeContextCoreOffset,
		coroTaskAllocationTaskOffset, coroTaskAllocationContextOffset,
		unsafe.Offsetof(ctx.g.atomicstatus), unsafe.Offsetof(ctx.g.goid),
	}
	for value := 0; value < 256; value++ {
		if runtimeContextKind(value).isCoro() != (value == 1 || value == 2) {
			return layout, "invalid context kind accepted or logical kind rejected"
		}
	}
	ctx.g.context = ctx
	ctx.g.localContext = &ctx.local
	ctx.g.startarg = unsafe.Pointer(task)
	ctx.g.contextKind = runtimeContextCoroEmbedded
	if !validCoroRuntimeTaskG(task, &ctx.g) {
		return layout, "embedded context rejected"
	}
	for _, kind := range []runtimeContextKind{runtimeContextNative, 3, 255} {
		ctx.g.contextKind = kind
		if validCoroRuntimeTaskG(task, &ctx.g) {
			return layout, "native or invalid kind interpreted as a logical task"
		}
	}
	ctx.g.contextKind = runtimeContextCoroEmbedded
	ctx.g.startarg = unsafe.Pointer(ctx)
	if validCoroRuntimeTaskG(task, &ctx.g) {
		return layout, "mismatched task identity accepted"
	}
	ctx.g.startarg = unsafe.Pointer(task)
	ctx.g.localContext = nil
	if validCoroRuntimeTaskG(task, &ctx.g) {
		return layout, "missing local storage owner accepted"
	}
	ctx.g.localContext = &ctx.local
	var separate coroRuntimeContext
	separate.g.context = &separate
	separate.g.localContext = &separate.local
	separate.g.startarg = unsafe.Pointer(task)
	separate.g.contextKind = runtimeContextCoroEmbedded
	if validCoroRuntimeTaskG(task, &separate.g) {
		return layout, "separate context accepted as an embedded tail"
	}
	separate.g.contextKind = runtimeContextCoroRoot
	if validCoroRuntimeTaskG(task, &separate.g) {
		return layout, "non-main task accepted with separate root storage"
	}
	separate.g.isMain = true
	if !validCoroRuntimeTaskG(task, &separate.g) {
		return layout, "command-root context rejected"
	}
	separate.g.context = nil
	if validCoroRuntimeTaskG(task, &separate.g) {
		return layout, "retired context accepted"
	}
	if layout[4] != 0 || layout[5] != 0 || layout[7]%4 != 0 || layout[8]%8 != 0 ||
		layout[6]%unsafe.Alignof(coroRuntimeContext{}) != 0 {
		return layout, "allocation base or atomic alignment changed"
	}
	return layout, ""
}
