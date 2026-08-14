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
	"encoding/binary"
	"fmt"
	"go/types"

	"github.com/xgo-dev/llvm"
)

// CoroFrameOps emits target-independent coroutine frame allocation calls.
//
// The callbacks run at the builder's current insertion point. They deliberately
// receive both llvm.coro.size and the effective required allocation alignment
// so a later runtime can capture a frame descriptor without this package fixing
// that runtime's ABI. The alignment is at least llvm.coro.align and at least the
// guarantee declared by CoroOptions.AllocationAlign. Free is called only when
// llvm.coro.free returns a non-null allocation pointer. Each callback may append
// instructions but must leave the builder in the same unterminated basic block;
// CoroBuilder appends the required control-flow edge immediately afterwards.
// When the llvm.coro.alloc path executes, Alloc must return a non-null pointer;
// a target runtime must handle allocation failure before returning to the ramp.
type CoroFrameOps struct {
	Alloc func(b Builder, size, align Expr) Expr
	Free  func(b Builder, frame, size, align Expr)
}

// CoroOptions configures one LLVM switched-resume coroutine.
//
// Promise may be Nil when no promise is required. A non-Nil Promise must point
// to the alloca designated as the LLVM coroutine promise.
//
// AllocationAlign is the alignment guarantee passed to llvm.coro.id for memory
// returned by Frame.Alloc. Zero uses LLVM's default guarantee of twice the
// target pointer size. A non-zero value must be a power of two. Frame.Alloc is
// always passed an effective alignment that satisfies this guarantee as well as
// llvm.coro.align.
type CoroOptions struct {
	Promise Expr
	Frame   CoroFrameOps
	// BeforeInitialSuspend runs after llvm.coro.begin has produced the handle
	// and before the initial suspend is published. storage is the allocation
	// pointer passed to coro.begin (and may be null when allocation was elided).
	// The callback may initialize the promise/header and register the
	// handle/storage pair, but must leave the builder in the same unterminated
	// insertion block.
	BeforeInitialSuspend func(b Builder, handle, storage Expr)
	// AfterResume runs on every non-final case-0 resume edge immediately after
	// llvm.coro.suspend and before the frontend's resumed continuation. It does
	// not run on a conditional suspend's false edge. The callback may append
	// straight-line resume-prologue instructions only.
	AfterResume func(b Builder)
	// AfterResumeDispatch is the control-flow form of AfterResume. It runs in
	// a compiler-owned gate on every non-final case-0 resume edge. normal is a
	// fresh compiler-owned block in which the resumed frontend continuation
	// begins. The callback must terminate the gate without changing the
	// builder's insertion block; it may branch to normal or to a frontend-owned
	// shared cleanup block captured by the callback. AfterResume and
	// AfterResumeDispatch are mutually exclusive.
	AfterResumeDispatch CoroResumeDispatch
	AllocationAlign     uint32
}

// CoroResumeDispatch emits a terminating decision in a non-final coroutine
// resume gate. normal is the compiler-owned normal continuation. A dispatch
// callback may branch to another frontend-owned block (for example a shared
// language cleanup path), but it must not emit into that destination itself.
type CoroResumeDispatch func(b Builder, normal BasicBlock)

// CoroFrameDescriptorOptions describes the target-specific constant passed to
// the coroutine frame allocator and deallocator. ABIHash is computed by the
// frontend from the complete logical/physical function ABI. Result is the
// external result-slot payload type and must be non-nil.
type CoroFrameDescriptorOptions struct {
	Version  uint32
	ABIHash  [16]byte
	Flags    uint32
	Result   Type
	Function string
	File     string
}

// CoroRootFactoryDescriptorOptions describes the target-specific constant
// used to create a root coroutine. ABIHash is computed by the frontend from
// the complete logical/physical root ABI. Factory must be a constant function
// declaration or function pointer in the same package module with the fixed
// (unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) -> unsafe.Pointer ABI.
// Startup and Result are payload types from the package Program, not pointer
// types, and must be non-nil concrete types.
type CoroRootFactoryDescriptorOptions struct {
	Version uint32
	ABIHash [16]byte
	Flags   uint32
	Factory Expr
	Startup Type
	Result  Type
}

// CoroRootPackageAnchorOptions describes the linker-visible package root
// anchor. Descriptors must be non-empty constant root descriptor globals from
// this package module. ABIHash identifies the complete package root registry
// ABI, including the ordered descriptor list.
//
// The anchor flags field is reserved and is emitted as zero in this ABI.
type CoroRootPackageAnchorOptions struct {
	Version     uint32
	ABIHash     [16]byte
	Descriptors []Expr
}

// CoroProgramManifestOptions describes the entry module's complete package
// root catalog. PackageAnchors are constant package anchor globals declared or
// defined in the entry module. ABIHash identifies the ordered package catalog.
// Bootstrap may be Nil while the runtime catalog remains fail-closed, or a
// constant function/global pointer from the entry module.
//
// The manifest flags field is reserved and is emitted as zero in this ABI.
type CoroProgramManifestOptions struct {
	Version        uint32
	ABIHash        [16]byte
	PackageAnchors []Expr
	Bootstrap      Expr
}

// CoroProgramStepKind identifies one statically ordered program startup step.
// The numeric values are shared by the version-one and version-two runtime
// ABIs; zero is reserved so a zero-initialized or missing step always fails
// validation.
type CoroProgramStepKind uint32

const (
	// CoroProgramStepDirectPlain calls Target through the fixed void() C ABI.
	// Aux must be zero.
	CoroProgramStepDirectPlain CoroProgramStepKind = 1 + iota
	// CoroProgramStepCoroRoot resolves descriptor index Aux through the package
	// root anchor in Target.
	CoroProgramStepCoroRoot
)

// Version-one startup step role flags. Exactly one role is required on every
// step, and the canonical table order is Init followed by Main.
const (
	CoroProgramStepInit uint32 = 1 << iota
	CoroProgramStepMain
)

// Version-two startup step role flags. The bits intentionally start at bit
// zero again: a bootstrap version selects the meaning of the complete table,
// and a step role is never interpreted without first validating that version.
// Exactly one role is required on every step in this order.
const (
	CoroProgramStepInternalRuntimeInitV2 uint32 = 1 << iota
	CoroProgramStepCompilerABIInitV2
	CoroProgramStepPublicRuntimeInitV2
	CoroProgramStepPackageInitV2
	CoroProgramStepMainV2
)

const coroProgramStepMinimumV2 = 5

func coroProgramStepRoleV2(index, count int) (uint32, bool) {
	if count < coroProgramStepMinimumV2 || index < 0 || index >= count {
		return 0, false
	}
	switch index {
	case 0:
		return CoroProgramStepInternalRuntimeInitV2, true
	case 1:
		return CoroProgramStepCompilerABIInitV2, true
	case 2:
		return CoroProgramStepPublicRuntimeInitV2, true
	case count - 1:
		return CoroProgramStepMainV2, true
	default:
		return CoroProgramStepPackageInitV2, true
	}
}

// CoroProgramStep describes one entry in a versioned program startup table.
// Flags is the exact role required at the entry's canonical position for that
// bootstrap version. Target must be a same-module constant function for
// DirectPlain or a same-module constant global for CoroRoot. Aux is encoded as
// target uintptr and is the root descriptor index for CoroRoot.
type CoroProgramStep struct {
	Kind   CoroProgramStepKind
	Flags  uint32
	Target Expr
	Aux    uint64
}

// CoroProgramBootstrapOptions describes the entry module's immutable startup
// table. Version must be one or two. Version one requires zero flags; version
// two accepts the CoroProgramBootstrapFlag* capability bits below.
// ABIHash covers the ordered steps and their referenced catalog. Factory may
// be Nil in the data-only phase; a non-Nil factory must use the root factory
// ABI and belong to this module.
type CoroProgramBootstrapOptions struct {
	Version uint32
	Flags   uint32
	ABIHash [16]byte
	Steps   []CoroProgramStep
	Factory Expr
}

