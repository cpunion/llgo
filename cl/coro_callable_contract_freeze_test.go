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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestEmissionUniverseFreezesCallableDeclarationAndWrapperContracts(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/callablecontracts", `package callablecontracts

//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete inline-progress=executor-safe inline-affinity=any-thread inline-reentry=none inline-memory=borrow-until-return
//go:linkname Foreign C.callable_contract_foreign
func Foreign(int) int

//llgo:coro contract foreign.v1 scope=wrapper progress=async-completion affinity=host-main reentry=managed-callback memory=retained abi=word-call.v1/1
func Wrapper(value int) int { return value + 1 }

func Plain() {}
func root(value int) int { return Foreign(value) + Wrapper(value) }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "callable-contract-owner",
	}})
	if err != nil {
		t.Fatal(err)
	}

	foreign, ok, err := universe.CoroCallableContractCertificate(pkg.ssa.Func("Foreign"))
	if err != nil || !ok {
		t.Fatalf("Foreign callable contract = %+v, %t, %v", foreign, ok, err)
	}
	if len(foreign.ID) != 64 || len(foreign.ContractDigest) != 64 || len(foreign.TrustedInlineContractDigest) != 64 ||
		foreign.CanonicalFunctionIdentity == "" || foreign.LinkIdentity == "" ||
		foreign.Scope != CoroCallableContractScopeDeclaration ||
		foreign.CallableABIExplicit || !strings.HasPrefix(foreign.CallableABI, "typed.v1/") ||
		foreign.TypedABISignature == "" || foreign.PhysicalSymbol != "callable_contract_foreign" ||
		foreign.PhysicalABISignature != foreign.TypedABISignature ||
		!strings.HasPrefix(string(foreign.Contract.ID), coroCallableContractIDForeignV1+"/") ||
		foreign.Contract.Progress != coro.ProgressMayBlock || !foreign.HasTrustedInlineContract ||
		!strings.HasPrefix(string(foreign.TrustedInlineContract.ID), coroCallableContractIDForeignV1+"/") ||
		foreign.TrustedInlineContract.Progress != coro.ProgressExecutorSafe ||
		foreign.TrustedInlineContract.Memory != coro.MemoryBorrowUntilReturn ||
		foreign.TrustedInlineContract.ID == foreign.Contract.ID {
		t.Fatalf("Foreign frozen callable contract = %+v", foreign)
	}

	wrapper, ok, err := universe.CoroCallableContractCertificate(pkg.ssa.Func("Wrapper"))
	if err != nil || !ok {
		t.Fatalf("Wrapper callable contract = %+v, %t, %v", wrapper, ok, err)
	}
	if len(wrapper.ID) != 64 || len(wrapper.ContractDigest) != 64 || wrapper.ID == foreign.ID ||
		wrapper.Scope != CoroCallableContractScopeWrapper ||
		!wrapper.CallableABIExplicit || wrapper.CallableABI != "word-call.v1/1" ||
		wrapper.TypedABISignature == "" || wrapper.PhysicalSymbol != "" || wrapper.PhysicalABISignature != "" ||
		wrapper.Contract.Progress != coro.ProgressAsyncCompletion || wrapper.HasTrustedInlineContract ||
		wrapper.TrustedInlineContract != (coro.CallableContract{}) || wrapper.TrustedInlineContractDigest != "" {
		t.Fatalf("Wrapper frozen callable contract = %+v", wrapper)
	}
	if wrapper.Contract.ID == foreign.Contract.ID {
		t.Fatalf("different callable behaviors share frozen contract ID %q", wrapper.Contract.ID)
	}
	if _, ok, err := universe.CoroCallableContractCertificate(pkg.ssa.Func("Plain")); err != nil || ok {
		t.Fatalf("Plain callable contract = %t, %v; want absent", ok, err)
	}

	// The accessor must remain a construction-time snapshot after source AST
	// mutation; downstream code may not reopen comments.
	declaration := pkg.ssa.Func("Foreign").Syntax().(*ast.FuncDecl)
	declaration.Doc.List[0].Text = "//llgo:coro contract foreign.v1 progress=executor-safe affinity=caller-thread reentry=none memory=by-value"
	again, ok, err := universe.CoroCallableContractCertificate(pkg.ssa.Func("Foreign"))
	if err != nil || !ok || again != foreign {
		t.Fatalf("mutated source changed frozen callable contract: %+v, %t, %v; want %+v", again, ok, err, foreign)
	}
}

