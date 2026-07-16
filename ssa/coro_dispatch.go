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
	"strings"

	"github.com/xgo-dev/llvm"
)

// Coro plain-dispatch version and capability flags are linker-visible ABI.
// HasCoro is reserved by v1 even though the first production slice emits only
// the exact HasPlain|NoCapture capability set.
const CoroPlainDispatchVersionV1 uint32 = 1

const (
	CoroDispatchFlagHasPlain uint32 = 1 << iota
	CoroDispatchFlagHasCoro
	CoroDispatchFlagNoCapture

	CoroPlainDispatchFlagsV1 = CoroDispatchFlagHasPlain | CoroDispatchFlagNoCapture
)

const coroPlainDispatchThunkPrefix = "__llgo_coro_func_plain_v1."

// CoroPlainDispatchThunkName derives the dedicated v1 thunk symbol for one
// target-specific symbol identity. The frontend should pass its final planned
// symbol name, which is the only place target FunctionID identity belongs.
func CoroPlainDispatchThunkName(targetSymbol string) string {
	if targetSymbol == "" {
		panic("ssa: coroutine plain dispatch thunk requires a target symbol")
	}
	return coroPlainDispatchThunkPrefix + targetSymbol
}

// CoroPlainDispatchDescriptorOptions describes one v1 plain-only function
// descriptor. ABIHash is supplied by the frontend and deliberately does not
// include the target FunctionID. Target identity belongs only in Name and
// ThunkName, which lets identical ABI contracts share the same hash.
//
// Signature is the source Go signature. PlainTarget is its one compiler-owned
// plain body. Result is the canonical result-slot layout. This API creates a
// target-specific (env, args)->results thunk; callers must not pass or reuse
// the legacy closure stub.
type CoroPlainDispatchDescriptorOptions struct {
	Version     uint32
	Flags       uint32
	ABIHash     [16]byte
	PlainTarget Expr
	Signature   *types.Signature
	ThunkName   string
	Result      Type
}

// CoroPlainDispatchCallOptions is the caller's exact expected v1 contract.
// Result is the canonical result-slot layout used by the ABI hash and layout
// guards; it is distinct from the direct LLVM call's return type.
type CoroPlainDispatchCallOptions struct {
	Version uint32
	Flags   uint32
	ABIHash [16]byte
	Result  Type
}

