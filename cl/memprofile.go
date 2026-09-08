/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package cl

import (
	"go/token"
	"go/types"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func publicRuntimePackage(path string) bool {
	return path == "runtime" || path == "github.com/xgo-dev/llgo/runtime/internal/lib/runtime"
}

// MemProfileConsumer finds a real use of the public profiling API after SSA
// construction. Taking a function value or the address of MemProfileRate counts
// too: a later indirect call must not silently disable allocation recording.
// runtime/pprof calls its provider through linkname, not a normal SSA reference.
// The runtime implementation's own provider references are not consumers.
func MemProfileConsumer(pkgs []*ssa.Package) string {
	for _, pkg := range pkgs {
		if pkg == nil || pkg.Pkg == nil || publicRuntimePackage(pkg.Pkg.Path()) {
			continue
		}
		if pkg.Pkg.Path() == "runtime/pprof" {
			return pkg.Pkg.Path()
		}
		// Only source packages importing runtime can refer to its members.
		// Imported generic instances are also visited from their source package.
		importsRuntime := false
		for _, imp := range pkg.Pkg.Imports() {
			if publicRuntimePackage(imp.Path()) {
				importsRuntime = true
				break
			}
		}
		if !importsRuntime {
			continue
		}
		funcs, _ := collectRuntimeCallerFunctions(pkg)
		for fn := range memoryProfileFunctions(funcs) {
			if !functionBelongsToPackage(pkg, fn) {
				continue
			}
			for _, block := range fn.Blocks {
				for _, instr := range block.Instrs {
					for _, operand := range instr.Operands(nil) {
						if operand == nil {
							continue
						}
						switch value := (*operand).(type) {
						case *ssa.Global:
							if value.Pkg != nil && publicRuntimePackage(value.Pkg.Pkg.Path()) && value.Name() == "MemProfileRate" {
								return pkg.Pkg.Path()
							}
						case *ssa.Function:
							if value.Pkg != nil && publicRuntimePackage(value.Pkg.Pkg.Path()) && value.Name() == "MemProfile" {
								return pkg.Pkg.Path()
							}
						}
					}
				}
			}
		}
	}
	return ""
}

// SetMemoryProfileAttribution configures the shared frontend analysis before
// Precompute. Native programs never enable this Wasm-only attribution mode.
func (c *CallerTracking) SetMemoryProfileAttribution(enabled bool) {
	if c.precomputed {
		if c.memoryProfileAttribution != enabled {
			panic("memory profile attribution changed after caller precomputation")
		}
		return // Backends share a frozen analysis; even same-value writes race.
	}
	c.memoryProfileAttribution = enabled
}

// memoryProfileFunctions follows function operands, not package ownership.
// Concrete generic instances can have Pkg=nil and be emitted into an importing
// package; closures and method-expression values need not be static callees.
func memoryProfileFunctions(roots map[*ssa.Function]bool) map[*ssa.Function]bool {
	funcs := make(map[*ssa.Function]bool)
	var visit func(*ssa.Function)
	visit = func(fn *ssa.Function) {
		if fn == nil || funcs[fn] {
			return
		}
		funcs[fn] = true
		for _, anon := range fn.AnonFuncs {
			visit(anon)
		}
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				for _, operand := range instr.Operands(nil) {
					if operand != nil {
						if child, ok := (*operand).(*ssa.Function); ok {
							visit(child)
						}
					}
				}
			}
		}
	}
	for fn := range roots {
		visit(fn)
	}
	return funcs
}

