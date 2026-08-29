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

// CoroDispatchDescriptorOptions describes the shared v2 dynamic function
// descriptor primitive. Signature is the logical Go signature and Result is
// its canonical result-slot layout. Entry functions already have their final
// physical C ABI:
//
//	plain:   (env, args...) -> results
//	outcome: (g, out, completion, env, args...) -> void
//	coro:    (g, out, env, args...) -> handle
//
// An entry is required exactly when its matching capability flag is present.
// NoCapture asserts that every value carrying this descriptor has a nil env.
type CoroDispatchDescriptorOptions struct {
	Version      uint32
	Flags        uint32
	ABIHash      [16]byte
	Signature    *types.Signature
	PlainEntry   Expr
	OutcomeEntry Expr
	CoroEntry    Expr
	// CodeEntry is the compiler-known physical function identity returned by
	// reflection. It is never invoked through the descriptor dispatch ABI.
	CodeEntry Expr
	Result    Type
}

// CoroDispatchCallOptions is the caller's exact v2 ABI contract. Capability is
// selected by the typed dispatch operation rather than copied into this
// structure.
type CoroDispatchCallOptions struct {
	Version uint32
	ABIHash [16]byte
	Result  Type
	// DescriptorNonNil records that the frontend has already emitted its
	// language-specific nil-call edge before entering descriptor validation.
	// It suppresses only AssertNilDeref; every descriptor contract check below
	// remains mandatory.
	DescriptorNonNil bool
	// TrustedDescriptor records that the frontend's frozen whole-program plan
	// (or an already validated library/runtime publication boundary) proves the
	// descriptor schema, ABI hash, result layout, environment contract, and
	// advertised entries. The call still performs the language-level nil check
	// unless DescriptorNonNil is also set, but it does not repeat those invariant
	// checks on every invocation.
	TrustedDescriptor bool
}

// CoroDispatchSelection is one validated immutable descriptor snapshot. Its
// fields are deliberately opaque: frontend lowering may branch on the two
// structured capabilities and then invoke the matching typed entry, but it
// cannot reinterpret descriptor words or recover metadata from an address.
// Keeping the selected entries live across successor blocks also prevents each
// branch from reloading and revalidating the complete nine-word descriptor.
type CoroDispatchSelection struct {
	prepared   coroDispatchPreparedCall
	hasOutcome Expr
	hasCoro    Expr
}

func (selection CoroDispatchSelection) HasOutcome() Expr { return selection.hasOutcome }
func (selection CoroDispatchSelection) HasCoro() Expr    { return selection.hasCoro }
func (selection CoroDispatchSelection) CodeEntry() Expr  { return selection.prepared.code }

// CoroDispatchStructuredOnlySelection is the narrow descriptor snapshot used
// by a frozen closed call whose complete target set is proven to publish one
// exact structured ABI (outcome or coroutine). It deliberately carries no
// capability flags or plain entry: those values are impossible at this
// occurrence and loading them would turn a direct call back into universal
// dispatch.
//
// CodeEntry is loaded only when the frontend requests it for transparent
// recover bookkeeping. Other narrow calls therefore read exactly the
// structured entry word from descriptor storage.
type CoroDispatchStructuredOnlySelection struct {
	prepared      coroDispatchPreparedCall
	codeRequested bool
}

// CodeEntry returns the optional physical identity requested while preparing
// this selection. It must not be used as a dispatch target.
func (selection CoroDispatchStructuredOnlySelection) CodeEntry() Expr {
	if !selection.codeRequested || selection.prepared.code.IsNil() {
		panic("ssa: structured-only coroutine dispatch code identity was not requested")
	}
	return selection.prepared.code
}

