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
	"go/token"
	"go/types"

	"github.com/xgo-dev/llvm"
)

// CoroDispatchDescriptorOptions describes the shared v1 dynamic function
// descriptor primitive. Signature is the logical Go signature and Result is
// its canonical result-slot layout. Entry functions already have their final
// physical C ABI:
//
//	plain: (env, args...) -> results
//	coro:  (g, out, env, args...) -> handle
//
// An entry is required exactly when its HasPlain or HasCoro flag is present.
// NoCapture asserts that every value carrying this descriptor has a nil env.
type CoroDispatchDescriptorOptions struct {
	Version    uint32
	Flags      uint32
	ABIHash    [16]byte
	Signature  *types.Signature
	PlainEntry Expr
	CoroEntry  Expr
	Result     Type
}

// CoroDispatchCallOptions is the caller's exact v1 ABI contract. Capability is
// selected by CallCoroDispatchPlain or CallCoroDispatchCoro rather than copied
// into this structure, so a coro call accepts both coro-only and dual entries.
type CoroDispatchCallOptions struct {
	Version uint32
	ABIHash [16]byte
	Result  Type
	// DescriptorNonNil records that the frontend has already emitted its
	// language-specific nil-call edge before entering descriptor validation.
	// It suppresses only AssertNilDeref; every descriptor contract check below
	// remains mandatory.
	DescriptorNonNil bool
}

// NewCoroDispatchDescriptor defines one link-once eight-field descriptor. It
// only publishes typed entry points; allocation, child registration, awaiting,
// cancellation, and result consumption remain frontend/scheduler operations.
func (p Package) NewCoroDispatchDescriptor(
	name string, opts CoroDispatchDescriptorOptions,
) Expr {
	if name == "" {
		panic("ssa: coroutine dispatch descriptor requires a name")
	}
	validateCoroDispatchContract(opts.Version, opts.Flags)
	if opts.Signature == nil {
		panic("ssa: coroutine dispatch descriptor requires a signature")
	}
	if err := validateCoroDispatchSignature(opts.Signature); err != nil {
		panic("ssa: coroutine dispatch descriptor: " + err.Error())
	}
	validateCoroDispatchResult(p.Prog, opts.Result, "descriptor")

	plain := p.validateCoroDispatchEntry(
		opts.PlainEntry,
		p.Prog.CoroDispatchPlainEntrySignature(opts.Signature),
		opts.Flags&CoroDispatchFlagHasPlain != 0,
		"plain",
	)
	coro := p.validateCoroDispatchEntry(
		opts.CoroEntry,
		p.Prog.CoroDispatchCoroEntrySignature(opts.Signature),
		opts.Flags&CoroDispatchFlagHasCoro != 0,
		"coroutine",
	)
	if descriptor := p.VarOf(name); descriptor != nil {
		if p.matchesCoroDispatchDescriptor(descriptor, plain, coro, opts) {
			return descriptor.Expr
		}
		panic(fmt.Sprintf("ssa: coroutine dispatch symbol %q conflicts with an existing descriptor", name))
	}
	if _, knownFunction := p.fns[name]; knownFunction ||
		!p.mod.NamedGlobal(name).IsNil() || !p.mod.NamedFunction(name).IsNil() {
		panic(fmt.Sprintf("ssa: coroutine dispatch symbol %q already exists", name))
	}
	return p.newCoroDispatchDescriptorGlobal(
		name, opts.Version, opts.Flags, opts.ABIHash, plain, coro, opts.Result,
	)
}

// CoroDispatchPlainEntrySignature returns the final C-ABI signature for a
// logical Go function's descriptor plain entry: (env,args...)->results.
func (p Program) CoroDispatchPlainEntrySignature(sig *types.Signature) *types.Signature {
	if sig == nil {
		panic("ssa: coroutine dispatch plain entry requires a signature")
	}
	if err := validateCoroDispatchSignature(sig); err != nil {
		panic("ssa: coroutine dispatch plain entry: " + err.Error())
	}
	return coroDispatchPlainEntrySignature(p.PhysicalFuncDecl(sig, InGo))
}

// CoroDispatchCoroEntrySignature returns the final C-ABI signature for a
// logical Go function's descriptor coroutine entry:
// (g,out,env,args...)->handle.
func (p Program) CoroDispatchCoroEntrySignature(sig *types.Signature) *types.Signature {
	if sig == nil {
		panic("ssa: coroutine dispatch coroutine entry requires a signature")
	}
	if err := validateCoroDispatchSignature(sig); err != nil {
		panic("ssa: coroutine dispatch coroutine entry: " + err.Error())
	}
	return coroDispatchCoroEntrySignature(p.PhysicalFuncDecl(sig, InGo))
}

