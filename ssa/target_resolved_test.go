//go:build !llgo

package ssa

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestResolvedTargetConfig(t *testing.T) {
	native := &Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	thumb := &Target{
		GOOS:   "linux",
		GOARCH: "arm",
		Target: "rp2040",
		Resolved: &TargetSpec{
			Triple:   "thumbv6m-unknown-unknown-eabi",
			CPU:      "cortex-m0plus",
			Features: "+armv6-m,+soft-float,+strict-align,+thumb-mode",
		},
	}
	riscv32 := &Target{
		GOOS:   "linux",
		GOARCH: "arm",
		Target: "riscv32",
		Resolved: &TargetSpec{
			Triple:    "riscv32-unknown-none",
			CPU:       "generic-rv32",
			Features:  "+m,+a,+c",
			TargetABI: "ilp32",
		},
	}
	tests := []struct {
		name          string
		target        *Target
		wantRequested TargetSpec
		wantEffective TargetSpec
		wantPtrSize   int
		wantLayout    string
	}{
		{
			name:          "native",
			target:        native,
			wantRequested: native.Spec(),
			wantEffective: native.Spec(),
			wantPtrSize:   strconv.IntSize / 8,
		},
		{
			name:   "wasm32",
			target: &Target{GOOS: "wasip1", GOARCH: "wasm"},
			wantRequested: TargetSpec{
				Triple:   "wasm32-unknown-wasip1",
				CPU:      "generic",
				Features: "+bulk-memory,+mutable-globals,+nontrapping-fptoint,+sign-ext",
			},
			wantEffective: TargetSpec{
				Triple:   "wasm32-unknown-wasip1",
				CPU:      "generic",
				Features: "+bulk-memory,+mutable-globals,+nontrapping-fptoint,+sign-ext",
			},
			wantPtrSize: 4,
			wantLayout:  "e-m:e-p:32:32-p10:8:8-p20:8:8-i64:64-n32:64-S128-ni:1:10:20",
		},
		{
			name:          "thumb",
			target:        thumb,
			wantRequested: *thumb.Resolved,
			wantEffective: *thumb.Resolved,
			wantPtrSize:   4,
			wantLayout:    "e-m:e-p:32:32-Fi8-i64:64-v128:64:128-a:0:32-n32-S64",
		},
		{
			name:          "riscv32",
			target:        riscv32,
			wantRequested: *riscv32.Resolved,
			wantEffective: *riscv32.Resolved,
			wantPtrSize:   4,
			wantLayout:    "e-m:e-p:32:32-i64:64-n32-S128",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog := NewProgram(tt.target)
			defer prog.Dispose()

			if got := prog.RequestedTargetSpec(); !reflect.DeepEqual(got, tt.wantRequested) {
				t.Fatalf("RequestedTargetSpec() = %#v, want %#v", got, tt.wantRequested)
			}
			if got := prog.TargetSpec(); !reflect.DeepEqual(got, tt.wantEffective) {
				t.Fatalf("TargetSpec() = %#v, want %#v", got, tt.wantEffective)
			}
			if got := prog.TargetMachine().Triple(); got != tt.wantEffective.Triple {
				t.Fatalf("TargetMachine().Triple() = %q, want %q", got, tt.wantEffective.Triple)
			}
			if got := prog.PointerSize(); got != tt.wantPtrSize {
				t.Fatalf("PointerSize() = %d, want %d", got, tt.wantPtrSize)
			}
			if got := prog.DataLayout(); got == "" {
				t.Fatal("DataLayout() is empty")
			} else if tt.wantLayout != "" && got != tt.wantLayout {
				t.Fatalf("DataLayout() = %q, want %q", got, tt.wantLayout)
			}

			pkg := prog.NewPackage("targettest", "target/test")
			if got := pkg.Module().Target(); got != tt.wantEffective.Triple {
				t.Fatalf("module target = %q, want %q", got, tt.wantEffective.Triple)
			}
			if got := pkg.Module().DataLayout(); got != prog.DataLayout() {
				t.Fatalf("module data layout = %q, want %q", got, prog.DataLayout())
			}
			pbo := llvm.NewPassBuilderOptions()
			defer pbo.Dispose()
			if err := pkg.Module().RunPasses("default<O0>", prog.TargetMachine(), pbo); err != nil {
				t.Fatalf("RunPasses() failed: %v", err)
			}
			obj, err := prog.TargetMachine().EmitToMemoryBuffer(pkg.Module(), llvm.ObjectFile)
			if err != nil {
				t.Fatalf("EmitToMemoryBuffer() failed: %v", err)
			}
			defer obj.Dispose()
			if len(obj.Bytes()) == 0 {
				t.Fatal("object code is empty")
			}
		})
	}
}