// CoroProgramBootstrapFlagWorkerV2 says that the final physical program owns
// at least one reachable bounded-worker transaction. It is program demand,
// not a declaration that the selected target supports workers.
const CoroProgramBootstrapFlagWorkerV2 uint32 = 1 << 0

// NewCoroFrameDescriptor defines a link-once constant descriptor with layout:
//
//	{ version i32, flags i32, hashLo i64, hashHi i64,
//	  resultSize uintptr, resultAlign uintptr,
//	  function { data ptr, length uintptr },
//	  file { data ptr, length uintptr } }
//
// The returned expression points at the descriptor. The hash words use big
// endian byte order so their textual IR form is deterministic across hosts.
// Trace text deliberately uses the target-neutral two-word Go string layout
// directly instead of asking Program.String for the runtime's named string
// type. Compiler-owned bootstrap modules and closed ABI fixtures do not import
// runtime, yet their descriptors must have exactly the same byte layout.
func (p Package) NewCoroFrameDescriptor(name string, opts CoroFrameDescriptorOptions) Expr {
	if name == "" {
		panic("ssa: coroutine frame descriptor requires a name")
	}
	if opts.Result == nil {
		panic("ssa: coroutine frame descriptor requires a result type")
	}
	if opts.Function == "" {
		panic("ssa: coroutine frame descriptor requires a logical function name")
	}
	prog := p.Prog
	traceTextType := prog.Struct(prog.VoidPtr(), prog.Uintptr())
	descriptorType := prog.Struct(
		prog.Uint32(),
		prog.Uint32(),
		prog.Uint64(),
		prog.Uint64(),
		prog.Uintptr(),
		prog.Uintptr(),
		traceTextType,
		traceTextType,
	)
	descriptor := p.NewVarEx(name, prog.Pointer(descriptorType))
	traceText := func(value string) llvm.Value {
		return prog.ctx.ConstStruct([]llvm.Value{
			p.createGlobalStr(value),
			prog.IntVal(uint64(len(value)), prog.Uintptr()).impl,
		}, false)
	}
	fields := []llvm.Value{
		prog.IntVal(uint64(opts.Version), prog.Uint32()).impl,
		prog.IntVal(uint64(opts.Flags), prog.Uint32()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[:8]), prog.Uint64()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[8:]), prog.Uint64()).impl,
		prog.IntVal(prog.SizeOf(opts.Result), prog.Uintptr()).impl,
		prog.IntVal(uint64(prog.td.ABITypeAlignment(opts.Result.ll)), prog.Uintptr()).impl,
		traceText(opts.Function),
		traceText(opts.File),
	}
	descriptor.impl.SetInitializer(prog.ctx.ConstStruct(fields, false))
	descriptor.impl.SetGlobalConstant(true)
	descriptor.impl.SetLinkage(llvm.LinkOnceODRLinkage)
	descriptor.impl.SetUnnamedAddr(true)
	return descriptor.Expr
}

// NewCoroRootFactoryDescriptor defines a link-once constant descriptor with
// layout:
//
//	{ version i32, flags i32, hashLo i64, hashHi i64, factory ptr,
//	  startupSize uintptr, startupAlign uintptr,
//	  resultSize uintptr, resultAlign uintptr }
//
// The returned expression points at the descriptor. Size, alignment, and
// uintptr fields follow the package target data layout. The hash words use big
// endian byte order so their textual IR form is deterministic across hosts.
func (p Package) NewCoroRootFactoryDescriptor(
	name string, opts CoroRootFactoryDescriptorOptions,
) Expr {
	if name == "" {
		panic("ssa: coroutine root factory descriptor requires a name")
	}
	if opts.Factory.IsNil() ||
		(opts.Factory.kind != vkFuncDecl && opts.Factory.kind != vkFuncPtr) ||
		opts.Factory.impl.IsAConstant().IsNil() ||
		!opts.Factory.impl.IsAConstantPointerNull().IsNil() {
		panic("ssa: coroutine root factory descriptor requires a non-null constant function factory")
	}
	factoryFunction := coroRootFactoryFunction(opts.Factory.impl)
	if factoryFunction.IsNil() || factoryFunction.GlobalParent().C != p.mod.C {
		panic("ssa: coroutine root factory descriptor requires a factory from the same package module")
	}
	if !isCoroRootFactorySignature(opts.Factory.RawType()) {
		panic("ssa: coroutine root factory descriptor requires factory signature (unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) -> unsafe.Pointer")
	}
	if opts.Startup == nil || opts.Startup.kind == vkInvalid {
		panic("ssa: coroutine root factory descriptor requires a concrete startup type")
	}
	if opts.Result == nil || opts.Result.kind == vkInvalid {
		panic("ssa: coroutine root factory descriptor requires a concrete result type")
	}

	prog := p.Prog
	if opts.Startup.ll.Context().C != prog.ctx.C {
		panic("ssa: coroutine root factory descriptor startup type belongs to another program")
	}
	if opts.Result.ll.Context().C != prog.ctx.C {
		panic("ssa: coroutine root factory descriptor result type belongs to another program")
	}
	descriptorType := prog.Struct(
		prog.Uint32(),
		prog.Uint32(),
		prog.Uint64(),
		prog.Uint64(),
		prog.VoidPtr(),
		prog.Uintptr(),
		prog.Uintptr(),
		prog.Uintptr(),
		prog.Uintptr(),
	)
	descriptor := p.NewVarEx(name, prog.Pointer(descriptorType))
	factory := opts.Factory.impl
	if factory.Type().C != prog.VoidPtr().ll.C {
		factory = llvm.ConstBitCast(factory, prog.VoidPtr().ll)
	}
	fields := []llvm.Value{
		prog.IntVal(uint64(opts.Version), prog.Uint32()).impl,
		prog.IntVal(uint64(opts.Flags), prog.Uint32()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[:8]), prog.Uint64()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[8:]), prog.Uint64()).impl,
		factory,
		prog.IntVal(prog.SizeOf(opts.Startup), prog.Uintptr()).impl,
		prog.IntVal(uint64(prog.td.ABITypeAlignment(opts.Startup.ll)), prog.Uintptr()).impl,
		prog.IntVal(prog.SizeOf(opts.Result), prog.Uintptr()).impl,
		prog.IntVal(uint64(prog.td.ABITypeAlignment(opts.Result.ll)), prog.Uintptr()).impl,
	}
	descriptor.impl.SetInitializer(prog.ctx.ConstStruct(fields, false))
	descriptor.impl.SetGlobalConstant(true)
	descriptor.impl.SetLinkage(llvm.LinkOnceODRLinkage)
	descriptor.impl.SetUnnamedAddr(true)
	// Root descriptors are runtime/linker discovery points and otherwise have
	// no ordinary IR user. llvm.used preserves the descriptor through final-link
	// dead stripping; its initializer keeps the typed wrapper reachable.
	p.markLLVMRetained(descriptor.impl)
	return descriptor.Expr
}

