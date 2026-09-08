//go:build llgo && wasm && llgo.wasm.gc.linear

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package runtime

import (
	"unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	"github.com/xgo-dev/llgo/runtime/internal/runtime/tinygogc"
)

// InitWasmGCPolicy runs once, after the executable's main context has been
// published, or before a library's user package initialization. getenv may
// allocate, so do not invoke it while initializing or locking the collector.
// A prior explicit SetGCPercent also initializes the policy and is preserved.
func InitWasmGCPolicy() {
	tinygogc.DiagnosticAllocationHook = diagnoseGCAllocation
	if tinygogc.GCPercentInitialized() {
		return
	}
	value := gcGetenv(c.Str("GOGC"))
	percent := int32(100)
	if value != nil {
		percent = tinygogc.ParseGCPercent(c.GoString(value))
	}
	tinygogc.InitGCPercent(percent)
}

func diagnoseGCAllocation(size uintptr) {
	c.Printf(c.Str("DIAG collecting allocation=%llu\n"), uint64(size))
	store := callerLocationStoreCurrent
	if store == nil {
		return
	}
	start := len(store.stack) - 4
	if start < 0 {
		start = 0
	}
	for i := start; i < len(store.stack); i++ {
		frame := &store.stack[i]
		c.Printf(c.Str("DIAG frame %.*s:%d\n"), c.Int(len(frame.Function)), unsafe.StringData(frame.Function), c.Int(frame.Line))
	}
}

//go:linkname gcGetenv C.getenv
func gcGetenv(name *c.Char) *c.Char
