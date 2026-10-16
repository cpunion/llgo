//go:build !llgo

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
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

package build

import (
	"go/token"
	"go/types"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
)

func TestWasmFrontendLayoutMatchesLLVM(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name       string
		target     *llssa.Target
		frontend   string
		llvmTarget string
	}{
		{
			name:     "goarch-wasm",
			target:   &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"},
			frontend: "wasm",
		},
		{
			name: "named-wasm-target",
			target: &llssa.Target{
				GOOS: "linux", GOARCH: "arm", Target: "wasm-unknown",
				Resolved: &llssa.TargetSpec{Triple: "wasm32-unknown-unknown"},
			},
			frontend:   "arm",
			llvmTarget: "wasm32-unknown-unknown",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testWasmFrontendLayoutMatchesLLVM(t, test.target, test.frontend, test.llvmTarget)
		})
	}
}

func TestBaremetalFrontendLayoutMatchesLLVM(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name, triple, cpu string
	}{
		{name: "native-arm"},
		{name: "cortex-m3", triple: "thumbv7m-unknown-unknown-eabi", cpu: "cortex-m3"},
		{name: "riscv32", triple: "riscv32-unknown-unknown-eabi", cpu: "generic-rv32"},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := &llssa.Target{GOOS: "linux", GOARCH: "arm"}
			if test.triple != "" {
				target.Resolved = &llssa.TargetSpec{Triple: test.triple, CPU: test.cpu}
			}
			testWasmFrontendLayoutMatchesLLVM(t, target, "arm", test.triple)
		})
	}
}

func testWasmFrontendLayoutMatchesLLVM(t *testing.T, target *llssa.Target, frontend, llvmTarget string) {
	t.Helper()
	prog := llssa.NewProgram(target)
	defer prog.Dispose()

	sizes := prog.TypeSizes(llgoTargetTypeSizes(types.SizesFor("gc", frontend), "gc", frontend, llvmTarget))
	pointer := types.NewPointer(types.Typ[types.Byte])
	if got := sizes.Sizeof(pointer); got != 4 {
		t.Fatalf("frontend pointer size = %d, want 4", got)
	}
	if got := sizes.Alignof(types.Typ[types.Uint64]); got != 8 {
		t.Fatalf("frontend uint64 alignment = %d, want LLVM ABI alignment 8", got)
	}

	callback := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	fields := []*types.Var{
		types.NewField(token.NoPos, nil, "Byte", types.Typ[types.Byte], false),
		types.NewField(token.NoPos, nil, "Callback", callback, false),
		types.NewField(token.NoPos, nil, "Integer", types.Typ[types.Uint64], false),
		types.NewField(token.NoPos, nil, "Pointer", pointer, false),
		types.NewField(token.NoPos, nil, "Float", types.Typ[types.Float64], false),
		types.NewField(token.NoPos, nil, "Complex", types.Typ[types.Complex128], false),
		types.NewField(token.NoPos, nil, "Tail", types.Typ[types.Byte], false),
	}
	structure := types.NewStruct(fields, nil)
	assertWasmPhysicalLayout(t, prog, sizes, "direct", structure)

	nested := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "Context", structure, false),
		types.NewField(token.NoPos, nil, "Callbacks", types.NewArray(callback, 3), false),
		types.NewField(token.NoPos, nil, "Root", pointer, false),
	}, nil)
	assertWasmPhysicalLayout(t, prog, sizes, "nested", nested)

	// Match the failure shape of the G sidecar: scalar status precedes 64-bit
	// IDs, and a pointer-bearing local context follows the embedded G. On ARM,
	// gc's 4-byte uint64 alignment previously underallocated the entire object.
	g := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "Pointers", types.NewArray(pointer, 6), false),
		types.NewField(token.NoPos, nil, "Status", types.Typ[types.Uint32], false),
		types.NewField(token.NoPos, nil, "ID", types.Typ[types.Uint64], false),
		types.NewField(token.NoPos, nil, "ParentID", types.Typ[types.Uint64], false),
		types.NewField(token.NoPos, nil, "Tail", types.NewArray(pointer, 4), false),
		types.NewField(token.NoPos, nil, "Flags", types.NewArray(types.Typ[types.Bool], 4), false),
	}, nil)
	context := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "G", g, false),
		types.NewField(token.NoPos, nil, "Local", pointer, false),
	}, nil)
	assertWasmPhysicalLayout(t, prog, sizes, "runtime-context", context)
}

func assertWasmPhysicalLayout(t *testing.T, prog llssa.Program, sizes types.Sizes, name string, structure *types.Struct) {
	t.Helper()
	physical := prog.Type(structure, llssa.InGo)

	fields := make([]*types.Var, structure.NumFields())
	for index := range fields {
		fields[index] = structure.Field(index)
	}
	offsets := sizes.Offsetsof(fields)
	for index, want := range offsets {
		if got := int64(prog.OffsetOf(physical, index)); got != want {
			t.Fatalf("%s field %d LLVM offset = %d, frontend offset = %d", name, index, got, want)
		}
	}
	if got, want := int64(prog.SizeOf(physical)), sizes.Sizeof(structure); got != want {
		t.Fatalf("%s LLVM struct size = %d, frontend size = %d", name, got, want)
	}
	if got, want := int64(prog.AlignOf(physical)), sizes.Alignof(structure); got != want {
		t.Fatalf("%s LLVM struct alignment = %d, frontend alignment = %d", name, got, want)
	}
}
