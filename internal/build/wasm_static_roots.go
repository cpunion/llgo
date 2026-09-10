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

package build

import (
	"fmt"
	"os"
	"path/filepath"

	gllvm "github.com/xgo-dev/llvm"
)

// prepareWasmStaticRootMarker creates the first input to wasm-ld's ordinary
// mutable-data and TLS segments. The linear collector starts its conservative
// global scan at the earlier marker, covering native TLS caches and ordinary
// mutable data while excluding packed immutable data that can accidentally
// resemble a heap pointer.
func prepareWasmStaticRootMarker(ctx *context, outputPath string) (string, func(), error) {
	noop := func() {}
	if ctx == nil || ctx.prog == nil || ctx.buildConf == nil ||
		runtimeSiteObjectFormat(ctx) != siteObjectWasm ||
		!ctx.prog.GCRootsEnabled() {
		return "", noop, nil
	}
	f, err := os.CreateTemp(filepath.Dir(outputPath), ".llgo-wasm-static-roots-*.o")
	if err != nil {
		return "", noop, fmt.Errorf("create WebAssembly static-root marker: %w", err)
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := f.Close(); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("close WebAssembly static-root marker: %w", err)
	}
	if err := writeWasmStaticRootMarkerObject(ctx, path); err != nil {
		cleanup()
		return "", noop, err
	}
	return path, cleanup, nil
}

func writeWasmStaticRootMarkerObject(ctx *context, path string) error {
	llvmCtx := gllvm.NewContext()
	defer llvmCtx.Dispose()
	mod := llvmCtx.NewModule("llgo.wasm.static.roots")
	defer mod.Dispose()
	mod.SetDataLayout(ctx.prog.DataLayout())
	mod.SetTarget(ctx.prog.Target().Spec().Triple)

	wordType := llvmCtx.Int32Type()
	if ctx.prog.PointerSize() == 8 {
		wordType = llvmCtx.Int64Type()
	}
	dataMarker := gllvm.AddGlobal(mod, wordType, "llgo_gc_globals_start_marker")
	dataMarker.SetInitializer(gllvm.ConstNull(wordType))
	dataMarker.SetAlignment(ctx.prog.PointerSize())
	dataMarker.SetSection(".data.llgo_gc_start")

	tlsMarker := gllvm.AddGlobal(mod, wordType, "llgo_gc_tls_start_marker")
	tlsMarker.SetInitializer(gllvm.ConstNull(wordType))
	tlsMarker.SetAlignment(ctx.prog.PointerSize())
	tlsMarker.SetThreadLocal(true)
	tlsMarker.SetSection(".tdata.llgo_gc_start")

	// Executables always link this object before package archives. Its strong
	// getter replaces the collector object's weak c-archive fallback. Wasm-ld
	// may place .data before or after TLS, so use the earlier active address.
	getter := gllvm.AddFunction(mod, "llgo_gc_globals_start", gllvm.FunctionType(wordType, nil, false))
	getter.AddFunctionAttr(llvmCtx.CreateStringAttribute("target-features", "+atomics"))
	builder := llvmCtx.NewBuilder()
	defer builder.Dispose()
	builder.SetInsertPointAtEnd(gllvm.AddBasicBlock(getter, "entry"))
	dataAddr := builder.CreatePtrToInt(dataMarker, wordType, "")
	tlsAddr := builder.CreatePtrToInt(tlsMarker, wordType, "")
	tlsFirst := builder.CreateICmp(gllvm.IntULT, tlsAddr, dataAddr, "")
	builder.CreateRet(builder.CreateSelect(tlsFirst, tlsAddr, dataAddr, ""))

	buf, err := ctx.prog.TargetMachine().EmitToMemoryBuffer(mod, gllvm.ObjectFile)
	if err != nil {
		return fmt.Errorf("emit WebAssembly static-root marker: %w", err)
	}
	defer buf.Dispose()
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write WebAssembly static-root marker: %w", err)
	}
	return nil
}