// Adapted from the sampled native profiler: allocation-bearing functions and
// their callers retain their logical frame. Indirect/unknown calls must be
// conservative, but a known nonallocating helper remains uninstrumented.
func memoryProfileAllocationFrames(funcs map[*ssa.Function]bool) map[*ssa.Function]bool {
	frames := make(map[*ssa.Function]bool)
	callers := make(map[*ssa.Function][]*ssa.Function)
	var queue []*ssa.Function
	mark := func(fn *ssa.Function) {
		if !frames[fn] {
			frames[fn] = true
			queue = append(queue, fn)
		}
	}
	for fn := range funcs {
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if memoryProfileInstructionMayAllocate(instr, funcs) {
					mark(fn)
				}
				if call, ok := instr.(ssa.CallInstruction); ok {
					if callee := call.Common().StaticCallee(); callee != nil {
						callers[callee] = append(callers[callee], fn)
					}
				}
			}
		}
	}
	for len(queue) != 0 {
		fn := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, caller := range callers[fn] {
			mark(caller)
		}
	}
	return frames
}

func memoryProfileInstructionMayAllocate(instr ssa.Instruction, funcs map[*ssa.Function]bool) bool {
	switch instr := instr.(type) {
	case *ssa.Alloc:
		return instr.Heap
	case *ssa.MakeChan, *ssa.MakeClosure, *ssa.MakeInterface, *ssa.MakeMap, *ssa.MakeSlice,
		*ssa.Defer, *ssa.Go, *ssa.MapUpdate, *ssa.Send, *ssa.Select, *ssa.Range:
		return true
	case *ssa.UnOp:
		// A blocked receive creates a chanWaiter, just like a blocked send.
		return instr.Op == token.ARROW
	case *ssa.BinOp:
		return instr.Op == token.ADD && basicKind(instr.Type()) == types.String
	case *ssa.Convert:
		return memoryProfileConvertMayAllocate(instr.X.Type(), instr.Type())
	case *ssa.MultiConvert:
		return true
	case ssa.CallInstruction:
		call := instr.Common()
		if builtin, ok := call.Value.(*ssa.Builtin); ok {
			return builtin.Name() == "append"
		}
		callee := call.StaticCallee()
		return callee == nil || !funcs[callee] || len(callee.Blocks) == 0
	}
	return false
}

func memoryProfileConvertMayAllocate(from, to types.Type) bool {
	from = types.Unalias(from).Underlying()
	to = types.Unalias(to).Underlying()
	switch to := to.(type) {
	case *types.Basic:
		if to.Kind() != types.String {
			return false
		}
		switch from := from.(type) {
		case *types.Basic:
			return from.Info()&types.IsInteger != 0
		case *types.Slice:
			return memoryProfileByteOrRune(from.Elem())
		}
	case *types.Slice:
		from, ok := from.(*types.Basic)
		return ok && from.Kind() == types.String && memoryProfileByteOrRune(to.Elem())
	}
	return false
}

func memoryProfileByteOrRune(typ types.Type) bool {
	kind := basicKind(typ)
	return kind == types.Uint8 || kind == types.Int32
}

func memoryProfileFrameSet(prog llssa.Program, ct *CallerTracking, pkg *ssa.Package) map[*ssa.Function]bool {
	if !prog.WasmMemoryProfilingEnabled() || excludeSafepointPackage(pkg.Pkg.Path()) {
		// Runtime bookkeeping uses sync/atomic while initializing goroutine
		// locality. Instrumenting those primitive wrappers would recursively
		// initialize that same locality before its first frame can be pushed.
		return nil
	}
	ct.SetMemoryProfileAttribution(true)
	if !ct.precomputed {
		ct.Precompute(pkg.Prog.AllPackages())
	}
	return ct.memoryProfileFrames
}

func (p *context) omitWasmMemProfileCall(fn *ssa.Function) bool {
	if p.prog.Target().GOARCH != "wasm" || !p.prog.GCRootsEnabled() ||
		!p.prog.LogicalGoroutineLocalityEnabled() || p.prog.WasmMemoryProfilingEnabled() {
		// A disabled single-worker sampler does not disable the independent
		// legacy profiling path of the WASI pthreads backend.
		return false
	}
	path := p.pkg.Path()
	return path == llssa.PkgRuntime && (fn.Name() == "recordMemProfileAlloc" || fn.Name() == "recordWasmMemProfileAlloc") ||
		path == llssa.PkgRuntime+"/tinygogc" && fn.Name() == "memProfileFree"
}
