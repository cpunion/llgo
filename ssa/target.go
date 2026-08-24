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
	"fmt"
	"runtime"
	"strings"

	archcfg "github.com/xgo-dev/llgo/internal/goarch"
	"github.com/xgo-dev/llgo/internal/optlevel"
	intllvm "github.com/xgo-dev/llgo/internal/xtool/llvm"
	"github.com/xgo-dev/llvm"
)

// -----------------------------------------------------------------------------

type Target struct {
	GOOS                    string
	GOARCH                  string
	GO386                   string // "sse2" (default) or "softfloat"
	GOAMD64                 string // "v1" (default), "v2", "v3", or "v4"
	GOARM                   string // "5", "6", "7" (default), with optional float mode
	GOARM64                 string // "v8.0" (default) through "v9.5", with optional extensions
	Target                  string // target name from -target flag (e.g., "esp32", "arm7tdmi", "wasi")
	LLVMTarget              string // physical LLVM target selected by a target configuration
	OptLevel                optlevel.Level
	SaturatingFloatToUint32 bool

	// Resolved is the requested LLVM configuration produced by target
	// resolution. When it is nil, Spec derives the legacy defaults from
	// GOOS/GOARCH/GOARM. A non-nil value with a Triple keeps CPU, Features, and
	// TargetABI authoritative even when any is intentionally empty. NewProgram
	// records this requested value separately from the effective in-process target.
	Resolved *TargetSpec
}

func (p *Target) effectiveGOOS() string {
	if p.GOOS == "" {
		return runtime.GOOS
	}
	return p.GOOS
}

func (p *Target) effectiveGOARCH() string {
	if p.GOARCH == "" {
		return runtime.GOARCH
	}
	return p.GOARCH
}

func (p *Target) targetInfo(ctx llvm.Context, spec TargetSpec) (TargetSpec, llvm.TargetData, llvm.TargetMachine) {
	if spec.Triple == "" {
		spec.Triple = llvm.DefaultTargetTriple()
	}
	td, machine, err := p.createTargetInfo(spec)
	if err != nil && p.Resolved != nil && usesExternalLLVMBackend(spec.Triple) {
		// The in-process LLVM linked by llgo does not currently include every
		// backend shipped by a target's external clang toolchain. Preserve the
		// legacy frontend layout for those known targets until that backend is
		// available in the Go binding; supported targets must never silently
		// discard their resolved configuration.
		spec = p.defaultSpec()
		td, machine, err = p.createTargetInfo(spec)
	}
	if err != nil {
		panic(err)
	}
	if p.Resolved != nil {
		legacySpec := p.defaultSpec()
		if !sameTargetMachineLayoutInputs(spec, legacySpec) {
			legacyTD, legacyMachine, legacyErr := p.createTargetInfo(legacySpec)
			if legacyErr != nil {
				td.Dispose()
				machine.Dispose()
				panic(legacyErr)
			}
			if targetDataLayoutCompatibilityError(ctx, td, legacyTD) != nil {
				// A target may use another GOARCH as its Go frontend surrogate. Only
				// adopt its requested TargetMachine when the Go-visible LLVM object
				// layout is identical to the legacy surrogate layout; this preserves
				// existing behavior without claiming to fix historical go/types vs
				// LLVM layout differences in the surrogate itself.
				td.Dispose()
				machine.Dispose()
				spec, td, machine = legacySpec, legacyTD, legacyMachine
			} else {
				legacyTD.Dispose()
				legacyMachine.Dispose()
			}
		}
	}
	return spec, td, machine
}

func (p *Target) createTargetInfo(spec TargetSpec) (llvm.TargetData, llvm.TargetMachine, error) {
	t, err := llvm.GetTargetFromTriple(spec.Triple)
	if err != nil {
		return llvm.TargetData{}, llvm.TargetMachine{}, err
	}
	opts := p.targetMachineOptions()
	opts.ABIName = spec.TargetABI
	machine := t.CreateTargetMachineWithOptions(
		spec.Triple,
		spec.CPU,
		spec.Features,
		p.codeGenOptLevel(),
		p.targetRelocMode(),
		llvm.CodeModelDefault,
		opts,
	)
	return machine.CreateTargetData(), machine, nil
}