// MakeCoroDispatchValue constructs the canonical function value
// {descriptor,env}. A zero Expr env means a null environment; any concrete env
// must be pointer-shaped and is normalized to unsafe.Pointer.
func (b Builder) MakeCoroDispatchValue(
	sig *types.Signature, descriptor, env Expr,
) Expr {
	if sig == nil {
		panic("ssa: coroutine dispatch value requires a signature")
	}
	if err := validateCoroDispatchSignature(sig); err != nil {
		panic("ssa: coroutine dispatch value: " + err.Error())
	}
	if !b.Pkg.isCoroDispatchDescriptor(descriptor) {
		panic("ssa: coroutine dispatch value requires a descriptor from the same package module")
	}
	if env.IsNil() {
		env = b.Prog.Nil(b.Prog.VoidPtr())
	} else {
		if env.Type.ll.Context().C != b.Prog.ctx.C || env.Type.ll.TypeKind() != llvm.PointerTypeKind {
			panic("ssa: coroutine dispatch value environment must be a pointer from the same program")
		}
		env = b.Convert(b.Prog.VoidPtr(), env)
	}
	return b.aggregateValue(b.Prog.Closure(sig), descriptor.impl, env.impl)
}

// CallCoroDispatchPlain validates a dynamic descriptor and performs its typed
// (env,args...)->results call. It does not accept a coro-only descriptor.
func (b Builder) CallCoroDispatchPlain(
	fn Expr, args []Expr, opts CoroDispatchCallOptions,
) (ret Expr) {
	call := b.prepareCoroDispatchCall(fn, args, opts, CoroDispatchFlagHasPlain)
	plainSig := coroDispatchPlainEntrySignature(call.signature)
	ret.Type = b.Prog.retType(call.signature)
	ret.impl = llvm.CreateCall(
		b.impl, b.Prog.FuncDecl(plainSig, InC).ll, call.entry.impl,
		llvmParamsEx(call.env, args, plainSig.Params(), b),
	)
	return
}

// CoroDispatchHasCoro validates a dynamic descriptor's shared v1 contract and
// reports whether it publishes a coroutine entry. Unlike the two dispatch
// operations, this capability probe accepts any valid non-empty capability
// set: in particular, a valid plain-only descriptor returns false instead of
// trapping. Frontend lowering can use the result to choose between the plain
// and coroutine paths without weakening descriptor validation.
func (b Builder) CoroDispatchHasCoro(
	fn Expr, opts CoroDispatchCallOptions,
) Expr {
	call := b.prepareCoroDispatchCall(fn, nil, opts, 0)
	hasCoro := llvm.CreateAnd(
		b.impl, call.flags.impl,
		b.Prog.IntVal(uint64(CoroDispatchFlagHasCoro), b.Prog.Uint32()).impl,
	)
	return Expr{
		impl: llvm.CreateICmp(
			b.impl, llvm.IntNE, hasCoro,
			b.Prog.IntVal(0, b.Prog.Uint32()).impl,
		),
		Type: b.Prog.Bool(),
	}
}

// CallCoroDispatchCoro validates a dynamic descriptor and invokes its typed
// coroutine entry. The returned handle is deliberately not awaited here;
// frontend lowering owns child registration, suspension, and result loading.
func (b Builder) CallCoroDispatchCoro(
	fn, g, out Expr, args []Expr, opts CoroDispatchCallOptions,
) (ret Expr) {
	call := b.prepareCoroDispatchCall(fn, args, opts, CoroDispatchFlagHasCoro)
	g = b.requireCoroDispatchPointer(g, "g")
	out = b.requireCoroDispatchPointer(out, "out")
	coroSig := coroDispatchCoroEntrySignature(call.signature)
	physicalArgs := make([]Expr, 0, len(args)+3)
	physicalArgs = append(physicalArgs, g, out, call.env)
	physicalArgs = append(physicalArgs, args...)
	ret.Type = b.Prog.VoidPtr()
	ret.impl = llvm.CreateCall(
		b.impl, b.Prog.FuncDecl(coroSig, InC).ll, call.entry.impl,
		llvmParams(0, physicalArgs, coroSig.Params(), b),
	)
	return
}

type coroDispatchPreparedCall struct {
	entry     Expr
	env       Expr
	flags     Expr
	signature *types.Signature
}

