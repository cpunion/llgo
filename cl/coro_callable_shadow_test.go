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
)

const coroCallableContractWorkerFixture = `package contractworker

//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(fn any) uintptr

//llgo:link raw llgo.syscall
func raw(fn, a0 uintptr) (uintptr, uintptr, uintptr)

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/1
func libc_contract_worker_v1_trampoline()

func Fixed(a0 uintptr) uintptr {
	r1, _, _ := raw(funcPCABI0(libc_contract_worker_v1_trampoline), a0)
	return r1
}
`

const coroCallableShadowManagedCodeAddressFixture = `package managedcodeaddr

//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(fn any) uintptr

//llgo:link funcPCABIInternal llgo.funcPCABIInternal
func funcPCABIInternal(fn any) uintptr

//llgo:link raw llgo.syscall
func raw(fn, a0 uintptr) (uintptr, uintptr, uintptr)

func managedTarget() {}

func ObserveABI0() uintptr {
	return funcPCABI0(managedTarget)
}

func ObserveABIInternal() uintptr {
	return funcPCABIInternal(managedTarget)
}

func MisuseAsWorker(a0 uintptr) uintptr {
	r1, _, _ := raw(funcPCABIInternal(managedTarget), a0)
	return r1
}
`

func TestCoroCallableShadowClassifiesManagedFuncPCAsCodeAddressOnly(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/managedcodeaddr", coroCallableShadowManagedCodeAddressFixture)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
		EmissionUniverseOptions{CoroProfile: CoroProfileStackless, CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := AnalyzeCoroCallableShadows(universe)
	if err != nil {
		t.Fatal(err)
	}

	const wantReason = "managed-go-code-address-is-not-worker-callable"
	for _, name := range []string{"ObserveABI0", "ObserveABIInternal", "MisuseAsWorker"} {
		producer := exactIntrinsicOpcodeCall(t, universe, pkg.ssa.Func(name), llgoFuncPCABI0)
		if shadow, ok := analysis.Producer(producer); ok {
			t.Fatalf("%s managed FuncPC producer unexpectedly received worker shadow %+v", name, shadow)
		}
		if reason, ok := analysis.ProducerRejection(producer); !ok || reason != wantReason {
			t.Fatalf("%s managed FuncPC rejection = %q, %t; want %q", name, reason, ok, wantReason)
		}
	}

	call := exactWorkerSyscallCall(t, universe, pkg.ssa.Func("MisuseAsWorker"))
	sink, ok := analysis.Sink(call)
	if !ok || sink.Certified || sink.Reason != wantReason || len(sink.Candidates) != 0 {
		t.Fatalf("managed FuncPC worker sink = %+v, %t; want exact fail-closed rejection", sink, ok)
	}
	if certificate, certified, err := universe.CoroWorkerSyscallCertificate(call); err != nil || certified || certificate.ID != "" {
		t.Fatalf("managed FuncPC worker certificate = %+v, %t, %v; want absent, false, nil", certificate, certified, err)
	}
}

func TestCoroCallableShadowFlowsForwardFromFuncPCABI0(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/callableshadow", coroWorkerSyscallCapabilityFixture)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
		EmissionUniverseOptions{CoroProfile: CoroProfileStackless, CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := AnalyzeCoroCallableShadows(universe)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		certified bool
		arity     int
		reason    string
	}{
		{name: "Fixed", certified: true, arity: 1},
		{name: "FixedSix", certified: true, arity: 6},
		{name: "privateCarrier", certified: true, arity: 1},
		{name: "privateMixedCarrier", certified: true, arity: 1},
		{name: "Arbitrary", certified: false, arity: 1, reason: "open-or-escaped-parameter-carrier"},
		{name: "ExportedCarrier", certified: false, arity: 1, reason: "open-or-escaped-parameter-carrier"},
		{name: "privateEscapedCarrier", certified: false, arity: 1, reason: "open-or-escaped-parameter-carrier"},
		{name: "Arithmetic", certified: false, arity: 1, reason: "arithmetic"},
		{name: "Incompatible", certified: false, arity: 1, reason: "abi-mismatch"},
	} {
		call := exactWorkerSyscallCall(t, universe, pkg.ssa.Func(test.name))
		sink, ok := analysis.Sink(call)
		if !ok {
			t.Fatalf("%s has no callable-shadow sink result", test.name)
		}
		if sink.ABI.Family != coroCallableShadowWorkerSyscallFamily || sink.ABI.WordArgs != test.arity {
			t.Fatalf("%s sink ABI = %+v; want family %q arity %d", test.name, sink.ABI, coroCallableShadowWorkerSyscallFamily, test.arity)
		}
		if sink.Certified != test.certified {
			t.Fatalf("%s certified = %t, reason=%q, candidates=%+v, incoming=%+v; want %t", test.name, sink.Certified, sink.Reason, sink.Candidates, sink.Incoming, test.certified)
		}
		if test.reason != "" && !strings.Contains(sink.Reason, test.reason) {
			t.Fatalf("%s reason = %q; want substring %q", test.name, sink.Reason, test.reason)
		}
	}
}

func TestCoroCallableShadowBindsABIAtProducerAndKeepsConditionalEdges(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/callableshadowconditional", coroWorkerSyscallCapabilityFixture)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
		EmissionUniverseOptions{CoroProfile: CoroProfileStackless, CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := AnalyzeCoroCallableShadows(universe)
	if err != nil {
		t.Fatal(err)
	}

	okProducer := exactIntrinsicOpcodeCall(t, universe, pkg.ssa.Func("ThroughMixedCarrierOK"), llgoFuncPCABI0)
	okShadow, ok := analysis.Producer(okProducer)
	if !ok {
		t.Fatal("safe FuncPCABI0 producer did not receive a compiler shadow")
	}
	if okShadow.Target == nil || okShadow.Target.Name() != "libc_fixed_worker_v1_trampoline" ||
		okShadow.ABI != (CoroCallableShadowABI{Family: coroCallableShadowWorkerSyscallFamily, WordArgs: 1}) {
		t.Fatalf("safe producer shadow = %+v", okShadow)
	}

	wrongProducer := exactIntrinsicOpcodeCall(t, universe, pkg.ssa.Func("ThroughMixedCarrierWrong"), llgoFuncPCABI0)
	wrongShadow, ok := analysis.Producer(wrongProducer)
	if !ok {
		t.Fatal("incompatible FuncPCABI0 producer did not receive its independent compiler shadow")
	}
	if wrongShadow.ABI.WordArgs != 0 {
		t.Fatalf("wrong producer ABI = %+v; want producer-declared arity 0", wrongShadow.ABI)
	}

	call := exactWorkerSyscallCall(t, universe, pkg.ssa.Func("privateMixedCarrier"))
	sink, ok := analysis.Sink(call)
	if !ok || !sink.Certified {
		t.Fatalf("conditional sink = %+v, %t; want conditionally certified", sink, ok)
	}
	if len(sink.Incoming) != 2 {
		t.Fatalf("conditional incoming edge count = %d; want 2 (%+v)", len(sink.Incoming), sink.Incoming)
	}
	certified, rejected := 0, 0
	for _, edge := range sink.Incoming {
		if edge.Certified {
			certified++
		} else {
			rejected++
			if !strings.Contains(edge.Reason, "abi-mismatch") {
				t.Fatalf("rejected conditional edge reason = %q; want ABI mismatch", edge.Reason)
			}
		}
	}
	if certified != 1 || rejected != 1 {
		t.Fatalf("conditional edge inventory certified=%d rejected=%d; want 1/1", certified, rejected)
	}
}

func TestCoroCallableShadowRejectsUnannotatedProducerWithoutAddressRecovery(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/callableshadowunknown", coroWorkerSyscallCapabilityFixture)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
		EmissionUniverseOptions{CoroProfile: CoroProfileStackless, CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := AnalyzeCoroCallableShadows(universe)
	if err != nil {
		t.Fatal(err)
	}
	producer := exactIntrinsicOpcodeCall(t, universe, pkg.ssa.Func("Uncertified"), llgoFuncPCABI0)
	if shadow, ok := analysis.Producer(producer); ok {
		t.Fatalf("unannotated producer unexpectedly received shadow %+v", shadow)
	}
	if reason, ok := analysis.ProducerRejection(producer); !ok || reason != "target-lacks-workeraddr" {
		t.Fatalf("unannotated producer rejection = %q, %t; want target-lacks-workeraddr", reason, ok)
	}
	sink, ok := analysis.Sink(exactWorkerSyscallCall(t, universe, pkg.ssa.Func("Uncertified")))
	if !ok || sink.Certified || sink.Reason != "target-lacks-workeraddr" {
		t.Fatalf("unannotated sink = %+v, %t; want producer-side fail-closed result", sink, ok)
	}
}

func TestCoroCallableShadowRejectsDynamicTrapDispatcherWithoutOperationProof(t *testing.T) {
	testProg := newEmissionTestProgram()
	const packagePath = "example.com/emission/dynamictrap"
	pkg := testProg.addPackage(t, packagePath, `package dynamictrap
//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(fn any) uintptr
//llgo:link raw llgo.syscall
func raw(fn, trap, a0, a1, a2 uintptr) (uintptr, uintptr, uintptr)
func libc_arbitrary_trap_dispatcher_trampoline()
func RawSyscall(trap, a0, a1, a2 uintptr) uintptr {
	r1, _, _ := raw(funcPCABI0(libc_arbitrary_trap_dispatcher_trampoline), trap, a0, a1, a2)
	return r1
}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	prog.SetLinkname(packagePath+".libc_arbitrary_trap_dispatcher_trampoline", "C.__arbitrary_trap_dispatcher")
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
		EmissionUniverseOptions{CoroProfile: CoroProfileStackless, CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := AnalyzeCoroCallableShadows(universe)
	if err != nil {
		t.Fatal(err)
	}
	producer := exactIntrinsicOpcodeCall(t, universe, pkg.ssa.Func("RawSyscall"), llgoFuncPCABI0)
	if shadow, ok := analysis.Producer(producer); ok {
		t.Fatalf("arbitrary trap dispatcher unexpectedly received worker shadow %+v", shadow)
	}
	if reason, ok := analysis.ProducerRejection(producer); !ok || reason != "target-lacks-workeraddr" {
		t.Fatalf("arbitrary trap producer rejection = %q, %t; want target-lacks-workeraddr", reason, ok)
	}
	// StaticCodeAddress is only the occurrence proof that FuncPCABI0 consumes
	// the exact target without materializing a managed interface. It is not a
	// worker-call capability: the independent callable shadow and operation
	// certificate checks above and below must still reject this dispatcher.
	if observed, err := universe.CoroStaticCodeAddressCallArgument(producer, 0); err != nil || !observed {
		t.Fatalf("arbitrary trap dispatcher static code-address occurrence = %t, %v; want true, nil", observed, err)
	}
	call := exactWorkerSyscallCall(t, universe, pkg.ssa.Func("RawSyscall"))
	if sink, ok := analysis.Sink(call); !ok || sink.Certified || sink.Reason != "target-lacks-workeraddr" {
		t.Fatalf("arbitrary trap worker sink = %+v, %t; want exact fail-closed rejection", sink, ok)
	}
	if certificate, certified, err := universe.CoroWorkerSyscallCertificate(call); err != nil || certified || certificate.ID != "" {
		t.Fatalf("arbitrary trap worker certificate = %+v, %t, %v; want absent, false, nil", certificate, certified, err)
	}
}

func TestCoroCallableShadowGenericContractAuthorizesProductionWorkerCertificate(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/callableshadowcontract", coroCallableContractWorkerFixture)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
		EmissionUniverseOptions{CoroProfile: CoroProfileStackless, CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	producer := exactIntrinsicOpcodeCall(t, universe, pkg.ssa.Func("Fixed"), llgoFuncPCABI0)
	analysis, err := AnalyzeCoroCallableShadows(universe)
	if err != nil {
		t.Fatal(err)
	}
	shadow, ok := analysis.Producer(producer)
	if !ok || shadow.ContractCertificateID == "" || shadow.LegacyWorkerAddressCompat ||
		shadow.ABI != (CoroCallableShadowABI{Family: coroCallableShadowWorkerSyscallFamily, WordArgs: 1}) {
		reason, rejected := analysis.ProducerRejection(producer)
		t.Fatalf("generic contract producer shadow = %+v, %t; rejection=%q,%t", shadow, ok, reason, rejected)
	}
	call := exactWorkerSyscallCall(t, universe, pkg.ssa.Func("Fixed"))
	certificate, certified, err := universe.CoroWorkerSyscallCertificate(call)
	if err != nil || !certified || certificate.ID == "" || certificate.CallableShadowSetID == "" ||
		certificate.StaticTargetCount != 1 {
		t.Fatalf("generic contract worker certificate = %+v, %t, %v", certificate, certified, err)
	}

	sink, ok := analysis.Sink(call)
	if !ok || len(sink.Candidates) != 1 {
		t.Fatalf("generic contract sink = %+v, %t", sink, ok)
	}
	sink.Candidates[0].PhysicalSymbol += "_forged"
	opcode, intrinsic, opcodeErr := universe.coroIntrinsicOpcode(call.Common().StaticCallee())
	if opcodeErr != nil || !intrinsic {
		t.Fatalf("worker opcode = %d, %t, %v", opcode, intrinsic, opcodeErr)
	}
	if _, _, _, err := freezeCoroWorkerSyscallShadowCertificate(universe, call, opcode, sink); err == nil ||
		!strings.Contains(err.Error(), "differs from its exact producer contract") {
		t.Fatalf("forged forward shadow target was not rejected: %v", err)
	}
}

func TestCoroWorkerCallableGenericContractEligibilityIsExact(t *testing.T) {
	for _, test := range []struct {
		name       string
		properties string
		want       bool
		wantArity  int
	}{
		{"valid", "progress=may-block affinity=any-thread reentry=none memory=by-value abi=word-call.v1/3", true, 3},
		{"progress", "progress=executor-safe affinity=any-thread reentry=none memory=by-value abi=word-call.v1/3", false, 0},
		{"affinity", "progress=may-block affinity=caller-thread reentry=none memory=by-value abi=word-call.v1/3", false, 0},
		{"reentry", "progress=may-block affinity=any-thread reentry=managed-callback memory=by-value abi=word-call.v1/3", false, 0},
		{"unknown memory", "progress=may-block affinity=any-thread reentry=none memory=unknown abi=word-call.v1/3", false, 0},
		{"retained memory", "progress=may-block affinity=any-thread reentry=none memory=retained abi=word-call.v1/3", false, 0},
		{"implicit ABI", "progress=may-block affinity=any-thread reentry=none memory=by-value", false, 0},
		{"other ABI", "progress=may-block affinity=any-thread reentry=none memory=by-value abi=typed.v1/3", false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			pkg := testProg.addPackage(t, "example.com/emission/callableeligibility", `package callableeligibility
//llgo:coro contract foreign.v1 scope=declaration `+test.properties+`
func libc_eligibility_v1_trampoline()
`)
			testProg.ssa.Build()
			arity, ok, err := coroWorkerCallableDeclarationContractArity(pkg.ssa.Func("libc_eligibility_v1_trampoline"))
			if err != nil || ok != test.want || arity != test.wantArity {
				t.Fatalf("eligibility = %d, %t, %v; want %d, %t, nil", arity, ok, err, test.wantArity, test.want)
			}
		})
	}
}