func TestCoroWasmTargetMachineEmitsMultipleDefinedFunctions(t *testing.T) {
	prog := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	pkg := prog.NewPackage("wasmsections", "target/wasmsections")
	mod := pkg.Module()
	defer mod.Dispose()

	ctx := mod.Context()
	functionType := llvm.FunctionType(ctx.VoidType(), nil, false)
	for _, name := range []string{"first", "second"} {
		function := llvm.AddFunction(mod, name, functionType)
		entry := llvm.AddBasicBlock(function, "entry")
		builder := ctx.NewBuilder()
		builder.SetInsertPointAtEnd(entry)
		builder.CreateRetVoid()
		builder.Dispose()
	}
	if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify wasm multi-function module: %v\n%s", err, mod.String())
	}
	object, err := prog.TargetMachine().EmitToMemoryBuffer(mod, llvm.ObjectFile)
	if err != nil {
		t.Fatalf("emit wasm multi-function object: %v\n%s", err, mod.String())
	}
	defer object.Dispose()
	if len(object.Bytes()) == 0 {
		t.Fatal("wasm multi-function object is empty")
	}
}

func TestResolvedTargetConfigIsAuthoritativeAndFrozen(t *testing.T) {
	resolved := &TargetSpec{
		Triple: "avr",
		CPU:    "atmega328p",
		// An empty feature set is intentional and must not inherit ARM defaults
		// merely because the frontend uses GOARCH=arm for this target.
	}
	target := &Target{GOOS: "linux", GOARCH: "arm", Target: "arduino", Resolved: resolved}
	if got := target.Spec(); !reflect.DeepEqual(got, *resolved) {
		t.Fatalf("Spec() = %#v, want authoritative %#v", got, *resolved)
	}

	prog := NewProgram(target)
	defer prog.Dispose()
	wantRequested := prog.RequestedTargetSpec()
	want := target.defaultSpec()
	if got := prog.TargetSpec(); !reflect.DeepEqual(got, want) {
		t.Fatalf("incompatible AVR target effective spec = %#v, want frontend surrogate %#v", got, want)
	}
	if got := prog.PointerSize(); got != 4 {
		t.Fatalf("incompatible AVR target pointer size = %d, want frontend arm size 4", got)
	}
	resolved.Triple = "thumbv6m-unknown-unknown-eabi"
	resolved.CPU = "cortex-m0"
	if got := prog.TargetSpec(); !reflect.DeepEqual(got, want) {
		t.Fatalf("program target changed after input mutation: got %#v, want %#v", got, want)
	}
	if got := prog.RequestedTargetSpec(); !reflect.DeepEqual(got, wantRequested) {
		t.Fatalf("requested target changed after input mutation: got %#v, want %#v", got, wantRequested)
	}
	if got := prog.NewPackage("frozen", "target/frozen").Module().Target(); got != want.Triple {
		t.Fatalf("module target = %q after input mutation, want frozen %q", got, want.Triple)
	}
}