// NewCoroRootPackageAnchor defines one externally named, hidden package root
// anchor and its explicit descriptor pointer array. Its layout is:
//
//	{ version i32, flags i32, hashLo i64, hashHi i64,
//	  count uintptr, entries ptr }
//
// The entries array is an internal constant named name + ".entries". The
// anchor is retained through llvm.used so final-link dead stripping cannot
// remove the registry after its package object has been selected from an
// archive. The external anchor name lets the build driver select that archive
// member explicitly; runtime discovery never relies on section enumeration.
// Each package may define at most one root anchor.
func (p Package) NewCoroRootPackageAnchor(
	name string, opts CoroRootPackageAnchorOptions,
) Expr {
	if name == "" {
		panic("ssa: coroutine root package anchor requires a name")
	}
	if p.coroRootAnchor != "" {
		panic(fmt.Sprintf("ssa: coroutine root package anchor already defined as %q", p.coroRootAnchor))
	}
	if len(opts.Descriptors) == 0 {
		panic("ssa: coroutine root package anchor requires at least one descriptor")
	}

	entriesName := name + ".entries"
	for _, symbol := range []string{name, entriesName} {
		_, knownGlobal := p.vars[symbol]
		_, knownFunction := p.fns[symbol]
		if knownGlobal || knownFunction ||
			!p.mod.NamedGlobal(symbol).IsNil() || !p.mod.NamedFunction(symbol).IsNil() {
			panic(fmt.Sprintf("ssa: coroutine root package anchor symbol %q already exists", symbol))
		}
	}

	values := make([]llvm.Value, len(opts.Descriptors))
	seen := make(map[llvm.Value]struct{}, len(opts.Descriptors))
	voidPtrType := p.Prog.VoidPtr().ll
	for i, descriptor := range opts.Descriptors {
		if descriptor.IsNil() || descriptor.impl.IsNil() || descriptor.impl.IsAConstant().IsNil() {
			panic(fmt.Sprintf("ssa: coroutine root package anchor descriptor %d is not a constant global", i))
		}
		global := descriptor.impl.IsAGlobalVariable()
		if global.IsNil() || !global.IsGlobalConstant() || global.Initializer().IsNil() ||
			global.Initializer().IsAConstant().IsNil() {
			panic(fmt.Sprintf("ssa: coroutine root package anchor descriptor %d is not a constant global", i))
		}
		if global.GlobalParent().C != p.mod.C {
			panic(fmt.Sprintf("ssa: coroutine root package anchor descriptor %d belongs to another package module", i))
		}
		if _, exists := seen[global]; exists {
			panic(fmt.Sprintf("ssa: coroutine root package anchor contains duplicate descriptor %q", global.Name()))
		}
		seen[global] = struct{}{}
		value := global
		if value.Type().C != voidPtrType.C {
			value = llvm.ConstBitCast(value, voidPtrType)
		}
		values[i] = value
	}

	prog := p.Prog
	entriesType := prog.rawType(types.NewArray(types.Typ[types.UnsafePointer], int64(len(values))))
	entries := p.NewVarEx(entriesName, prog.Pointer(entriesType))
	entries.impl.SetInitializer(llvm.ConstArray(voidPtrType, values))
	entries.impl.SetGlobalConstant(true)
	entries.impl.SetLinkage(llvm.InternalLinkage)
	entries.impl.SetUnnamedAddr(true)

	anchorType := prog.Struct(
		prog.Uint32(),
		prog.Uint32(),
		prog.Uint64(),
		prog.Uint64(),
		prog.Uintptr(),
		prog.VoidPtr(),
	)
	anchor := p.NewVarEx(name, prog.Pointer(anchorType))
	entriesPointer := entries.impl
	if entriesPointer.Type().C != voidPtrType.C {
		entriesPointer = llvm.ConstBitCast(entriesPointer, voidPtrType)
	}
	fields := []llvm.Value{
		prog.IntVal(uint64(opts.Version), prog.Uint32()).impl,
		prog.IntVal(0, prog.Uint32()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[:8]), prog.Uint64()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[8:]), prog.Uint64()).impl,
		prog.IntVal(uint64(len(values)), prog.Uintptr()).impl,
		entriesPointer,
	}
	anchor.impl.SetInitializer(prog.ctx.ConstStruct(fields, false))
	anchor.impl.SetGlobalConstant(true)
	anchor.impl.SetLinkage(llvm.ExternalLinkage)
	anchor.impl.SetVisibility(llvm.HiddenVisibility)
	p.markLLVMRetained(anchor.impl)
	p.coroRootAnchor = name
	return anchor.Expr
}

// CoroRootPackageAnchor returns the linker-visible root anchor symbol emitted
// by this package, or an empty string when the package has no root anchor.
func (p Package) CoroRootPackageAnchor() string {
	return p.coroRootAnchor
}

// NewCoroProgramManifest defines the entry module's one externally named,
// hidden program manifest. Its layout is:
//
//	{ version i32, flags i32, hashLo i64, hashHi i64,
//	  packageCount uintptr, packages ptr, bootstrap ptr }
//
// A non-empty package catalog is materialized as an internal constant pointer
// array named name + ".packages". An empty catalog uses count zero and a null
// packages pointer, without creating an empty array. External package anchor
// declarations are normalized to constant declarations; definitions must
// already be constant. The manifest is retained through llvm.used. Each entry
// module may define at most one program manifest.
func (p Package) NewCoroProgramManifest(
	name string, opts CoroProgramManifestOptions,
) Expr {
	if name == "" {
		panic("ssa: coroutine program manifest requires a name")
	}
	if p.coroProgramManifest != "" {
		panic(fmt.Sprintf("ssa: coroutine program manifest already defined as %q", p.coroProgramManifest))
	}

	packagesName := name + ".packages"
	symbols := []string{name}
	if len(opts.PackageAnchors) != 0 {
		symbols = append(symbols, packagesName)
	}
	for _, symbol := range symbols {
		_, knownGlobal := p.vars[symbol]
		_, knownFunction := p.fns[symbol]
		if knownGlobal || knownFunction ||
			!p.mod.NamedGlobal(symbol).IsNil() || !p.mod.NamedFunction(symbol).IsNil() {
			panic(fmt.Sprintf("ssa: coroutine program manifest symbol %q already exists", symbol))
		}
	}

	voidPtrType := p.Prog.VoidPtr().ll
	packageValues := make([]llvm.Value, len(opts.PackageAnchors))
	packageDeclarations := make([]llvm.Value, 0, len(opts.PackageAnchors))
	seen := make(map[llvm.Value]struct{}, len(opts.PackageAnchors))
	for i, anchor := range opts.PackageAnchors {
		if anchor.IsNil() || anchor.impl.IsNil() || anchor.impl.IsAConstant().IsNil() ||
			!anchor.impl.IsAConstantPointerNull().IsNil() {
			panic(fmt.Sprintf("ssa: coroutine program manifest package anchor %d is not a non-null constant global", i))
		}
		global := coroManifestGlobal(anchor.impl)
		if global.IsNil() {
			panic(fmt.Sprintf("ssa: coroutine program manifest package anchor %d is not a non-null constant global", i))
		}
		if global.GlobalParent().C != p.mod.C {
			panic(fmt.Sprintf("ssa: coroutine program manifest package anchor %d belongs to another entry module", i))
		}
		if _, exists := seen[global]; exists {
			panic(fmt.Sprintf("ssa: coroutine program manifest contains duplicate package anchor %q", global.Name()))
		}
		seen[global] = struct{}{}
		if global.Initializer().IsNil() {
			packageDeclarations = append(packageDeclarations, global)
		} else if !global.IsGlobalConstant() || global.Initializer().IsAConstant().IsNil() {
			panic(fmt.Sprintf("ssa: coroutine program manifest package anchor %d is not a constant global", i))
		}
		value := anchor.impl
		if value.Type().C != voidPtrType.C {
			value = llvm.ConstBitCast(value, voidPtrType)
		}
		packageValues[i] = value
	}

	bootstrap := llvm.ConstNull(voidPtrType)
	if !opts.Bootstrap.IsNil() {
		if opts.Bootstrap.impl.IsNil() || opts.Bootstrap.impl.IsAConstant().IsNil() ||
			!opts.Bootstrap.impl.IsAConstantPointerNull().IsNil() {
			panic("ssa: coroutine program manifest bootstrap is not a non-null constant function/global pointer")
		}
		base := coroManifestPointerBase(opts.Bootstrap.impl)
		if base.IsNil() || (base.IsAFunction().IsNil() && base.IsAGlobalVariable().IsNil()) {
			panic("ssa: coroutine program manifest bootstrap is not a non-null constant function/global pointer")
		}
		if base.GlobalParent().C != p.mod.C {
			panic("ssa: coroutine program manifest bootstrap belongs to another entry module")
		}
		bootstrap = opts.Bootstrap.impl
		if bootstrap.Type().C != voidPtrType.C {
			bootstrap = llvm.ConstBitCast(bootstrap, voidPtrType)
		}
	}

	// Commit declaration normalization only after all validation succeeds.
	for _, declaration := range packageDeclarations {
		declaration.SetGlobalConstant(true)
	}

	packages := llvm.ConstNull(voidPtrType)
	if len(packageValues) != 0 {
		prog := p.Prog
		packagesType := prog.rawType(types.NewArray(types.Typ[types.UnsafePointer], int64(len(packageValues))))
		array := p.NewVarEx(packagesName, prog.Pointer(packagesType))
		array.impl.SetInitializer(llvm.ConstArray(voidPtrType, packageValues))
		array.impl.SetGlobalConstant(true)
		array.impl.SetLinkage(llvm.InternalLinkage)
		array.impl.SetUnnamedAddr(true)
		packages = array.impl
		if packages.Type().C != voidPtrType.C {
			packages = llvm.ConstBitCast(packages, voidPtrType)
		}
	}

	prog := p.Prog
	manifestType := prog.Struct(
		prog.Uint32(),
		prog.Uint32(),
		prog.Uint64(),
		prog.Uint64(),
		prog.Uintptr(),
		prog.VoidPtr(),
		prog.VoidPtr(),
	)
	manifest := p.NewVarEx(name, prog.Pointer(manifestType))
	fields := []llvm.Value{
		prog.IntVal(uint64(opts.Version), prog.Uint32()).impl,
		prog.IntVal(0, prog.Uint32()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[:8]), prog.Uint64()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[8:]), prog.Uint64()).impl,
		prog.IntVal(uint64(len(packageValues)), prog.Uintptr()).impl,
		packages,
		bootstrap,
	}
	manifest.impl.SetInitializer(prog.ctx.ConstStruct(fields, false))
	manifest.impl.SetGlobalConstant(true)
	manifest.impl.SetLinkage(llvm.ExternalLinkage)
	manifest.impl.SetVisibility(llvm.HiddenVisibility)
	p.markLLVMRetained(manifest.impl)
	p.coroProgramManifest = name
	return manifest.Expr
}

