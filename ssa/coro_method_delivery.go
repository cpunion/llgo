/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package ssa

import (
	"encoding/binary"
	"go/types"

	"github.com/xgo-dev/llvm"
)

func (p Program) coroMethodDescriptorType(delivery bool) Type {
	base := p.coroDispatchDescriptorType()
	if delivery {
		base = p.coroDeliveryDescriptorType()
	}
	return p.Struct(base, p.VoidPtr())
}

func (p Program) coroMethodResultPrototypeType() Type {
	return p.Struct(p.Uint64(), p.Uint64(), p.Uintptr(), p.Uintptr(),
		p.Struct(p.Uint32(), p.Uint32(), p.VoidPtr(), p.VoidPtr(),
			p.Uint64(), p.Uint64(), p.Uint64(), p.Uint64()))
}

// NewCoroMethodResultPrototype publishes only the receiver-free result ABI
// and real typed helper views. The runtime may bind this exact contract to
// reflect's fixed method invoker; arbitrary MakeFunc never consumes it.
func (p Package) NewCoroMethodResultPrototype(name string, signature *types.Signature, hash [16]byte, result Type, delivery CoroDispatchDeliveryOptions) Expr {
	if name == "" || !delivery.RequiresState {
		panic("ssa: method result prototype requires its exact state-bound contract")
	}
	tail := p.validateCoroDispatchDelivery(CoroDispatchDescriptorOptions{
		Flags: CoroDispatchFlagHasDelivery, Signature: signature, Result: result, Delivery: &delivery,
	})
	initializer := p.Prog.ctx.ConstStruct([]llvm.Value{
		p.Prog.IntVal(binary.BigEndian.Uint64(hash[:8]), p.Prog.Uint64()).impl,
		p.Prog.IntVal(binary.BigEndian.Uint64(hash[8:]), p.Prog.Uint64()).impl,
		p.Prog.IntVal(p.Prog.SizeOf(result), p.Prog.Uintptr()).impl,
		p.Prog.IntVal(p.Prog.AlignOf(result), p.Prog.Uintptr()).impl,
		p.Prog.ctx.ConstStruct(tail, false),
	}, false)
	if existing := p.VarOf(name); existing != nil {
		if existing.impl.Initializer().C != initializer.C {
			panic("ssa: conflicting method result prototype")
		}
		return existing.Expr
	}
	global := p.NewVarEx(name, p.Prog.Pointer(p.Prog.coroMethodResultPrototypeType()))
	global.impl.SetInitializer(initializer)
	global.impl.SetGlobalConstant(true)
	global.impl.SetLinkage(llvm.LinkOnceODRLinkage)
	global.impl.SetUnnamedAddr(true)
	return global.Expr
}
