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

package cl

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	llssa "github.com/goplus/llgo/ssa"
	llvm "github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func emissionTestGlobal(t *testing.T, pkg emissionTestPackage, name string) *ssa.Global {
	t.Helper()
	global, ok := pkg.ssa.Members[name].(*ssa.Global)
	if !ok || global == nil {
		t.Fatalf("package %q member %q = %T; want *ssa.Global", pkg.types.Path(), name, pkg.ssa.Members[name])
	}
	return global
}

func TestCoroGlobalPhysicalIdentityInternalLinkageGates(t *testing.T) {
	for _, test := range []struct {
		name    string
		source  string
		profile CoroRawDataSymbolProfile
		global  string
		want    bool
	}{
		{
			name: "private complete raw profile",
			source: `package p
var slot func()
func Call() { slot() }
`,
			profile: CoroRawDataSymbolProfile{Complete: true}, global: "slot", want: true,
		},
		{
			name: "same package raw mention",
			source: `package p
var slot func()
func Call() { slot() }
`,
			profile: CoroRawDataSymbolProfile{Complete: true, Mentions: []string{"example.com/emission/internalgate.slot"}}, global: "slot",
		},
		{
			name: "incomplete raw profile",
			source: `package p
var slot func()
`,
			profile: CoroRawDataSymbolProfile{Blockers: []string{"opaque C input"}}, global: "slot",
		},
		{
			name: "exported cell",
			source: `package p
var Slot func()
`,
			profile: CoroRawDataSymbolProfile{Complete: true}, global: "Slot",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			pkg := testProg.addPackage(t, "example.com/emission/internalgate", test.source)
			testProg.ssa.Build()
			prog := llssa.NewProgram(nil)
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
				SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "internal-gate", RawDataSymbols: test.profile,
			}})
			if err != nil {
				t.Fatal(err)
			}
			identity, certified, err := universe.CoroGlobalPhysicalIdentity(emissionTestGlobal(t, pkg, test.global))
			if err != nil || !certified || identity.InternalLinkage != test.want {
				t.Fatalf("identity = %+v, certified=%t, err=%v; want internal=%t", identity, certified, err, test.want)
			}
		})
	}
}

func TestCoroGlobalPhysicalIdentityRejectsCrossOwnerGenericReference(t *testing.T) {
	testProg := newEmissionTestProgram()
	gen := testProg.addPackage(t, "example.com/emission/internalgen", `package internalgen
var slot func()
func F[T any]() { slot() }
`)
	use1 := testProg.addPackage(t, "example.com/emission/internaluse1", `package internaluse1
import "example.com/emission/internalgen"
func Use() { internalgen.F[int]() }
`)
	use2 := testProg.addPackage(t, "example.com/emission/internaluse2", `package internaluse2
import "example.com/emission/internalgen"
func Use() { internalgen.F[int]() }
`)
	testProg.ssa.Build()
	origin := gen.ssa.Func("F")
	var instance *ssa.Function
	for function := range ssautil.AllFunctions(testProg.ssa) {
		if function != nil && function.Origin() == origin && len(function.TypeArgs()) == 1 {
			instance = function
			break
		}
	}
	if instance == nil {
		t.Fatal("F[int] instance was not found")
	}
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	complete := CoroRawDataSymbolProfile{Complete: true}
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: gen.ssa, Files: []*ast.File{gen.file}, Identity: "gen", RawDataSymbols: complete},
		{SSA: use1.ssa, Files: []*ast.File{use1.file}, Identity: "use1", RawDataSymbols: complete},
		{SSA: use2.ssa, Files: []*ast.File{use2.file}, Identity: "use2", RawDataSymbols: complete},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(universe.materializedOwners[instance]) != 2 {
		t.Fatalf("F[int] materialized owners = %d; want 2", len(universe.materializedOwners[instance]))
	}
	identity, certified, err := universe.CoroGlobalPhysicalIdentity(emissionTestGlobal(t, gen, "slot"))
	if err != nil || !certified || identity.InternalLinkage {
		t.Fatalf("cross-owner identity = %+v, certified=%t, err=%v; want external linkage", identity, certified, err)
	}
}

