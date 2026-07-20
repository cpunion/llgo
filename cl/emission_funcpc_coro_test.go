//go:build !llgo
// +build !llgo

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

package cl

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/typepatch"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestFuncPCABI0ElidesExactStaticAddressCall(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/funcpc", `package funcpc
//llgo:link FuncPCABI0 llgo.funcPCABI0
func FuncPCABI0(fn any) uintptr
func target() {}
func Use() uintptr { return FuncPCABI0(target) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
	semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("funcPCABI0 semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}
	if raw, err := universe.CoroRawFunctionAddressCallArgument(call, 0); err != nil || raw {
		t.Fatalf("funcPCABI0 raw invocation operand = %v, %v; want false, nil", raw, err)
	}
	if observed, err := universe.CoroStaticCodeAddressCallArgument(call, 0); err != nil || !observed {
		t.Fatalf("funcPCABI0 code-address operand = %v, %v; want true, nil", observed, err)
	}
}

func TestFuncPCABI0RejectsWrongResultShape(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/funcpcbad", `package funcpcbad
//llgo:link FuncPCABI0 llgo.funcPCABI0
func FuncPCABI0(fn any) int
func target() {}
func Use() int { return FuncPCABI0(target) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
	if _, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err == nil || !intrinsic || !strings.Contains(err.Error(), "func(any) uintptr") {
		t.Fatalf("wrong-shape funcPCABI0 semantics = _, %v, %v; want exact-shape error", intrinsic, err)
	}
}

func TestFuncPCABI0AcceptsStaticCTrampolineWithoutManagedRawTarget(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/funcpctrampoline", `package funcpctrampoline
//llgo:link FuncPCABI0 llgo.funcPCABI0
func FuncPCABI0(fn any) uintptr
func libc_access_trampoline(path *byte, mode int32) int32
func Access() uintptr { return FuncPCABI0(libc_access_trampoline) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Access"))[0]
	boxed, ok := call.Common().Args[0].(*ssa.MakeInterface)
	if !ok {
		t.Fatalf("FuncPCABI0 C trampoline operand = %T; want exact MakeInterface", call.Common().Args[0])
	}
	target, ok := boxed.X.(*ssa.Function)
	if !ok || target.Name() != "libc_access_trampoline" || len(target.FreeVars) != 0 {
		t.Fatalf("FuncPCABI0 C trampoline boxed target = %T %v; want exact non-capturing static function", boxed.X, boxed.X)
	}
	refs := boxed.Referrers()
	if refs == nil || len(*refs) != 1 || (*refs)[0] != call {
		t.Fatalf("FuncPCABI0 C trampoline MakeInterface referrers = %v; want exact call as sole consumer", refs)
	}
	semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("C trampoline funcPCABI0 semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}
	if raw, err := universe.CoroRawFunctionAddressCallArgument(call, 0); err != nil || raw {
		t.Fatalf("C trampoline managed raw operand = %v, %v; want false, nil", raw, err)
	}
	if observed, err := universe.CoroStaticCodeAddressCallArgument(call, 0); err != nil || !observed {
		t.Fatalf("C trampoline static code-address operand = %v, %v; want true, nil", observed, err)
	}
	if universe.Contains(target) {
		t.Fatal("compiler-generated C trampoline unexpectedly entered the managed emission universe")
	}
	audit, err := newCoroPhysicalPureSSAAudit(universe, nil, call.Parent(), "")
	if err != nil {
		t.Fatal(err)
	}
	if handled, reason := audit.validate(boxed); !handled || reason != "" {
		t.Fatalf("exact C trampoline MakeInterface physical audit = handled %t, reason %q", handled, reason)
	}
}

func TestCoroPureFunctionAddressMakeInterfaceElisionRemainsExact(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		wantReject bool
	}{
		{
			name: "exact funcAddr operand",
			source: `package exact
import "unsafe"
//llgo:link FuncAddr llgo.funcAddr
func FuncAddr(fn any) unsafe.Pointer
func target() {}
func Use() unsafe.Pointer { return FuncAddr(target) }
`,
		},
		{
			name: "escaped function box",
			source: `package escaped
func target() {}
func Use() any { return target }
`,
			wantReject: true,
		},
		{
			name: "function box with intrinsic and escaping consumers",
			source: `package multi
//llgo:link FuncPCABI0 llgo.funcPCABI0
func FuncPCABI0(fn any) uintptr
func target() {}
func Use() (uintptr, any) {
	value := any(target)
	return FuncPCABI0(value), value
}
`,
			wantReject: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			// The funcAddr signature uses unsafe.Pointer. emissionTestProgram's
			// type importer knows unsafe, but its SSA program intentionally starts
			// empty, so register the import package before Build.
			testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			pkg := testProg.addPackage(t, "example.com/emission/"+strings.ReplaceAll(test.name, " ", "_"), test.source)
			testProg.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
			if err != nil {
				t.Fatal(err)
			}
			use := pkg.ssa.Func("Use")
			var boxed *ssa.MakeInterface
			for _, block := range use.Blocks {
				for _, instruction := range block.Instrs {
					if candidate, ok := instruction.(*ssa.MakeInterface); ok {
						if boxed != nil {
							t.Fatal("fixture has more than one MakeInterface")
						}
						boxed = candidate
					}
				}
			}
			if boxed == nil {
				t.Fatal("fixture has no MakeInterface")
			}
			audit, err := newCoroPhysicalPureSSAAudit(universe, nil, use, "")
			if err != nil {
				t.Fatal(err)
			}
			handled, reason := audit.validate(boxed)
			if !handled {
				t.Fatal("MakeInterface was not handled by pure SSA audit")
			}
			if test.wantReject {
				if !strings.Contains(reason, "function-valued interface payload") &&
					!strings.Contains(reason, "boxing a function value requires canonical dynamic-dispatch descriptor validation") {
					t.Fatalf("escaping/multi-use function box rejection = %q", reason)
				}
			} else if reason != "" {
				t.Fatalf("exact function-address operand rejected: %s", reason)
			}
		})
	}
}

