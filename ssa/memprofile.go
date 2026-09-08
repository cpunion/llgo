/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package ssa

// RuntimeWasmMemProfileEnabledVar is the immutable configuration read by the
// single-worker linear-memory allocator. Native profiling is unchanged.
const RuntimeWasmMemProfileEnabledVar = PkgRuntime + ".wasmMemProfileEnabled"

// EnableWasmMemoryProfiling is set once, before any package is compiled.
func (p Program) EnableWasmMemoryProfiling(enabled bool) {
	p.wasmMemoryProfiling = enabled && p.target != nil && p.target.GOARCH == "wasm" && p.enableGCRoots
}

func (p Program) WasmMemoryProfilingEnabled() bool {
	return p.wasmMemoryProfiling
}

// InitWasmMemProfileEnabled lets ordinary LLVM optimization remove the entire
// sampling path when the executable has no memory-profile consumer.
func (p Program) InitWasmMemProfileEnabled(g Global) {
	g.Init(p.Val(p.wasmMemoryProfiling))
	g.impl.SetGlobalConstant(true)
}
