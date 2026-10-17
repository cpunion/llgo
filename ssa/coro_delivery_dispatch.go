/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package ssa

import (
	"encoding/binary"
	"go/token"
	"go/types"

	"github.com/xgo-dev/llvm"
)

const CoroDispatchDeliveryVersionV2 uint32 = 2

// CoroDispatchDeliveryABI is the consumer's independently computed helper ABI.
// The cold tail stores the producer's expected hashes, not hashes recovered
// from the helper descriptors themselves. This also permits a dynamic ABI
// consumer to validate those descriptors without reversing a code address.
type CoroDispatchDeliveryABI struct {
	Initializer [16]byte
	Drop        [16]byte
}

// CoroDispatchDeliveryHelper binds a compiler-injected descriptor to its exact
// logical type and ABI. No function address is reverse-mapped at runtime.
type CoroDispatchDeliveryHelper struct {
	Descriptor Expr
	Signature  *types.Signature
	ABIHash    [16]byte
}

// CoroDispatchDeliveryOptions adds only a cold descriptor tail. Both helpers
// are ordinary context-free managed descriptors, so existing typed dispatch,
// child awaiting and internal cleanup shielding remain the only call engine.
// The frontend must bind physical custody before publishing this capability.
type CoroDispatchDeliveryOptions struct {
	Version     uint32
	Initializer CoroDispatchDeliveryHelper
	Drop        CoroDispatchDeliveryHelper
}

func (p Program) coroDeliveryDescriptorType() Type {
	// Nest the complete base, including its target padding. On wasm32 its
	// trailing padding must not become the first tail word.
	return p.Struct(p.coroDispatchDescriptorType(),
		p.Struct(p.Uint32(), p.Uint32(), p.VoidPtr(), p.VoidPtr(),
			p.Uint64(), p.Uint64(), p.Uint64(), p.Uint64()))
}

func (p Program) validateCoroDeliveryHelperTypes(producer *types.Signature, result Type, initializer, drop *types.Signature) {
	if producer == nil || producer.Results().Len() == 0 || result == nil ||
		initializer == nil || drop == nil ||
		validateCoroDispatchSignature(initializer) != nil || validateCoroDispatchSignature(drop) != nil ||
		initializer.Variadic() || drop.Variadic() ||
		initializer.Params().Len() != 1 || initializer.Results().Len() != 0 ||
		drop.Params().Len() != producer.Params().Len()+2 || drop.Results().Len() != 0 {
		panic("ssa: coroutine delivery requires exact typed initializer and drop signatures")
	}
	// Publication starts with source signatures; prepared calls already carry
	// physical signatures. Normalize both sides through the same conversion,
	// including aggregates containing Go func values. Exact go/types identity
	// is still required: equal LLVM layouts alone do not grant this contract.
	producer = p.PhysicalFuncDecl(producer, InGo)
	initializer = p.PhysicalFuncDecl(initializer, InGo)
	drop = p.PhysicalFuncDecl(drop, InGo)
	want := types.NewPointer(result.RawType())
	if !types.Identical(initializer.Params().At(0).Type(), want) ||
		!types.Identical(drop.Params().At(0).Type(), want) ||
		!types.Identical(drop.Params().At(1).Type(), types.Typ[types.UnsafePointer]) {
		panic("ssa: coroutine delivery helpers do not match the exact output and environment types")
	}
	for index := 0; index < producer.Params().Len(); index++ {
		if !types.Identical(drop.Params().At(index+2).Type(), producer.Params().At(index).Type()) {
			panic("ssa: coroutine delivery drop does not match the exact producer arguments")
		}
	}
}

