/*
 * Copyright (c) 2024 The XGo Authors (xgo.dev). All rights reserved.
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
	"go/token"
	"go/types"

	"github.com/goplus/llgo/ssa/abi"
	"github.com/xgo-dev/llvm"
)

// -----------------------------------------------------------------------------

// unsafeEface(t *abi.Type, data unsafe.Pointer) Eface
func (b Builder) unsafeEface(t, data llvm.Value) llvm.Value {
	return aggregateValue(b.impl, b.Prog.rtEface(), t, data)
}

// unsafeIface(itab *runtime.Itab, data unsafe.Pointer) Eface
func (b Builder) unsafeIface(itab, data llvm.Value) llvm.Value {
	return aggregateValue(b.impl, b.Prog.rtIface(), itab, data)
}

// func NewItab(tintf *InterfaceType, typ *Type) *runtime.Itab
func (b Builder) newItab(tintf, typ Expr) Expr {
	return b.Call(b.Pkg.rtFunc("NewItab"), tintf, typ)
}

func (b Builder) unsafeInterface(rawIntf *types.Interface, t Expr, data llvm.Value) llvm.Value {
	if rawIntf.Empty() {
		return b.unsafeEface(t.impl, data)
	}
	tintf := b.abiType(rawIntf)
	itab := b.newItab(tintf, t)
	return b.unsafeIface(itab.impl, data)
}

type staticItabKey struct {
	interfaceType string
	concreteType  string
}

// CanBuildStaticItab reports whether a concrete-to-interface conversion has a
// statically known concrete type. A well-typed MakeInterface instruction is
// already the assignability proof; do not repeat types.Implements here because
// frontend physical type patches may use equivalent package-local type copies
// with deliberately different go/types object identity. staticItab validates
// every ABI method key before publishing the constant. Interface-to-interface
// conversions retain the runtime path because their concrete type is dynamic.
func CanBuildStaticItab(target, concrete types.Type) bool {
	rawIntf, ok := types.Unalias(target).Underlying().(*types.Interface)
	if !ok || rawIntf.Empty() || concrete == nil {
		return false
	}
	if _, dynamic := types.Unalias(concrete).Underlying().(*types.Interface); dynamic {
		return false
	}
	return true
}

func (b Builder) staticItab(rawIntf *types.Interface, concrete Type, tintf, typ Expr) Expr {
	prog := b.Prog
	if !CanBuildStaticItab(rawIntf, concrete.raw.Type) {
		panic("ssa: staticItab requires a statically known concrete type")
	}
	interfaceName, _ := prog.abi.TypeName(rawIntf)
	concreteName, _ := prog.abi.TypeName(concrete.raw.Type)
	key := staticItabKey{interfaceType: interfaceName, concreteType: concreteName}
	if itab, ok := b.Pkg.staticItabs[key]; ok {
		return itab
	}

	methods := make([]llvm.Value, rawIntf.NumMethods())
	for index := range methods {
		method := rawIntf.Method(index)
		methodType := funcType(prog, method.Type())
		methodTypeName, _ := prog.abi.TypeName(methodType)
		ifn, ok := b.Pkg.abiMethodIfns[abiMethodEntryKey{
			concrete:  concreteName,
			name:      abiMethodName(method),
			methodTyp: methodTypeName,
		}]
		if !ok || ifn.IsNil() || ifn.IsAConstant().IsNil() {
			panic("ssa: concrete interface method is missing from ABI metadata")
		}
		methods[index] = ifn
	}

	runtimeItab := prog.rtType("Itab")
	interField := prog.Field(runtimeItab, 0)
	typeField := prog.Field(runtimeItab, 1)
	hashField := prog.Field(runtimeItab, 2)
	funField := prog.Field(runtimeItab, 3)
	textField := prog.Index(funField)
	funArray := llvm.ArrayType(textField.ll, len(methods))
	layout := prog.ctx.StructType([]llvm.Type{
		interField.ll,
		typeField.ll,
		hashField.ll,
		funArray,
	}, false)
	initializer := prog.ctx.ConstStruct([]llvm.Value{
		tintf.impl,
		typ.impl,
		prog.IntVal(uint64(abiTypeHash(concreteName)), hashField).impl,
		llvm.ConstArray(textField.ll, methods),
	}, false)
	global := llvm.AddGlobal(b.Pkg.mod, layout, staticItabSymbol(b.Pkg.Path(), interfaceName, concreteName))
	global.SetInitializer(initializer)
	global.SetLinkage(llvm.PrivateLinkage)
	global.SetGlobalConstant(true)
	global.SetUnnamedAddr(true)
	global.SetAlignment(prog.td.ABITypeAlignment(runtimeItab.ll))
	ret := Expr{global, prog.Pointer(runtimeItab)}
	if b.Pkg.staticItabs == nil {
		b.Pkg.staticItabs = make(map[staticItabKey]Expr)
	}
	b.Pkg.staticItabs[key] = ret
	return ret
}

func (b Builder) unsafeConcreteInterface(rawIntf *types.Interface, concrete Type, typ Expr, data llvm.Value) llvm.Value {
	if rawIntf.Empty() {
		return b.unsafeEface(typ.impl, data)
	}
	tintf := b.abiType(rawIntf)
	if CanBuildStaticItab(rawIntf, concrete.raw.Type) {
		itab := b.staticItab(rawIntf, concrete, tintf, typ)
		return b.unsafeIface(itab.impl, data)
	}
	itab := b.newItab(tintf, typ)
	return b.unsafeIface(itab.impl, data)
}

func iMethodOf(rawIntf *types.Interface, name string) int {
	n := rawIntf.NumMethods()
	for i := 0; i < n; i++ {
		m := rawIntf.Method(i)
		if m.Name() == name {
			// TODO(xsw): check signature
			return i
		}
	}
	return -1
}

// Imethod returns closure of an interface method.
func (b Builder) Imethod(intf Expr, method *types.Func) Expr {
	return b.imethod(intf, method, false)
}

// ImethodRawDataKnownNonNil returns an interface method closure whose
// environment is the interface's unmodified data word. The caller must prove
// both that the interface is non-nil and that its dynamic representation
// already is the stable receiver pointer expected by the selected method
// entry. This is the zero-copy transport used by target-proven coroutine
// interface dispatch; ordinary interface calls continue to use Imethod.
func (b Builder) ImethodRawDataKnownNonNil(intf Expr, method *types.Func) Expr {
	return b.imethod(intf, method, true)
}

func (b Builder) imethod(intf Expr, method *types.Func, rawDataKnownNonNil bool) Expr {
	prog := b.Prog
	intfType := types.Unalias(intf.raw.Type)
	patchedIntfType := prog.patch(intfType)
	rawIntf := patchedIntfType.Underlying().(*types.Interface)
	sig := method.Type().(*types.Signature)
	if sig.Recv() == nil && sig.Params().Len() > 0 {
		pt := types.Unalias(sig.Params().At(0).Type())
		if types.Identical(pt, rawIntf) {
			n := sig.Params().Len()
			vars := make([]*types.Var, n-1)
			for i := 1; i < n; i++ {
				vars[i-1] = sig.Params().At(i)
			}
			sig = types.NewSignatureType(nil, nil, nil, types.NewTuple(vars...), sig.Results(), sig.Variadic())
		}
	}
	tclosure := prog.Type(sig, InGo)
	i := iMethodOf(rawIntf, method.Name())
	b.recordUseIfaceMethod(rawIntf, i)
	var data Expr
	if rawDataKnownNonNil {
		data = b.InterfaceData(intf)
	} else {
		data = b.InlineCall(b.Pkg.rtFunc("IfacePtrData"), intf)
	}
	var fn Expr
	impl := intf.impl
	itab := Expr{b.faceItab(impl), prog.VoidPtrPtr()}
	pfn := b.Advance(itab, prog.IntVal(uint64(i+3), prog.Int()))
	if prog.enableGoGlobalDCE {
		fnType := prog.Elem(pfn.Type)
		fn = Expr{
			prog.methodCheckedLoad(b.impl, pfn.impl, methodCapabilityKey(method)),
			fnType,
		}
	} else {
		fn = b.Load(pfn)
	}
	// This is a transient interface invocation pair, not a first-class
	// funcval. The method receiver remains an ordinary ABI parameter.
	tmethod := &aType{tclosure.ll, tclosure.raw, vkIfaceMethod}
	ret := b.aggregateValue(tmethod, fn.impl, data.impl)
	return ret
}

// -----------------------------------------------------------------------------

// MakeInterface constructs an instance of an interface type from a
// value of a concrete type.
//
// Use Program.MethodSets.MethodSet(X.Type()) to find the method-set
// of X, and Program.MethodValue(m) to find the implementation of a method.
//
// To construct the zero value of an interface type T, use:
//
//	NewConst(constant.MakeNil(), T, pos)
//
// Example printed form:
//
//	t1 = make interface{} <- int (42:int)
//	t2 = make Stringer <- t0
func (b Builder) MakeInterface(tinter Type, x Expr) (ret Expr) {
	return b.makeInterface(tinter, x, false)
}

// MakeInterfaceFromConstant constructs an interface from a compile-time
// constant. Indirect interface payloads use immutable package-local backing
// storage instead of a fresh heap allocation. This is safe because an
// interface exposes a copy of the concrete value, not writable access to its
// backing storage.
func (b Builder) MakeInterfaceFromConstant(tinter Type, x Expr) (ret Expr) {
	if x.IsNil() || x.impl.IsAConstant().IsNil() {
		panic("ssa: MakeInterfaceFromConstant requires an LLVM constant")
	}
	return b.makeInterface(tinter, x, true)
}

func (b Builder) makeInterface(tinter Type, x Expr, constantBacking bool) (ret Expr) {
	rawIntf := tinter.raw.Type.Underlying().(*types.Interface)
	dbgInstrf("MakeInterface %v, %v\n", rawIntf, x.impl)
	if x.kind == vkFuncDecl {
		typ := b.Prog.Type(x.raw.Type, InGo)
		x = checkExpr(x, typ.raw.Type, b)
	}
	prog := b.Prog
	typ := x.Type
	b.recordUseIface(typ)
	tabi := b.abiType(typ.raw.Type)
	if !directIfaceType(typ.raw.Type) {
		if constantBacking {
			vptr := b.Pkg.constantAddress(x)
			return Expr{b.unsafeConcreteInterface(rawIntf, typ, tabi, vptr.impl), tinter}
		}
		vptr := b.AllocU(typ)
		b.Store(vptr, x)
		return Expr{b.unsafeConcreteInterface(rawIntf, typ, tabi, vptr.impl), tinter}
	}
	kind, _, lvl := abi.DataKindOf(typ.raw.Type, 0, prog.is32Bits)
	switch kind {
	case abi.Indirect:
		if constantBacking {
			vptr := b.Pkg.constantAddress(x)
			return Expr{b.unsafeConcreteInterface(rawIntf, typ, tabi, vptr.impl), tinter}
		}
		vptr := b.AllocU(typ)
		b.Store(vptr, x)
		return Expr{b.unsafeConcreteInterface(rawIntf, typ, tabi, vptr.impl), tinter}
	}
	ximpl := x.impl
	if lvl > 0 {
		ximpl = extractVal(b.impl, ximpl, lvl)
	}
	var u llvm.Value
	switch kind {
	case abi.Pointer:
		return Expr{b.unsafeConcreteInterface(rawIntf, typ, tabi, ximpl), tinter}
	case abi.Integer:
		tu := prog.Uintptr()
		u = llvm.CreateIntCast(b.impl, ximpl, tu.ll)
	case abi.BitCast:
		tu := prog.Uintptr()
		if b.Prog.td.TypeAllocSize(typ.ll) < b.Prog.td.TypeAllocSize(tu.ll) {
			u = llvm.CreateBitCast(b.impl, ximpl, prog.Uint32().ll)
		} else {
			u = llvm.CreateBitCast(b.impl, ximpl, tu.ll)
		}
	default:
		panic("todo")
	}
	data := llvm.CreateIntToPtr(b.impl, u, prog.tyVoidPtr())
	return Expr{b.unsafeConcreteInterface(rawIntf, typ, tabi, data), tinter}
}

func (b Builder) MakeInterfaceFromPtr(tinter Type, ptr Expr) (ret Expr) {
	return b.makeInterfaceFromPtr(tinter, ptr, false)
}

// MakeInterfaceFromKnownNonNilPtr boxes the value at ptr without synthesizing
// a nil-dereference helper. The caller must already own or prove the
// source-language nil edge.
func (b Builder) MakeInterfaceFromKnownNonNilPtr(tinter Type, ptr Expr) (ret Expr) {
	return b.makeInterfaceFromPtr(tinter, ptr, true)
}

func (b Builder) makeInterfaceFromPtr(tinter Type, ptr Expr, knownNonNil bool) (ret Expr) {
	rawIntf := tinter.raw.Type.Underlying().(*types.Interface)
	prog := b.Prog
	if !knownNonNil {
		b.AssertNilDeref(ptr)
	}

	typ := prog.Elem(ptr.Type)
	tabi := b.abiType(typ.raw.Type)
	if kind, _, _ := abi.DataKindOf(typ.raw.Type, 0, prog.is32Bits); kind != abi.Indirect {
		if knownNonNil {
			return b.MakeInterface(tinter, b.LoadKnownNonNil(ptr))
		}
		return b.MakeInterface(tinter, b.Load(ptr))
	}

	b.recordUseIface(typ)
	vptr := b.AllocU(typ)
	dst := b.Convert(prog.VoidPtr(), vptr)
	src := b.Convert(prog.VoidPtr(), ptr)
	b.Call(b.Pkg.rtFunc("Typedmemmove"), tabi, dst, src)
	return Expr{b.unsafeConcreteInterface(rawIntf, typ, tabi, vptr.impl), tinter}
}

func (b Builder) recordUseIface(typ Type) {
	if mb := b.Pkg.metaBuilder; mb != nil {
		if _, ok := types.Unalias(typ.raw.Type).Underlying().(*types.Interface); !ok {
			typeName, _ := b.Prog.abi.TypeName(typ.raw.Type)
			mb.AddIfaceUse(mb.Sym(b.Func.Name()), mb.Sym(typeName))
		}
	}
}

func (b Builder) recordUseIfaceMethod(rawIntf *types.Interface, methodIndex int) {
	if mb := b.Pkg.metaBuilder; mb != nil {
		intfSymName, _ := b.Prog.abi.TypeName(rawIntf)
		intfSym := mb.Sym(intfSymName)
		b.recordInterfaceInfo(rawIntf, intfSymName)
		mb.AddIfaceMethodUse(mb.Sym(b.Func.Name()), intfSym, uint32(methodIndex))
	}
}

func (b Builder) recordInterfaceInfo(t *types.Interface, typeName string) {
	mb := b.Pkg.metaBuilder
	if mb == nil {
		return
	}
	prog := b.Prog
	intfSym := mb.Sym(typeName)
	for i := 0; i < t.NumMethods(); i++ {
		f := t.Method(i)
		ftypName, _ := prog.abi.TypeName(funcType(prog, f.Type()))
		mb.AddIfaceMethod(intfSym, abiMethodName(f), mb.Sym(ftypName))
	}
}

func (b Builder) valFromData(typ Type, data llvm.Value) Expr {
	prog := b.Prog
	if !directIfaceType(typ.raw.Type) {
		impl := b.impl
		tll := typ.ll
		tptr := llvm.PointerType(tll, 0)
		ptr := llvm.CreatePointerCast(impl, data, tptr)
		return Expr{llvm.CreateLoad(impl, tll, ptr), typ}
	}
	kind, real, lvl := abi.DataKindOf(typ.raw.Type, 0, prog.is32Bits)
	switch kind {
	case abi.Indirect:
		impl := b.impl
		tll := typ.ll
		tptr := llvm.PointerType(tll, 0)
		ptr := llvm.CreatePointerCast(impl, data, tptr)
		return Expr{llvm.CreateLoad(impl, tll, ptr), typ}
	}
	t := typ
	if lvl > 0 {
		t = prog.rawType(real)
	}
	switch kind {
	case abi.Pointer:
		return b.buildVal(typ, data, lvl)
	case abi.Integer:
		x := castUintptr(b, data, prog.VoidPtr(), prog.Uintptr())
		return b.buildVal(typ, castInt(b, x, prog.Uintptr(), t), lvl)
	case abi.BitCast:
		x := castUintptr(b, data, prog.VoidPtr(), prog.Uintptr())
		if int(prog.SizeOf(t)) != prog.PointerSize() {
			x = castInt(b, x, prog.Uintptr(), prog.Int32())
		}
		return b.buildVal(typ, llvm.CreateBitCast(b.impl, x, t.ll), lvl)
	}
	panic("todo")
}

func extractVal(b llvm.Builder, val llvm.Value, lvl int) llvm.Value {
	for lvl > 0 {
		// TODO(xsw): check array support
		val = llvm.CreateExtractValue(b, val, 0)
		lvl--
	}
	return val
}

func (b Builder) buildVal(typ Type, val llvm.Value, lvl int) Expr {
	if lvl == 0 {
		return Expr{val, typ}
	}
	switch t := typ.raw.Type.Underlying().(type) {
	case *types.Struct:
		telem := b.Prog.rawType(t.Field(0).Type())
		elem := b.buildVal(telem, val, lvl-1)
		return Expr{aggregateValue(b.impl, typ.ll, elem.impl), typ}
	case *types.Array:
		telem := b.Prog.rawType(t.Elem())
		elem := b.buildVal(telem, val, lvl-1)
		return Expr{aggregateValue(b.impl, typ.ll, elem.impl), typ}
	}
	panic("todo")
}

// The TypeAssert instruction tests whether interface value X has type
// AssertedType.
//
// If !CommaOk, on success it returns v, the result of the conversion
// (defined below); on failure it panics.
//
// If CommaOk: on success it returns a pair (v, true) where v is the
// result of the conversion; on failure it returns (z, false) where z
// is AssertedType's zero value.  The components of the pair must be
// accessed using the Extract instruction.
//
// If Underlying: tests whether interface value X has the underlying
// type AssertedType.
//
// If AssertedType is a concrete type, TypeAssert checks whether the
// dynamic type in interface X is equal to it, and if so, the result
// of the conversion is a copy of the value in the interface.
//
// If AssertedType is an interface, TypeAssert checks whether the
// dynamic type of the interface is assignable to it, and if so, the
// result of the conversion is a copy of the interface value X.
// If AssertedType is a superinterface of X.Type(), the operation will
// fail iff the operand is nil.  (Contrast with ChangeInterface, which
// performs no nil-check.)
//
// Type() reflects the actual type of the result, possibly a
// 2-types.Tuple; AssertedType is the asserted type.
//
// Depending on the TypeAssert's purpose, Pos may return:
//   - the ast.CallExpr.Lparen of an explicit T(e) conversion;
//   - the ast.TypeAssertExpr.Lparen of an explicit e.(T) operation;
//   - the ast.CaseClause.Case of a case of a type-switch statement;
//   - the Ident(m).NamePos of an interface method value i.m
//     (for which TypeAssert may be used to effect the nil check).
//
// Example printed form:
//
//	t1 = typeassert t0.(int)
//	t3 = typeassert,ok t2.(T)
func (b Builder) TypeAssert(x Expr, assertedTyp Type, commaOk bool) Expr {
	dbgInstrf("TypeAssert %v, %v, %v\n", x.impl, assertedTyp.raw.Type, commaOk)
	logical := b.blk
	if logical == nil {
		panic("TypeAssert: no active logical block")
	}
	tx := b.faceAbiType(x)
	tabi := b.abiType(assertedTyp.raw.Type)
	var eq Expr
	var val func() Expr
	if x.RawType() == assertedTyp.RawType() {
		eq = b.BinOp(token.NEQ, tx, b.Prog.Zero(b.Prog.AbiTypePtr()))
		val = func() Expr { return x }
	} else {
		if rawIntf, ok := assertedTyp.raw.Type.Underlying().(*types.Interface); ok {
			eq = b.InlineCall(b.Pkg.rtFunc("Implements"), tabi, tx)
			val = func() Expr { return Expr{b.unsafeInterface(rawIntf, tx, b.faceData(x.impl)), assertedTyp} }
		} else if assertedTyp.kind == vkClosure {
			eq = b.InlineCall(b.Pkg.rtFunc("MatchesClosure"), tabi, tx)
			val = func() Expr { return b.valFromData(assertedTyp, b.faceData(x.impl)) }
		} else {
			eq = b.BinOp(token.EQL, tx, tabi)
			val = func() Expr { return b.valFromData(assertedTyp, b.faceData(x.impl)) }
		}
	}

	if commaOk {
		prog := b.Prog
		t := prog.Struct(assertedTyp, prog.Bool())
		blks := b.Func.MakeBlocks(3)
		b.If(eq, blks[0], blks[1])

		b.SetBlockEx(blks[2], AtEnd, false)
		phi := b.Phi(t)
		phi.AddIncoming(b, blks[:2], func(i int, blk BasicBlock) Expr {
			// Runtime operations used to materialize the asserted value may be
			// expanded into multiple physical blocks (notably a coroutine child
			// await).  Make this branch the active logical block so its last field
			// follows that expansion and Phi observes the real predecessor.
			b.SetBlockEx(blk, AtEnd, true)
			if i == 0 {
				valTrue := aggregateValue(b.impl, t.ll, val().impl, prog.BoolVal(true).impl)
				b.Jump(blks[2])
				return Expr{valTrue, t}
			}
			zero := prog.Zero(assertedTyp)
			valFalse := aggregateValue(b.impl, t.ll, zero.impl, prog.BoolVal(false).impl)
			b.Jump(blks[2])
			return Expr{valFalse, t}
		})
		b.SetBlockEx(blks[2], AtEnd, false)
		b.blk = logical
		logical.last = blks[2].last
		return phi.Expr
	}
	blks := b.Func.MakeBlocks(2)
	b.If(eq, blks[0], blks[1])
	b.SetBlockEx(blks[1], AtEnd, true)
	b.Call(b.Pkg.rtFunc("PanicTypeAssert"), tx, b.Str(assertedTyp.RawType().String()), b.Str(typeAssertMissingMethod(assertedTyp)))
	b.Unreachable()
	b.SetBlockEx(blks[0], AtEnd, true)
	result := val()
	b.blk = logical
	logical.last = blks[0].last
	return result
}

func typeAssertMissingMethod(assertedTyp Type) string {
	if rawIntf, ok := assertedTyp.RawType().Underlying().(*types.Interface); ok && rawIntf.NumMethods() > 0 {
		return rawIntf.Method(0).Name()
	}
	return ""
}

// ChangeInterface constructs a value of one interface type from a
// value of another interface type known to be assignable to it.
// This operation cannot fail.
//
// Pos() returns the ast.CallExpr.Lparen if the instruction arose from
// an explicit T(e) conversion; the ast.TypeAssertExpr.Lparen if the
// instruction arose from an explicit e.(T) operation; or token.NoPos
// otherwise.
//
// Example printed form:
//
//	t1 = change interface interface{} <- I (t0)
func (b Builder) ChangeInterface(typ Type, x Expr) (ret Expr) {
	rawIntf := typ.raw.Type.Underlying().(*types.Interface)
	tabi := b.faceAbiType(x)
	data := b.faceData(x.impl)
	return Expr{b.unsafeInterface(rawIntf, tabi, data), typ}
}

// -----------------------------------------------------------------------------

// InterfaceData returns the data pointer of an interface.
func (b Builder) InterfaceData(x Expr) Expr {
	dbgInstrf("InterfaceData %v\n", x.impl)
	return Expr{b.faceData(x.impl), b.Prog.VoidPtr()}
}

// EfaceType returns the dynamic ABI type descriptor stored directly in an
// empty-interface value. It deliberately rejects non-empty interfaces: their
// first word is an itab rather than an ABI type descriptor.
func (b Builder) EfaceType(x Expr) Expr {
	raw, ok := types.Unalias(x.raw.Type).Underlying().(*types.Interface)
	if !ok || !raw.Empty() {
		panic("EfaceType requires an empty-interface value")
	}
	dbgInstrf("EfaceType %v\n", x.impl)
	return Expr{llvm.CreateExtractValue(b.impl, x.impl, 0), b.Prog.AbiTypePtr()}
}

// InterfaceTypeWord returns the first pointer-sized word of any interface
// value. For an empty interface this is the ABI type descriptor; for a
// non-empty interface it is the itab. In both representations the interface is
// nil exactly when this word is nil. Callers must not otherwise reinterpret an
// itab as an ABI type descriptor.
func (b Builder) InterfaceTypeWord(x Expr) Expr {
	if _, ok := types.Unalias(x.raw.Type).Underlying().(*types.Interface); !ok {
		panic("InterfaceTypeWord requires an interface value")
	}
	dbgInstrf("InterfaceTypeWord %v\n", x.impl)
	return Expr{llvm.CreateExtractValue(b.impl, x.impl, 0), b.Prog.VoidPtr()}
}

func (b Builder) faceData(x llvm.Value) llvm.Value {
	return llvm.CreateExtractValue(b.impl, x, 1)
}

func (b Builder) faceItab(x llvm.Value) llvm.Value {
	return llvm.CreateExtractValue(b.impl, x, 0)
}

func (b Builder) faceAbiType(x Expr) Expr {
	if x.kind == vkIface {
		return b.InlineCall(b.Pkg.rtFunc("IfaceType"), x)
	}
	typ := llvm.CreateExtractValue(b.impl, x.impl, 0)
	return Expr{typ, b.Prog.AbiTypePtr()}
}

// -----------------------------------------------------------------------------
