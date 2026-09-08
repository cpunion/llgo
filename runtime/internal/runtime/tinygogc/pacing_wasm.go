//go:build wasm && llgo.wasm.gc.linear

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package tinygogc

import "github.com/xgo-dev/llgo/runtime/internal/gcroot"

var wasmGCPacing gcPacing

func GCPercentInitialized() bool { return wasmGCPacing.initialized }

// InitGCPercent is called after startup has published the initial roots. In
// particular, getenv may allocate through libc; it must run before this call,
// outside the collector lock and with automatic collection still disabled.
func InitGCPercent(percent int32) {
	lock(&gcMutex)
	lazyInit()
	wasmGCPacing.init(percent, gcLiveBytes())
	unlock(&gcMutex)
}

func SetGCPercent(percent int32) int32 {
	lock(&gcMutex)
	lazyInit()
	old := wasmGCPacing.setPercent(percent, gcLiveBytes())
	unlock(&gcMutex)
	return old
}

func gcAutomaticAllowed() bool {
	// Asyncify replay has not republished the complete live root chain yet.
	// Allocation must grow (or fail with OOM), never collect, in that window.
	// A hard Wasm memory maximum is not a Go soft memory limit: this backend
	// does not implement the latter's exception to GOGC=off.
	return wasmGCPacing.automatic() && !gcroot.Rebuilding()
}

func gcAllocationDue(size uint64) bool {
	return wasmGCPacing.shouldCollect(gcLiveBytes(), size) && !gcroot.Rebuilding()
}

func gcCollectionComplete() { wasmGCPacing.collected(gcLiveBytes()) }
func gcNextGoal() uint64    { return wasmGCPacing.nextGC() }