// NewCoroDispatchDescriptor defines one link-once nine-field descriptor. It
// only publishes typed entry points; allocation, child registration, awaiting,
// cancellation, and result consumption remain frontend/scheduler operations.
func (p Package) NewCoroDispatchDescriptor(
	name string, opts CoroDispatchDescriptorOptions,
) Expr {
	if name == "" {
		panic("ssa: coroutine dispatch descriptor requires a name")
	}
	validateCoroDispatchContract(opts.Version, opts.Flags)
	if opts.Flags&CoroDispatchFlagRuntimeTyped != 0 {
		panic("ssa: compiler-owned coroutine dispatch descriptor cannot use RuntimeTyped")
	}
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
	outcome := p.validateCoroDispatchEntry(
		opts.OutcomeEntry,
		p.Prog.CoroDispatchOutcomeEntrySignature(opts.Signature),
		opts.Flags&CoroDispatchFlagHasOutcome != 0,
		"outcome",
	)
	coro := p.validateCoroDispatchEntry(
		opts.CoroEntry,
		p.Prog.CoroDispatchCoroEntrySignature(opts.Signature),
		opts.Flags&CoroDispatchFlagHasCoro != 0,
		"coroutine",
	)
	structured := outcome
	if structured.IsNil() {
		structured = coro
	}
	code := p.validateCoroDispatchCodeEntry(opts.CodeEntry)
	if descriptor := p.VarOf(name); descriptor != nil {
		if p.matchesCoroDispatchDescriptor(descriptor, plain, structured, code, opts) {
			return descriptor.Expr
		}
		panic(fmt.Sprintf("ssa: coroutine dispatch symbol %q conflicts with an existing descriptor", name))
	}
	if _, knownFunction := p.fns[name]; knownFunction ||
		!p.mod.NamedGlobal(name).IsNil() || !p.mod.NamedFunction(name).IsNil() {
		panic(fmt.Sprintf("ssa: coroutine dispatch symbol %q already exists", name))
	}
	return p.newCoroDispatchDescriptorGlobal(
		name, opts.Version, opts.Flags, opts.ABIHash, plain, structured, code, opts.Result,
	)
}

// CoroDispatchPlainEntrySignature returns the final C-ABI signature for a
// logical Go function's descriptor plain entry: (env,args...)->results.
// The env parameter is deliberately an ordinary C-ABI argument. The
// target-specific descriptor thunk is the final chunk that transfers it into
// the target body's nest/swiftself context register.
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
// (g,out,env,args...)->handle. As with the plain entry, env remains ordinary
// at this dynamic boundary and only the thunk-to-body call uses the hidden
// closure-context ABI.
func (p Program) CoroDispatchCoroEntrySignature(sig *types.Signature) *types.Signature {
	if sig == nil {
		panic("ssa: coroutine dispatch coroutine entry requires a signature")
	}
	if err := validateCoroDispatchSignature(sig); err != nil {
		panic("ssa: coroutine dispatch coroutine entry: " + err.Error())
	}
	return coroDispatchCoroEntrySignature(p.PhysicalFuncDecl(sig, InGo))
}

// CoroDispatchOutcomeEntrySignature returns the final C-ABI signature for a
// logical Go function's explicit-status descriptor entry:
// (g,out,completion,env,args...)->void. Outcome and coroutine entries share
// one descriptor word and are distinguished by mutually exclusive flags.
func (p Program) CoroDispatchOutcomeEntrySignature(sig *types.Signature) *types.Signature {
	if sig == nil {
		panic("ssa: coroutine dispatch outcome entry requires a signature")
	}
	if err := validateCoroDispatchSignature(sig); err != nil {
		panic("ssa: coroutine dispatch outcome entry: " + err.Error())
	}
	return coroDispatchOutcomeEntrySignature(p.PhysicalFuncDecl(sig, InGo))
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
	return b.callPreparedCoroDispatchPlain(call, args)
}

// CallPreparedCoroDispatchPlain invokes the plain entry selected by
// PrepareCoroDispatchCall. It must execute only on the !HasOutcome && !HasCoro
// successor of that exact selection.
func (b Builder) CallPreparedCoroDispatchPlain(
	selection CoroDispatchSelection, args []Expr,
) Expr {
	call := b.requireCoroDispatchSelection(selection, args, "plain")
	call.entry = call.plain
	return b.callPreparedCoroDispatchPlain(call, args)
}

func (b Builder) callPreparedCoroDispatchPlain(
	call coroDispatchPreparedCall, args []Expr,
) (ret Expr) {
	plainSig := coroDispatchPlainEntrySignature(call.signature)
	ret.Type = b.Prog.retType(call.signature)
	ret.impl = llvm.CreateCall(
		b.impl, b.Prog.FuncDecl(plainSig, InC).ll, call.entry.impl,
		llvmParamsEx(call.env, args, plainSig.Params(), b),
	)
	return
}