// CoroProgramManifest returns the linker-visible program manifest symbol
// emitted by this entry module, or an empty string when none was emitted.
func (p Package) CoroProgramManifest() string {
	return p.coroProgramManifest
}

// NewCoroProgramBootstrap defines the entry module's one externally named,
// hidden program startup descriptor. Its layout is:
//
//	{ version i32, flags i32, hashLo i64, hashHi i64,
//	  stepCount uintptr, steps ptr, factory ptr }
//
// The canonical version-specific step list is materialized as an internal
// constant array named name + ".steps", whose element layout is:
//
//	{ kind i32, flags i32, target ptr, aux uintptr }
//
// Version one requires exactly Init, Main. Version two requires
// InternalRuntimeInit, CompilerABIInit, PublicRuntimeInit, one or more
// PackageInit steps in Go initialization order, and Main. Factory is null when
// omitted. Both the table and each step use target uintptr width and alignment.
// Each entry module may define at most one program bootstrap descriptor.
func (p Package) NewCoroProgramBootstrap(
	name string, opts CoroProgramBootstrapOptions,
) Expr {
	if name == "" {
		panic("ssa: coroutine program bootstrap requires a name")
	}
	if p.coroProgramBootstrap != "" {
		panic(fmt.Sprintf("ssa: coroutine program bootstrap already defined as %q", p.coroProgramBootstrap))
	}
	var roles []uint32
	switch opts.Version {
	case 1:
		if opts.Flags != 0 {
			panic("ssa: coroutine program bootstrap version 1 flags must be zero")
		}
		roles = []uint32{CoroProgramStepInit, CoroProgramStepMain}
	case 2:
		if unknown := opts.Flags &^ CoroProgramBootstrapFlagWorkerV2; unknown != 0 {
			panic(fmt.Sprintf("ssa: coroutine program bootstrap version 2 has unknown capability flags %#x", unknown))
		}
		if len(opts.Steps) < coroProgramStepMinimumV2 {
			panic(fmt.Sprintf(
				"ssa: coroutine program bootstrap version %d requires at least %d steps, got %d",
				opts.Version, coroProgramStepMinimumV2, len(opts.Steps),
			))
		}
		roles = make([]uint32, len(opts.Steps))
		for index := range roles {
			roles[index], _ = coroProgramStepRoleV2(index, len(roles))
		}
	default:
		panic(fmt.Sprintf("ssa: coroutine program bootstrap has unsupported version %d", opts.Version))
	}
	if len(opts.Steps) != len(roles) {
		panic(fmt.Sprintf("ssa: coroutine program bootstrap requires exactly two steps, got %d", len(opts.Steps)))
	}
	if !coroProgramFitsUintptr(p.Prog, uint64(len(opts.Steps))) {
		panic("ssa: coroutine program bootstrap step count overflows target uintptr")
	}

	stepsName := name + ".steps"
	symbols := []string{name, stepsName}
	for _, symbol := range symbols {
		_, knownGlobal := p.vars[symbol]
		_, knownFunction := p.fns[symbol]
		if knownGlobal || knownFunction ||
			!p.mod.NamedGlobal(symbol).IsNil() || !p.mod.NamedFunction(symbol).IsNil() {
			panic(fmt.Sprintf("ssa: coroutine program bootstrap symbol %q already exists", symbol))
		}
	}

	prog := p.Prog
	voidPtrType := prog.VoidPtr().ll
	stepType := prog.Struct(
		prog.Uint32(),
		prog.Uint32(),
		prog.VoidPtr(),
		prog.Uintptr(),
	)
	stepValues := make([]llvm.Value, len(opts.Steps))
	constantDeclarations := make([]llvm.Value, 0, len(opts.Steps))
	for i, step := range opts.Steps {
		wantRole := roles[i]
		if step.Flags != wantRole {
			panic(fmt.Sprintf(
				"ssa: coroutine program bootstrap step %d flags %#x must be %#x",
				i, step.Flags, wantRole,
			))
		}
		if step.Target.IsNil() || step.Target.impl.IsNil() ||
			step.Target.impl.IsAConstant().IsNil() ||
			step.Target.impl.Type().TypeKind() != llvm.PointerTypeKind ||
			!step.Target.impl.IsAConstantPointerNull().IsNil() {
			panic(fmt.Sprintf("ssa: coroutine program bootstrap step %d target is not a non-null constant pointer", i))
		}
		if !coroProgramFitsUintptr(prog, step.Aux) {
			panic(fmt.Sprintf("ssa: coroutine program bootstrap step %d aux overflows target uintptr", i))
		}

		target := step.Target.impl
		switch step.Kind {
		case CoroProgramStepDirectPlain:
			if step.Aux != 0 {
				panic(fmt.Sprintf("ssa: coroutine program bootstrap direct-plain step %d aux must be zero", i))
			}
			function := coroRootFactoryFunction(target)
			if function.IsNil() {
				panic(fmt.Sprintf("ssa: coroutine program bootstrap direct-plain step %d target is not a constant function", i))
			}
			if function.GlobalParent().C != p.mod.C {
				panic(fmt.Sprintf("ssa: coroutine program bootstrap direct-plain step %d target belongs to another entry module", i))
			}
			if !isCoroProgramDirectPlainSignature(step.Target.RawType()) {
				panic(fmt.Sprintf("ssa: coroutine program bootstrap direct-plain step %d requires target signature ()", i))
			}

		case CoroProgramStepCoroRoot:
			global := coroManifestGlobal(target)
			if global.IsNil() {
				panic(fmt.Sprintf("ssa: coroutine program bootstrap coro-root step %d target is not a constant global", i))
			}
			if global.GlobalParent().C != p.mod.C {
				panic(fmt.Sprintf("ssa: coroutine program bootstrap coro-root step %d target belongs to another entry module", i))
			}
			if global.Initializer().IsNil() {
				constantDeclarations = append(constantDeclarations, global)
			} else if !global.IsGlobalConstant() || global.Initializer().IsAConstant().IsNil() {
				panic(fmt.Sprintf("ssa: coroutine program bootstrap coro-root step %d target is not a constant global", i))
			}

		default:
			panic(fmt.Sprintf("ssa: coroutine program bootstrap step %d has invalid kind %d", i, step.Kind))
		}

		if target.Type().C != voidPtrType.C {
			target = llvm.ConstBitCast(target, voidPtrType)
		}
		stepValues[i] = prog.ctx.ConstStruct([]llvm.Value{
			prog.IntVal(uint64(step.Kind), prog.Uint32()).impl,
			prog.IntVal(uint64(step.Flags), prog.Uint32()).impl,
			target,
			prog.IntVal(step.Aux, prog.Uintptr()).impl,
		}, false)
	}

	factory := llvm.ConstNull(voidPtrType)
	if !opts.Factory.IsNil() {
		if opts.Factory.impl.IsNil() || opts.Factory.impl.IsAConstant().IsNil() ||
			!opts.Factory.impl.IsAConstantPointerNull().IsNil() {
			panic("ssa: coroutine program bootstrap factory is not a non-null constant function")
		}
		function := coroRootFactoryFunction(opts.Factory.impl)
		if function.IsNil() {
			panic("ssa: coroutine program bootstrap factory is not a non-null constant function")
		}
		if function.GlobalParent().C != p.mod.C {
			panic("ssa: coroutine program bootstrap factory belongs to another entry module")
		}
		if !isCoroRootFactorySignature(opts.Factory.RawType()) {
			panic("ssa: coroutine program bootstrap requires factory signature (unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) -> unsafe.Pointer")
		}
		factory = opts.Factory.impl
		if factory.Type().C != voidPtrType.C {
			factory = llvm.ConstBitCast(factory, voidPtrType)
		}
	}

	// Commit declaration normalization only after every input has validated.
	for _, declaration := range constantDeclarations {
		declaration.SetGlobalConstant(true)
	}

	steps := llvm.ConstNull(voidPtrType)
	if len(stepValues) != 0 {
		stepsArrayType := prog.rawType(types.NewArray(stepType.RawType(), int64(len(stepValues))))
		array := p.NewVarEx(stepsName, prog.Pointer(stepsArrayType))
		array.impl.SetInitializer(llvm.ConstArray(stepType.ll, stepValues))
		array.impl.SetGlobalConstant(true)
		array.impl.SetLinkage(llvm.InternalLinkage)
		array.impl.SetUnnamedAddr(true)
		steps = array.impl
		if steps.Type().C != voidPtrType.C {
			steps = llvm.ConstBitCast(steps, voidPtrType)
		}
	}

	bootstrapType := prog.Struct(
		prog.Uint32(),
		prog.Uint32(),
		prog.Uint64(),
		prog.Uint64(),
		prog.Uintptr(),
		prog.VoidPtr(),
		prog.VoidPtr(),
	)
	bootstrap := p.NewVarEx(name, prog.Pointer(bootstrapType))
	bootstrap.impl.SetInitializer(prog.ctx.ConstStruct([]llvm.Value{
		prog.IntVal(uint64(opts.Version), prog.Uint32()).impl,
		prog.IntVal(uint64(opts.Flags), prog.Uint32()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[:8]), prog.Uint64()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[8:]), prog.Uint64()).impl,
		prog.IntVal(uint64(len(stepValues)), prog.Uintptr()).impl,
		steps,
		factory,
	}, false))
	bootstrap.impl.SetGlobalConstant(true)
	bootstrap.impl.SetLinkage(llvm.ExternalLinkage)
	bootstrap.impl.SetVisibility(llvm.HiddenVisibility)
	p.markLLVMRetained(bootstrap.impl)
	p.coroProgramBootstrap = name
	return bootstrap.Expr
}

