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

	"github.com/goplus/llgo/internal/optlevel"
	intllvm "github.com/goplus/llgo/internal/xtool/llvm"
	"github.com/xgo-dev/llvm"
)

// -----------------------------------------------------------------------------

type Target struct {
	GOOS     string
	GOARCH   string
	GOARM    string // "5", "6", "7" (default)
	Target   string // target name from -target flag (e.g., "esp32", "arm7tdmi", "wasi")
	OptLevel optlevel.Level

	// Resolved is the requested LLVM configuration produced by target
	// resolution. When it is nil, Spec derives the legacy defaults from
	// GOOS/GOARCH/GOARM. A non-nil value with a Triple keeps CPU, Features, and
	// TargetABI authoritative even when any is intentionally empty. NewProgram
	// records this requested value separately from the effective in-process target.
	Resolved *TargetSpec
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
		return optlevel.Oz
	}
	return optlevel.O2
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
	goos := p.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := p.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
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

func (p *Target) Spec() TargetSpec {
	if p.Resolved != nil && p.Resolved.Triple != "" {
		return *p.Resolved
	}
	return p.defaultSpec()
}

func (p *Target) defaultSpec() TargetSpec {
	resolved := intllvm.GetTargetSpec(p.GOOS, p.GOARCH, p.GOARM)
	return TargetSpec{
		Triple:   resolved.Triple,
		CPU:      resolved.CPU,
		Features: resolved.Features,
	}
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