func (b Builder) prepareCoroDispatchCall(
	fn Expr, args []Expr, opts CoroDispatchCallOptions, capability uint32,
) coroDispatchPreparedCall {
	if opts.Version != CoroDispatchVersionV1 {
		panic(fmt.Sprintf(
			"ssa: coroutine dispatch version is %d, want %d",
			opts.Version, CoroDispatchVersionV1,
		))
	}
	if capability != 0 && capability != CoroDispatchFlagHasPlain && capability != CoroDispatchFlagHasCoro {
		panic("ssa: coroutine dispatch call requires one known capability")
	}
	if fn.IsNil() || fn.kind != vkClosure {
		panic("ssa: coroutine dispatch call requires a closure value")
	}
	sig, ok := b.Prog.Field(fn.Type, 0).RawType().(*types.Signature)
	if !ok {
		panic("ssa: coroutine dispatch call has no function signature")
	}
	if err := validateCoroDispatchPhysicalSignature(sig); err != nil {
		panic("ssa: coroutine dispatch call: " + err.Error())
	}
	if capability != 0 && len(args) != sig.Params().Len() {
		panic(fmt.Sprintf(
			"ssa: coroutine dispatch call has %d arguments, want %d",
			len(args), sig.Params().Len(),
		))
	}
	validateCoroDispatchResult(b.Prog, opts.Result, "call")

	descriptorWord := b.Field(fn, 0)
	env := b.Field(fn, 1)
	// Keep the ordinary recoverable Go nil-function call path. Descriptor
	// validation begins only after AssertNilDeref returns on its non-nil edge.
	if !opts.DescriptorNonNil {
		b.AssertNilDeref(descriptorWord)
	}
	descriptorPtr := Expr{descriptorWord.impl, b.Prog.Pointer(b.Prog.coroDispatchDescriptorType())}
	var descriptor Expr
	if opts.DescriptorNonNil {
		// A compile-time null descriptor can remain in the continuation of a
		// frontend-owned constant-true fault branch until LLVM simplifies the
		// unreachable block. LoadKnownNonNil prevents that dead load from
		// recreating the very AssertNilDeref edge suppressed above.
		descriptor = b.LoadKnownNonNil(descriptorPtr)
	} else {
		descriptor = b.Load(descriptorPtr)
	}
	fields := make([]Expr, 8)
	for i := range fields {
		fields[i] = b.Field(descriptor, i)
	}

	var invalid llvm.Value
	addInvalid := func(name string, condition llvm.Value) {
		condition.SetName(name)
		invalid = coroPlainDispatchOr(b.impl, invalid, condition)
	}
	equalInvalid := func(name string, got, want llvm.Value) {
		addInvalid(name, llvm.CreateICmp(b.impl, llvm.IntNE, got, want))
	}
	equalInvalid(
		"coro.dispatch.version.invalid", fields[0].impl,
		b.Prog.IntVal(uint64(opts.Version), b.Prog.Uint32()).impl,
	)
	flags := fields[1].impl
	zeroFlags := b.Prog.IntVal(0, b.Prog.Uint32()).impl
	unknown := llvm.CreateAnd(
		b.impl, flags,
		b.Prog.IntVal(uint64(^uint32(CoroDispatchKnownFlagsV1)), b.Prog.Uint32()).impl,
	)
	addInvalid("coro.dispatch.flags.unknown", llvm.CreateICmp(b.impl, llvm.IntNE, unknown, zeroFlags))
	capabilities := llvm.CreateAnd(
		b.impl, flags,
		b.Prog.IntVal(uint64(CoroDispatchCapabilityMaskV1), b.Prog.Uint32()).impl,
	)
	addInvalid("coro.dispatch.flags.empty", llvm.CreateICmp(b.impl, llvm.IntEQ, capabilities, zeroFlags))
	if capability != 0 {
		required := llvm.CreateAnd(
			b.impl, flags, b.Prog.IntVal(uint64(capability), b.Prog.Uint32()).impl,
		)
		addInvalid("coro.dispatch.capability.missing", llvm.CreateICmp(b.impl, llvm.IntEQ, required, zeroFlags))
	}

	plainFlag := llvm.CreateICmp(
		b.impl, llvm.IntNE,
		llvm.CreateAnd(b.impl, flags, b.Prog.IntVal(uint64(CoroDispatchFlagHasPlain), b.Prog.Uint32()).impl),
		zeroFlags,
	)
	plainEntry := llvm.CreateICmp(
		b.impl, llvm.IntNE, fields[4].impl, llvm.ConstNull(fields[4].impl.Type()),
	)
	addInvalid("coro.dispatch.plain.entry.mismatch", llvm.CreateXor(b.impl, plainFlag, plainEntry))
	coroFlag := llvm.CreateICmp(
		b.impl, llvm.IntNE,
		llvm.CreateAnd(b.impl, flags, b.Prog.IntVal(uint64(CoroDispatchFlagHasCoro), b.Prog.Uint32()).impl),
		zeroFlags,
	)
	coroEntry := llvm.CreateICmp(
		b.impl, llvm.IntNE, fields[5].impl, llvm.ConstNull(fields[5].impl.Type()),
	)
	addInvalid("coro.dispatch.coro.entry.mismatch", llvm.CreateXor(b.impl, coroFlag, coroEntry))
	noCapture := llvm.CreateICmp(
		b.impl, llvm.IntNE,
		llvm.CreateAnd(b.impl, flags, b.Prog.IntVal(uint64(CoroDispatchFlagNoCapture), b.Prog.Uint32()).impl),
		zeroFlags,
	)
	envNonNil := llvm.CreateICmp(b.impl, llvm.IntNE, env.impl, llvm.ConstNull(env.impl.Type()))
	addInvalid("coro.dispatch.nocapture.env.nonnull", llvm.CreateAnd(b.impl, noCapture, envNonNil))

	equalInvalid(
		"coro.dispatch.hash.lo.invalid", fields[2].impl,
		b.Prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[:8]), b.Prog.Uint64()).impl,
	)
	equalInvalid(
		"coro.dispatch.hash.hi.invalid", fields[3].impl,
		b.Prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[8:]), b.Prog.Uint64()).impl,
	)
	equalInvalid(
		"coro.dispatch.result.size.invalid", fields[6].impl,
		b.Prog.IntVal(b.Prog.SizeOf(opts.Result), b.Prog.Uintptr()).impl,
	)
	equalInvalid(
		"coro.dispatch.result.align.invalid", fields[7].impl,
		b.Prog.IntVal(b.Prog.AlignOf(opts.Result), b.Prog.Uintptr()).impl,
	)
	b.coroPlainDispatchTrapIf(invalid)

	prepared := coroDispatchPreparedCall{
		env:       env,
		flags:     fields[1],
		signature: sig,
	}
	if capability == CoroDispatchFlagHasPlain {
		prepared.entry = fields[4]
	} else if capability == CoroDispatchFlagHasCoro {
		prepared.entry = fields[5]
	}
	return prepared
}

