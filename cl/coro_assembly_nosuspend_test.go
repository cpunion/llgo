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

	llssa "github.com/goplus/llgo/ssa"
)

func TestEmissionUniverseFreezesExactAssemblyNoSuspendProof(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/asmleaf", `package asmleaf
func Leaf(value int) int
func Call(value int) int { return Leaf(value) }
`)
	testProg.ssa.Build()

	physical := "example.com/emission/asmleaf.Leaf"
	proof := CoroAssemblyNoSuspendProof{
		PhysicalSymbol: physical,
		ABISignature:   `{"args":["i64"],"results":["i64"]}`,
		CallClosure:    []string{physical},
		ClosureSHA256:  strings.Repeat("1a", 32),
	}
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: pkg.types.Path(),
		AssemblyNoSuspendProofs: []CoroAssemblyNoSuspendProof{proof},
	}})
	if err != nil {
		t.Fatal(err)
	}
	leaf := pkg.ssa.Func("Leaf")
	certificate, ok, err := universe.CoroAssemblyNoSuspendCertificate(leaf)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || certificate.ID == "" || certificate.PhysicalSymbol != physical ||
		certificate.ABISignature != proof.ABISignature || certificate.ClosureSHA256 != proof.ClosureSHA256 {
		t.Fatalf("assembly certificate = %+v, %t; want exact frozen proof", certificate, ok)
	}
	if _, ok, err := universe.CoroAssemblyNoSuspendCertificate(pkg.ssa.Func("Call")); err != nil || ok {
		t.Fatalf("bodyful Call certificate = _, %t, %v; want false, nil", ok, err)
	}

	proof.CallClosure[0] = "mutated"
	certificateAfterMutation, ok, err := universe.CoroAssemblyNoSuspendCertificate(leaf)
	if err != nil || !ok || certificateAfterMutation != certificate {
		t.Fatalf("certificate changed after caller mutation: %+v, %t, %v", certificateAfterMutation, ok, err)
	}
}

func TestEmissionUniverseAssemblyNoSuspendProofFailsClosed(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/asmfail", `package asmfail
func Leaf()
`)
	testProg.ssa.Build()
	physical := "example.com/emission/asmfail.Leaf"
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()

	for _, test := range []struct {
		name  string
		proof CoroAssemblyNoSuspendProof
		want  string
	}{
		{
			name: "invalid digest",
			proof: CoroAssemblyNoSuspendProof{PhysicalSymbol: physical, ABISignature: `{}`,
				CallClosure: []string{physical}, ClosureSHA256: "not-a-digest"},
			want: "invalid SHA-256",
		},
		{
			name: "unsorted closure",
			proof: CoroAssemblyNoSuspendProof{PhysicalSymbol: physical, ABISignature: `{}`,
				CallClosure: []string{physical, "aaa"}, ClosureSHA256: strings.Repeat("00", 32)},
			want: "non-canonical call closure",
		},
		{
			name: "missing root",
			proof: CoroAssemblyNoSuspendProof{PhysicalSymbol: physical, ABISignature: `{}`,
				CallClosure: []string{"other"}, ClosureSHA256: strings.Repeat("00", 32)},
			want: "omits its root",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{
				SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: pkg.types.Path(),
				AssemblyNoSuspendProofs: []CoroAssemblyNoSuspendProof{test.proof},
			}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("PrepareEmissionUniverse error = %v; want %q", err, test.want)
			}
		})
	}
}