// NewCoroPlainDispatchDescriptor defines a link-once constant descriptor:
//
//	{ version i32, flags i32, hashLo i64, hashHi i64,
//	  plainEntry ptr, coroEntry ptr, resultSize uintptr,
//	  resultAlign uintptr }
//
// plainEntry is a target-specific context thunk and coroEntry is null. The
// descriptor is returned as a pointer. Hash words use big-endian byte order so
// their textual IR form is deterministic across hosts.
func (p Package) NewCoroPlainDispatchDescriptor(
	name string, opts CoroPlainDispatchDescriptorOptions,
) Expr {
	if name == "" {
		panic("ssa: coroutine plain dispatch descriptor requires a name")
	}
	validateCoroPlainDispatchContract(opts.Version, opts.Flags)
	if opts.Signature == nil {
		panic("ssa: coroutine plain dispatch descriptor requires a signature")
	}
	if err := validateCoroPlainDispatchSignature(p.Prog, opts.Signature); err != nil {
		panic("ssa: coroutine plain dispatch descriptor: " + err.Error())
	}
	if opts.ThunkName == "" {
		panic("ssa: coroutine plain dispatch descriptor requires a target-specific thunk name")
	}
	if opts.ThunkName == name {
		panic("ssa: coroutine plain dispatch descriptor and thunk require distinct symbols")
	}
	if strings.HasPrefix(opts.ThunkName, closureStub) {
		panic("ssa: coroutine plain dispatch thunk must not reuse the legacy closure stub namespace")
	}
	if opts.Result == nil || opts.Result.kind == vkInvalid ||
		opts.Result.ll.Context().C != p.Prog.ctx.C {
		panic("ssa: coroutine plain dispatch descriptor requires a result layout from the same program")
	}
	target := coroPlainDispatchFunction(opts.PlainTarget.impl)
	if opts.PlainTarget.IsNil() || opts.PlainTarget.kind != vkFuncDecl ||
		target.IsNil() || target.GlobalParent().C != p.mod.C {
		panic("ssa: coroutine plain dispatch requires a plain target from the same package module")
	}
	targetFn := p.FuncOf(target.Name())
	if targetFn == nil || targetFn.impl.C != target.C || targetFn.base != 0 {
		panic("ssa: coroutine plain dispatch requires a no-capture plain target")
	}
	physicalSig := p.Prog.PhysicalFuncDecl(opts.Signature, InGo)
	if closureCtxParam(physicalSig) != nil ||
		!types.Identical(opts.PlainTarget.RawType(), physicalSig) {
		panic("ssa: coroutine plain dispatch target does not match the lowered signature")
	}
	if descriptor := p.VarOf(name); descriptor != nil {
		thunk := p.FuncOf(opts.ThunkName)
		if thunk != nil && p.matchesCoroPlainDispatchDescriptor(
			descriptor, thunk, target, physicalSig, opts,
		) {
			return descriptor.Expr
		}
		panic(fmt.Sprintf("ssa: coroutine plain dispatch symbol %q conflicts with an existing descriptor", name))
	}
	for _, symbol := range []string{name, opts.ThunkName} {
		_, knownGlobal := p.vars[symbol]
		_, knownFunction := p.fns[symbol]
		if knownGlobal || knownFunction ||
			!p.mod.NamedGlobal(symbol).IsNil() || !p.mod.NamedFunction(symbol).IsNil() {
			panic(fmt.Sprintf("ssa: coroutine plain dispatch symbol %q already exists", symbol))
		}
	}

	thunk := p.newCoroPlainDispatchThunk(opts.ThunkName, opts.PlainTarget, physicalSig)
	descriptorType := p.Prog.coroPlainDispatchDescriptorType()
	descriptor := p.NewVarEx(name, p.Prog.Pointer(descriptorType))
	fields := []llvm.Value{
		p.Prog.IntVal(uint64(opts.Version), p.Prog.Uint32()).impl,
		p.Prog.IntVal(uint64(opts.Flags), p.Prog.Uint32()).impl,
		p.Prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[:8]), p.Prog.Uint64()).impl,
		p.Prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[8:]), p.Prog.Uint64()).impl,
		thunk.impl,
		p.Prog.Nil(p.Prog.VoidPtr()).impl,
		p.Prog.IntVal(p.Prog.SizeOf(opts.Result), p.Prog.Uintptr()).impl,
		p.Prog.IntVal(p.Prog.AlignOf(opts.Result), p.Prog.Uintptr()).impl,
	}
	descriptor.impl.SetInitializer(p.Prog.ctx.ConstStruct(fields, false))
	descriptor.impl.SetGlobalConstant(true)
	descriptor.impl.SetLinkage(llvm.LinkOnceODRLinkage)
	descriptor.impl.SetUnnamedAddr(true)
	return descriptor.Expr
}

// MakeCoroPlainDispatchValue constructs the canonical two-pointer function
// value {descriptor, nil}. The descriptor occupies the existing code word;
// LLVM opaque pointers keep the physical closure layout unchanged.
func (b Builder) MakeCoroPlainDispatchValue(
	sig *types.Signature, descriptor Expr,
) Expr {
	if sig == nil {
		panic("ssa: coroutine plain dispatch value requires a signature")
	}
	if err := validateCoroPlainDispatchSignature(b.Prog, sig); err != nil {
		panic("ssa: coroutine plain dispatch value: " + err.Error())
	}
	if !b.Pkg.isCoroPlainDispatchDescriptor(descriptor) {
		panic("ssa: coroutine plain dispatch value requires a descriptor from the same package module")
	}
	return b.aggregateValue(
		b.Prog.Closure(sig), descriptor.impl, b.Prog.Nil(b.Prog.VoidPtr()).impl,
	)
}