func sameTargetMachineLayoutInputs(a, b TargetSpec) bool {
	return a.Triple == b.Triple && a.CPU == b.CPU && a.Features == b.Features && a.TargetABI == b.TargetABI
}

// targetDataLayoutCompatibilityError compares the LLVM layout facts that can
// change Go object representation. Stack alignment, mangling, and the native
// integer token list do not affect that representation and are intentionally
// ignored. LLVM's C API does not expose pointer index width, so that remains a
// follow-up binding capability.
func targetDataLayoutCompatibilityError(ctx llvm.Context, requested, legacy llvm.TargetData) error {
	if requested.ByteOrder() != legacy.ByteOrder() {
		return fmt.Errorf("requested LLVM byte order differs from the legacy surrogate")
	}
	ptrType := llvm.PointerType(ctx.Int8Type(), 0)
	typesToCompare := []struct {
		name string
		typ  llvm.Type
	}{
		{"pointer", ptrType},
		{"i1", ctx.Int1Type()},
		{"i8", ctx.Int8Type()},
		{"i16", ctx.Int16Type()},
		{"i32", ctx.Int32Type()},
		{"i64", ctx.Int64Type()},
		{"f32", ctx.FloatType()},
		{"f64", ctx.DoubleType()},
	}
	for _, item := range typesToCompare {
		if err := compareTargetDataTypeLayout(requested, legacy, item.name, item.typ); err != nil {
			return err
		}
	}
	structType := ctx.StructType([]llvm.Type{
		ctx.Int8Type(), ctx.Int64Type(), ctx.DoubleType(), ptrType, ctx.Int16Type(), ctx.Int8Type(),
	}, false)
	for i := 0; i < structType.StructElementTypesCount(); i++ {
		requestedOffset, legacyOffset := requested.ElementOffset(structType, i), legacy.ElementOffset(structType, i)
		if requestedOffset != legacyOffset {
			return fmt.Errorf("requested representative struct field %d offset %d differs from legacy offset %d", i, requestedOffset, legacyOffset)
		}
	}
	if err := compareTargetDataTypeLayout(requested, legacy, "representative struct", structType); err != nil {
		return err
	}
	arrayType := llvm.ArrayType(structType, 3)
	if err := compareTargetDataTypeLayout(requested, legacy, "representative array", arrayType); err != nil {
		return err
	}
	byteStruct := ctx.StructType([]llvm.Type{ctx.Int8Type(), ctx.Int8Type()}, false)
	if err := compareTargetDataTypeLayout(requested, legacy, "byte struct", byteStruct); err != nil {
		return err
	}
	byteArray := llvm.ArrayType(ctx.Int8Type(), 3)
	if err := compareTargetDataTypeLayout(requested, legacy, "byte array", byteArray); err != nil {
		return err
	}
	complex64 := ctx.StructType([]llvm.Type{ctx.FloatType(), ctx.FloatType()}, false)
	if err := compareTargetDataTypeLayout(requested, legacy, "complex64", complex64); err != nil {
		return err
	}
	complex128 := ctx.StructType([]llvm.Type{ctx.DoubleType(), ctx.DoubleType()}, false)
	if err := compareTargetDataTypeLayout(requested, legacy, "complex128", complex128); err != nil {
		return err
	}
	return nil
}

func compareTargetDataTypeLayout(requested, legacy llvm.TargetData, name string, typ llvm.Type) error {
	requestedSize, legacySize := requested.TypeAllocSize(typ), legacy.TypeAllocSize(typ)
	if requestedSize != legacySize {
		return fmt.Errorf("requested %s ABI size %d differs from legacy size %d", name, requestedSize, legacySize)
	}
	requestedAlign, legacyAlign := requested.ABITypeAlignment(typ), legacy.ABITypeAlignment(typ)
	if requestedAlign != legacyAlign {
		return fmt.Errorf("requested %s ABI alignment %d differs from legacy alignment %d", name, requestedAlign, legacyAlign)
	}
	return nil
}

