//go:build llgo && wasm && !(wasip1 && llgo.wasi_threads)

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package runtime

// memProfileCaptureStack captures only the currently executing allocation
// chain. Callers' panic splice includes frames already unwound by longjmp,
// which must not become the allocation stack of a recovering defer.
// The caller suppresses recursive samples before entering: interning a new
// process-stable PC can allocate, though it cannot schedule another goroutine.
func memProfileCaptureStack(pcs []uintptr) int {
	// The scheduler's system G has no goroutine-local context. Check the raw
	// current G before accessing a GLS variable, and never bootstrap a G merely
	// to record a runtime-internal allocation. Even reading a nil GLS value
	// first would invoke GoroutineLocalPackage and panic on the system stack.
	if !memProfileHasCurrentG() {
		return 0
	}
	return memProfileCaptureActiveStack(pcs)
}

func memProfileHasCurrentG() bool {
	return currentG != nil && currentG.context != nil
}

// The GLS resolver is emitted at function entry, before source-level guards.
// Keep this boundary out of line so it cannot run before the raw G check above.
//
//go:noinline
func memProfileCaptureActiveStack(pcs []uintptr) int {
	store := callerLocationStoreCurrent
	if store == nil {
		return 0
	}
	registry := callerSyntheticRegistryFor(store)
	n := 0
	for i := len(store.stack) - 1; i >= 0 && n < len(pcs); i-- {
		pcs[n] = registry.capturePC(&store.stack[i], callersPCValue)
		n++
	}
	return n
}