// CoroDispatchHasCoro validates a dynamic descriptor's shared v2 contract and
// reports whether it publishes a coroutine entry. Unlike the two dispatch
// operations, this capability probe accepts any valid non-empty capability
// set: in particular, a valid plain-only descriptor returns false instead of
// trapping. Frontend lowering can use the result to choose between the plain
// and coroutine paths without weakening descriptor validation.
func (b Builder) CoroDispatchHasCoro(
	fn Expr, opts CoroDispatchCallOptions,
) Expr {
	hasCoro, _ := b.CoroDispatchHasCoroAndCodeEntry(fn, opts)
	return hasCoro
}

// CoroDispatchHasOutcome reports whether a validated descriptor publishes an
// explicit-status synchronous entry.
func (b Builder) CoroDispatchHasOutcome(fn Expr, opts CoroDispatchCallOptions) Expr {
	hasOutcome, _, _ := b.CoroDispatchCapabilitiesAndCodeEntry(fn, opts)
	return hasOutcome
}

// CoroDispatchHasCoroAndCodeEntry performs the same descriptor validation as
// CoroDispatchHasCoro and also returns its compiler-injected physical code
// identity. The code entry is metadata, never a callable entry: transparent
// recover lowering uses it as the exact token expected by the selected plain
// body's BindRecoverFrame operation, without reverse-mapping a descriptor
// pointer at run time.
func (b Builder) CoroDispatchHasCoroAndCodeEntry(
	fn Expr, opts CoroDispatchCallOptions,
) (Expr, Expr) {
	_, hasCoro, code := b.CoroDispatchCapabilitiesAndCodeEntry(fn, opts)
	return hasCoro, code
}

// CoroDispatchCapabilitiesAndCodeEntry validates the shared descriptor once
// and returns its two structured capabilities plus physical code identity.
func (b Builder) CoroDispatchCapabilitiesAndCodeEntry(
	fn Expr, opts CoroDispatchCallOptions,
) (Expr, Expr, Expr) {
	selection := b.PrepareCoroDispatchCall(fn, opts)
	return selection.HasOutcome(), selection.HasCoro(), selection.CodeEntry()
}

// PrepareCoroDispatchCall validates and snapshots one descriptor before a
// frontend emits its capability branch. Calls in the mutually exclusive
// successors must use the CallPreparedCoroDispatch* operations with this exact
// value; no successor reloads descriptor storage or repeats its contract.
func (b Builder) PrepareCoroDispatchCall(
	fn Expr, opts CoroDispatchCallOptions,
) CoroDispatchSelection {
	call := b.prepareCoroDispatchCall(fn, nil, opts, 0)
	hasOutcome := llvm.CreateAnd(
		b.impl, call.flags.impl,
		b.Prog.IntVal(uint64(CoroDispatchFlagHasOutcome), b.Prog.Uint32()).impl,
	)
	hasCoro := llvm.CreateAnd(
		b.impl, call.flags.impl,
		b.Prog.IntVal(uint64(CoroDispatchFlagHasCoro), b.Prog.Uint32()).impl,
	)
	boolean := func(value llvm.Value) Expr {
		return Expr{
			impl: llvm.CreateICmp(
				b.impl, llvm.IntNE, value,
				b.Prog.IntVal(0, b.Prog.Uint32()).impl,
			),
			Type: b.Prog.Bool(),
		}
	}
	return CoroDispatchSelection{
		prepared:   call,
		hasOutcome: boolean(hasOutcome),
		hasCoro:    boolean(hasCoro),
	}
}