func (b Builder) requireCoroDispatchPointer(value Expr, role string) Expr {
	if value.IsNil() || value.Type.ll.Context().C != b.Prog.ctx.C ||
		value.Type.ll.TypeKind() != llvm.PointerTypeKind {
		panic("ssa: coroutine dispatch " + role + " must be a pointer from the same program")
	}
	return b.Convert(b.Prog.VoidPtr(), value)
}

func (p Package) validateCoroDispatchEntry(
	entry Expr, want *types.Signature, required bool, role string,
) llvm.Value {
	if entry.IsNil() {
		if required {
			panic("ssa: coroutine dispatch descriptor requires a " + role + " entry")
		}
		return llvm.Value{}
	}
	if !required {
		panic("ssa: coroutine dispatch descriptor has a " + role + " entry without its capability")
	}
	target := coroPlainDispatchFunction(entry.impl)
	if entry.kind != vkFuncDecl || target.IsNil() || target.GlobalParent().C != p.mod.C {
		panic("ssa: coroutine dispatch descriptor requires a " + role + " entry from the same package module")
	}
	if !types.Identical(entry.RawType(), want) {
		panic("ssa: coroutine dispatch descriptor " + role + " entry does not match its physical signature")
	}
	return target
}

func (p Package) newCoroDispatchDescriptorGlobal(
	name string, version, flags uint32, hash [16]byte,
	plain, coro llvm.Value, result Type,
) Expr {
	descriptorType := p.Prog.coroDispatchDescriptorType()
	descriptor := p.NewVarEx(name, p.Prog.Pointer(descriptorType))
	if plain.IsNil() {
		plain = p.Prog.Nil(p.Prog.VoidPtr()).impl
	}
	if coro.IsNil() {
		coro = p.Prog.Nil(p.Prog.VoidPtr()).impl
	}
	fields := []llvm.Value{
		p.Prog.IntVal(uint64(version), p.Prog.Uint32()).impl,
		p.Prog.IntVal(uint64(flags), p.Prog.Uint32()).impl,
		p.Prog.IntVal(binary.BigEndian.Uint64(hash[:8]), p.Prog.Uint64()).impl,
		p.Prog.IntVal(binary.BigEndian.Uint64(hash[8:]), p.Prog.Uint64()).impl,
		plain,
		coro,
		p.Prog.IntVal(p.Prog.SizeOf(result), p.Prog.Uintptr()).impl,
		p.Prog.IntVal(p.Prog.AlignOf(result), p.Prog.Uintptr()).impl,
	}
	descriptor.impl.SetInitializer(p.Prog.ctx.ConstStruct(fields, false))
	descriptor.impl.SetGlobalConstant(true)
	descriptor.impl.SetLinkage(llvm.LinkOnceODRLinkage)
	descriptor.impl.SetUnnamedAddr(true)
	return descriptor.Expr
}