func TestEmissionUniverseCallableTrustedInlineRefinementBindsCertificateIdentity(t *testing.T) {
	build := func(inline string) CoroCallableContractCertificate {
		t.Helper()
		testProg := newEmissionTestProgram()
		pkg := testProg.addPackage(t, "example.com/emission/callableinlineidentity", `package callableinlineidentity
//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete`+inline+`
//go:linkname Foreign C.callable_inline_identity
func Foreign(int) int
func root(value int) int { return Foreign(value) }
`)
		testProg.ssa.Build()
		prog := llssa.NewProgram(nil)
		defer prog.Dispose()
		universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{
			SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "callable-inline-owner",
		}})
		if err != nil {
			t.Fatal(err)
		}
		certificate, ok, err := universe.CoroCallableContractCertificate(pkg.ssa.Func("Foreign"))
		if err != nil || !ok {
			t.Fatalf("callable certificate = %+v, %t, %v", certificate, ok, err)
		}
		return certificate
	}
	without := build("")
	with := build(" inline-progress=executor-safe inline-affinity=any-thread inline-reentry=none inline-memory=borrow-until-return")
	if without.Contract != with.Contract || without.ContractDigest != with.ContractDigest ||
		without.CallableABI != with.CallableABI || without.TypedABISignature != with.TypedABISignature {
		t.Fatalf("trusted-inline refinement changed default behavior/ABI: without=%+v with=%+v", without, with)
	}
	if without.HasTrustedInlineContract || without.TrustedInlineContract != (coro.CallableContract{}) ||
		without.TrustedInlineContractDigest != "" {
		t.Fatalf("absent trusted-inline refinement retained data: %+v", without)
	}
	if !with.HasTrustedInlineContract || len(with.TrustedInlineContractDigest) != 64 ||
		with.TrustedInlineContract.Progress != coro.ProgressExecutorSafe || with.ID == without.ID {
		t.Fatalf("trusted-inline refinement did not bind certificate identity: without=%+v with=%+v", without, with)
	}
}