func usesExternalLLVMBackend(triple string) bool {
	arch, _, _ := strings.Cut(triple, "-")
	return arch == "xtensa"
}

func (p *Target) effectiveOptLevel() optlevel.Level {
	if p != nil && p.OptLevel.IsValid() {
		return p.OptLevel
	}
	if p != nil && p.Target != "" {
		return optlevel.TargetDefault
	}
	return optlevel.Default
}

func (p *Target) codeGenOptLevel() llvm.CodeGenOptLevel {
	switch p.effectiveOptLevel() {
	case optlevel.O0:
		return llvm.CodeGenLevelNone
	case optlevel.O1:
		return llvm.CodeGenLevelLess
	case optlevel.O3:
		return llvm.CodeGenLevelAggressive
	case optlevel.O2, optlevel.Os, optlevel.Oz:
		return llvm.CodeGenLevelDefault
	default:
		return llvm.CodeGenLevelNone
	}
}

func (p *Target) targetRelocMode() llvm.RelocMode {
	if p.useNativeObjectSections() {
		return llvm.RelocPIC
	}
	return llvm.RelocDefault
}

func (p *Target) targetMachineOptions() llvm.TargetMachineOptions {
	if !p.useNativeObjectSections() && !p.useWasmObjectSections() {
		return llvm.TargetMachineOptions{}
	}
	return llvm.TargetMachineOptions{
		FunctionSections:   true,
		DataSections:       true,
		UniqueSectionNames: true,
	}
}

// The WebAssembly backend requires each defined function to own a distinct
// object section. A single-function module can appear to work without these
// options, but coroutine splitting materializes ramp, resume, destroy, and
// cleanup functions in the same module and object emission then fails because
// they all try to define the shared .text section. Keep relocation selection
// independent: wasm needs section uniqueness, not the native PIC policy.
func (p *Target) useWasmObjectSections() bool {
	goarch := p.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goarch == "wasm"
}

func (p *Target) useNativeObjectSections() bool {
	goos := p.effectiveGOOS()
	goarch := p.effectiveGOARCH()
	return p.Target == "" && goos == runtime.GOOS && goarch == runtime.GOARCH && goarch != "wasm"
}

type TargetSpec struct {
	Triple   string
	CPU      string
	Features string

	// TargetABI is the LLVM target ABI identity (for example ilp32 or lp64),
	// not the Go/coroutine runtime ABI. It is passed to LLVM as ABIName while
	// constructing the TargetMachine; an empty value selects LLVM's default.
	TargetABI string
}

func (p *Target) goArchitectureSetting(value string) string {
	if p.Target != "" {
		return ""
	}
	return value
}

func (p *Target) Spec() TargetSpec {
	if p.Resolved != nil && p.Resolved.Triple != "" {
		return *p.Resolved
	}
	return p.defaultSpec()
}