func TestTargetDataLegacyLayoutCompatibility(t *testing.T) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	legacy := llvm.NewTargetData("e-p:32:32-i64:64-f64:64-n32-S64")
	defer legacy.Dispose()
	tests := []struct {
		name       string
		layout     string
		wantReason string
	}{
		{
			name:   "same-go-visible-layout",
			layout: "e-m:e-p:32:32-i64:64-f64:64-v128:64:128-Fi8-n8:16:32:64-S128",
		},
		{
			name:       "bool-alignment-mismatch",
			layout:     "e-p:32:32-i1:16-i64:64-f64:64-n32-S64",
			wantReason: "i1 ABI",
		},
		{
			name:       "pointer-width-mismatch",
			layout:     "e-p:16:16-i64:64-f64:64-n8:16-S16",
			wantReason: "pointer ABI size",
		},
		{
			name:       "pointer-alignment-mismatch",
			layout:     "e-p:32:16-i64:64-f64:64-n32-S32",
			wantReason: "pointer ABI alignment",
		},
		{
			name:       "same-width-i64-alignment-mismatch",
			layout:     "e-p:32:32-i64:32-f64:64-n32-S64",
			wantReason: "i64 ABI alignment",
		},
		{
			name:       "byte-order-mismatch",
			layout:     "E-p:32:32-i64:64-f64:64-n32-S64",
			wantReason: "byte order",
		},
		{
			name:       "float64-alignment-mismatch",
			layout:     "e-p:32:32-i64:64-f64:32-n32-S64",
			wantReason: "f64 ABI alignment",
		},
		{
			name:       "aggregate-padding-mismatch",
			layout:     "e-p:32:32-i64:64-f64:64-a:32:32-n32-S64",
			wantReason: "byte struct",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requested := llvm.NewTargetData(tt.layout)
			defer requested.Dispose()
			err := targetDataLayoutCompatibilityError(ctx, requested, legacy)
			if tt.wantReason == "" && err != nil {
				t.Fatalf("compatible layout %q rejected: %v", tt.layout, err)
			}
			if tt.wantReason != "" && (err == nil || !strings.Contains(err.Error(), tt.wantReason)) {
				t.Fatalf("layout %q compatibility error = %v, want reason containing %q", tt.layout, err, tt.wantReason)
			}
		})
	}
}

func TestResolvedPointerWidthMismatchFallsBack(t *testing.T) {
	tests := []struct {
		name     string
		resolved TargetSpec
	}{
		{
			name:     "avr16-with-arm-frontend",
			resolved: TargetSpec{Triple: "avr", CPU: "atmega328p"},
		},
		{
			name:     "riscv64-with-arm-frontend",
			resolved: TargetSpec{Triple: "riscv64-unknown-none", CPU: "generic-rv64", TargetABI: "lp64"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &Target{GOOS: "linux", GOARCH: "arm", Target: tt.name, Resolved: &tt.resolved}
			prog := NewProgram(target)
			defer prog.Dispose()
			if got := prog.RequestedTargetSpec(); !reflect.DeepEqual(got, tt.resolved) {
				t.Fatalf("requested spec = %#v, want %#v", got, tt.resolved)
			}
			if got, want := prog.TargetSpec(), target.defaultSpec(); !reflect.DeepEqual(got, want) {
				t.Fatalf("effective spec = %#v, want frontend surrogate %#v", got, want)
			}
		})
	}
}

func TestNewProgramDefaultTargetCompatibility(t *testing.T) {
	want := (&Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}).Spec()
	prog := NewProgram(nil)
	defer prog.Dispose()
	if got := prog.TargetSpec(); !reflect.DeepEqual(got, want) {
		t.Fatalf("NewProgram(nil) target = %#v, want legacy default %#v", got, want)
	}
}

func TestResolvedExternalBackendCompatibilityFallback(t *testing.T) {
	if _, err := llvm.GetTargetFromTriple("xtensa"); err == nil {
		t.Skip("in-process LLVM includes Xtensa; no compatibility fallback is needed")
	}
	target := &Target{
		GOOS:   "linux",
		GOARCH: "arm",
		Target: "esp32",
		Resolved: &TargetSpec{
			Triple:   "xtensa",
			CPU:      "esp32",
			Features: "+density,+windowed",
		},
	}
	prog := NewProgram(target)
	defer prog.Dispose()
	if got := prog.RequestedTargetSpec(); !reflect.DeepEqual(got, *target.Resolved) {
		t.Fatalf("requested external target = %#v, want %#v", got, *target.Resolved)
	}
	if got, want := prog.TargetSpec(), target.defaultSpec(); !reflect.DeepEqual(got, want) {
		t.Fatalf("external backend fallback = %#v, want legacy %#v", got, want)
	}
}

