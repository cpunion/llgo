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
	"bytes"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroImplicitNilFaultFixture = `package foo

var Sink uint32

type Box struct { Value uint32 }
type Empty struct{}

func Cleanup() { Sink++ }
func RecoverFault() { recover() }

func Nullable(box *Box) uint32 { return box.Value }
func EmptyLoad(value *Empty) Empty { return *value }

func Guarded(box *Box) uint32 {
	if box == nil { return 0 }
	return box.Value
}

func WithCleanup(box *Box) {
	defer Cleanup()
	Sink = box.Value
}

func WithRecover(box *Box) {
	defer RecoverFault()
	Sink = box.Value
}

func StringAt(value string, index int) byte { return value[index] }

func ConstantStringAt(index int) byte { return "0123456789abcdef"[index] }

type Array4 [4]uint32

func ArrayAt(values Array4, index int) uint32 { return [4]uint32(values)[index] }

func SliceAt(values []uint32, index int) uint32 { return values[index] }

func PointerEqual(first, second *Box) bool { return first == second }
`

func TestCoroImplicitNilFieldAddrNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroImplicitNilFaultFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify implicit nil fault before CoroSplit: %v\n%s", err, module.String())
			}
			for _, name := range []string{"Nullable", "EmptyLoad", "WithCleanup"} {
				function := functions[name]
				functionPlan, ok := plan.FunctionPlan(function)
				if !ok || functionPlan.Emission != coro.EmitCoroutine || !functionPlan.Exec.Contains(coro.MayUnwind) {
					t.Fatalf("%s plan = %+v, present=%t; want may-unwind coroutine", name, functionPlan, ok)
				}
				body := requireCoroPhysicalFunction(t, module, "foo."+name).String()
				wantPrepare, wantPayload := 1, 0
				if name == "WithCleanup" {
					wantPrepare, wantPayload = 0, 1
				}
				if got := strings.Count(body, "call void @"+coroFaultPrepareHookV1); got != wantPrepare {
					t.Fatalf("%s nil-fault prepare calls = %d, want %d:\n%s", name, got, wantPrepare, body)
				}
				if got := strings.Count(body, "call void @"+coroFaultPayloadHookV1); got != wantPayload {
					t.Fatalf("%s nil-fault payload calls = %d, want %d:\n%s", name, got, wantPayload, body)
				}
				if !strings.Contains(body, "icmp eq ptr") || strings.Contains(body, "AssertNilDeref") {
					t.Fatalf("%s did not use an inline pointer guard exclusively:\n%s", name, body)
				}
				if name != "WithCleanup" {
					if hook := strings.Index(body, "call void @"+coroFaultPrepareHookV1); hook < 0 ||
						!strings.Contains(body[:hook], "store i16 5") || !strings.Contains(body[:hook], "store i16 4") {
						t.Fatalf("%s did not publish Panic/FinalSuspended before its hook:\n%s", name, body)
					}
				}
			}

			guarded := requireCoroPhysicalFunction(t, module, "foo.Guarded").String()
			if strings.Contains(guarded, coroFaultPrepareHookV1) || strings.Contains(guarded, "AssertNilDeref") {
				t.Fatalf("dominated non-nil FieldAddr retained a runtime/terminal guard:\n%s", guarded)
			}
			cleanup := requireCoroPhysicalFunction(t, module, "foo.WithCleanup").String()
			payload := strings.Index(cleanup, "call void @"+coroFaultPayloadHookV1)
			if !strings.Contains(cleanup, "switch i32") || payload < 0 || !strings.Contains(cleanup, "foo.Cleanup") ||
				!strings.Contains(cleanup, "call void @"+coroPanicPrepareHookV1) ||
				strings.Contains(cleanup, "call void @"+coroFaultPrepareHookV1) {
				t.Fatalf("implicit nil fault bypassed the static cleanup dispatcher:\n%s", cleanup)
			}
			recovering := requireCoroPhysicalFunction(t, module, "foo.WithRecover").String()
			if strings.Count(recovering, "call void @"+coroFaultPayloadHookV1) != 1 ||
				strings.Contains(recovering, "call void @"+coroFaultPrepareHookV1) ||
				countCoroIRDirectCalls(requireCoroPhysicalFunction(t, module, "foo.WithRecover"), coroAwaitPrepareHookV1) != 1 ||
				countCoroIRDirectCalls(requireCoroPhysicalFunction(t, module, "foo.RecoverFault"), coroRecoverTakeHookV1) != 1 {
				t.Fatalf("recoverable implicit fault does not use the shared panic/child transaction:\nWithRecover:\n%s\nRecoverFault:\n%s",
					recovering, requireCoroPhysicalFunction(t, module, "foo.RecoverFault").String())
			}

			runCoroABITestPipeline(t, prog, module)
			for _, name := range []string{"Nullable", "EmptyLoad", "WithCleanup"} {
				resume := module.NamedFunction("foo." + name + "$coro.resume")
				wantPrepare, wantPayload := 1, 0
				if name == "WithCleanup" {
					wantPrepare, wantPayload = 0, 1
				}
				if resume.IsNil() || strings.Count(resume.String(), "call void @"+coroFaultPrepareHookV1) != wantPrepare ||
					strings.Count(resume.String(), "call void @"+coroFaultPayloadHookV1) != wantPayload {
					t.Fatalf("post-split %s resume lost its nil-fault edge:\n%s", name, module.String())
				}
			}
			withRecover := module.NamedFunction("foo.WithRecover$coro.resume")
			recoverFault := module.NamedFunction("foo.RecoverFault$coro.resume")
			if withRecover.IsNil() || recoverFault.IsNil() ||
				strings.Count(withRecover.String(), "call void @"+coroFaultPayloadHookV1) != 1 ||
				countCoroIRDirectCalls(withRecover, coroAwaitPrepareHookV1) != 1 ||
				countCoroIRDirectCalls(recoverFault, coroRecoverTakeHookV1) != 1 {
				t.Fatalf("post-split recoverable implicit fault lost its payload/recover transaction:\n%s", module.String())
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit implicit nil-fault object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(coroFaultPrepareHookV1)) ||
				!bytes.Contains(object.Bytes(), []byte(coroFaultPayloadHookV1)) {
				t.Fatal("post-CoroSplit object lost a nil-fault hook")
			}
		})
	}
}

func TestCoroImplicitIndexAddrBoundsNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroImplicitNilFaultFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			function := functions["SliceAt"]
			functionPlan, ok := plan.FunctionPlan(function)
			if !ok || functionPlan.Emission != coro.EmitCoroutine || !functionPlan.Exec.Contains(coro.MayUnwind) {
				t.Fatalf("SliceAt plan = %+v, present=%t; want may-unwind coroutine", functionPlan, ok)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify structured IndexAddr before CoroSplit: %v\n%s", err, module.String())
			}
			body := requireCoroPhysicalFunction(t, module, "foo.SliceAt").String()
			if got := strings.Count(body, "call void @"+coroFaultPrepareHookV1); got != 1 {
				t.Fatalf("SliceAt fault prepare calls = %d, want one:\n%s", got, body)
			}
			if strings.Contains(body, "CheckIndexRange") || strings.Contains(body, "AssertIndexRange") {
				t.Fatalf("SliceAt retained a native-stack bounds helper:\n%s", body)
			}
			hook := strings.Index(body, "call void @"+coroFaultPrepareHookV1)
			if hook < 0 || !strings.Contains(body[hook:], "i32 2") {
				t.Fatalf("SliceAt did not select the index-bounds fault kind:\n%s", body)
			}
			gep := strings.Index(body, "getelementptr inbounds i32")
			if gep < 0 || hook > gep {
				t.Fatalf("SliceAt formed its element address before the terminal bounds edge:\n%s", body)
			}

			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.SliceAt$coro.resume")
			if resume.IsNil() || strings.Count(resume.String(), "call void @"+coroFaultPrepareHookV1) != 1 {
				t.Fatalf("post-split SliceAt resume lost its bounds-fault edge:\n%s", module.String())
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit structured IndexAddr object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(coroFaultPrepareHookV1)) {
				t.Fatal("post-CoroSplit object lost the bounds-fault hook")
			}
		})
	}
}

func TestCoroPurePointerEqualityNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroImplicitNilFaultFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			function := functions["PointerEqual"]
			functionPlan, ok := plan.FunctionPlan(function)
			if !ok || functionPlan.Emission != coro.EmitCoroutine {
				t.Fatalf("PointerEqual plan = %+v, present=%t; want coroutine", functionPlan, ok)
			}
			body := requireCoroPhysicalFunction(t, module, "foo.PointerEqual").String()
			if !strings.Contains(body, "icmp eq ptr") || strings.Contains(body, coroFaultPrepareHookV1) {
				t.Fatalf("pointer equality did not remain one direct non-faulting comparison:\n%s", body)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify pointer equality before CoroSplit: %v\n%s", err, module.String())
			}
			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.PointerEqual$coro.resume")
			if resume.IsNil() || !strings.Contains(resume.String(), "icmp eq ptr") {
				t.Fatalf("post-split pointer equality lost its direct comparison:\n%s", module.String())
			}
		})
	}
}

func TestCoroImplicitNilFieldAddrProofSeparatesRootFromAccess(t *testing.T) {
	prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, `package foo
type Box struct { Value uint32 }
func (box *Box) Root() uint32 { return box.Value }
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()

	var field *ssa.FieldAddr
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.FieldAddr); ok {
				field = candidate
			}
		}
	}
	if field == nil {
		t.Fatal("fixture has no FieldAddr")
	}
	if !proof.provesGuardableStableAddress(field, field) || proof.provesDominatedStableAddress(field, field) {
		t.Fatal("nullable FieldAddr did not retain separate transport/nonnull facts")
	}
	if roots := rootNames(proof.exactRetainedRoots()); len(roots) != 1 || roots[0] != "box" {
		t.Fatalf("nullable receiver is not the sole exact retained root: %v", roots)
	}
	if len(root.Params) != 1 || proof.exactRoots[root.Params[0]].kind != coroFrameRetentionRootReceiver {
		t.Fatalf("nullable method parameter was not classified as the receiver root: %+v", proof.exactRoots)
	}
	if reason := audit.validateFieldAddr(field); !strings.Contains(reason, "non-nil") {
		t.Fatalf("legacy audit accepted nullable FieldAddr or changed fail-closed reason: %q", reason)
	}
	audit.allowImplicitNilFault = true
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if handled, reason := audit.validate(instruction); handled && reason != "" {
				t.Fatalf("explicit-status instruction %T %q rejected: %s", instruction, instruction, reason)
			}
		}
	}
}

func compileCoroImplicitNilFaultFixture(
	t *testing.T,
	target *llssa.Target,
) (llssa.Program, llssa.Package, *coro.SSAPlan, map[string]*ssa.Function) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroImplicitNilFaultFixture)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	functions := map[string]*ssa.Function{
		"Nullable":         ssaPkg.Func("Nullable"),
		"EmptyLoad":        ssaPkg.Func("EmptyLoad"),
		"Guarded":          ssaPkg.Func("Guarded"),
		"WithCleanup":      ssaPkg.Func("WithCleanup"),
		"RecoverFault":     ssaPkg.Func("RecoverFault"),
		"WithRecover":      ssaPkg.Func("WithRecover"),
		"StringAt":         ssaPkg.Func("StringAt"),
		"ConstantStringAt": ssaPkg.Func("ConstantStringAt"),
		"ArrayAt":          ssaPkg.Func("ArrayAt"),
		"SliceAt":          ssaPkg.Func("SliceAt"),
		"PointerEqual":     ssaPkg.Func("PointerEqual"),
	}
	roots := make(coro.Roots, 0, len(functions))
	for _, function := range functions {
		roots = append(roots, coro.Root{Function: function, Demand: coro.AsyncDemand})
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, roots, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			for _, root := range functions {
				if function == root {
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
				}
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.CoroProfile = CoroProfileStackless
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, functions
}