func (p Package) validateCoroDispatchDelivery(opts CoroDispatchDescriptorOptions) []llvm.Value {
	if opts.Delivery == nil {
		if opts.Flags&CoroDispatchFlagHasDelivery != 0 {
			panic("ssa: coroutine delivery capability requires its typed tail")
		}
		return nil
	}
	delivery := opts.Delivery
	if opts.Flags&CoroDispatchFlagHasDelivery == 0 {
		panic("ssa: coroutine delivery tail requires its capability")
	}
	if delivery.Version != CoroDispatchDeliveryVersionV2 {
		panic("ssa: unsupported coroutine delivery tail version")
	}
	if p.Prog.SizeOf(opts.Result) == 0 {
		panic("ssa: coroutine delivery requires nonempty output storage")
	}
	p.Prog.validateCoroDeliveryHelperTypes(opts.Signature, opts.Result,
		delivery.Initializer.Signature, delivery.Drop.Signature)
	for _, helper := range []CoroDispatchDeliveryHelper{delivery.Initializer, delivery.Drop} {
		if !p.isCoroDispatchDescriptor(helper.Descriptor) {
			panic("ssa: coroutine delivery helper requires a descriptor from the same package module")
		}
		base := coroPlainDispatchGlobal(helper.Descriptor.impl).Initializer()
		if base.IsAConstantStruct().IsNil() || base.OperandsCount() != 9 {
			panic("ssa: coroutine delivery helper must not publish another delivery tail")
		}
		flags := uint32(base.Operand(1).ZExtValue())
		validateCoroDispatchContract(uint32(base.Operand(0).ZExtValue()), flags)
		if flags&CoroDispatchFlagNoCapture == 0 || flags&(CoroDispatchFlagRuntimeTyped|CoroDispatchFlagHasDelivery) != 0 {
			panic("ssa: coroutine delivery helper must be compiler-owned and context-free")
		}
		code := coroPlainDispatchFunction(base.Operand(8))
		if code.IsNil() || p.FuncOf(code.Name()) == nil || p.FuncOf(code.Name()).impl.C != code.C {
			panic("ssa: coroutine delivery helper lost its physical code identity")
		}
		if base.Operand(2).ZExtValue() != binary.BigEndian.Uint64(helper.ABIHash[:8]) ||
			base.Operand(3).ZExtValue() != binary.BigEndian.Uint64(helper.ABIHash[8:]) ||
			base.Operand(6).ZExtValue() != 0 || base.Operand(7).ZExtValue() != p.Prog.AlignOf(p.Prog.Struct()) {
			panic("ssa: coroutine delivery helper does not match its exact ABI and empty result layout")
		}
		for _, entry := range []struct {
			field int
			flag  uint32
			want  *types.Signature
		}{
			{4, CoroDispatchFlagHasPlain, p.Prog.CoroDispatchPlainEntrySignature(helper.Signature)},
			{5, CoroDispatchFlagHasOutcome, p.Prog.CoroDispatchOutcomeEntrySignature(helper.Signature)},
			{5, CoroDispatchFlagHasCoro, p.Prog.CoroDispatchCoroEntrySignature(helper.Signature)},
		} {
			if flags&entry.flag == 0 {
				continue
			}
			function := coroPlainDispatchFunction(base.Operand(entry.field))
			if function.IsNil() || p.FuncOf(function.Name()) == nil {
				panic("ssa: coroutine delivery helper lost its typed entry declaration")
			}
			p.validateCoroDispatchEntry(p.FuncOf(function.Name()).Expr, entry.want, true, "delivery helper")
		}
	}
	return []llvm.Value{
		p.Prog.IntVal(uint64(delivery.Version), p.Prog.Uint32()).impl,
		p.Prog.IntVal(0, p.Prog.Uint32()).impl,
		delivery.Initializer.Descriptor.impl, delivery.Drop.Descriptor.impl,
		p.Prog.IntVal(binary.BigEndian.Uint64(delivery.Initializer.ABIHash[:8]), p.Prog.Uint64()).impl,
		p.Prog.IntVal(binary.BigEndian.Uint64(delivery.Initializer.ABIHash[8:]), p.Prog.Uint64()).impl,
		p.Prog.IntVal(binary.BigEndian.Uint64(delivery.Drop.ABIHash[:8]), p.Prog.Uint64()).impl,
		p.Prog.IntVal(binary.BigEndian.Uint64(delivery.Drop.ABIHash[8:]), p.Prog.Uint64()).impl,
	}
}