// CoroProgramBootstrap returns the linker-visible program bootstrap symbol
// emitted by this entry module, or an empty string when none was emitted.
func (p Package) CoroProgramBootstrap() string {
	return p.coroProgramBootstrap
}

func coroProgramFitsUintptr(prog Program, value uint64) bool {
	bits := prog.PointerSize() * 8
	return bits >= 64 || value < uint64(1)<<bits
}

func isCoroProgramDirectPlainSignature(typ types.Type) bool {
	sig, ok := typ.(*types.Signature)
	return ok && sig.Recv() == nil && !sig.Variadic() &&
		sig.Params().Len() == 0 && sig.Results().Len() == 0
}

func coroManifestGlobal(value llvm.Value) llvm.Value {
	return coroManifestPointerBase(value).IsAGlobalVariable()
}

func coroManifestPointerBase(value llvm.Value) llvm.Value {
	for !value.IsAConstantExpr().IsNil() && value.OperandsCount() == 1 {
		value = value.Operand(0)
	}
	return value
}

func coroRootFactoryFunction(value llvm.Value) llvm.Value {
	for !value.IsAConstantExpr().IsNil() && value.OperandsCount() == 1 {
		value = value.Operand(0)
	}
	return value.IsAFunction()
}

func isCoroRootFactorySignature(typ types.Type) bool {
	sig, ok := typ.(*types.Signature)
	if !ok || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 3 || sig.Results().Len() != 1 {
		return false
	}
	pointer := types.Typ[types.UnsafePointer]
	for i := 0; i < sig.Params().Len(); i++ {
		if !types.Identical(sig.Params().At(i).Type(), pointer) {
			return false
		}
	}
	return types.Identical(sig.Results().At(0).Type(), pointer)
}

// CoroBuilder owns the structured presplit control flow for one coroutine.
// It does not define the promise, result, scheduler, or runtime frame ABI.
type CoroBuilder struct {
	b Builder

	id     llvm.Value
	handle Expr
	frame  CoroFrameOps
	// allocationAlign is the literal guarantee supplied to llvm.coro.id. Zero
	// retains LLVM's target-dependent 2*pointer default.
	allocationAlign uint32

	suspendBlk          BasicBlock
	cleanupBlk          BasicBlock
	initialResumeBlk    BasicBlock
	afterResume         func(Builder)
	afterResumeDispatch CoroResumeDispatch
	finished            bool
}

// BeginCoro emits the coroutine allocation prologue and initial suspend. The
// enclosing function must return exactly one unsafe.Pointer coroutine handle.
// On return, b is positioned at the initial-resume body block.
func (b Builder) BeginCoro(opts CoroOptions) *CoroBuilder {
	validateCoroOptions(b, opts)
	markPresplitCoroutine(b.Func)

	prog := b.Prog
	fn := b.Func
	entryBlk := b.blk
	allocBlk := fn.MakeBlock()
	beginBlk := fn.MakeBlock()
	suspendBlk := fn.MakeBlock()
	cleanupBlk := fn.MakeBlock()

	promise := prog.Nil(prog.VoidPtr())
	if !opts.Promise.IsNil() {
		promise = b.Convert(prog.VoidPtr(), opts.Promise)
	}
	null := prog.Nil(prog.VoidPtr())
	align := prog.IntVal(uint64(opts.AllocationAlign), prog.Int32())
	id := b.coroIntrinsic(
		"llvm.coro.id",
		prog.ctx.TokenType(),
		[]llvm.Value{align.impl, promise.impl, null.impl, null.impl},
		"coro.id",
	)
	needAlloc := b.coroIntrinsic(
		"llvm.coro.alloc",
		prog.Bool().ll,
		[]llvm.Value{id},
		"coro.alloc",
	)
	b.If(Expr{needAlloc, prog.Bool()}, allocBlk, beginBlk)

	b.SetBlock(allocBlk)
	size, frameAlign := b.coroFrameLayout(opts.AllocationAlign)
	allocCallbackPoint := captureCoroFrameCallbackPoint(b)
	allocated := opts.Frame.Alloc(b, size, frameAlign)
	allocCallbackPoint.ensureContinuation(b, "allocator")
	if allocated.IsNil() || allocated.kind != vkPtr {
		panic("ssa: coroutine frame allocator returned a non-pointer expression")
	}
	allocated = b.Convert(prog.VoidPtr(), allocated)
	b.Jump(beginBlk)

	b.SetBlock(beginBlk)
	storage := b.Phi(prog.VoidPtr())
	storage.AddIncoming(b, []BasicBlock{entryBlk, allocBlk}, func(i int, _ BasicBlock) Expr {
		if i == 0 {
			return null
		}
		return allocated
	})
	handleValue := b.coroIntrinsic(
		"llvm.coro.begin",
		prog.VoidPtr().ll,
		[]llvm.Value{id, storage.impl},
		"coro.handle",
	)

	coro := &CoroBuilder{
		b:                   b,
		id:                  id,
		handle:              Expr{handleValue, prog.VoidPtr()},
		frame:               opts.Frame,
		allocationAlign:     opts.AllocationAlign,
		suspendBlk:          suspendBlk,
		cleanupBlk:          cleanupBlk,
		afterResume:         opts.AfterResume,
		afterResumeDispatch: opts.AfterResumeDispatch,
	}
	if callback := opts.BeforeInitialSuspend; callback != nil {
		callbackPoint := captureCoroFrameCallbackPoint(b)
		callback(b, coro.handle, storage.Expr)
		callbackPoint.ensureContinuation(b, "before-initial-suspend")
	}
	coro.initialResumeBlk = coro.emitSuspend(false)
	return coro
}

