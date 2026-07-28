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
)

func TestEmissionUniverseBindsInferredForeignExecutorLeafProof(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/inferredc", `package inferredc
//go:linkname Leaf C.inferred_leaf
func Leaf(uintptr) uint64
func root(value uintptr) uint64 { return Leaf(value) }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	proof := CoroForeignExecutorLeafProof{
		ProducerIdentity: "producer-package",
		PhysicalSymbol:   "inferred_leaf",
		LLVMABISignature: "i64 (i64)",
		LLVMTargetTriple: prog.TargetSpec().Triple,
		LLVMDataLayout:   prog.DataLayout(),
		CallClosure:      []string{"inferred_leaf"},
		ClosureSHA256:    strings.Repeat("1a", 32),
	}
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog,
		nil,
		[]EmissionPackage{{
			SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "consumer-package",
		}},
		EmissionUniverseOptions{
			CoroForeignExecutorLeafProofs: []CoroForeignExecutorLeafProof{proof},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	leaf := pkg.ssa.Func("Leaf")
	if legacy, inferred, err := universe.CoroForeignNoBlockCertificate(leaf); err != nil ||
		inferred || legacy != (CoroForeignNoBlockCertificate{}) {
		t.Fatalf(
			"inferred implementation acquired legacy certificate = %+v, %t, %v",
			legacy, inferred, err,
		)
	}
	certificate, inferred, err :=
		universe.CoroCallableContractCertificate(leaf)
	if err != nil || !inferred || certificate.ID == "" ||
		certificate.PhysicalSymbol != proof.PhysicalSymbol ||
		!strings.HasPrefix(
			string(certificate.Contract.ID),
			"llvm-executor-leaf.v1/",
		) ||
		!coro.CallableContractDirectExecutorCompatible(
			certificate.Contract,
		) {
		t.Fatalf(
			"inferred executor-leaf contract = %+v, %t, %v",
			certificate, inferred, err,
		)
	}
	if defaulted, err := universe.CoroLibraryEffects().
		CallableContractDefault(leaf); err != nil || defaulted {
		t.Fatalf(
			"inferred executor-leaf contract remains a default: %t, %v",
			defaulted, err,
		)
	}
	identity, identified, err :=
		universe.CoroCallableIdentityCertificate(leaf)
	if err != nil || !identified {
		t.Fatalf("inferred executor-leaf identity = %+v, %t, %v", identity, identified, err)
	}
	archiveFact := coro.LibraryEffectForeignCallable{
		Function:    "inferred-leaf",
		Identity:    identity,
		Contract:    certificate,
		HasContract: true,
	}
	if err := archiveFact.Validate(); err != nil {
		t.Fatalf("inferred executor-leaf archive fact: %v", err)
	}
	imported, err := archiveFact.ImportedPolicy()
	if err != nil ||
		imported.External != coro.ExternalKnown ||
		imported.Exec != coro.IRQUnsafe ||
		imported.CallableContractCertificate != certificate ||
		imported.ForeignNoBlockCertificate != "" {
		t.Fatalf(
			"inferred executor-leaf imported policy = %+v, %v",
			imported, err,
		)
	}
	if err := validateCoroDynamicDispatchTarget(
		leaf,
		coro.FunctionPlan{
			ID:       "inferred-leaf",
			External: coro.ExternalKnown,
			Effect:   coro.NoSuspend,
			Exec:     coro.IRQUnsafe,
			FuncRep:  coro.Dispatch,
			Emission: coro.EmitExternal,
			Primary:  coro.PrimaryExternal,
		},
		universe,
	); err != nil {
		t.Fatalf(
			"inferred executor-leaf contract cannot back dynamic dispatch: %v",
			err,
		)
	}

	proof.CallClosure[0] = "mutated"
	again, inferred, err :=
		universe.CoroCallableContractCertificate(leaf)
	if err != nil || !inferred || again != certificate {
		t.Fatalf(
			"caller mutation changed inferred certificate: %+v, %t, %v; want %+v",
			again, inferred, err, certificate,
		)
	}
}

func TestExplicitCallableContractOverridesInferredForeignProof(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/explicitc", `package explicitc
//llgo:coro contract foreign.v1 progress=executor-safe affinity=caller-thread reentry=none memory=by-value
//go:linkname Leaf C.explicit_leaf
func Leaf(uintptr) uint64
func root(value uintptr) uint64 { return Leaf(value) }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog,
		nil,
		[]EmissionPackage{{
			SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "consumer-package",
		}},
		EmissionUniverseOptions{
			CoroForeignExecutorLeafProofs: []CoroForeignExecutorLeafProof{{
				ProducerIdentity: "producer-package",
				PhysicalSymbol:   "explicit_leaf",
				LLVMABISignature: "i64 (i64)",
				LLVMTargetTriple: prog.TargetSpec().Triple,
				LLVMDataLayout:   prog.DataLayout(),
				CallClosure:      []string{"explicit_leaf"},
				ClosureSHA256:    strings.Repeat("2b", 32),
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	leaf := pkg.ssa.Func("Leaf")
	if certificate, inferred, err := universe.CoroForeignNoBlockCertificate(leaf); err != nil ||
		inferred || certificate != (CoroForeignNoBlockCertificate{}) {
		t.Fatalf(
			"explicit contract acquired inferred legacy certificate: %+v, %t, %v",
			certificate, inferred, err,
		)
	}
	if contract, certified, err := universe.CoroCallableContractCertificate(leaf); err != nil ||
		!certified || contract.IsZero() ||
		!strings.HasPrefix(string(contract.Contract.ID), "foreign.v1/") {
		t.Fatalf("explicit contract = %+v, %t, %v", contract, certified, err)
	}
	if defaulted, err := universe.CoroLibraryEffects().
		CallableContractDefault(leaf); err != nil || defaulted {
		t.Fatalf("explicit contract defaulted = %t, %v", defaulted, err)
	}
}

func TestInferredForeignExecutorLeafProofTargetOrABIMismatchStaysConservative(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/targetc", `package targetc
//go:linkname Leaf C.target_leaf
func Leaf()
func root() { Leaf() }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	base := CoroForeignExecutorLeafProof{
		ProducerIdentity: "producer-package",
		PhysicalSymbol:   "target_leaf",
		LLVMABISignature: "void ()",
		LLVMTargetTriple: prog.TargetSpec().Triple,
		LLVMDataLayout:   prog.DataLayout(),
		CallClosure:      []string{"target_leaf"},
		ClosureSHA256:    strings.Repeat("3c", 32),
	}
	for _, test := range []struct {
		name   string
		mutate func(*CoroForeignExecutorLeafProof)
	}{
		{
			name: "architecture",
			mutate: func(proof *CoroForeignExecutorLeafProof) {
				proof.LLVMTargetTriple = "wasm32-unknown-unknown"
			},
		},
		{
			name: "data layout",
			mutate: func(proof *CoroForeignExecutorLeafProof) {
				proof.LLVMDataLayout += "-mismatch"
			},
		},
		{
			name: "LLVM ABI",
			mutate: func(proof *CoroForeignExecutorLeafProof) {
				proof.LLVMABISignature = "i32 ()"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			proof := base
			test.mutate(&proof)
			universe, err := prepareStacklessEmissionUniverseWithOptions(
				prog,
				nil,
				[]EmissionPackage{{
					SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "consumer",
				}},
				EmissionUniverseOptions{
					CoroForeignExecutorLeafProofs: []CoroForeignExecutorLeafProof{proof},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			leaf := pkg.ssa.Func("Leaf")
			if certificate, inferred, err := universe.CoroForeignNoBlockCertificate(leaf); err != nil ||
				inferred || certificate != (CoroForeignNoBlockCertificate{}) {
				t.Fatalf(
					"mismatched target certificate = %+v, %t, %v",
					certificate, inferred, err,
				)
			}
			if contract, certified, err := universe.CoroCallableContractCertificate(leaf); err != nil ||
				!certified || contract.IsZero() {
				t.Fatalf(
					"mismatched target lost conservative default: %+v, %t, %v",
					contract, certified, err,
				)
			}
			if defaulted, err := universe.CoroLibraryEffects().
				CallableContractDefault(leaf); err != nil || !defaulted {
				t.Fatalf(
					"mismatched target replaced conservative default: %t, %v",
					defaulted, err,
				)
			}
		})
	}
}

func TestInferredForeignExecutorLeafProofFailsClosed(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/badinferredc", `package badinferredc
//go:linkname Leaf C.bad_inferred_leaf
func Leaf()
func root() { Leaf() }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	for _, test := range []struct {
		name  string
		proof CoroForeignExecutorLeafProof
		want  string
	}{
		{
			name: "invalid digest",
			proof: CoroForeignExecutorLeafProof{
				ProducerIdentity: "producer", PhysicalSymbol: "bad_inferred_leaf",
				LLVMABISignature: "void ()", CallClosure: []string{"bad_inferred_leaf"},
				ClosureSHA256: "invalid",
			},
			want: "invalid SHA-256",
		},
		{
			name: "unsorted closure",
			proof: CoroForeignExecutorLeafProof{
				ProducerIdentity: "producer", PhysicalSymbol: "bad_inferred_leaf",
				LLVMABISignature: "void ()",
				CallClosure:      []string{"bad_inferred_leaf", "aaa"},
				ClosureSHA256:    strings.Repeat("00", 32),
			},
			want: "non-canonical call closure",
		},
		{
			name: "missing root",
			proof: CoroForeignExecutorLeafProof{
				ProducerIdentity: "producer", PhysicalSymbol: "bad_inferred_leaf",
				LLVMABISignature: "void ()", CallClosure: []string{"other"},
				ClosureSHA256: strings.Repeat("00", 32),
			},
			want: "omits its root",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.proof.LLVMTargetTriple = prog.TargetSpec().Triple
			test.proof.LLVMDataLayout = prog.DataLayout()
			_, err := prepareStacklessEmissionUniverseWithOptions(
				prog,
				nil,
				[]EmissionPackage{{
					SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "consumer",
				}},
				EmissionUniverseOptions{
					CoroForeignExecutorLeafProofs: []CoroForeignExecutorLeafProof{test.proof},
				},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("PrepareEmissionUniverse error = %v; want %q", err, test.want)
			}
		})
	}
}
