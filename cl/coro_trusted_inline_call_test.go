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
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
)

func TestEmissionUniverseFreezesOnlyExactWrapperTrustedInlineCalls(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/trustedinline", `package trustedinline

//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none memory=borrow-until-return inline-progress=executor-safe inline-affinity=any-thread inline-reentry=none inline-memory=borrow-until-return
//go:linkname Foreign C.trusted_inline_foreign
func Foreign(int) int

//llgo:coro contract foreign.v1 progress=may-block affinity=unknown reentry=none memory=borrow-until-return inline-progress=executor-safe inline-affinity=owner-thread inline-reentry=none inline-memory=borrow-until-return
//go:linkname NeedsAdapter C.trusted_inline_needs_adapter
func NeedsAdapter(int) int

//llgo:coro contract foreign.v1 progress=unknown affinity=unknown reentry=unknown memory=unknown inline-progress=executor-safe inline-affinity=any-thread inline-reentry=none inline-memory=borrow-until-return
//go:linkname UnknownDefault C.trusted_inline_unknown_default
func UnknownDefault(int) int

//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none memory=borrow-until-return
//go:linkname NoRefinement C.trusted_inline_no_refinement
func NoRefinement(int) int

//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
func Fast(value int) int { return Foreign(value) }

//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
func UnknownFast(value int) int { return UnknownDefault(value) }

//llgo:coro contract foreign.v1 scope=wrapper progress=may-block affinity=caller-thread reentry=none memory=borrow-until-return
func Auto(value int) int { return Foreign(value) }

//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
func AdapterMissing(value int) int { return NeedsAdapter(value) }

//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
func RefinementMissing(value int) int { return NoRefinement(value) }

func root(value int) int { return Fast(value) + UnknownFast(value) + Auto(value) + AdapterMissing(value) + RefinementMissing(value) }
`)
	testProg.ssa.Build()
	program := llssa.NewProgram(nil)
	defer program.Dispose()
	universe, err := prepareStacklessEmissionUniverse(program, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "trusted-inline-owner",
	}})
	if err != nil {
		t.Fatal(err)
	}

	fast := pkg.ssa.Func("Fast")
	fastCall := findStaticCallByName(t, fast, "Foreign")
	certificate, certified, err := universe.CoroTrustedInlineCallCertificate(fast, fastCall)
	if err != nil || !certified || len(certificate.ID) != 64 {
		t.Fatalf("Fast trusted-inline certificate = %+v, %t, %v", certificate, certified, err)
	}
	target, ok, err := universe.CoroCallableContractCertificate(pkg.ssa.Func("Foreign"))
	if err != nil || !ok {
		t.Fatalf("Foreign callable certificate = %+v, %t, %v", target, ok, err)
	}
	if certificate.Contract != target.TrustedInlineContract.ID || certificate.ABI != target.CallableABI {
		t.Fatalf("Fast certificate = %+v; target = %+v", certificate, target)
	}
	unknownFast := pkg.ssa.Func("UnknownFast")
	unknownCall := findStaticCallByName(t, unknownFast, "UnknownDefault")
	unknownCertificate, certified, err := universe.CoroTrustedInlineCallCertificate(unknownFast, unknownCall)
	if err != nil || !certified || len(unknownCertificate.ID) != 64 {
		t.Fatalf("UnknownFast trusted-inline certificate = %+v, %t, %v", unknownCertificate, certified, err)
	}
	unknownTarget, ok, err := universe.CoroCallableContractCertificate(pkg.ssa.Func("UnknownDefault"))
	if err != nil || !ok || coro.CallableContractExecConstraints(unknownTarget.Contract) != coro.ThreadAffine|coro.OpaqueExec ||
		coro.CallableContractExecConstraints(unknownTarget.TrustedInlineContract) != 0 ||
		unknownCertificate.Contract != unknownTarget.TrustedInlineContract.ID || unknownCertificate.ABI != unknownTarget.CallableABI {
		t.Fatalf("UnknownFast certificate = %+v; target = %+v, %t, %v", unknownCertificate, unknownTarget, ok, err)
	}

	for _, test := range []struct {
		caller string
		target string
	}{
		{caller: "Auto", target: "Foreign"},
		{caller: "AdapterMissing", target: "NeedsAdapter"},
		{caller: "RefinementMissing", target: "NoRefinement"},
	} {
		caller := pkg.ssa.Func(test.caller)
		call := findStaticCallByName(t, caller, test.target)
		got, ok, err := universe.CoroTrustedInlineCallCertificate(caller, call)
		if err != nil || ok || got != (coro.SSATrustedInlineCallCertificate{}) {
			t.Fatalf("%s trusted-inline certificate = %+v, %t, %v; want absent", test.caller, got, ok, err)
		}
	}
	if got, ok, err := universe.CoroTrustedInlineCallCertificate(pkg.ssa.Func("Auto"), fastCall); err != nil || ok || got != (coro.SSATrustedInlineCallCertificate{}) {
		t.Fatalf("certificate replay under wrong caller = %+v, %t, %v; want absent", got, ok, err)
	}
}