func (p *Target) defaultSpec() (spec TargetSpec) {
	// Configure based on GOOS/GOARCH environment variables (falling back to
	// runtime.GOOS/runtime.GOARCH), and generate a LLVM target based on it.
	goarch := p.effectiveGOARCH()
	goos := p.effectiveGOOS()
	goarm := p.goArchitectureSetting(p.GOARM)
	spec.Triple = intllvm.GetTargetTripleWithGOARM(goos, goarch, goarm)
	// Build validates these settings before constructing Target. Spec also
	// accepts hand-built Targets, so it intentionally uses each resolver's
	// documented Go-default fallback when its error cannot be returned here.
	switch goarch {
	case "386":
		spec.CPU = "pentium4"
		go386, _ := archcfg.Resolve386(p.goArchitectureSetting(p.GO386))
		if go386 == "softfloat" {
			spec.Features = "+cx8,+fxsr,+mmx,+soft-float,-sse,-sse2,-x87"
		} else {
			spec.Features = "+cx8,+fxsr,+mmx,+sse,+sse2,+x87"
		}
	case "amd64":
		goamd64, _ := archcfg.ResolveAMD64(p.goArchitectureSetting(p.GOAMD64))
		spec.CPU = "x86-64"
		if goamd64 != "v1" {
			spec.CPU += "-" + goamd64
		}
		spec.Features = "+cx8,+fxsr,+mmx,+sse,+sse2,+x87"
	case "arm":
		spec.CPU = "generic"
		arm, _ := archcfg.ParseARM(goarm)
		switch arm.Version {
		case "5":
			if arm.SoftFloat {
				spec.Features = "+armv5t,+strict-align,-aes,-bf16,-d32,-dotprod,-fp-armv8,-fp-armv8d16,-fp-armv8d16sp,-fp-armv8sp,-fp16,-fp16fml,-fp64,-fpregs,-fullfp16,-mve.fp,-neon,-sha2,-thumb-mode,-vfp2,-vfp2sp,-vfp3,-vfp3d16,-vfp3d16sp,-vfp3sp,-vfp4,-vfp4d16,-vfp4d16sp,-vfp4sp"
			} else {
				// GOARM=5,hardfloat explicitly enables VFPv2 without also
				// carrying contradictory disable tokens for the same features.
				spec.Features = "+armv5t,+strict-align,-aes,-bf16,-d32,-dotprod,-fp-armv8,-fp-armv8d16,-fp-armv8d16sp,-fp-armv8sp,-fp16,-fp16fml,+fp64,+fpregs,-fullfp16,-mve.fp,-neon,-sha2,-thumb-mode,+vfp2,+vfp2sp,-vfp3,-vfp3d16,-vfp3d16sp,-vfp3sp,-vfp4,-vfp4d16,-vfp4d16sp,-vfp4sp"
			}
		case "6":
			spec.Features = "+armv6,+dsp,+fp64,+strict-align,+vfp2,+vfp2sp,-aes,-d32,-fp-armv8,-fp-armv8d16,-fp-armv8d16sp,-fp-armv8sp,-fp16,-fp16fml,-fullfp16,-neon,-sha2,-thumb-mode,-vfp3,-vfp3d16,-vfp3d16sp,-vfp3sp,-vfp4,-vfp4d16,-vfp4d16sp,-vfp4sp"
		case "7":
			spec.Features = "+armv7-a,+d32,+dsp,+fp64,+neon,+vfp2,+vfp2sp,+vfp3,+vfp3d16,+vfp3d16sp,+vfp3sp,-aes,-fp-armv8,-fp-armv8d16,-fp-armv8d16sp,-fp-armv8sp,-fp16,-fp16fml,-fullfp16,-sha2,-thumb-mode,-vfp4,-vfp4d16,-vfp4d16sp,-vfp4sp"
		}
		if arm.SoftFloat {
			spec.Features += ",+soft-float"
		}
	case "arm64":
		spec.CPU = "generic"
		arm64, _ := archcfg.ParseARM64(p.goArchitectureSetting(p.GOARM64))
		archFeature := arm64.Version + "a"
		if arm64.Version == "v9.0" {
			archFeature = "v9a"
		}
		features := make([]string, 0, 5)
		if arm64.Version != "v8.0" {
			features = append(features, "+"+archFeature)
		}
		features = append(features, "+neon")
		if arm64.LSE {
			features = append(features, "+lse")
		}
		if arm64.Crypto {
			features = append(features, "+crypto")
		}
		if goos != "darwin" { // windows, linux
			features = append(features, "-fmv")
		}
		spec.Features = strings.Join(features, ",")
	case "wasm":
		spec.CPU = "generic"
		spec.Features = "+bulk-memory,+mutable-globals,+nontrapping-fptoint,+sign-ext"
	}
	return
}

func StripModuleTarget(ir string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(ir, "\n") {
		trimmed := strings.TrimSuffix(line, "\n")
		if strings.HasPrefix(trimmed, "target datalayout = ") ||
			strings.HasPrefix(trimmed, "target triple = ") {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// -----------------------------------------------------------------------------