// CallCoroPlainDispatch validates and calls an ordinary v1 plain-only dynamic
// function value. A nil descriptor uses the same recoverable Go nil-call panic
// path as the legacy closure call. Invalid or forged non-nil representation
// state traps. All checks precede the descriptor entry call; success performs
// a typed (env,args)->results indirect call.
func (b Builder) CallCoroPlainDispatch(
	fn Expr, args []Expr, opts CoroPlainDispatchCallOptions,
) (ret Expr) {
	validateCoroPlainDispatchContract(opts.Version, opts.Flags)
	if fn.IsNil() || fn.kind != vkClosure {
		panic("ssa: coroutine plain dispatch call requires a closure value")
	}
	sig, ok := b.Prog.Field(fn.Type, 0).RawType().(*types.Signature)
	if !ok {
		panic("ssa: coroutine plain dispatch call has no function signature")
	}
	if err := validateCoroPlainDispatchPhysicalSignature(b.Prog, sig); err != nil {
		panic("ssa: coroutine plain dispatch call: " + err.Error())
	}
	if len(args) != sig.Params().Len() {
		panic(fmt.Sprintf(
			"ssa: coroutine plain dispatch call has %d arguments, want %d",
			len(args), sig.Params().Len(),
		))
	}
	wantResult := b.Prog.retType(sig)
	if opts.Result == nil || opts.Result.kind == vkInvalid ||
		opts.Result.ll.Context().C != b.Prog.ctx.C {
		panic("ssa: coroutine plain dispatch call requires a result layout from the same program")
	}

	descriptorWord := b.Field(fn, 0)
	env := b.Field(fn, 1)
	// Preserve Go's recoverable nil function-call semantics. AssertNilDeref
	// returns only on the non-nil path, so the descriptor load below is safe.
	b.AssertNilDeref(descriptorWord)
	envNonNil := llvm.CreateICmp(
		b.impl, llvm.IntNE, env.impl, llvm.ConstNull(env.impl.Type()),
	)
	envNonNil.SetName("coro.dispatch.env.nonnull")
	b.coroPlainDispatchTrapIf(envNonNil)

	descriptorType := b.Prog.coroPlainDispatchDescriptorType()
	descriptorPtr := Expr{descriptorWord.impl, b.Prog.Pointer(descriptorType)}
	descriptor := b.Load(descriptorPtr)
	fields := make([]Expr, 8)
	for i := range fields {
		fields[i] = b.Field(descriptor, i)
	}

	expected := []llvm.Value{
		b.Prog.IntVal(uint64(opts.Version), b.Prog.Uint32()).impl,
		b.Prog.IntVal(uint64(opts.Flags), b.Prog.Uint32()).impl,
		b.Prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[:8]), b.Prog.Uint64()).impl,
		b.Prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[8:]), b.Prog.Uint64()).impl,
	}
	var invalid llvm.Value
	for i, want := range expected {
		mismatch := llvm.CreateICmp(b.impl, llvm.IntNE, fields[i].impl, want)
		mismatch.SetName(fmt.Sprintf("coro.dispatch.field.%d.invalid", i))
		invalid = coroPlainDispatchOr(b.impl, invalid, mismatch)
	}
	plainNil := llvm.CreateICmp(
		b.impl, llvm.IntEQ, fields[4].impl, llvm.ConstNull(fields[4].impl.Type()),
	)
	plainNil.SetName("coro.dispatch.plain.nil")
	invalid = coroPlainDispatchOr(b.impl, invalid, plainNil)
	coroNonNil := llvm.CreateICmp(
		b.impl, llvm.IntNE, fields[5].impl, llvm.ConstNull(fields[5].impl.Type()),
	)
	coroNonNil.SetName("coro.dispatch.coro.nonnull")
	invalid = coroPlainDispatchOr(b.impl, invalid, coroNonNil)
	resultSizeInvalid := llvm.CreateICmp(
		b.impl, llvm.IntNE, fields[6].impl,
		b.Prog.IntVal(b.Prog.SizeOf(opts.Result), b.Prog.Uintptr()).impl,
	)
	resultSizeInvalid.SetName("coro.dispatch.result.size.invalid")
	invalid = coroPlainDispatchOr(b.impl, invalid, resultSizeInvalid)
	resultAlignInvalid := llvm.CreateICmp(
		b.impl, llvm.IntNE, fields[7].impl,
		b.Prog.IntVal(b.Prog.AlignOf(opts.Result), b.Prog.Uintptr()).impl,
	)
	resultAlignInvalid.SetName("coro.dispatch.result.align.invalid")
	invalid = coroPlainDispatchOr(b.impl, invalid, resultAlignInvalid)
	b.coroPlainDispatchTrapIf(invalid)

	ctx := types.NewParam(token.NoPos, nil, closureCtx, types.Typ[types.UnsafePointer])
	sigCtx := FuncAddCtx(ctx, sig)
	ret.Type = wantResult
	ret.impl = llvm.CreateCall(
		b.impl, b.Prog.FuncDecl(sigCtx, InC).ll, fields[4].impl,
		llvmParamsEx(env, args, sigCtx.Params(), b),
	)
	return
}