func TestEmissionUniverseCallableContractAccessorCanonicalizesExactGoAlias(t *testing.T) {
	testProg := newEmissionTestProgram()
	declaration := testProg.addPackage(t, "example.com/emission/callablealias", `package callablealias
//go:linkname Hook
func Hook(int) int
func Root(value int) int { return Hook(value) }
`)
	definition := testProg.addPackage(t, "example.com/emission/callablealiasimpl", `package callablealiasimpl
//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
//go:linkname implementation example.com/emission/callablealias.Hook
func implementation(value int) int { return value + 1 }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: declaration.ssa, Files: []*ast.File{declaration.file}},
		{SSA: definition.ssa, Files: []*ast.File{definition.file}},
	})
	if err != nil {
		t.Fatal(err)
	}
	alias := declaration.ssa.Func("Hook")
	canonical := definition.ssa.Func("implementation")
	resolved, body := universe.Resolve(alias)
	if !body || resolved != canonical {
		t.Fatalf("Resolve(alias) = %v, %t; want exact definition %v", resolved, body, canonical)
	}
	fromAlias, aliasOK, aliasErr := universe.CoroCallableContractCertificate(alias)
	fromCanonical, canonicalOK, canonicalErr := universe.CoroCallableContractCertificate(canonical)
	if aliasErr != nil || canonicalErr != nil || !aliasOK || !canonicalOK || fromAlias != fromCanonical {
		t.Fatalf("alias/canonical contracts = (%+v,%t,%v) and (%+v,%t,%v)", fromAlias, aliasOK, aliasErr, fromCanonical, canonicalOK, canonicalErr)
	}
	if fromAlias.Scope != CoroCallableContractScopeWrapper || fromAlias.PhysicalSymbol != "" {
		t.Fatalf("alias contract = %+v; want exact Go wrapper", fromAlias)
	}
}

func TestEmissionUniverseCallableContractsFailClosedOnInvalidScope(t *testing.T) {
	for _, test := range []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name: "bodyless Go declaration has no physical C ABI",
			source: `package badcallable
//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete
func Missing(int) int
func root() { _ = Missing(1) }
`,
			wantErr: "requires an exact frozen C declaration and physical ABI",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			pkg := testProg.addPackage(t, "example.com/emission/badcallable", test.source)
			testProg.ssa.Build()
			prog := llssa.NewProgram(nil)
			defer prog.Dispose()
			_, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{
				SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "bad-callable-owner",
			}})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("PrepareEmissionUniverse error = %v; want %q", err, test.wantErr)
			}
		})
	}
}

func TestEmissionUniverseCallableIdentityAllowsRepeatedPhysicalTargets(t *testing.T) {
	testProg := newEmissionTestProgram()
	firstPkg := testProg.addPackage(t, "example.com/emission/callableidentityrepeat/first", `package first
//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete
//go:linkname First C.callable_identity_repeat
func First(int) int
func root() { _ = First(1) }
`)
	secondPkg := testProg.addPackage(t, "example.com/emission/callableidentityrepeat/second", `package second
//go:linkname Second C.callable_identity_repeat
func Second(int) int
func root() { _ = Second(2) }
`)
	differentPkg := testProg.addPackage(t, "example.com/emission/callableidentityrepeat/different", `package different
//go:linkname DifferentABI C.callable_identity_repeat
func DifferentABI(string) string
func root() { _ = DifferentABI("") }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: firstPkg.ssa, Files: []*ast.File{firstPkg.file}, Identity: "callable-identity-repeat-first"},
		{SSA: secondPkg.ssa, Files: []*ast.File{secondPkg.file}, Identity: "callable-identity-repeat-second"},
		{SSA: differentPkg.ssa, Files: []*ast.File{differentPkg.file}, Identity: "callable-identity-repeat-different"},
	})
	if err != nil {
		t.Fatal(err)
	}

	identities := make(map[string]CoroCallableIdentityCertificate)
	functions := map[string]*ssa.Function{
		"First": firstPkg.ssa.Func("First"), "Second": secondPkg.ssa.Func("Second"),
		"DifferentABI": differentPkg.ssa.Func("DifferentABI"),
	}
	for _, name := range []string{"First", "Second", "DifferentABI"} {
		identity, ok, err := universe.CoroCallableIdentityCertificate(functions[name])
		if err != nil || !ok {
			t.Fatalf("%s identity = %+v, %t, %v", name, identity, ok, err)
		}
		if err := identity.Validate(); err != nil || identity.PhysicalSymbol != "callable_identity_repeat" {
			t.Fatalf("%s identity = %+v: %v", name, identity, err)
		}
		if previous, duplicate := identities[identity.ID]; duplicate {
			t.Fatalf("%s and another exact declaration share identity %+v", name, previous)
		}
		identities[identity.ID] = identity
	}
	first, _, _ := universe.CoroCallableIdentityCertificate(functions["First"])
	second, _, _ := universe.CoroCallableIdentityCertificate(functions["Second"])
	different, _, _ := universe.CoroCallableIdentityCertificate(functions["DifferentABI"])
	if first.PhysicalABISignature != second.PhysicalABISignature ||
		first.PhysicalABISignature == different.PhysicalABISignature {
		t.Fatalf("repeated physical ABI inventory = first:%q second:%q different:%q", first.PhysicalABISignature, second.PhysicalABISignature, different.PhysicalABISignature)
	}
	contract, ok, err := universe.CoroCallableContractCertificate(functions["First"])
	if err != nil || !ok {
		t.Fatalf("First contract = %+v, %t, %v", contract, ok, err)
	}
	if err := coro.ValidateCallableContractIdentity(first, contract); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Second", "DifferentABI"} {
		if _, ok, err := universe.CoroCallableContractCertificate(functions[name]); err != nil || ok {
			t.Fatalf("%s behavior contract = %t, %v; want identity-only", name, ok, err)
		}
	}
}

func TestEmissionUniverseCallableContractsRejectDuplicateExactAlias(t *testing.T) {
	testProg := newEmissionTestProgram()
	declaration := testProg.addPackage(t, "example.com/emission/callabledupalias", `package callabledupalias
//llgo:coro contract foreign.v1 scope=declaration progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
//go:linkname Hook
func Hook(int) int
func Root(value int) int { return Hook(value) }
`)
	definition := testProg.addPackage(t, "example.com/emission/callabledupaliasimpl", `package callabledupaliasimpl
//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
//go:linkname implementation example.com/emission/callabledupalias.Hook
func implementation(value int) int { return value + 1 }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	_, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: declaration.ssa, Files: []*ast.File{declaration.file}},
		{SSA: definition.ssa, Files: []*ast.File{definition.file}},
	})
	if err == nil || !strings.Contains(err.Error(), "same exact canonical function") {
		t.Fatalf("PrepareEmissionUniverse error = %v; want duplicate exact alias rejection", err)
	}
}
