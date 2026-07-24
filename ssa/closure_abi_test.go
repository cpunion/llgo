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
		name      string
		triple    string
		goos      string
		llvmMajor int
		want      closureContextABI
	}{
		{"amd64", "x86_64-unknown-linux-gnu", "linux", 19, closureContextNest},
		{"386", "i386-unknown-linux-gnu", "linux", 22, closureContextNest},
		{"arm", "thumbv7em-none-eabi", "linux", 19, closureContextNest},
		{"riscv32", "riscv32-unknown-none", "linux", 20, closureContextNest},
		{"riscv64", "riscv64-unknown-linux-gnu", "linux", 22, closureContextNest},
		{"arm64 linux llvm19", "aarch64-unknown-linux-gnu", "linux", 19, closureContextNest},
		{"arm64 darwin llvm19", "arm64-apple-darwin", "darwin", 19, closureContextSwiftSelf},
		{"arm64 windows llvm20", "aarch64-pc-windows-msvc", "windows", 20, closureContextSwiftSelf},
		{"arm64 android llvm20", "aarch64-linux-android", "android", 20, closureContextSwiftSelf},
		{"arm64 darwin llvm21", "arm64-apple-darwin", "darwin", 21, closureContextNest},
		{"arm64 windows llvm22", "aarch64-pc-windows-msvc", "windows", 22, closureContextNest},
		{"wasm", "wasm32-unknown-wasip1", "wasip1", 22, closureContextExplicit},
		{"xtensa", "xtensa-esp32-none-elf", "linux", 22, closureContextExplicit},
		{"avr", "avr-unknown-unknown", "linux", 22, closureContextExplicit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := closureContextABIForTarget(test.triple, test.goos, test.llvmMajor); got != test.want {
				t.Fatalf(
					"closureContextABIForTarget(%q, %q, %d) = %d, want %d",
					test.triple, test.goos, test.llvmMajor, got, test.want,
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

func TestClosureExplicitFallbackRetainsAdapter(t *testing.T) {
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
	if !strings.Contains(ir, "ptr @__llgo_stub.target") ||
		!strings.Contains(ir, "define linkonce") {
		t.Fatalf("explicit-context target lost its adapter:\n%s", ir)
	}
	if got, want := prog.SizeOf(prog.Closure(sig)), uint64(2*prog.PointerSize()); got != want {
		t.Fatalf("fallback funcval size = %d, want two pointers (%d)", got, want)
	}
}