func (p Program) coroPlainDispatchDescriptorType() Type {
	return p.Struct(
		p.Uint32(),
		p.Uint32(),
		p.Uint64(),
		p.Uint64(),
		p.VoidPtr(),
		p.VoidPtr(),
		p.Uintptr(),
		p.Uintptr(),
	)
}

func (p Package) newCoroPlainDispatchThunk(
	name string, target Expr, physicalSig *types.Signature,
) Function {
	ctx := types.NewParam(token.NoPos, nil, closureCtx, types.Typ[types.UnsafePointer])
	thunk := p.NewFunc(name, FuncAddCtx(ctx, physicalSig), InC)
	thunk.impl.SetLinkage(llvm.LinkOnceODRLinkage)
	thunk.impl.SetUnnamedAddr(true)
	b := thunk.MakeBody(1)
	ret := b.Call(target, closureWrapArgs(thunk)...)
	closureWrapReturn(b, physicalSig, ret)
	b.EndBuild()
	b.Dispose()
	return thunk
}

func (p Package) isCoroPlainDispatchDescriptor(descriptor Expr) bool {
	if descriptor.IsNil() || descriptor.kind != vkPtr ||
		!descriptor.impl.IsAConstantPointerNull().IsNil() {
		return false
	}
	global := coroPlainDispatchGlobal(descriptor.impl)
	if global.IsNil() || global.GlobalParent().C != p.mod.C ||
		!global.IsGlobalConstant() || global.Linkage() != llvm.LinkOnceODRLinkage {
		return false
	}
	want := p.Prog.Pointer(p.Prog.coroPlainDispatchDescriptorType())
	return types.Identical(descriptor.RawType(), want.RawType())
}

func (p Package) matchesCoroPlainDispatchDescriptor(
	descriptor Global,
	thunk Function,
	target llvm.Value,
	physicalSig *types.Signature,
	opts CoroPlainDispatchDescriptorOptions,
) bool {
	if descriptor == nil || thunk == nil ||
		!p.isCoroPlainDispatchDescriptor(descriptor.Expr) ||
		thunk.impl.GlobalParent().C != p.mod.C ||
		thunk.impl.Linkage() != llvm.LinkOnceODRLinkage ||
		!types.Identical(
			thunk.RawType(),
			FuncAddCtx(
				types.NewParam(token.NoPos, nil, closureCtx, types.Typ[types.UnsafePointer]),
				physicalSig,
			),
		) || !coroPlainDispatchThunkCalls(thunk, target) {
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
	plain := coroPlainDispatchFunction(initializer.Operand(4))
	if plain.IsNil() || plain.C != thunk.impl.C ||
		initializer.Operand(5).IsAConstantPointerNull().IsNil() {
		return false
	}
	return initializer.Operand(6).ZExtValue() == p.Prog.SizeOf(opts.Result) &&
		initializer.Operand(7).ZExtValue() == p.Prog.AlignOf(opts.Result)
}

func coroPlainDispatchThunkCalls(thunk Function, target llvm.Value) bool {
	calls := 0
	for _, block := range thunk.impl.BasicBlocks() {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if instruction.InstructionOpcode() != llvm.Call {
				continue
			}
			called := coroPlainDispatchFunction(instruction.CalledValue())
			if called.IsNil() || called.C != target.C {
				return false
			}
			calls++
		}
	}
	return calls == 1
}