// Handle returns the coroutine handle produced by llvm.coro.begin.
func (c *CoroBuilder) Handle() Expr {
	if c == nil {
		return Nil
	}
	return c.handle
}

// InitialResumeBlock returns the block in which the source coroutine body must
// begin. It is distinct from the ramp entry block, which has already emitted
// allocation, coro.begin, and the initial suspend.
func (c *CoroBuilder) InitialResumeBlock() BasicBlock {
	if c == nil {
		return nil
	}
	return c.initialResumeBlk
}

// Suspend emits a non-final stack cut and positions the builder at the newly
// created resume block. Scheduler state and suspend reasons must be published
// by the caller before invoking Suspend.
func (c *CoroBuilder) Suspend() BasicBlock {
	c.requireActive("suspend")
	return c.emitSuspend(false)
}

// SuspendCurrentBlock emits a non-final stack cut while preserving the
// builder's current logical BasicBlock. The physical resume block becomes the
// logical block's last LLVM block, so later branches and phi incoming edges
// continue to refer to the source block even when one or more coroutine cuts
// split its physical control flow. Frontends lowering a multi-block source CFG
// must use this form; Suspend remains the low-level form that exposes the new
// resume block as a distinct logical block.
func (c *CoroBuilder) SuspendCurrentBlock() BasicBlock {
	c.requireActive("suspend current block")
	b := c.b
	logical := b.blk
	if logical == nil {
		panic("ssa: suspend current block requires an active logical block")
	}
	resume := c.emitSuspend(false)
	logical.last = resume.last
	b.blk = logical
	return logical
}

// SuspendCurrentBlockWithAfterResume is SuspendCurrentBlock with one non-nil
// resume callback that replaces CoroOptions.AfterResume for this suspend only.
// It is the specialization point for a suspension whose resume protocol (for
// example an exact V2 park ticket) differs from the coroutine's default gate.
// The callback may append straight-line instructions only.
func (c *CoroBuilder) SuspendCurrentBlockWithAfterResume(afterResume func(Builder)) BasicBlock {
	c.requireActive("suspend current block with after-resume override")
	if afterResume == nil {
		panic("ssa: suspend current block after-resume override requires a callback")
	}
	b := c.b
	logical := b.blk
	if logical == nil {
		panic("ssa: suspend current block with after-resume override requires an active logical block")
	}
	resume := c.emitSuspendWithAfterResume(false, afterResume)
	logical.last = resume.last
	b.blk = logical
	return logical
}

// SuspendCurrentBlockWithResumeDispatch is SuspendCurrentBlock with one
// non-nil terminating resume dispatch that replaces both CoroOptions resume
// callbacks for this suspend only. The callback runs in a compiler-owned gate
// and receives the compiler-owned normal continuation. After it terminates the
// gate, the builder is restored to normal and the logical block's physical tail
// is updated to that block.
func (c *CoroBuilder) SuspendCurrentBlockWithResumeDispatch(dispatch CoroResumeDispatch) BasicBlock {
	c.requireActive("suspend current block with resume-dispatch override")
	if dispatch == nil {
		panic("ssa: suspend current block resume-dispatch override requires a callback")
	}
	b := c.b
	logical := b.blk
	if logical == nil {
		panic("ssa: suspend current block with resume-dispatch override requires an active logical block")
	}
	resume := c.emitSuspendWithResumeDispatch(false, dispatch)
	logical.last = resume.last
	b.blk = logical
	return logical
}

// SuspendCurrentBlockIf emits a non-final stack cut only on condition's true
// edge. before runs in that edge immediately before llvm.coro.suspend and must
// append straight-line state publication only. Both the false edge and the
// resumed true edge join a new physical continuation that becomes the current
// logical block's tail, preserving source-CFG phi predecessor identity.
func (c *CoroBuilder) SuspendCurrentBlockIf(condition Expr, before func(Builder)) BasicBlock {
	c.requireActive("conditionally suspend current block")
	b := c.b
	logical := b.blk
	if logical == nil {
		panic("ssa: conditionally suspend current block requires an active logical block")
	}
	if condition.IsNil() || condition.kind != vkBool {
		panic("ssa: conditional coroutine suspend requires a boolean condition")
	}
	suspendBlk := b.Func.MakeBlock()
	continueBlk := b.Func.MakeBlock()
	b.If(condition, suspendBlk, continueBlk)

	b.SetBlock(suspendBlk)
	if before != nil {
		callbackPoint := captureCoroFrameCallbackPoint(b)
		before(b)
		callbackPoint.ensureContinuation(b, "conditional-suspend")
	}
	c.emitSuspend(false)
	b.Jump(continueBlk)

	b.SetBlock(continueBlk)
	logical.last = continueBlk.last
	b.blk = logical
	return logical
}

// SuspendCurrentBlockIfWithResumeDispatch combines a conditional stack cut
// with a per-site terminating resume gate. The false edge enters the joined
// continuation directly; only the resumed true edge passes through dispatch.
// This is the specialization point for operations with a synchronous fast
// path and an exact-ticket slow path, such as a channel operation which parks
// only when its first non-blocking attempt fails.
//
// before has the same straight-line publication contract as
// SuspendCurrentBlockIf. dispatch has the same terminating contract as
// SuspendCurrentBlockWithResumeDispatch and replaces the coroutine's default
// resume callbacks for this suspend only.
func (c *CoroBuilder) SuspendCurrentBlockIfWithResumeDispatch(
	condition Expr,
	before func(Builder),
	dispatch CoroResumeDispatch,
) BasicBlock {
	c.requireActive("conditionally suspend current block with resume dispatch")
	if dispatch == nil {
		panic("ssa: conditional coroutine suspend resume-dispatch override requires a callback")
	}
	b := c.b
	logical := b.blk
	if logical == nil {
		panic("ssa: conditional coroutine suspend with resume dispatch requires an active logical block")
	}
	if condition.IsNil() || condition.kind != vkBool {
		panic("ssa: conditional coroutine suspend requires a boolean condition")
	}
	suspendBlk := b.Func.MakeBlock()
	continueBlk := b.Func.MakeBlock()
	b.If(condition, suspendBlk, continueBlk)

	b.SetBlock(suspendBlk)
	if before != nil {
		callbackPoint := captureCoroFrameCallbackPoint(b)
		before(b)
		callbackPoint.ensureContinuation(b, "conditional-suspend")
	}
	c.emitSuspendWithResumeDispatch(false, dispatch)
	b.Jump(continueBlk)

	b.SetBlock(continueBlk)
	logical.last = continueBlk.last
	b.blk = logical
	return logical
}

