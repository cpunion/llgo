package ssa

import (
	"go/token"
	"go/types"

	"github.com/xgo-dev/llvm"
)

// wasiReflectCallBridge adapts an array of addresses to the exact LLVM
// signature. In particular, an env-bearing closure (or interface method) has
// a different WebAssembly type from a plain function, even with a nil receiver.
// Let normal LLVM lowering handle aggregates, zero-sized arguments and sret.
func (p Package) wasiReflectCallBridge(sig *types.Signature, typeName string) Function {
	name := typeName + "$reflect.call"
	if fn := p.FuncOf(name); fn != nil {
		return fn
	}
	ptr := types.Typ[types.UnsafePointer]
	ptrs := types.NewPointer(ptr)
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "fn", ptr),
		types.NewParam(token.NoPos, nil, "env", ptr),
		types.NewParam(token.NoPos, nil, "prefix", types.Typ[types.Bool]),
		types.NewParam(token.NoPos, nil, "args", ptrs),
		types.NewParam(token.NoPos, nil, "results", ptrs),
	)
	fn := p.NewFunc(name, types.NewSignatureType(nil, nil, nil, params, nil, false), InC)
	fn.impl.SetLinkage(llvm.LinkOnceODRLinkage)
	comdat := p.mod.Comdat(name)
	comdat.SetSelectionKind(llvm.AnyComdatSelectionKind)
	fn.impl.SetComdat(comdat)
	b := fn.MakeBody(3)
	defer b.Dispose()
	prog := p.Prog
	rawSig := prog.gocvt.cvtFunc(sig, nil)
	args := make([]Expr, rawSig.Params().Len())
	for i := range args {
		typ := prog.Type(rawSig.Params().At(i).Type(), InC)
		args[i] = b.Load(b.Convert(prog.Pointer(typ), b.wasiReflectSlot(fn.Param(3), i)))
	}
	b.If(fn.Param(2), fn.Block(1), fn.Block(2))
	for _, prefix := range []bool{true, false} {
		entrySig, callArgs, block := rawSig, args, 2
		if prefix {
			entrySig = FuncAddCtx(types.NewParam(token.NoPos, nil, "env", ptr), rawSig)
			callArgs = append([]Expr{fn.Param(1)}, args...)
			block = 1
		}
		b.SetBlock(fn.Block(block))
		entry := Expr{fn.Param(0).impl, prog.FuncDecl(entrySig, InC)}
		ret := b.Call(entry, callArgs...)
		for i := 0; i < rawSig.Results().Len(); i++ {
			value := ret
			if rawSig.Results().Len() > 1 {
				value = b.Extract(ret, i)
			}
			b.Store(b.Convert(prog.Pointer(value.Type), b.wasiReflectSlot(fn.Param(4), i)), value)
		}
		b.Return()
	}
	b.EndBuild()
	return fn
}

func (b Builder) wasiReflectSlot(base Expr, i int) Expr {
	prog := b.Prog
	slot := llvm.CreateInBoundsGEP(b.impl, prog.tyVoidPtr(), base.impl, []llvm.Value{
		llvm.ConstInt(prog.Int().ll, uint64(i), false),
	})
	return b.Load(Expr{slot, prog.Pointer(prog.VoidPtr())})
}

// wasiReflectMakeBridge is a normal env-bearing Go entry. Its environment
// owns the user callback, so no executable allocation or finite trampoline
// pool is required. Pointer arguments are rooted before entering reflection.
func (p Package) wasiReflectMakeBridge(sig *types.Signature, typeName string) Function {
	name := typeName + "$reflect.make"
	if fn := p.FuncOf(name); fn != nil {
		return fn
	}
	ptr := types.Typ[types.UnsafePointer]
	fn := p.NewEnvFunc(name, sig, InGo, types.NewParam(token.NoPos, nil, "env", ptr), false)
	fn.impl.SetLinkage(llvm.LinkOnceODRLinkage)
	comdat := p.mod.Comdat(name)
	comdat.SetSelectionKind(llvm.AnyComdatSelectionKind)
	fn.impl.SetComdat(comdat)
	b := fn.MakeBody(1)
	defer b.Dispose()
	prog := p.Prog
	rawSig := fn.raw.Type.(*types.Signature)
	rootValues := []Expr{fn.Env()}
	for i := 0; i < rawSig.Params().Len(); i++ {
		rootValues = append(rootValues, b.GCRootPointers(fn.Param(i))...)
	}
	if prog.GCRootsEnabled() {
		roots := fn.NewGCRoots(len(rootValues))
		for i, value := range rootValues {
			b.SetGCRoot(roots[i], value)
		}
	}
	args, _ := b.wasiReflectFrame(rawSig.Params(), func(i int) Expr { return fn.Param(i) })
	results, slots := b.wasiReflectFrame(rawSig.Results(), nil)
	callbackSig := types.NewSignatureType(nil, nil, nil, types.NewTuple(
		types.NewParam(token.NoPos, nil, "env", ptr),
		types.NewParam(token.NoPos, nil, "args", types.NewPointer(ptr)),
		types.NewParam(token.NoPos, nil, "results", types.NewPointer(ptr)),
	), nil, false)
	callback := b.Load(b.Convert(prog.Pointer(prog.VoidPtr()), fn.Env()))
	b.Call(Expr{callback.impl, prog.FuncDecl(callbackSig, InC)}, fn.Env(), args, results)
	ret := make([]Expr, len(slots))
	for i, slot := range slots {
		ret[i] = b.Load(slot)
	}
	b.Return(ret...)
	b.EndBuild()
	return fn
}

func (b Builder) wasiReflectFrame(tuple *types.Tuple, value func(int) Expr) (Expr, []Expr) {
	prog := b.Prog
	if tuple.Len() == 0 {
		return prog.Nil(prog.Pointer(prog.VoidPtr())), nil
	}
	frame := Expr{
		llvm.CreateArrayAlloca(b.impl, prog.tyVoidPtr(), prog.IntVal(uint64(tuple.Len()), prog.Int()).impl),
		prog.Pointer(prog.VoidPtr()),
	}
	slots := make([]Expr, tuple.Len())
	for i := range slots {
		typ := prog.Type(tuple.At(i).Type(), InC)
		slots[i] = b.AllocaT(typ)
		initial := prog.Nil(typ)
		if value != nil {
			initial = value(i)
		}
		b.Store(slots[i], initial)
		address := llvm.CreateInBoundsGEP(b.impl, prog.tyVoidPtr(), frame.impl, []llvm.Value{
			llvm.ConstInt(prog.Int().ll, uint64(i), false),
		})
		b.Store(Expr{address, prog.Pointer(prog.VoidPtr())}, b.Convert(prog.VoidPtr(), slots[i]))
	}
	return frame, slots
}