func validateCoroPlainDispatchContract(version, flags uint32) {
	if version != CoroPlainDispatchVersionV1 {
		panic(fmt.Sprintf(
			"ssa: coroutine plain dispatch version is %d, want %d",
			version, CoroPlainDispatchVersionV1,
		))
	}
	if flags != CoroPlainDispatchFlagsV1 {
		panic(fmt.Sprintf(
			"ssa: coroutine plain dispatch flags are %#x, want exact HasPlain|NoCapture (%#x)",
			flags, CoroPlainDispatchFlagsV1,
		))
	}
}

func validateCoroPlainDispatchSignature(prog Program, sig *types.Signature) error {
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
	return validateCoroPlainDispatchPhysicalSignature(prog, prog.PhysicalFuncDecl(sig, InGo))
}

func validateCoroPlainDispatchPhysicalSignature(prog Program, sig *types.Signature) error {
	if sig == nil || sig.Recv() != nil || sig.Variadic() {
		return fmt.Errorf("requires an ordinary non-variadic function signature")
	}
	if sig.Results().Len() > 1 {
		return fmt.Errorf("multiple results are not supported")
	}
	for _, item := range []struct {
		role  string
		tuple *types.Tuple
	}{
		{"parameter", sig.Params()},
		{"result", sig.Results()},
	} {
		role, tuple := item.role, item.tuple
		for i := 0; i < tuple.Len(); i++ {
			if !isCoroPlainDispatchScalar(prog.rawType(tuple.At(i).Type())) {
				return fmt.Errorf("%s %d is not a supported scalar", role, i)
			}
		}
	}
	return nil
}

func isCoroPlainDispatchScalar(typ Type) bool {
	switch typ.ll.TypeKind() {
	case llvm.IntegerTypeKind,
		llvm.FloatTypeKind,
		llvm.DoubleTypeKind,
		llvm.X86_FP80TypeKind,
		llvm.FP128TypeKind,
		llvm.PPC_FP128TypeKind,
		llvm.PointerTypeKind:
		return true
	default:
		return false
	}
}

func (b Builder) coroPlainDispatchTrapIf(invalid llvm.Value) {
	b.IfThen(Expr{invalid, b.Prog.Bool()}, func() {
		b.impl.CreateIntrinsic(
			b.Prog.Void().ll, llvm.LookupIntrinsicID("llvm.trap"), nil, "",
		)
		b.Unreachable()
	})
}

func coroPlainDispatchOr(b llvm.Builder, left, right llvm.Value) llvm.Value {
	if left.IsNil() {
		return right
	}
	return b.CreateOr(left, right, "coro.dispatch.invalid")
}

func coroPlainDispatchFunction(value llvm.Value) llvm.Value {
	for !value.IsAConstantExpr().IsNil() && value.OperandsCount() == 1 {
		value = value.Operand(0)
	}
	return value.IsAFunction()
}

func coroPlainDispatchGlobal(value llvm.Value) llvm.Value {
	for !value.IsAConstantExpr().IsNil() && value.OperandsCount() == 1 {
		value = value.Operand(0)
	}
	return value.IsAGlobalVariable()
}
