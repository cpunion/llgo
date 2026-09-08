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

package ssa

import (
	"go/types"

	llabi "github.com/xgo-dev/llgo/internal/abi"
	"github.com/xgo-dev/llvm"
)

const gcRootChainName = "llvm_gc_root_chain"

// EnableGCRoots controls compiler-maintained GC roots.
func (p Program) EnableGCRoots(enable bool) {
	p.enableGCRoots = enable
}

// GCRootsEnabled reports whether compiler-maintained GC roots are enabled.
func (p Program) GCRootsEnabled() bool {
	return p.enableGCRoots
}

// NewGCRoots reserves count pointer roots in one compiler-maintained frame.
// It must be called once, before the function emits a return.
func (p Function) NewGCRoots(count int) []Expr {
	if count <= 0 {
		return nil
	}
	if !p.gcRootPrev.IsNil() {
		panic("ssa: GC roots already reserved")
	}
	_ = p.Block(0) // Materialize the entry before adding its root prologue.
	prog := p.Prog
	frame := llabi.NewGCRootFrame(p.Pkg.mod, p.impl, count, prog.PointerSize(), prog.target.GOARCH == "wasm")
	roots := make([]Expr, count)
	for i := range roots {
		roots[i] = Expr{frame.Slots[i], prog.Pointer(prog.VoidPtr())}
	}
	p.gcRootFrame = Expr{frame.Frame, prog.VoidPtr()}
	p.gcRootPrev = Expr{frame.Prev, prog.Pointer(prog.VoidPtr())}
	return roots
}

// SetGCRoot publishes value through a root created by NewGCRoots.
func (b Builder) SetGCRoot(root, value Expr) {
	b.Store(root, b.Convert(b.Prog.VoidPtr(), value))
}

func (p Function) gcRootChain() llvm.Value {
	global := p.Pkg.mod.NamedGlobal(gcRootChainName)
	if global.IsNil() {
		global = llvm.AddGlobal(p.Pkg.mod, p.Prog.tyVoidPtr(), gcRootChainName)
	}
	global.SetInitializer(llvm.ConstNull(p.Prog.tyVoidPtr()))
	global.SetLinkage(llvm.LinkOnceAnyLinkage)
	global.SetAlignment(p.Prog.PointerSize())
	return global
}

// currentGCRootChain loads the compiler-maintained chain at the call site.
// Runtime helpers must not discover this through a Go call: their own root
// frame is already linked by then and would be mistaken for the caller frame.
func (b Builder) currentGCRootChain() Expr {
	chain := b.Func.gcRootChain()
	return Expr{llvm.CreateLoad(b.impl, b.Prog.tyVoidPtr(), chain), b.Prog.VoidPtr()}
}

func (p Function) endGCRoots(b Builder) {
	if p.gcRootPrev.IsNil() {
		return
	}
	llabi.PopGCRootFrame(p.Pkg.mod, p.impl, llabi.GCRootFrame{Frame: p.gcRootFrame.impl, Prev: p.gcRootPrev.impl})
}

// GCRootCount reports how many heap pointers typ contributes to a root frame.
func (p Program) GCRootCount(typ Type) int {
	switch typ.kind {
	case vkPtr, vkString, vkSlice, vkMap, vkEface, vkIface, vkClosure, vkChan:
		return 1
	case vkStruct:
		raw := typ.raw.Type.Underlying().(*types.Struct)
		count := 0
		for i := 0; i < raw.NumFields(); i++ {
			count += p.GCRootCount(p.Field(typ, i))
		}
		return count
	case vkArray:
		raw := typ.raw.Type.Underlying().(*types.Array)
		return int(raw.Len()) * p.GCRootCount(p.Index(typ))
	case vkTuple:
		raw := typ.raw.Type.Underlying().(*types.Tuple)
		count := 0
		for i := 0; i < raw.Len(); i++ {
			count += p.GCRootCount(p.Field(typ, i))
		}
		return count
	default:
		return 0
	}
}

// GCRootPointers extracts the heap pointers represented by value.
func (b Builder) GCRootPointers(value Expr) []Expr {
	var roots []Expr
	b.appendGCRootPointers(&roots, value)
	return roots
}

func (b Builder) appendGCRootPointers(roots *[]Expr, value Expr) {
	switch value.Type.kind {
	case vkPtr, vkMap, vkChan:
		*roots = append(*roots, b.Convert(b.Prog.VoidPtr(), value))
	case vkString:
		*roots = append(*roots, b.Convert(b.Prog.VoidPtr(), b.StringData(value)))
	case vkSlice:
		*roots = append(*roots, b.Convert(b.Prog.VoidPtr(), b.SliceData(value)))
	case vkEface, vkIface:
		*roots = append(*roots, b.InterfaceData(value))
	case vkClosure:
		data := llvm.CreateExtractValue(b.impl, value.impl, 1)
		*roots = append(*roots, Expr{data, b.Prog.VoidPtr()})
	case vkStruct, vkTuple:
		var count int
		switch raw := value.Type.raw.Type.Underlying().(type) {
		case *types.Struct:
			count = raw.NumFields()
		case *types.Tuple:
			count = raw.Len()
		}
		for i := 0; i < count; i++ {
			b.appendGCRootPointers(roots, b.Field(value, i))
		}
	case vkArray:
		raw := value.Type.raw.Type.Underlying().(*types.Array)
		elem := b.Prog.Index(value.Type)
		for i := 0; i < int(raw.Len()); i++ {
			part := llvm.CreateExtractValue(b.impl, value.impl, i)
			b.appendGCRootPointers(roots, Expr{part, elem})
		}
	}
}

// ClosureContextParam returns the hidden closure context parameter.
func (p Function) ClosureContextParam() Expr {
	if p.env == nil {
		return Nil
	}
	return Expr{p.impl.Param(0), p.env}
}