func (p Package) matchesCoroDispatchDelivery(tail llvm.Value, want CoroDispatchDeliveryOptions) bool {
	return !tail.IsAConstantStruct().IsNil() && tail.OperandsCount() == 8 &&
		tail.Operand(0).ZExtValue() == uint64(want.Version) && tail.Operand(1).ZExtValue() == 0 &&
		tail.Operand(2).C == want.Initializer.Descriptor.impl.C && tail.Operand(3).C == want.Drop.Descriptor.impl.C &&
		tail.Operand(4).ZExtValue() == binary.BigEndian.Uint64(want.Initializer.ABIHash[:8]) &&
		tail.Operand(5).ZExtValue() == binary.BigEndian.Uint64(want.Initializer.ABIHash[8:]) &&
		tail.Operand(6).ZExtValue() == binary.BigEndian.Uint64(want.Drop.ABIHash[:8]) &&
		tail.Operand(7).ZExtValue() == binary.BigEndian.Uint64(want.Drop.ABIHash[8:])
}

// PrepareCoroDispatchDelivery reads the optional tail only after a dominating
// HasDelivery check. It returns ordinary nil-environment managed function
// values, not raw callback pointers or a separate invocation protocol. The
// caller invokes/awaits them through the existing typed dispatch operations.
func (b Builder) PrepareCoroDispatchDelivery(selection CoroDispatchSelection, initializer, drop *types.Signature, abi CoroDispatchDeliveryABI) (Expr, Expr) {
	call := selection.prepared
	if call.descriptor.IsNil() {
		panic("ssa: coroutine delivery requires a prepared descriptor")
	}
	b.Prog.validateCoroDeliveryHelperTypes(call.signature, call.result, initializer, drop)
	b.coroPlainDispatchTrapIf(b.UnOp(token.NOT, selection.HasDelivery()).impl)
	pointer := Expr{call.descriptor.impl, b.Prog.Pointer(b.Prog.coroDeliveryDescriptorType())}
	tail := b.LoadKnownNonNil(b.FieldAddrKnownNonNil(pointer, 1))
	version, reserved := b.Field(tail, 0), b.Field(tail, 1)
	initDescriptor, dropDescriptor := b.Field(tail, 2), b.Field(tail, 3)
	invalid := b.BinOp(token.OR,
		b.BinOp(token.NEQ, version, b.Prog.IntVal(uint64(CoroDispatchDeliveryVersionV2), b.Prog.Uint32())),
		b.BinOp(token.NEQ, reserved, b.Prog.IntVal(0, b.Prog.Uint32())))
	for _, descriptor := range []Expr{initDescriptor, dropDescriptor} {
		invalid = b.BinOp(token.OR, invalid, b.BinOp(token.EQL, descriptor, b.Prog.Nil(b.Prog.VoidPtr())))
	}
	for index, hash := range [][16]byte{abi.Initializer, abi.Drop} {
		for word := 0; word < 2; word++ {
			want := b.Prog.IntVal(binary.BigEndian.Uint64(hash[word*8:word*8+8]), b.Prog.Uint64())
			invalid = b.BinOp(token.OR, invalid, b.BinOp(token.NEQ, b.Field(tail, 4+index*2+word), want))
		}
	}
	b.coroPlainDispatchTrapIf(invalid.impl)
	makeValue := func(signature *types.Signature, descriptor Expr) Expr {
		return b.aggregateValue(b.Prog.Closure(signature), descriptor.impl, b.Prog.Nil(b.Prog.VoidPtr()).impl)
	}
	return makeValue(initializer, initDescriptor), makeValue(drop, dropDescriptor)
}