func TestCompileGlobalAppliesCertifiedInternalLinkage(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/internalcodegen", `package internalcodegen
var slot func()
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "internal-codegen",
		RawDataSymbols: CoroRawDataSymbolProfile{Complete: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	emitted := prog.NewPackage(pkg.types.Name(), pkg.types.Path())
	compiler := &context{
		prog: prog, pkg: emitted, fset: testProg.fset, goProg: testProg.ssa,
		goTyps: pkg.types, goPkg: pkg.ssa, patches: make(Patches), skips: make(map[string]none),
		loaded: make(map[*types.Package]*pkgInfo), linkOnceFns: make(map[*ssa.Function]none),
		emissionUniverse: universe,
	}
	compiler.compileGlobal(emitted, emissionTestGlobal(t, pkg, "slot"))
	global := emitted.Module().NamedGlobal(pkg.types.Path() + ".slot")
	if global.IsNil() {
		t.Fatal("compiled private function cell is missing")
	}
	if global.Linkage() != llvm.InternalLinkage {
		t.Fatalf("compiled private function cell linkage = %v; want internal", global.Linkage())
	}
}

func TestCoroGlobalPhysicalIdentityOrdinaryAndDefensiveCopy(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/globalphysical", `package globalphysical
var Slot func(int) int
var NonFunction int
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "ordinary-global-variant",
	}})
	if err != nil {
		t.Fatal(err)
	}
	slot := emissionTestGlobal(t, pkg, "Slot")
	identity, certified, err := universe.CoroGlobalPhysicalIdentity(slot)
	if err != nil || !certified {
		t.Fatalf("CoroGlobalPhysicalIdentity(Slot) = %+v, %v, %v", identity, certified, err)
	}
	if identity.ID == "" || identity.PackageIdentity != "ordinary-global-variant" ||
		identity.PhysicalSymbol != pkg.types.Path()+".Slot" || identity.StructuralType == "" ||
		identity.Background != llssa.InGo || !identity.Define ||
		len(identity.Members) != 1 || identity.Members[0] != slot {
		t.Fatalf("ordinary global physical identity = %+v", identity)
	}

	// Both the slice and the surrounding value are caller-owned copies.
	identity.Members[0] = nil
	identity.Members = append(identity.Members, nil)
	identity.ID = "mutated"
	again, certified, err := universe.CoroGlobalPhysicalIdentity(slot)
	if err != nil || !certified || again.ID == "mutated" ||
		len(again.Members) != 1 || again.Members[0] != slot {
		t.Fatalf("identity mutated through returned copy: %+v, %v, %v", again, certified, err)
	}

	nonFunction := emissionTestGlobal(t, pkg, "NonFunction")
	if got, certified, err := universe.CoroGlobalPhysicalIdentity(nonFunction); err != nil || certified || got.ID != "" {
		t.Fatalf("non-function global identity = %+v, %v, %v; want open", got, certified, err)
	}
}

