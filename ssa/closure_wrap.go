package ssa

import (
	"go/token"
	"go/types"

	"github.com/xgo-dev/llvm"
)

// removeCtx drops the leading __llgo_ctx parameter, if present.
func removeCtx(sig *types.Signature) *types.Signature {
	if closureCtxParam(sig) == nil {
		return sig
	}
	params := sig.Params()
	n := params.Len()
	args := make([]*types.Var, n-1)
	for i := 0; i < n-1; i++ {
		args[i] = params.At(i + 1)
	}
	return types.NewSignature(sig.Recv(), types.NewTuple(args...), sig.Results(), sig.Variadic())
}

func isClosureCtxParam(param *types.Var) bool {
	if param == nil || param.Name() != closureCtx {
		return false
	}
	switch typ := param.Type().Underlying().(type) {
	case *types.Pointer:
		return true
	case *types.Basic:
		return typ.Kind() == types.UnsafePointer
	default:
		return false
	}
}

// closureCtxParam returns the leading __llgo_ctx parameter if present. Source
// closure signatures keep the context first even when a later physical ABI
// prefixes coroutine-owned parameters.
func closureCtxParam(sig *types.Signature) *types.Var {
	if sig == nil || sig.Params().Len() == 0 {
		return nil
	}
	first := sig.Params().At(0)
	if !isClosureCtxParam(first) {
		return nil
	}
	return first
}

// closureCtxPhysicalParamIndex returns the context's actual LLVM parameter
// position. Coroutine entries can prefix __llgo_g and __llgo_out, so ABI
// attribute placement must not assume that the closure context is parameter 0.
func closureCtxPhysicalParamIndex(sig *types.Signature) int {
	if sig == nil {
		return -1
	}
	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		if isClosureCtxParam(params.At(i)) {
			return i
		}
	}
	return -1
}

// closureWrapArgs returns wrapper arguments excluding the ctx parameter.
func closureWrapArgs(fn Function) []Expr {
	n := len(fn.params)
	if n <= 1 {
		return nil
	}
	args := make([]Expr, n-1)
	for i := 1; i < n; i++ {
		args[i-1] = fn.Param(i)
	}
	return args
}

// closureWrapReturn returns from wrapper, preserving tail-call eligibility.
func closureWrapReturn(b Builder, sig *types.Signature, ret Expr) {
	n := sig.Results().Len()
	if n == 0 {
		if !ret.impl.IsNil() {
			ret.impl.SetTailCall(true)
		}
		b.impl.CreateRetVoid()
		return
	}
	ret.impl.SetTailCall(true)
	b.impl.CreateRet(ret.impl)
}

// closureWrapDecl is the explicit-context fallback for targets without a
// hidden closure-context register. It directly calls the target symbol and
// ignores the ctx parameter.
func (p Package) closureWrapDecl(fn Expr, sig *types.Signature) Function {
	name := closureStub + fn.impl.Name()
	if wrap := p.FuncOf(name); wrap != nil {
		return wrap
	}
	ctx := types.NewParam(token.NoPos, nil, closureCtx, types.Typ[types.UnsafePointer])
	sigCtx := FuncAddCtx(ctx, sig)
	wrap := p.NewFunc(name, sigCtx, InC)
	wrap.impl.SetLinkage(llvm.LinkOnceAnyLinkage)
	b := wrap.MakeBody(1)
	args := closureWrapArgs(wrap)
	ret := b.Call(fn, args...)
	closureWrapReturn(b, sig, ret)
	return wrap
}

// closureWrapPtr is the explicit-context fallback for raw function pointers.
// The ctx parameter is treated as a pointer to a stored function pointer cell.
func (p Package) closureWrapPtr(sig *types.Signature) Function {
	name := closureStub + p.Prog.abi.FuncName(sig)
	if wrap := p.FuncOf(name); wrap != nil {
		return wrap
	}
	ctx := types.NewParam(token.NoPos, nil, closureCtx, types.Typ[types.UnsafePointer])
	sigCtx := FuncAddCtx(ctx, sig)
	wrap := p.NewFunc(name, sigCtx, InC)
	wrap.impl.SetLinkage(llvm.LinkOnceAnyLinkage)
	b := wrap.MakeBody(1)
	ctxArg := wrap.Param(0)
	fnType := p.Prog.rawType(sig)
	fnPtrType := p.Prog.Pointer(fnType)
	// ctxArg is expected to be a non-nil pointer to a stored function pointer cell.
	// We intentionally avoid runtime null checks here; invalid ctx is a compiler/user error.
	fnPtr := b.Convert(fnPtrType, ctxArg)
	fnVal := b.Load(fnPtr)
	args := closureWrapArgs(wrap)
	ret := b.Call(fnVal, args...)
	closureWrapReturn(b, sig, ret)
	return wrap
}