func (p Package) matchesCoroDispatchDescriptor(
	descriptor Global, plain, coro llvm.Value, opts CoroDispatchDescriptorOptions,
) bool {
	if descriptor == nil || !p.isCoroDispatchDescriptor(descriptor.Expr) {
		return false
	}
	initializer := descriptor.impl.Initializer()
	if initializer.IsAConstantStruct().IsNil() || initializer.OperandsCount() != 8 {
		return false
	}
	wantFixed := []uint64{
		uint64(opts.Version),
		uint64(opts.Flags),
		binary.BigEndian.Uint64(opts.ABIHash[:8]),
		binary.BigEndian.Uint64(opts.ABIHash[8:]),
	}
	for i, want := range wantFixed {
		if initializer.Operand(i).ZExtValue() != want {
			return false
		}
	}
	for i, want := range []llvm.Value{plain, coro} {
		got := initializer.Operand(4 + i)
		if want.IsNil() {
			if got.IsAConstantPointerNull().IsNil() {
				return false
			}
			continue
		}
		if actual := coroPlainDispatchFunction(got); actual.IsNil() || actual.C != want.C {
			return false
		}
	}
	return initializer.Operand(6).ZExtValue() == p.Prog.SizeOf(opts.Result) &&
		initializer.Operand(7).ZExtValue() == p.Prog.AlignOf(opts.Result)
}

func validateCoroDispatchContract(version, flags uint32) {
	if version != CoroDispatchVersionV1 {
		panic(fmt.Sprintf(
			"ssa: coroutine dispatch version is %d, want %d",
			version, CoroDispatchVersionV1,
		))
	}
	if unknown := flags &^ CoroDispatchKnownFlagsV1; unknown != 0 {
		panic(fmt.Sprintf("ssa: coroutine dispatch flags contain unknown bits %#x", unknown))
	}
	if flags&CoroDispatchCapabilityMaskV1 == 0 {
		panic("ssa: coroutine dispatch flags require HasPlain or HasCoro")
	}
}

func validateCoroDispatchSignature(sig *types.Signature) error {
	if sig.Recv() != nil {
		return fmt.Errorf("methods are not supported")
	}
	if sig.Variadic() {
		return fmt.Errorf("variadic signatures are not supported")
	}
	if params := sig.TypeParams(); params != nil && params.Len() != 0 {
		return fmt.Errorf("generic signatures are not supported")
	}
	if params := sig.RecvTypeParams(); params != nil && params.Len() != 0 {
		return fmt.Errorf("generic receiver signatures are not supported")
	}
	return nil
}

func validateCoroDispatchPhysicalSignature(sig *types.Signature) error {
	if sig == nil || sig.Recv() != nil || sig.Variadic() {
		return fmt.Errorf("requires an ordinary non-variadic function signature")
	}
	return nil
}

func validateCoroDispatchResult(prog Program, result Type, role string) {
	if result == nil || result.kind == vkInvalid || result.ll.Context().C != prog.ctx.C {
		panic("ssa: coroutine dispatch " + role + " requires a result layout from the same program")
	}
}

func coroDispatchPlainEntrySignature(sig *types.Signature) *types.Signature {
	ctx := types.NewParam(token.NoPos, nil, closureCtx, types.Typ[types.UnsafePointer])
	return FuncAddCtx(ctx, sig)
}

func coroDispatchCoroEntrySignature(sig *types.Signature) *types.Signature {
	params := make([]*types.Var, 0, sig.Params().Len()+3)
	for _, name := range []string{"__llgo_g", "__llgo_out", closureCtx} {
		params = append(params, types.NewParam(token.NoPos, nil, name, types.Typ[types.UnsafePointer]))
	}
	for i := 0; i < sig.Params().Len(); i++ {
		params = append(params, sig.Params().At(i))
	}
	result := types.NewParam(token.NoPos, nil, "__llgo_handle", types.Typ[types.UnsafePointer])
	return types.NewSignatureType(
		nil, nil, nil, types.NewTuple(params...), types.NewTuple(result), false,
	)
}
