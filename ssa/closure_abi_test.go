//go:build !llgo

package ssa

import (
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func closureContextIRAttr(prog Program) string {
	switch prog.closureContextABI() {
	case closureContextNest:
		return " nest"
	case closureContextSwiftSelf:
		return " swiftself"
	default:
		return ""
	}
}

func TestClosureContextABIForTarget(t *testing.T) {
	tests := []struct {
		name   string
		triple string
		goos   string
		want   closureContextABI
	}{
		{"amd64", "x86_64-unknown-linux-gnu", "linux", closureContextNest},
		{"386", "i386-unknown-linux-gnu", "linux", closureContextNest},
		{"arm", "thumbv7em-none-eabi", "linux", closureContextSwiftSelf},
		{"riscv32", "riscv32-unknown-none", "linux", closureContextNest},
		{"riscv64", "riscv64-unknown-linux-gnu", "linux", closureContextNest},
		{"arm64 linux", "aarch64-unknown-linux-gnu", "linux", closureContextNest},
		{"arm64 darwin", "arm64-apple-darwin", "darwin", closureContextSwiftSelf},
		{"arm64 windows", "aarch64-pc-windows-msvc", "windows", closureContextSwiftSelf},
		{"arm64 android", "aarch64-linux-android", "android", closureContextSwiftSelf},
		{"wasm", "wasm32-unknown-wasip1", "wasip1", closureContextExplicit},
		{"xtensa", "xtensa-esp32-none-elf", "linux", closureContextExplicit},
		{"avr", "avr-unknown-unknown", "linux", closureContextExplicit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := closureContextABIForTarget(test.triple, test.goos); got != test.want {
				t.Fatalf(
					"closureContextABIForTarget(%q, %q) = %d, want %d",
					test.triple, test.goos, got, test.want,
				)
			}
		})
	}
}

func TestClosureContextAttributeUsesPhysicalParameterIndex(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(&Target{GOOS: "linux", GOARCH: "amd64"})
	defer prog.Dispose()
	pkg := prog.NewPackage("closureabi", "test/closureabi")

	voidPtr := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "__llgo_g", voidPtr),
		types.NewParam(token.NoPos, nil, "__llgo_out", voidPtr),
		types.NewParam(token.NoPos, nil, closureCtx, voidPtr),
		types.NewParam(token.NoPos, nil, "value", types.Typ[types.Int]),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "", types.Typ[types.Int]))
	sig := types.NewSignatureType(nil, nil, nil, params, results, false)
	target := pkg.NewFunc("target", sig, InC)

	nest := llvm.AttributeKindID("nest")
	if attr := target.impl.GetEnumAttributeAtIndex(3, nest); attr.IsNil() {
		t.Fatalf("physical context parameter has no nest attribute:\n%s", target.impl.String())
	}
	for _, index := range []int{1, 2, 4} {
		if attr := target.impl.GetEnumAttributeAtIndex(index, nest); !attr.IsNil() {
			t.Fatalf("non-context parameter %d has nest attribute", index)
		}
	}

	callerSig := types.NewSignatureType(nil, nil, nil, nil, results, false)
	caller := pkg.NewFunc("caller", callerSig, InC)
	b := caller.MakeBody(1)
	nilPtr := prog.Nil(prog.VoidPtr())
	ret := b.Call(target.Expr, nilPtr, nilPtr, nilPtr, prog.IntVal(7, prog.Int()))
	b.Return(ret)
	call := caller.impl.EntryBasicBlock().FirstInstruction()
	if call.InstructionOpcode() != llvm.Call {
		t.Fatalf("caller first instruction is %v, want call:\n%s", call.InstructionOpcode(), caller.impl.String())
	}
	if attr := call.GetCallSiteEnumAttribute(3, nest); attr.IsNil() {
		t.Fatalf("physical context call argument has no nest attribute:\n%s", caller.impl.String())
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("closure-context module is invalid: %v\n%s", err, pkg.String())
	}
}

func TestClosureContextAttributeRecognizesCoroutinePhysicalEnvironment(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(&Target{GOOS: "darwin", GOARCH: "arm64"})
	defer prog.Dispose()
	pkg := prog.NewPackage("corophysicalenv", "test/corophysicalenv")

	voidPtr := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "__llgo_g", voidPtr),
		types.NewParam(token.NoPos, nil, "__llgo_out", voidPtr),
		types.NewParam(token.NoPos, nil, "$env", voidPtr),
		types.NewParam(token.NoPos, nil, "value", types.Typ[types.Int]),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "", voidPtr))
	sig := types.NewSignatureType(nil, nil, nil, params, results, false)
	target := pkg.NewFunc("target$coro", sig, InGo)

	swiftself := llvm.AttributeKindID("swiftself")
	if attr := target.impl.GetEnumAttributeAtIndex(3, swiftself); attr.IsNil() {
		t.Fatalf("coroutine environment parameter has no swiftself attribute:\n%s", target.impl.String())
	}

	caller := pkg.NewFunc("caller", types.NewSignatureType(nil, nil, nil, nil, results, false), InGo)
	b := caller.MakeBody(1)
	nilPtr := prog.Nil(prog.VoidPtr())
	ret := b.Call(target.Expr, nilPtr, nilPtr, nilPtr, prog.IntVal(7, prog.Int()))
	b.Return(ret)
	call := caller.impl.EntryBasicBlock().FirstInstruction()
	if call.InstructionOpcode() != llvm.Call || call.GetCallSiteEnumAttribute(3, swiftself).IsNil() {
		t.Fatalf("coroutine environment call argument has no swiftself attribute:\n%s", caller.impl.String())
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("coroutine environment module is invalid: %v\n%s", err, pkg.String())
	}
}

func TestClosureExplicitFallbackUsesFixedFuncval(t *testing.T) {
	Initialize(InitAll)
	prog := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	pkg := prog.NewPackage("fallback", "test/fallback")

	params := types.NewTuple(types.NewParam(token.NoPos, nil, "x", types.Typ[types.Int]))
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "", types.Typ[types.Int]))
	sig := types.NewSignatureType(nil, nil, nil, params, results, false)
	target := pkg.NewFunc("target", sig, InGo)
	tb := target.MakeBody(1)
	tb.Return(target.Param(0))

	holderSig := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	holder := pkg.NewFunc("holder", holderSig, InGo)
	hb := holder.MakeBody(1)
	slot := hb.AllocaT(prog.Closure(sig))
	hb.Store(slot, target.Expr)
	hb.Return()

	ir := pkg.String()
	if !strings.Contains(ir, "ptr @target") || strings.Contains(ir, legacyClosureStubPrefix) {
		t.Fatalf("explicit-context target did not retain its direct code pointer:\n%s", ir)
	}
	if got, want := prog.SizeOf(prog.Closure(sig)), uint64(2*prog.PointerSize()); got != want {
		t.Fatalf("fallback funcval size = %d, want two pointers (%d)", got, want)
	}
}