func TestCoroGlobalPhysicalIdentityPatchedMembers(t *testing.T) {
	t.Run("original only", func(t *testing.T) {
		universe, original, _, dispose := preparePatchedEmissionTest(t, `package p
var Optional func()
`, `package p
func Unrelated() {}
`)
		defer dispose()
		global := emissionTestGlobal(t, original, "Optional")
		identity, certified, err := universe.CoroGlobalPhysicalIdentity(global)
		if err != nil || !certified || len(identity.Members) != 1 || identity.Members[0] != global {
			t.Fatalf("patched original-only identity = %+v, %v, %v", identity, certified, err)
		}
		if identity.PhysicalSymbol != original.types.Path()+".Optional" {
			t.Fatalf("patched original-only physical symbol = %q", identity.PhysicalSymbol)
		}
	})

	t.Run("alternate then original exact members", func(t *testing.T) {
		universe, original, alternate, dispose := preparePatchedEmissionTest(t, `package p
var Slot func(int) int
`, `package p
var Slot func(int) int
`)
		defer dispose()
		originalGlobal := emissionTestGlobal(t, original, "Slot")
		alternateGlobal := emissionTestGlobal(t, alternate, "Slot")
		originalIdentity, originalCertified, originalErr := universe.CoroGlobalPhysicalIdentity(originalGlobal)
		alternateIdentity, alternateCertified, alternateErr := universe.CoroGlobalPhysicalIdentity(alternateGlobal)
		if originalErr != nil || alternateErr != nil || !originalCertified || !alternateCertified {
			t.Fatalf("patched shared identities = %+v/%v/%v, %+v/%v/%v",
				originalIdentity, originalCertified, originalErr, alternateIdentity, alternateCertified, alternateErr)
		}
		if originalIdentity.ID == "" || originalIdentity.ID != alternateIdentity.ID {
			t.Fatalf("original/alternate IDs = %q, %q", originalIdentity.ID, alternateIdentity.ID)
		}
		if len(originalIdentity.Members) != 2 ||
			originalIdentity.Members[0] != alternateGlobal || originalIdentity.Members[1] != originalGlobal {
			t.Fatalf("patched members = %v; want exact alt-first pointers", originalIdentity.Members)
		}
	})
}

func TestCoroGlobalPhysicalIdentityRejectsOpenAndConflictingCells(t *testing.T) {
	t.Run("linkname", func(t *testing.T) {
		testProg := newEmissionTestProgram()
		pkg := testProg.addPackage(t, "example.com/emission/globallink", `package globallink
var Slot func()
`)
		testProg.ssa.Build()
		prog := llssa.NewProgram(nil)
		defer prog.Dispose()
		prog.SetLinkname(pkg.types.Path()+".Slot", "example.com/external.Slot")
		universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
		if err != nil {
			t.Fatal(err)
		}
		identity, certified, err := universe.CoroGlobalPhysicalIdentity(emissionTestGlobal(t, pkg, "Slot"))
		if err != nil || certified || identity.ID != "" {
			t.Fatalf("linknamed global identity = %+v, %v, %v; want open", identity, certified, err)
		}
	})

	t.Run("type conflict", func(t *testing.T) {
		universe, original, alternate, dispose := preparePatchedEmissionTest(t, `package p
var Slot func()
`, `package p
var Slot func(int)
`)
		defer dispose()
		for _, global := range []*ssa.Global{
			emissionTestGlobal(t, alternate, "Slot"),
			emissionTestGlobal(t, original, "Slot"),
		} {
			identity, certified, err := universe.CoroGlobalPhysicalIdentity(global)
			if err != nil || certified || identity.ID != "" {
				t.Fatalf("conflicting global %p identity = %+v, %v, %v; want open", global, identity, certified, err)
			}
		}
	})

	t.Run("original skip removes conflict", func(t *testing.T) {
		universe, original, alternate, dispose := preparePatchedEmissionTest(t, `package p
var Slot func()
`, `package p
//llgo:skip Slot
type PatchControl struct{}
var Slot func(int)
`)
		defer dispose()
		alternateGlobal := emissionTestGlobal(t, alternate, "Slot")
		identity, certified, err := universe.CoroGlobalPhysicalIdentity(alternateGlobal)
		if err != nil || !certified || len(identity.Members) != 1 || identity.Members[0] != alternateGlobal {
			t.Fatalf("skip-filtered alternate identity = %+v, %v, %v", identity, certified, err)
		}
		_, certified, err = universe.CoroGlobalPhysicalIdentity(emissionTestGlobal(t, original, "Slot"))
		if err == nil || certified || !strings.Contains(err.Error(), "absent from the frozen processPkg inventory") {
			t.Fatalf("skipped original lookup = %v, %v; want exact inventory rejection", certified, err)
		}
	})
}