func TestFuncPCABI0RejectsStaticallyNilOperand(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/funcpcnil", `package funcpcnil
//llgo:link FuncPCABI0 llgo.funcPCABI0
func FuncPCABI0(fn any) uintptr
func Use() uintptr { return FuncPCABI0(nil) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
	if _, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err == nil || !intrinsic || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil funcPCABI0 semantics = _, %v, %v; want static nil rejection", intrinsic, err)
	}
}

func TestFuncPCABI0AcceptsDynamicInterfaceWithoutRawClassification(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/funcpcdynamic", `package funcpcdynamic
//llgo:link FuncPCABI0 llgo.funcPCABI0
func FuncPCABI0(fn any) uintptr
func Use(fn any) uintptr { return FuncPCABI0(fn) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
	semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("dynamic funcPCABI0 semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}
	if raw, err := universe.CoroRawFunctionAddressCallArgument(call, 0); err != nil || raw {
		t.Fatalf("dynamic funcPCABI0 raw operand = %v, %v; want false, nil", raw, err)
	}
	if observed, err := universe.CoroStaticCodeAddressCallArgument(call, 0); err != nil || observed {
		t.Fatalf("dynamic funcPCABI0 code-address operand = %v, %v; want false, nil", observed, err)
	}
}

const funcPCABI0TestAlternatePath = "example.com/llgo-alt/internal/abi"

func preparePatchedInternalABIFuncPCABI0Test(
	t *testing.T, originalSource, alternateSource string, alternateLinks map[string]string,
) (*EmissionUniverse, emissionTestPackage, emissionTestPackage, func(), error) {
	t.Helper()
	testProg := newEmissionTestProgram()
	original := testProg.addPackage(t, coroFuncPCABI0PackagePath, originalSource)
	alternate := testProg.addPackage(t, funcPCABI0TestAlternatePath, alternateSource)
	testProg.ssa.Build()

	prog := llssa.NewProgram(nil)
	for localName, target := range alternateLinks {
		prog.SetLinkname(funcPCABI0TestAlternatePath+"."+localName, target)
	}
	patches := Patches{coroFuncPCABI0PackagePath: {
		Alt:   alternate.ssa,
		Types: typepatch.Clone(alternate.types),
	}}
	universe, err := PrepareEmissionUniverse(prog, patches, []EmissionPackage{{
		SSA:   original.ssa,
		Files: []*ast.File{original.file},
	}})
	return universe, original, alternate, prog.Dispose, err
}

func TestEmissionUniverseAliasesPatchedInternalABIFuncPCABI0(t *testing.T) {
	universe, original, alternate, dispose, err := preparePatchedInternalABIFuncPCABI0Test(t, `package abi
func FuncPCABI0(fn any) uintptr
func target() {}
func Use() uintptr { return FuncPCABI0(target) }
`, `package abi
func FuncPCABI0(fn any) uintptr
`, map[string]string{coroFuncPCABI0LocalName: "llgo.funcPCABI0"})
	defer dispose()
	// Use a deliberately non-standard alternate path in this unit fixture so
	// the exact link directive can be registered only for the alternate SSA
	// function. Production patch paths are normalized by llssa.PathOf; the
	// alias itself depends on the prepared Patch relationship, never this path.
	if err != nil {
		t.Fatal(err)
	}

	declaration := original.ssa.Func(coroFuncPCABI0LocalName)
	intrinsicFn := alternate.ssa.Func(coroFuncPCABI0LocalName)
	if resolved, ok := universe.Resolve(declaration); !ok || resolved != intrinsicFn {
		t.Fatalf("Resolve(original internal/abi.FuncPCABI0) = %v, %v; want exact alternate intrinsic %v", resolved, ok, intrinsicFn)
	}
	if universe.Contains(declaration) || !universe.Contains(intrinsicFn) {
		t.Fatalf("FuncPCABI0 canonical membership = original %t, intrinsic %t; want false, true", universe.Contains(declaration), universe.Contains(intrinsicFn))
	}
	if _, required := universe.required[declaration]; required || universe.fnOwners[declaration] != nil ||
		len(universe.useOwners[declaration]) != 0 || len(universe.ownerStates[declaration]) != 0 {
		t.Fatal("original FuncPCABI0 declaration retains frozen canonical ownership")
	}
	for ownerKey := range universe.functionKinds {
		if ownerKey.function == declaration {
			t.Fatal("original FuncPCABI0 declaration retains frontend-kind metadata")
		}
	}
	for ownerKey := range universe.finalKeys {
		if ownerKey.function == declaration {
			t.Fatal("original FuncPCABI0 declaration retains managed-symbol metadata")
		}
	}
	owner := universe.packages[original.ssa]
	intrinsicOwnerKey := emissionFunctionOwnerKey{function: intrinsicFn, owner: owner}
	if kind, ok := universe.functionKinds[intrinsicOwnerKey]; !ok || kind != llgoInstr {
		t.Fatalf("canonical FuncPCABI0 intrinsic kind = %d, %v; want llgoInstr, true", kind, ok)
	}
	if opcode, ok := universe.intrinsicOps[intrinsicOwnerKey]; !ok || opcode != llgoFuncPCABI0 {
		t.Fatalf("canonical FuncPCABI0 opcode = %d, %v; want llgoFuncPCABI0, true", opcode, ok)
	}
	for _, function := range []*ssa.Function{declaration, intrinsicFn} {
		semantics, intrinsic, err := universe.CoroIntrinsicSemantics(function)
		if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
			t.Fatalf("CoroIntrinsicSemantics(%v) = %v, %v, %v; want inline-no-suspend, true, nil", function, semantics, intrinsic, err)
		}
	}
	call := allocaCStrTestCalls(original.ssa.Func("Use"))[0]
	semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("patched FuncPCABI0 callsite semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}
	if raw, err := universe.CoroRawFunctionAddressCallArgument(call, 0); err != nil || raw {
		t.Fatalf("patched FuncPCABI0 raw invocation operand = %v, %v; want false, nil", raw, err)
	}
	if observed, err := universe.CoroStaticCodeAddressCallArgument(call, 0); err != nil || !observed {
		t.Fatalf("patched FuncPCABI0 code-address operand = %v, %v; want true, nil", observed, err)
	}
}

func TestEmissionUniverseAliasesPatchedInternalABIFuncPCABIInternal(t *testing.T) {
	universe, original, alternate, dispose, err := preparePatchedInternalABIFuncPCABI0Test(t, `package abi
func FuncPCABI0(fn any) uintptr
func FuncPCABIInternal(fn any) uintptr
func target(value uint32) uint32 { return value }
func Use() uintptr { return FuncPCABIInternal(target) }
`, `package abi
func FuncPCABI0(fn any) uintptr
func FuncPCABIInternal(fn any) uintptr
`, map[string]string{
		coroFuncPCABI0LocalName:        "llgo.funcPCABI0",
		coroFuncPCABIInternalLocalName: "llgo.funcPCABIInternal",
	})
	defer dispose()
	if err != nil {
		t.Fatal(err)
	}

	declaration := original.ssa.Func(coroFuncPCABIInternalLocalName)
	intrinsicFn := alternate.ssa.Func(coroFuncPCABIInternalLocalName)
	if resolved, ok := universe.Resolve(declaration); !ok || resolved != intrinsicFn {
		t.Fatalf("Resolve(original internal/abi.FuncPCABIInternal) = %v, %v; want %v", resolved, ok, intrinsicFn)
	}
	semantics, intrinsic, err := universe.CoroIntrinsicSemantics(declaration)
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("FuncPCABIInternal semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}
	call := allocaCStrTestCalls(original.ssa.Func("Use"))[0]
	if raw, err := universe.CoroRawFunctionAddressCallArgument(call, 0); err != nil || raw {
		t.Fatalf("FuncPCABIInternal raw invocation operand = %v, %v; want false, nil", raw, err)
	}
	if observed, err := universe.CoroStaticCodeAddressCallArgument(call, 0); err != nil || !observed {
		t.Fatalf("FuncPCABIInternal code-address operand = %v, %v; want true, nil", observed, err)
	}
}

func TestEmissionUniversePatchedInternalABIFuncPCABI0FailsClosed(t *testing.T) {
	tests := []struct {
		name            string
		alternateSource string
		alternateLinks  map[string]string
		wantError       string
	}{
		{
			name: "structural signature mismatch",
			alternateSource: `package abi
func FuncPCABI0(fn string) uintptr
`,
			alternateLinks: map[string]string{"FuncPCABI0": "llgo.funcPCABI0"},
			wantError:      "different structural ABI signatures",
		},
		{
			name: "ambiguous alternate intrinsic",
			alternateSource: `package abi
func FuncPCABI0(fn any) uintptr
func OtherFuncPCABI0(fn any) uintptr
`,
			alternateLinks: map[string]string{
				"FuncPCABI0":      "llgo.funcPCABI0",
				"OtherFuncPCABI0": "llgo.funcPCABI0",
			},
			wantError: "ambiguous alternate intrinsic replacements",
		},
		{
			name: "different local source name never aliases",
			alternateSource: `package abi
func OtherFuncPCABI0(fn any) uintptr
`,
			alternateLinks: map[string]string{"OtherFuncPCABI0": "llgo.funcPCABI0"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			universe, original, _, dispose, err := preparePatchedInternalABIFuncPCABI0Test(t, `package abi
func FuncPCABI0(fn any) uintptr
`, test.alternateSource, test.alternateLinks)
			defer dispose()
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("PrepareEmissionUniverse error = %v; want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			declaration := original.ssa.Func(coroFuncPCABI0LocalName)
			if resolved, ok := universe.Resolve(declaration); !ok || resolved != declaration || !universe.Contains(declaration) {
				t.Fatalf("Resolve(unmatched internal/abi.FuncPCABI0) = %v, %v (contained=%t); want exact original", resolved, ok, universe.Contains(declaration))
			}
		})
	}
}
