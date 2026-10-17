/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package ssa

import (
	"go/token"
	"go/types"

	"github.com/xgo-dev/llvm"
)

// CoroDeliveryStorageGoType projects the guard's nonempty-result storage for
// native-stack promotion before LLVM types are materialized. Empty results
// additionally reserve a byte in NewCoroDeliveryStorage so their address is
// interior; both empty layouts are necessarily below the native-stack limit.
func CoroDeliveryStorageGoType(result types.Type) *types.Struct {
	if result == nil {
		panic("ssa: delivery storage requires an exact result tuple")
	}
	if _, tuple := types.Unalias(result).Underlying().(*types.Struct); !tuple {
		panic("ssa: delivery storage requires an exact result tuple")
	}
	return types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "_llgo_f0", types.Typ[types.UnsafePointer], false),
		types.NewField(token.NoPos, nil, "_llgo_f1", result, false),
	}, nil)
}

// CoroDeliveryStorage is the target layout of a compiler-owned result guard.
// Its public result remains an exact typed tuple. A private pointer before that
// tuple can retain evaluated invocation state until the caller accepts it.
// This is a layout operation, not a resource capability or lifetime decision.
type CoroDeliveryStorage struct {
	storage Type
	result  Type
	offset  uint64
}

// NewCoroDeliveryStorage accepts an already physically lowered result tuple.
// It does not normalize function-containing fields a second time. Only result
// guards which own this complete allocation may use the address operations.
func (p Program) NewCoroDeliveryStorage(result Type) CoroDeliveryStorage {
	if result == nil || result.ll.Context().C != p.ctx.C {
		panic("ssa: delivery storage requires a result type from this program")
	}
	if _, tuple := types.Unalias(result.RawType()).Underlying().(*types.Struct); !tuple {
		panic("ssa: delivery storage requires an exact result tuple")
	}
	storage := p.rawType(CoroDeliveryStorageGoType(result.RawType()))
	if p.SizeOf(result) == 0 {
		// An empty result must still point inside the retained allocation.
		// A one-past-end pointer cannot serve as its collector-visible root.
		storage = p.Struct(p.VoidPtr(), result, p.Byte())
	}
	return CoroDeliveryStorage{storage: storage, result: result, offset: p.OffsetOf(storage, 1)}
}

func (layout CoroDeliveryStorage) Type() Type { return layout.storage }

func (layout CoroDeliveryStorage) ResultOffset() uint64 { return layout.offset }

// ResultAddress projects the same public result pointer passed to a producer
// and to ordinary borrowed C output parameters. The private state never
// crosses those source interfaces.
func (layout CoroDeliveryStorage) ResultAddress(b Builder, storage Expr) Expr {
	layout.requireProgram(b.Prog)
	if storage.Type == nil ||
		!types.Identical(storage.RawType(), types.NewPointer(layout.storage.RawType())) {
		panic("ssa: delivery result address requires its exact owned storage pointer")
	}
	return b.FieldAddrKnownNonNil(storage, 1)
}

// StateAddress recovers only the private field of this exact owned result
// allocation. The offset comes from its target layout, including alignment
// padding; no pointer-to-integer round trip or runtime address lookup occurs.
// The caller must already prove a non-nil pointer into the full allocation.
func (layout CoroDeliveryStorage) StateAddress(b Builder, result Expr) Expr {
	layout.requireProgram(b.Prog)
	if result.Type == nil ||
		!types.Identical(result.RawType(), types.NewPointer(layout.result.RawType())) {
		panic("ssa: delivery state address requires its exact owned result pointer")
	}
	index := b.Prog.IntVal(0-layout.offset, b.Prog.Int()).impl
	base := llvm.CreateGEP(b.impl, b.Prog.Byte().ll, result.impl, []llvm.Value{index})
	storage := Expr{base, b.Prog.Pointer(layout.storage)}
	return b.FieldAddrKnownNonNil(storage, 0)
}

func (layout CoroDeliveryStorage) requireProgram(p Program) {
	if layout.storage == nil || layout.result == nil || layout.storage.ll.Context().C != p.ctx.C {
		panic("ssa: delivery storage layout belongs to another program")
	}
}
