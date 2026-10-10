package ssa

import (
	"go/types"

	"github.com/xgo-dev/llvm"
)

var (
	nativeSignExtKind = llvm.AttributeKindID("signext")
	nativeZeroExtKind = llvm.AttributeKindID("zeroext")
)

var nativeIntegerAttributeKinds = [...]uint{nativeSignExtKind, nativeZeroExtKind}

// nativeIntegerAttrs records signedness while the Go signature is still
// available. LLVM's integer types alone cannot distinguish signed from unsigned
// C values. Apple's arm64 ABI and the WebAssembly C ABI require narrow arguments
// and results to be extended to 32 bits. Other arm64 ABIs leave argument
// extension to the callee.
func (p Program) nativeIntegerAttrs(sig *types.Signature, ft llvm.Type, add func(int, llvm.Attribute)) {
	if !p.nativeIntegerExtensionRequired() {
		return
	}
	// This runs during SSA codegen, before cabi.TransformModule can reorder or
	// expand parameters. toLLVMFuncBackground preserves signature order and
	// only omits the trailing __llgo_va_list from the fixed LLVM prototype.
	physicalParams := ft.ParamTypes()
	fixedCount := sig.Params().Len()
	if HasNameValist(sig) {
		fixedCount--
	}
	if len(physicalParams) != fixedCount {
		panic("ssa: native integer attributes require the unlowered function signature")
	}
	addInteger := func(index int, raw types.Type, physical llvm.Type) {
		basic, ok := raw.Underlying().(*types.Basic)
		if !ok || physical.TypeKind() != llvm.IntegerTypeKind || physical.IntTypeWidth() >= 32 {
			return
		}
		kind := nativeSignExtKind
		if basic.Info()&(types.IsUnsigned|types.IsBoolean) != 0 {
			kind = nativeZeroExtKind
		}
		add(index, p.ctx.CreateEnumAttribute(kind, 0))
	}
	if results := sig.Results(); results.Len() == 1 {
		addInteger(0, results.At(0).Type(), ft.ReturnType())
	}
	// The LLVM prototype omits __llgo_va_list. Only fixed parameters carry
	// extension attributes; the ellipsis arguments have already been promoted.
	for i, physical := range physicalParams {
		addInteger(i+1, sig.Params().At(i).Type(), physical)
	}
}

func (p Program) nativeIntegerExtensionRequired() bool {
	target := p.Target()
	return target.effectiveGOARCH() == "wasm" ||
		target.effectiveGOOS() == "darwin" && target.effectiveGOARCH() == "arm64"
}

func (b Builder) setNativeIntegerCallAttrs(call llvm.Value, fn Expr, sig *types.Signature) {
	if !b.Prog.nativeIntegerExtensionRequired() {
		return
	}
	if fn.kind == vkFuncPtr {
		b.Prog.nativeIntegerAttrs(sig, call.CalledFunctionType(), call.AddCallSiteAttribute)
		return
	}
	// Direct C declarations already carry their ABI attributes. Copy them onto
	// the call too: LLVM requires matching attributes at both ends of the boundary.
	if direct := fn.impl.IsAFunction(); !direct.IsNil() {
		copyInteger := func(i int, physical llvm.Type) {
			if physical.TypeKind() != llvm.IntegerTypeKind || physical.IntTypeWidth() >= 32 {
				return
			}
			for _, kind := range nativeIntegerAttributeKinds {
				if attr := direct.GetEnumAttributeAtIndex(i, kind); !attr.IsNil() {
					call.AddCallSiteAttribute(i, attr)
				}
			}
		}
		ft := call.CalledFunctionType()
		copyInteger(0, ft.ReturnType())
		for i, physical := range ft.ParamTypes() {
			copyInteger(i+1, physical)
		}
	}
}