// Finish emits the final suspend and completes the shared cleanup/return
// blocks. No further instructions may be emitted through c afterwards.
func (c *CoroBuilder) Finish() {
	c.requireActive("finish")
	c.finished = true

	b := c.b
	prog := b.Prog
	fn := b.Func
	finalResult := c.suspendIntrinsic(true)
	invalidResumeBlk := fn.MakeBlock()
	switchValue := b.impl.CreateSwitch(finalResult, c.suspendBlk.first, 2)
	switchValue.AddCase(llvm.ConstInt(prog.tyInt8(), 0, false), invalidResumeBlk.first)
	switchValue.AddCase(llvm.ConstInt(prog.tyInt8(), 1, false), c.cleanupBlk.first)

	b.SetBlock(invalidResumeBlk)
	b.coroIntrinsic("llvm.trap", prog.Void().ll, nil, "")
	b.Unreachable()

	b.SetBlock(c.cleanupBlk)
	frameValue := b.coroIntrinsic(
		"llvm.coro.free",
		prog.VoidPtr().ll,
		[]llvm.Value{c.id, c.handle.impl},
		"coro.frame",
	)
	frame := Expr{frameValue, prog.VoidPtr()}
	freeBlk := fn.MakeBlock()
	afterFreeBlk := fn.MakeBlock()
	nonNull := llvm.CreateICmp(b.impl, llvm.IntNE, frame.impl, prog.Nil(prog.VoidPtr()).impl)
	b.If(Expr{nonNull, prog.Bool()}, freeBlk, afterFreeBlk)

	b.SetBlock(freeBlk)
	size, align := b.coroFrameLayout(c.allocationAlign)
	freeCallbackPoint := captureCoroFrameCallbackPoint(b)
	c.frame.Free(b, frame, size, align)
	freeCallbackPoint.ensureContinuation(b, "free")
	b.Jump(afterFreeBlk)

	b.SetBlock(afterFreeBlk)
	b.Jump(c.suspendBlk)

	// LLVM's canonical switched-resume shape sends every suspend default edge
	// and the cleanup edge through one coro.end block. CoroSplit keeps the
	// following handle return in the ramp and replaces coro.end with ret void in
	// the resume/destroy functions.
	b.SetBlock(c.suspendBlk)
	b.coroEnd(c.handle)
	b.Return(c.handle)
}

func (c *CoroBuilder) emitSuspend(final bool) BasicBlock {
	return c.emitSuspendWithCallbacks(final, c.afterResume, c.afterResumeDispatch)
}

func (c *CoroBuilder) emitSuspendWithAfterResume(final bool, afterResume func(Builder)) BasicBlock {
	return c.emitSuspendWithCallbacks(final, afterResume, nil)
}

func (c *CoroBuilder) emitSuspendWithResumeDispatch(final bool, dispatch CoroResumeDispatch) BasicBlock {
	return c.emitSuspendWithCallbacks(final, nil, dispatch)
}

func (c *CoroBuilder) emitSuspendWithCallbacks(
	final bool, afterResume func(Builder), dispatch CoroResumeDispatch,
) BasicBlock {
	if afterResume != nil && dispatch != nil {
		panic("ssa: coroutine resume callbacks are mutually exclusive")
	}
	b := c.b
	prog := b.Prog
	resumeBlk := b.Func.MakeBlock()
	normalBlk := resumeBlk
	if !final && dispatch != nil {
		// A terminating dispatch needs a destination that it cannot accidentally
		// populate. Keeping normal distinct also lets this helper restore the
		// frontend insertion point after validating the gate.
		normalBlk = b.Func.MakeBlock()
	}
	result := c.suspendIntrinsic(final)
	switchValue := b.impl.CreateSwitch(result, c.suspendBlk.first, 2)
	switchValue.AddCase(llvm.ConstInt(prog.tyInt8(), 0, false), resumeBlk.first)
	switchValue.AddCase(llvm.ConstInt(prog.tyInt8(), 1, false), c.cleanupBlk.first)
	if !final {
		// A coroutine's normal continuation is entered by a later resume call,
		// not by the ramp invocation which first executes this switch. Generic
		// block-frequency analysis otherwise assigns the resume arm roughly one
		// third of the entry frequency and LLVM 22's CoroAnnotationElide rejects
		// every source-style static await as cold (its default threshold is 55%).
		// Describe the language-level common path explicitly. The suspend return
		// and destroy arms stay possible and retain their exact control flow.
		ctx := prog.ctx
		switchValue.SetMetadata(ctx.MDKindID("prof"), ctx.MDNode([]llvm.Metadata{
			ctx.MDString("branch_weights"),
			llvm.ConstInt(prog.tyInt32(), 1, false).ConstantAsMetadata(),
			llvm.ConstInt(prog.tyInt32(), 1000, false).ConstantAsMetadata(),
			llvm.ConstInt(prog.tyInt32(), 1, false).ConstantAsMetadata(),
		}))
	}
	b.SetBlock(resumeBlk)
	if !final && dispatch != nil {
		callbackPoint := captureCoroFrameCallbackPoint(b)
		dispatch(b, normalBlk)
		callbackPoint.ensureResumeDispatch(b)
		b.SetBlock(normalBlk)
	} else if callback := afterResume; !final && callback != nil {
		callbackPoint := captureCoroFrameCallbackPoint(b)
		callback(b)
		callbackPoint.ensureContinuation(b, "after-resume")
	}
	return normalBlk
}

func (c *CoroBuilder) suspendIntrinsic(final bool) llvm.Value {
	b := c.b
	return b.coroIntrinsic(
		"llvm.coro.suspend",
		b.Prog.Byte().ll,
		[]llvm.Value{b.Prog.ctx.ConstTokenNone(), b.Prog.BoolVal(final).impl},
		"coro.suspend",
	)
}

func (c *CoroBuilder) requireActive(operation string) {
	if c == nil {
		panic("ssa: " + operation + " nil coroutine builder")
	}
	if c.finished {
		panic("ssa: cannot " + operation + " finished coroutine")
	}
}

// CoroPromise returns a typed pointer to the promise associated with handle.
//
// promise is the promise payload type, not a pointer type. The generated
// llvm.coro.promise call uses the target ABI alignment of that payload and the
// handle-to-promise direction (from=false). The handle must be a pointer-valued
// expression produced by llvm.coro.begin or otherwise supplied by the
// coroutine runtime.
func (b Builder) CoroPromise(handle Expr, promise Type) Expr {
	b.requireCoroHandle("get promise for", handle)
	if promise == nil || promise.kind == vkInvalid {
		panic("ssa: coroutine promise requires a concrete payload type")
	}

	prog := b.Prog
	promisePtr := prog.Pointer(promise)
	value := b.coroIntrinsic(
		"llvm.coro.promise",
		promisePtr.ll,
		[]llvm.Value{
			b.Convert(prog.VoidPtr(), handle).impl,
			prog.IntVal(uint64(prog.td.ABITypeAlignment(promise.ll)), prog.Int32()).impl,
			prog.BoolVal(false).impl,
		},
		"coro.promise",
	)
	return Expr{value, promisePtr}
}

// CoroDone reports whether a suspended coroutine is at its final suspend.
// Calling it for a running coroutine or a coroutine without a final suspend is
// invalid according to LLVM's coroutine contract.
func (b Builder) CoroDone(handle Expr) Expr {
	b.requireCoroHandle("query done for", handle)
	prog := b.Prog
	value := b.coroIntrinsic(
		"llvm.coro.done",
		prog.Bool().ll,
		[]llvm.Value{b.Convert(prog.VoidPtr(), handle).impl},
		"coro.done",
	)
	return Expr{value, prog.Bool()}
}

// CoroResume resumes a suspended coroutine. A final-suspended coroutine must
// be destroyed instead and must never be resumed.
func (b Builder) CoroResume(handle Expr) {
	b.requireCoroHandle("resume", handle)
	b.coroIntrinsic(
		"llvm.coro.resume",
		b.Prog.Void().ll,
		[]llvm.Value{b.Convert(b.Prog.VoidPtr(), handle).impl},
		"",
	)
}

// CoroDestroy destroys a suspended coroutine exactly once.
func (b Builder) CoroDestroy(handle Expr) {
	b.requireCoroHandle("destroy", handle)
	b.coroIntrinsic(
		"llvm.coro.destroy",
		b.Prog.Void().ll,
		[]llvm.Value{b.Convert(b.Prog.VoidPtr(), handle).impl},
		"",
	)
}