func TestResolvedTargetABINameControlsRISCVObject(t *testing.T) {
	Initialize(InitAll)
	const (
		triple              = "riscv64-unknown-elf"
		riscvFloatABIMask   = uint32(0x6)
		riscvFloatABIDouble = uint32(0x4)
	)
	if _, err := llvm.GetTargetFromTriple(triple); err != nil {
		t.Skipf("RISC-V backend is unavailable: %v", err)
	}

	tests := []struct {
		name      string
		abi       string
		wantFlags uint32
	}{
		{name: "explicit-lp64", abi: "lp64", wantFlags: 0},
		{name: "backend-default-lp64d", wantFlags: riscvFloatABIDouble},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &Target{
				GOOS:   "linux",
				GOARCH: "riscv64",
				Target: "synthetic-riscv64-abi",
				Resolved: &TargetSpec{
					Triple:    triple,
					CPU:       "generic-rv64",
					Features:  "+m,+a,+f,+d,+c",
					TargetABI: tt.abi,
				},
			}
			prog := NewProgram(target)
			defer prog.Dispose()
			if got := prog.RequestedTargetSpec(); !reflect.DeepEqual(got, *target.Resolved) {
				t.Fatalf("requested target = %#v, want %#v", got, *target.Resolved)
			}
			if got := prog.TargetSpec(); !reflect.DeepEqual(got, *target.Resolved) {
				t.Fatalf("effective target = %#v, want requested %#v", got, *target.Resolved)
			}

			pkg := prog.NewPackage("targetabi", "target/abi")
			mod := pkg.Module()
			defer mod.Dispose()
			ctx := mod.Context()
			calleeType := llvm.FunctionType(ctx.VoidType(), []llvm.Type{ctx.DoubleType()}, false)
			callee := llvm.AddFunction(mod, "callee", calleeType)
			caller := llvm.AddFunction(mod, "caller", llvm.FunctionType(ctx.VoidType(), nil, false))
			entry := llvm.AddBasicBlock(caller, "entry")
			builder := ctx.NewBuilder()
			defer builder.Dispose()
			builder.SetInsertPointAtEnd(entry)
			builder.CreateCall(calleeType, callee, []llvm.Value{llvm.ConstFloat(ctx.DoubleType(), 1.25)}, "")
			builder.CreateRetVoid()
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}

			object, err := prog.TargetMachine().EmitToMemoryBuffer(mod, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("EmitToMemoryBuffer() failed: %v", err)
			}
			defer object.Dispose()
			flags := riscvELFFlags(t, object.Bytes())
			if got := flags & riscvFloatABIMask; got != tt.wantFlags {
				t.Fatalf("RISC-V ELF float ABI flags = %#x, want %#x (all flags %#x)", got, tt.wantFlags, flags)
			}
		})
	}
}

func riscvELFFlags(t *testing.T, object []byte) uint32 {
	t.Helper()
	file, err := elf.NewFile(bytes.NewReader(object))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if file.Machine != elf.EM_RISCV {
		t.Fatalf("ELF machine = %v, want %v", file.Machine, elf.EM_RISCV)
	}

	reader := bytes.NewReader(object)
	switch file.Class {
	case elf.ELFCLASS32:
		var header elf.Header32
		if err := binary.Read(reader, file.ByteOrder, &header); err != nil {
			t.Fatal(err)
		}
		return header.Flags
	case elf.ELFCLASS64:
		var header elf.Header64
		if err := binary.Read(reader, file.ByteOrder, &header); err != nil {
			t.Fatal(err)
		}
		return header.Flags
	default:
		t.Fatalf("unsupported ELF class %v", file.Class)
		return 0
	}
}