// PrepareCoroDispatchStructuredOnly snapshots only the words required by a
// compiler-frozen single-structured-capability call. TrustedDescriptor is
// mandatory. Unlike PrepareCoroDispatchCall, this operation intentionally
// omits runtime schema, hash, layout, and capability probes because
// whole-program/library metadata has already proved the exact descriptor
// family at this occurrence.
//
// needCodeEntry should be true only for transparent-recover bookkeeping. The
// returned selection invokes only the typed structured entry.
func (b Builder) PrepareCoroDispatchStructuredOnly(
	fn Expr, opts CoroDispatchCallOptions, needCodeEntry bool,
) CoroDispatchStructuredOnlySelection {
	if !opts.TrustedDescriptor {
		panic("ssa: structured-only coroutine dispatch requires a trusted descriptor proof")
	}
	if opts.Version != CoroDispatchVersionV2 {
		panic(fmt.Sprintf(
			"ssa: coroutine dispatch version is %d, want %d",
			opts.Version, CoroDispatchVersionV2,
		))
	}
	if fn.IsNil() || fn.kind != vkClosure && fn.kind != vkIfaceMethod {
		panic("ssa: coroutine dispatch call requires a function or interface-method pair")
	}
	sig, ok := b.Prog.Field(fn.Type, 0).RawType().(*types.Signature)
	if !ok {
		panic("ssa: coroutine dispatch call has no function signature")
	}
	if err := validateCoroDispatchPhysicalSignature(sig); err != nil {
		panic("ssa: coroutine dispatch call: " + err.Error())
	}
	validateCoroDispatchResult(b.Prog, opts.Result, "call")

	descriptorWord := b.Field(fn, 0)
	if !opts.DescriptorNonNil {
		b.AssertNilDeref(descriptorWord)
	}
	descriptor := Expr{
		descriptorWord.impl,
		b.Prog.Pointer(b.Prog.coroDispatchDescriptorType()),
	}
	structured := b.LoadKnownNonNil(b.FieldAddrKnownNonNil(descriptor, 5))
	prepared := coroDispatchPreparedCall{
		entry:      structured,
		structured: structured,
		env:        b.Field(fn, 1),
		signature:  sig,
	}
	if needCodeEntry {
		prepared.code = b.LoadKnownNonNil(b.FieldAddrKnownNonNil(descriptor, 8))
	}
	return CoroDispatchStructuredOnlySelection{
		prepared:      prepared,
		codeRequested: needCodeEntry,
	}
}

// CoroDispatchCodeEntry validates fn and returns the descriptor's physical
// code identity. It is used only at a compiler-frozen plain dispatch site that
// needs a transparent-recover token; calls continue through the typed plain or
// coroutine entry and never through this value.
func (b Builder) CoroDispatchCodeEntry(fn Expr, opts CoroDispatchCallOptions) Expr {
	return b.prepareCoroDispatchCall(fn, nil, opts, 0).code
}

// CallCoroDispatchCoro validates a dynamic descriptor and invokes its typed
// coroutine entry. The returned handle is deliberately not awaited here;
// frontend lowering owns child registration, suspension, and result loading.
func (b Builder) CallCoroDispatchCoro(
	fn, g, out Expr, args []Expr, opts CoroDispatchCallOptions,
) (ret Expr) {
	call := b.prepareCoroDispatchCall(fn, args, opts, CoroDispatchFlagHasCoro)
	return b.callPreparedCoroDispatchCoro(call, g, out, args)
}

// CallPreparedCoroDispatchCoro invokes the coroutine entry on the HasCoro
// successor of one exact prepared selection.
func (b Builder) CallPreparedCoroDispatchCoro(
	selection CoroDispatchSelection, g, out Expr, args []Expr,
) Expr {
	call := b.requireCoroDispatchSelection(selection, args, "coroutine")
	call.entry = call.structured
	return b.callPreparedCoroDispatchCoro(call, g, out, args)
}