// MarkCoroElideSafe marks one exact direct coroutine ramp call as having a
// caller-bounded lifetime. LLVM 22 uses this proof to synthesize and select a
// no-allocation ramp while both caller and callee are still presplit.
//
// The caller must prove that every use of the returned handle is contained by
// its own coroutine frame lifetime. This is deliberately not inferred from an
// arbitrary function value or exposed as a source annotation.
func (b Builder) MarkCoroElideSafe(call Expr) bool {
	if b == nil || b.Func == nil || b.blk == nil {
		panic("ssa: cannot mark coroutine elision without an active function block")
	}
	if call.IsNil() || call.impl.IsACallInst().IsNil() {
		panic("ssa: coroutine elision requires an exact call result")
	}
	kind := llvm.AttributeKindID("coro_elide_safe")
	if kind == 0 {
		panic(fmt.Sprintf("ssa: LLVM %s has no coro_elide_safe attribute", llvm.Version))
	}
	call.impl.AddCallSiteAttribute(-1, b.Prog.ctx.CreateEnumAttribute(kind, 0))
	return true
}

func (b Builder) requireCoroHandle(operation string, handle Expr) {
	if b == nil || b.Func == nil || b.blk == nil {
		panic("ssa: cannot " + operation + " coroutine without an active function block")
	}
	if handle.IsNil() || handle.kind != vkPtr {
		panic("ssa: coroutine handle must be a pointer")
	}
}

func validateCoroOptions(b Builder, opts CoroOptions) {
	if b == nil || b.Func == nil || b.blk == nil {
		panic("ssa: begin coroutine without an active function block")
	}
	sig, ok := b.Func.raw.Type.(*types.Signature)
	if !ok || sig.Results().Len() != 1 ||
		!types.Identical(sig.Results().At(0).Type(), types.Typ[types.UnsafePointer]) {
		panic("ssa: coroutine function must return exactly one unsafe.Pointer handle")
	}
	if opts.Frame.Alloc == nil || opts.Frame.Free == nil {
		panic("ssa: coroutine frame allocator and free callbacks are required")
	}
	if opts.AfterResume != nil && opts.AfterResumeDispatch != nil {
		panic("ssa: coroutine AfterResume and AfterResumeDispatch callbacks are mutually exclusive")
	}
	if opts.Promise.IsNil() {
		// A nil promise is valid independently of the frame allocation guarantee.
	} else if opts.Promise.kind != vkPtr {
		panic("ssa: coroutine promise must be a pointer")
	}
	if opts.AllocationAlign != 0 && opts.AllocationAlign&(opts.AllocationAlign-1) != 0 {
		panic("ssa: coroutine allocation alignment must be zero or a power of two")
	}
}

type coroFrameCallbackPoint struct {
	blk          BasicBlock
	insert       llvm.BasicBlock
	instructions []llvm.Value
}

func captureCoroFrameCallbackPoint(b Builder) coroFrameCallbackPoint {
	insert := b.impl.GetInsertBlock()
	return coroFrameCallbackPoint{
		blk:          b.blk,
		insert:       insert,
		instructions: coroBlockInstructions(insert),
	}
}

func (p coroFrameCallbackPoint) ensureContinuation(b Builder, callback string) {
	if b.blk != p.blk || b.impl.GetInsertBlock().C != p.insert.C {
		panic("ssa: coroutine frame " + callback + " callback changed insertion block")
	}
	current := coroBlockInstructions(p.insert)
	if len(current) < len(p.instructions) {
		panic("ssa: coroutine frame " + callback + " callback modified instructions before append point")
	}
	for i, instruction := range p.instructions {
		if current[i].C != instruction.C {
			panic("ssa: coroutine frame " + callback + " callback modified instructions before append point")
		}
	}
	for _, inst := range current {
		switch inst.InstructionOpcode() {
		case llvm.Ret, llvm.Br, llvm.Switch, llvm.IndirectBr, llvm.Invoke,
			llvm.Unreachable, llvm.Resume, llvm.CleanupRet, llvm.CatchRet,
			llvm.CatchSwitch:
			panic("ssa: coroutine frame " + callback + " callback terminated insertion block")
		}
	}
	// The callbacks are append-only. Re-establish the insertion point at the
	// end before CoroBuilder emits its own control-flow edge.
	b.impl.SetInsertPointAtEnd(p.insert)
}

func (p coroFrameCallbackPoint) ensureResumeDispatch(b Builder) {
	if b.blk != p.blk || b.impl.GetInsertBlock().C != p.insert.C {
		panic("ssa: coroutine frame resume-dispatch callback changed insertion block")
	}
	current := coroBlockInstructions(p.insert)
	if len(current) < len(p.instructions) {
		panic("ssa: coroutine frame resume-dispatch callback modified instructions before append point")
	}
	for i, instruction := range p.instructions {
		if current[i].C != instruction.C {
			panic("ssa: coroutine frame resume-dispatch callback modified instructions before append point")
		}
	}
	appended := current[len(p.instructions):]
	if len(appended) == 0 || !isCoroTerminator(appended[len(appended)-1]) {
		panic("ssa: coroutine frame resume-dispatch callback must terminate insertion block")
	}
	for _, instruction := range appended[:len(appended)-1] {
		if isCoroTerminator(instruction) {
			panic("ssa: coroutine frame resume-dispatch callback emitted instructions after a terminator")
		}
	}
}

func isCoroTerminator(instruction llvm.Value) bool {
	if instruction.IsNil() {
		return false
	}
	switch instruction.InstructionOpcode() {
	case llvm.Ret, llvm.Br, llvm.Switch, llvm.IndirectBr, llvm.Invoke,
		llvm.Unreachable, llvm.Resume, llvm.CleanupRet, llvm.CatchRet,
		llvm.CatchSwitch:
		return true
	}
	return false
}

func coroBlockInstructions(block llvm.BasicBlock) []llvm.Value {
	var instructions []llvm.Value
	for inst := block.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
		instructions = append(instructions, inst)
	}
	return instructions
}

func markPresplitCoroutine(fn Function) {
	ctx := fn.Pkg.mod.Context()
	kind := llvm.AttributeKindID("presplitcoroutine")
	if kind == 0 {
		panic(fmt.Sprintf("ssa: LLVM %s has no presplitcoroutine attribute", llvm.Version))
	}
	fn.impl.AddFunctionAttr(ctx.CreateEnumAttribute(kind, 0))
}

func (b Builder) coroFrameLayout(allocationAlign uint32) (size, align Expr) {
	typ := b.Prog.Uintptr()
	sizeValue := b.coroIntrinsic("llvm.coro.size", typ.ll, nil, "coro.size")
	alignValue := b.coroIntrinsic("llvm.coro.align", typ.ll, nil, "coro.align")
	minimum := uint64(allocationAlign)
	if minimum == 0 {
		minimum = uint64(2 * b.Prog.PointerSize())
	}
	minimumValue := llvm.ConstInt(typ.ll, minimum, false)
	belowMinimum := llvm.CreateICmp(b.impl, llvm.IntULT, alignValue, minimumValue)
	effectiveAlign := b.impl.CreateSelect(belowMinimum, minimumValue, alignValue, "coro.alloc.align")
	return Expr{sizeValue, typ}, Expr{effectiveAlign, typ}
}

func (b Builder) coroEnd(handle Expr) {
	args := []llvm.Value{
		handle.impl,
		b.Prog.BoolVal(false).impl,
		b.Prog.ctx.ConstTokenNone(),
	}
	b.coroIntrinsic("llvm.coro.end", b.Prog.Void().ll, args, "")
}

func (b Builder) coroIntrinsic(name string, ret llvm.Type, args []llvm.Value, resultName string) llvm.Value {
	id := llvm.LookupIntrinsicID(name)
	if id == 0 {
		panic(fmt.Sprintf("ssa: LLVM %s has no %s intrinsic", llvm.Version, name))
	}
	value := b.impl.CreateIntrinsic(ret, id, args, resultName)
	if value.IsNil() {
		panic(fmt.Sprintf("ssa: LLVM %s rejected %s intrinsic signature", llvm.Version, name))
	}
	return value
}