func (b Builder) callPreparedCoroDispatchCoro(
	call coroDispatchPreparedCall, g, out Expr, args []Expr,
) (ret Expr) {
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

// CallCoroDispatchOutcome validates a dynamic descriptor and invokes its
// synchronous explicit-status entry. The caller owns result and completion
// storage and consumes the published terminal state immediately.
func (b Builder) CallCoroDispatchOutcome(
	fn, g, out, completion Expr, args []Expr, opts CoroDispatchCallOptions,
) (ret Expr) {
	call := b.prepareCoroDispatchCall(fn, args, opts, CoroDispatchFlagHasOutcome)
	return b.callPreparedCoroDispatchOutcome(call, g, out, completion, args)
}

// CallPreparedCoroDispatchOutcome invokes the synchronous explicit-status
// entry on the HasOutcome successor of one exact prepared selection.
func (b Builder) CallPreparedCoroDispatchOutcome(
	selection CoroDispatchSelection, g, out, completion Expr, args []Expr,
) Expr {
	call := b.requireCoroDispatchSelection(selection, args, "outcome")
	call.entry = call.structured
	return b.callPreparedCoroDispatchOutcome(call, g, out, completion, args)
}

// CallPreparedCoroDispatchOutcomeOnly invokes the sole typed entry carried by
// one frozen outcome-only selection.
func (b Builder) CallPreparedCoroDispatchOutcomeOnly(
	selection CoroDispatchStructuredOnlySelection,
	g, out, completion Expr,
	args []Expr,
) Expr {
	call := b.requireCoroDispatchStructuredOnlySelection(selection, args, "outcome")
	return b.callPreparedCoroDispatchOutcome(call, g, out, completion, args)
}

// CallPreparedCoroDispatchCoroOnly invokes the sole typed entry carried by one
// frozen coroutine-only selection.
func (b Builder) CallPreparedCoroDispatchCoroOnly(
	selection CoroDispatchStructuredOnlySelection,
	g, out Expr,
	args []Expr,
) Expr {
	call := b.requireCoroDispatchStructuredOnlySelection(selection, args, "coroutine")
	return b.callPreparedCoroDispatchCoro(call, g, out, args)
}

func (b Builder) requireCoroDispatchStructuredOnlySelection(
	selection CoroDispatchStructuredOnlySelection, args []Expr, capability string,
) coroDispatchPreparedCall {
	call := selection.prepared
	if call.signature == nil || call.entry.IsNil() || call.structured.IsNil() || call.env.IsNil() {
		panic("ssa: prepared structured-only " + capability + " dispatch call has an incomplete selection")
	}
	if len(args) != call.signature.Params().Len() {
		panic(fmt.Sprintf(
			"ssa: prepared structured-only %s dispatch call has %d arguments, want %d",
			capability, len(args), call.signature.Params().Len(),
		))
	}
	return call
}

func (b Builder) callPreparedCoroDispatchOutcome(
	call coroDispatchPreparedCall, g, out, completion Expr, args []Expr,
) (ret Expr) {
	g = b.requireCoroDispatchPointer(g, "g")
	out = b.requireCoroDispatchPointer(out, "out")
	completion = b.requireCoroDispatchPointer(completion, "completion")
	outcomeSig := coroDispatchOutcomeEntrySignature(call.signature)
	physicalArgs := make([]Expr, 0, len(args)+4)
	physicalArgs = append(physicalArgs, g, out, completion, call.env)
	physicalArgs = append(physicalArgs, args...)
	ret.Type = b.Prog.Void()
	ret.impl = llvm.CreateCall(
		b.impl, b.Prog.FuncDecl(outcomeSig, InC).ll, call.entry.impl,
		llvmParams(0, physicalArgs, outcomeSig.Params(), b),
	)
	return
}

type coroDispatchPreparedCall struct {
	entry      Expr
	plain      Expr
	structured Expr
	code       Expr
	env        Expr
	flags      Expr
	signature  *types.Signature
}

func (b Builder) requireCoroDispatchSelection(
	selection CoroDispatchSelection, args []Expr, capability string,
) coroDispatchPreparedCall {
	call := selection.prepared
	if call.signature == nil || call.env.IsNil() || call.flags.IsNil() ||
		call.plain.IsNil() || call.structured.IsNil() || call.code.IsNil() {
		panic("ssa: prepared coroutine dispatch " + capability + " call has an incomplete selection")
	}
	if len(args) != call.signature.Params().Len() {
		panic(fmt.Sprintf(
			"ssa: prepared coroutine dispatch %s call has %d arguments, want %d",
			capability, len(args), call.signature.Params().Len(),
		))
	}
	return call
}

func (b Builder) prepareCoroDispatchCall(
	fn Expr, args []Expr, opts CoroDispatchCallOptions, capability uint32,
) coroDispatchPreparedCall {
	if opts.Version != CoroDispatchVersionV2 {
		panic(fmt.Sprintf(
			"ssa: coroutine dispatch version is %d, want %d",
			opts.Version, CoroDispatchVersionV2,
		))
	}
	if capability != 0 && capability != CoroDispatchFlagHasPlain &&
		capability != CoroDispatchFlagHasOutcome && capability != CoroDispatchFlagHasCoro {
		panic("ssa: coroutine dispatch call requires one known capability")
	}
	if fn.IsNil() || fn.kind != vkClosure && fn.kind != vkIfaceMethod {
		panic("ssa: coroutine dispatch call requires a function or interface-method pair")
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
	fields := make([]Expr, 9)
	for i := range fields {
		fields[i] = b.Field(descriptor, i)
	}
	if opts.TrustedDescriptor {
		prepared := coroDispatchPreparedCall{
			plain:      fields[4],
			structured: fields[5],
			env:        env,
			flags:      fields[1],
			code:       fields[8],
			signature:  sig,
		}
		if capability == CoroDispatchFlagHasPlain {
			prepared.entry = fields[4]
		} else if capability == CoroDispatchFlagHasOutcome || capability == CoroDispatchFlagHasCoro {
			prepared.entry = fields[5]
		}
		return prepared
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
		b.Prog.IntVal(uint64(^uint32(CoroDispatchKnownFlagsV2)), b.Prog.Uint32()).impl,
	)
	addInvalid("coro.dispatch.flags.unknown", llvm.CreateICmp(b.impl, llvm.IntNE, unknown, zeroFlags))
	capabilities := llvm.CreateAnd(
		b.impl, flags,
		b.Prog.IntVal(uint64(CoroDispatchCapabilityMaskV2), b.Prog.Uint32()).impl,
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
	outcomeFlag := llvm.CreateICmp(
		b.impl, llvm.IntNE,
		llvm.CreateAnd(b.impl, flags, b.Prog.IntVal(uint64(CoroDispatchFlagHasOutcome), b.Prog.Uint32()).impl),
		zeroFlags,
	)
	coroFlag := llvm.CreateICmp(
		b.impl, llvm.IntNE,
		llvm.CreateAnd(b.impl, flags, b.Prog.IntVal(uint64(CoroDispatchFlagHasCoro), b.Prog.Uint32()).impl),
		zeroFlags,
	)
	structuredEntry := llvm.CreateICmp(
		b.impl, llvm.IntNE, fields[5].impl, llvm.ConstNull(fields[5].impl.Type()),
	)
	structuredFlag := b.impl.CreateOr(outcomeFlag, coroFlag, "")
	addInvalid("coro.dispatch.structured.entry.mismatch", llvm.CreateXor(b.impl, structuredFlag, structuredEntry))
	addInvalid("coro.dispatch.structured.flags.conflict", llvm.CreateAnd(b.impl, outcomeFlag, coroFlag))
	plainNoUnwind := llvm.CreateICmp(
		b.impl, llvm.IntNE,
		llvm.CreateAnd(b.impl, flags, b.Prog.IntVal(uint64(CoroDispatchFlagPlainNoUnwind), b.Prog.Uint32()).impl),
		zeroFlags,
	)
	addInvalid(
		"coro.dispatch.plain.no-unwind-without-plain",
		llvm.CreateAnd(b.impl, plainNoUnwind, llvm.CreateNot(b.impl, plainFlag)),
	)
	addInvalid(
		"coro.dispatch.plain-only.no-unwind-missing",
		llvm.CreateAnd(
			b.impl,
			plainFlag,
			llvm.CreateAnd(b.impl, llvm.CreateNot(b.impl, structuredFlag), llvm.CreateNot(b.impl, plainNoUnwind)),
		),
	)
	noCapture := llvm.CreateICmp(
		b.impl, llvm.IntNE,
		llvm.CreateAnd(b.impl, flags, b.Prog.IntVal(uint64(CoroDispatchFlagNoCapture), b.Prog.Uint32()).impl),
		zeroFlags,
	)
	envNonNil := llvm.CreateICmp(b.impl, llvm.IntNE, env.impl, llvm.ConstNull(env.impl.Type()))
	addInvalid("coro.dispatch.nocapture.env.nonnull", llvm.CreateAnd(b.impl, noCapture, envNonNil))

	runtimeTyped := llvm.CreateICmp(
		b.impl, llvm.IntNE,
		llvm.CreateAnd(b.impl, flags, b.Prog.IntVal(uint64(CoroDispatchFlagRuntimeTyped), b.Prog.Uint32()).impl),
		zeroFlags,
	)
	hashLoMismatch := llvm.CreateICmp(
		b.impl, llvm.IntNE, fields[2].impl,
		b.Prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[:8]), b.Prog.Uint64()).impl,
	)
	hashHiMismatch := llvm.CreateICmp(
		b.impl, llvm.IntNE, fields[3].impl,
		b.Prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[8:]), b.Prog.Uint64()).impl,
	)
	addInvalid(
		"coro.dispatch.hash.invalid",
		llvm.CreateAnd(
			b.impl,
			llvm.CreateNot(b.impl, runtimeTyped),
			b.impl.CreateOr(hashLoMismatch, hashHiMismatch, ""),
		),
	)
	runtimeTypeNil := llvm.CreateICmp(
		b.impl, llvm.IntEQ, fields[2].impl,
		b.Prog.IntVal(0, b.Prog.Uint64()).impl,
	)
	runtimeMagicMismatch := llvm.CreateICmp(
		b.impl, llvm.IntNE, fields[3].impl,
		b.Prog.IntVal(CoroDispatchRuntimeTypeMagicV2, b.Prog.Uint64()).impl,
	)
	addInvalid(
		"coro.dispatch.runtime-type.invalid",
		llvm.CreateAnd(
			b.impl,
			runtimeTyped,
			b.impl.CreateOr(runtimeTypeNil, runtimeMagicMismatch, ""),
		),
	)
	equalInvalid(
		"coro.dispatch.result.size.invalid", fields[6].impl,
		b.Prog.IntVal(b.Prog.SizeOf(opts.Result), b.Prog.Uintptr()).impl,
	)
	equalInvalid(
		"coro.dispatch.result.align.invalid", fields[7].impl,
		b.Prog.IntVal(b.Prog.AlignOf(opts.Result), b.Prog.Uintptr()).impl,
	)
	addInvalid(
		"coro.dispatch.code.nil",
		llvm.CreateICmp(b.impl, llvm.IntEQ, fields[8].impl, llvm.ConstNull(fields[8].impl.Type())),
	)
	b.coroPlainDispatchTrapIf(invalid)

	prepared := coroDispatchPreparedCall{
		plain:      fields[4],
		structured: fields[5],
		env:        env,
		flags:      fields[1],
		code:       fields[8],
		signature:  sig,
	}
	if capability == CoroDispatchFlagHasPlain {
		prepared.entry = fields[4]
	} else if capability == CoroDispatchFlagHasOutcome || capability == CoroDispatchFlagHasCoro {
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
			article := "a "
			if role == "outcome" {
				article = "an "
			}
			panic("ssa: coroutine dispatch descriptor requires " + article + role + " entry")
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

func (p Package) validateCoroDispatchCodeEntry(entry Expr) llvm.Value {
	if entry.IsNil() || entry.kind != vkFuncDecl {
		panic("ssa: coroutine dispatch descriptor requires a physical code identity entry")
	}
	target := coroPlainDispatchFunction(entry.impl)
	if target.IsNil() || target.GlobalParent().C != p.mod.C {
		panic("ssa: coroutine dispatch descriptor requires a code identity entry from the same package module")
	}
	return target
}

func (p Package) newCoroDispatchDescriptorGlobal(
	name string, version, flags uint32, hash [16]byte,
	plain, structured, code llvm.Value, result Type,
) Expr {
	descriptorType := p.Prog.coroDispatchDescriptorType()
	descriptor := p.NewVarEx(name, p.Prog.Pointer(descriptorType))
	if plain.IsNil() {
		plain = p.Prog.Nil(p.Prog.VoidPtr()).impl
	}
	if structured.IsNil() {
		structured = p.Prog.Nil(p.Prog.VoidPtr()).impl
	}
	fields := []llvm.Value{
		p.Prog.IntVal(uint64(version), p.Prog.Uint32()).impl,
		p.Prog.IntVal(uint64(flags), p.Prog.Uint32()).impl,
		p.Prog.IntVal(binary.BigEndian.Uint64(hash[:8]), p.Prog.Uint64()).impl,
		p.Prog.IntVal(binary.BigEndian.Uint64(hash[8:]), p.Prog.Uint64()).impl,
		plain,
		structured,
		p.Prog.IntVal(p.Prog.SizeOf(result), p.Prog.Uintptr()).impl,
		p.Prog.IntVal(p.Prog.AlignOf(result), p.Prog.Uintptr()).impl,
		code,
	}
	descriptor.impl.SetInitializer(p.Prog.ctx.ConstStruct(fields, false))
	descriptor.impl.SetGlobalConstant(true)
	descriptor.impl.SetLinkage(llvm.LinkOnceODRLinkage)
	descriptor.impl.SetUnnamedAddr(true)
	return descriptor.Expr
}

func (p Package) matchesCoroDispatchDescriptor(
	descriptor Global, plain, structured, code llvm.Value, opts CoroDispatchDescriptorOptions,
) bool {
	if descriptor == nil || !p.isCoroDispatchDescriptor(descriptor.Expr) {
		return false
	}
	initializer := descriptor.impl.Initializer()
	if initializer.IsAConstantStruct().IsNil() || initializer.OperandsCount() != 9 {
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
	for i, want := range []llvm.Value{plain, structured} {
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
	identity := coroPlainDispatchFunction(initializer.Operand(8))
	return initializer.Operand(6).ZExtValue() == p.Prog.SizeOf(opts.Result) &&
		initializer.Operand(7).ZExtValue() == p.Prog.AlignOf(opts.Result) &&
		!identity.IsNil() && identity.C == code.C
}

func validateCoroDispatchContract(version, flags uint32) {
	if version != CoroDispatchVersionV2 {
		panic(fmt.Sprintf(
			"ssa: coroutine dispatch version is %d, want %d",
			version, CoroDispatchVersionV2,
		))
	}
	if unknown := flags &^ CoroDispatchKnownFlagsV2; unknown != 0 {
		panic(fmt.Sprintf("ssa: coroutine dispatch flags contain unknown bits %#x", unknown))
	}
	if flags&CoroDispatchCapabilityMaskV2 == 0 {
		panic("ssa: coroutine dispatch flags require HasPlain, HasOutcome, or HasCoro")
	}
	hasPlain := flags&CoroDispatchFlagHasPlain != 0
	hasOutcome := flags&CoroDispatchFlagHasOutcome != 0
	hasCoro := flags&CoroDispatchFlagHasCoro != 0
	if hasOutcome && hasCoro {
		panic("ssa: coroutine dispatch outcome and coroutine capabilities are mutually exclusive")
	}
	plainNoUnwind := flags&CoroDispatchFlagPlainNoUnwind != 0
	if plainNoUnwind && !hasPlain {
		panic("ssa: coroutine dispatch PlainNoUnwind requires HasPlain")
	}
	if hasPlain && !hasOutcome && !hasCoro && !plainNoUnwind {
		panic("ssa: coroutine dispatch plain-only capability requires PlainNoUnwind")
	}
}

func validateCoroDispatchSignature(sig *types.Signature) error {
	if sig.Recv() != nil {
		return fmt.Errorf("methods are not supported")
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
	if sig == nil || sig.Recv() != nil {
		return fmt.Errorf("requires an ordinary receiver-free function signature")
	}
	return nil
}

func validateCoroDispatchResult(prog Program, result Type, role string) {
	if result == nil || result.kind == vkInvalid || result.ll.Context().C != prog.ctx.C {
		panic("ssa: coroutine dispatch " + role + " requires a result layout from the same program")
	}
}

func coroDispatchPlainEntrySignature(sig *types.Signature) *types.Signature {
	env := types.NewParam(token.NoPos, nil, "__llgo_env", types.Typ[types.UnsafePointer])
	return FuncAddCtx(env, sig)
}

func coroDispatchCoroEntrySignature(sig *types.Signature) *types.Signature {
	params := make([]*types.Var, 0, sig.Params().Len()+3)
	for _, name := range []string{"__llgo_g", "__llgo_out", "__llgo_env"} {
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

func coroDispatchOutcomeEntrySignature(sig *types.Signature) *types.Signature {
	params := make([]*types.Var, 0, sig.Params().Len()+4)
	for _, name := range []string{"__llgo_g", "__llgo_out", "__llgo_completion", "__llgo_env"} {
		params = append(params, types.NewParam(token.NoPos, nil, name, types.Typ[types.UnsafePointer]))
	}
	for i := 0; i < sig.Params().Len(); i++ {
		params = append(params, sig.Params().At(i))
	}
	return types.NewSignatureType(
		nil, nil, nil, types.NewTuple(params...), types.NewTuple(), false,
	)
}
