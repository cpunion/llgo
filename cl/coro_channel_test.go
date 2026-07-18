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
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroChannelTestSource = `package foo

func Send(ch chan uint32, value uint32) {
	ch <- value
}

func Recv(ch chan uint32) uint32 {
	return <-ch
}

func RecvOK(ch chan uint32) (uint32, bool) {
	value, ok := <-ch
	return value, ok
}

func Select(first, second chan uint32, value uint32) (int, uint32, bool) {
	select {
	case first <- value:
		return 0, 0, true
	case received, ok := <-second:
		return 1, received, ok
	}
}

func TrySelectThenRecv(first, second chan uint32, value uint32) (int, uint32, bool) {
	selected := -1
	var received uint32
	var ok bool
	select {
	case first <- value:
		selected = 0
	case received, ok = <-second:
		selected = 1
	default:
	}
	received += <-second
	return selected, received, ok
}

func EmptySelect() {
	select {}
}
`

func TestCoroChannelNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroChannelFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			for _, fn := range functions {
				functionPlan, ok := plan.FunctionPlan(fn)
				if !ok || functionPlan.Emission != coro.EmitCoroutine || functionPlan.FuncRep != coro.DirectCoro ||
					functionPlan.Demand != coro.AsyncDemand || !functionPlan.Effect.Contains(coro.MayPark) {
					t.Fatalf("%s plan = %+v, present=%t; want async direct may-park coroutine", fn.Name(), functionPlan, ok)
				}
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify channel coroutine before CoroSplit: %v\n%s", err, module.String())
			}

			send := requireCoroPhysicalFunction(t, module, "foo.Send").String()
			assertCoroChannelBody(t, "Send", send, coroChanSendParkHookV1, []uint64{
				coroChanResumeSendOK,
				coroChanResumeSendClosed,
				coroChanResumeTaskAbort,
				coroChanResumeShutdown,
			})
			for _, symbol := range []string{"github.com/goplus/llgo/runtime/internal/runtime.CoroChanTrySend", coroChanSendClosedPanicHookV1} {
				if !strings.Contains(send, symbol) {
					t.Fatalf("Send coroutine lacks %q:\n%s", symbol, send)
				}
			}
			for _, name := range []string{"Recv", "RecvOK"} {
				recv := requireCoroPhysicalFunction(t, module, "foo."+name).String()
				assertCoroChannelBody(t, name, recv, coroChanRecvParkHookV1, []uint64{
					coroChanResumeRecvOK,
					coroChanResumeRecvClosed,
					coroChanResumeTaskAbort,
					coroChanResumeShutdown,
				})
				if !strings.Contains(recv, "@\"github.com/goplus/llgo/runtime/internal/runtime.CoroChanTryRecv\"") {
					t.Fatalf("%s coroutine lacks nonblocking receive helper:\n%s", name, recv)
				}
			}
			selectBody := requireCoroPhysicalFunction(t, module, "foo.Select").String()
			assertCoroSelectBody(t, selectBody)
			emptySelectBody := requireCoroPhysicalFunction(t, module, "foo.EmptySelect").String()
			assertCoroSelectBody(t, emptySelectBody)
			trySelectBody := requireCoroPhysicalFunction(t, module, "foo.TrySelectThenRecv").String()
			assertCoroTrySelectBody(t, trySelectBody)
			for _, forbidden := range []string{"runtime.ChanSend\"", "runtime.ChanRecv\"", "runtime.Select\"", "Future", "Promise", "Task"} {
				if strings.Contains(module.String(), forbidden) {
					t.Fatalf("channel lowering retained forbidden abstraction %q:\n%s", forbidden, module.String())
				}
			}

			runCoroABITestPipeline(t, prog, module)
			for _, name := range []string{"foo.Send$coro", "foo.Recv$coro", "foo.RecvOK$coro"} {
				resume := module.NamedFunction(name + ".resume")
				if resume.IsNil() || !strings.Contains(resume.String(), "call i32 @"+coroChanResumeHookV1) {
					t.Fatalf("CoroSplit lost channel resume dispatch in %s:\n%s", name, module.String())
				}
			}
			selectResume := module.NamedFunction("foo.Select$coro.resume")
			if selectResume.IsNil() || !strings.Contains(
				selectResume.String(),
				"github.com/goplus/llgo/runtime/internal/runtime.CoroChanSelectResume",
			) {
				t.Fatalf("CoroSplit lost channel select resume dispatch:\n%s", module.String())
			}
			emptySelectResume := module.NamedFunction("foo.EmptySelect$coro.resume")
			if emptySelectResume.IsNil() || !strings.Contains(
				emptySelectResume.String(),
				"github.com/goplus/llgo/runtime/internal/runtime.CoroChanSelectResume",
			) {
				t.Fatalf("CoroSplit lost empty channel select cancellation dispatch:\n%s", module.String())
			}
			trySelectResume := module.NamedFunction("foo.TrySelectThenRecv$coro.resume")
			if trySelectResume.IsNil() || strings.Count(
				trySelectResume.String(),
				"github.com/goplus/llgo/runtime/internal/runtime.CoroChanSelectTry",
			) != 1 || strings.Contains(
				trySelectResume.String(),
				"github.com/goplus/llgo/runtime/internal/runtime.CoroChanSelectPark",
			) {
				t.Fatalf("CoroSplit changed nonblocking channel select into a physical park:\n%s", module.String())
			}
			for _, intrinsic := range []string{"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend", "llvm.coro.end"} {
				if hasLLVMCall(module.String(), intrinsic) {
					t.Fatalf("post-split channel module still calls %s:\n%s", intrinsic, module.String())
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit post-CoroSplit channel object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			for _, symbol := range []string{
				coroChanSendParkHookV1,
				coroChanRecvParkHookV1,
				coroChanResumeHookV1,
				coroChanSendClosedPanicHookV1,
				"github.com/goplus/llgo/runtime/internal/runtime.CoroChanSelectTry",
				"github.com/goplus/llgo/runtime/internal/runtime.CoroChanSelectPark",
				"github.com/goplus/llgo/runtime/internal/runtime.CoroChanSelectResume",
			} {
				if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(symbol)) {
					t.Fatalf("post-CoroSplit channel object lost ABI symbol %q", symbol)
				}
			}
		})
	}
}

func assertCoroSelectBody(t *testing.T, body string) {
	t.Helper()
	if got := strings.Count(body, "call i8 @llvm.coro.suspend"); got != 3 {
		t.Fatalf("Select coro.suspend calls = %d, want initial + select + final:\n%s", got, body)
	}
	for _, symbol := range []string{
		"github.com/goplus/llgo/runtime/internal/runtime.CoroChanSelectTry",
		"github.com/goplus/llgo/runtime/internal/runtime.CoroChanSelectPark",
		"github.com/goplus/llgo/runtime/internal/runtime.CoroChanSelectResume",
	} {
		if got := strings.Count(body, symbol); got != 1 {
			t.Fatalf("Select references to %q = %d, want 1:\n%s", symbol, got, body)
		}
	}
	for _, status := range []uint64{
		coroChanResumeSendOK,
		coroChanResumeRecvOK,
		coroChanResumeRecvClosed,
		coroChanResumeSendClosed,
		coroChanResumeTaskAbort,
		coroChanResumeShutdown,
	} {
		if !regexp.MustCompile(`(?m)^\s+i32 ` + strconv.FormatUint(status, 10) + `, label `).MatchString(body) {
			t.Fatalf("Select resume dispatch lacks status %d:\n%s", status, body)
		}
	}
	park := strings.Index(body, "runtime.CoroChanSelectPark")
	if park < 0 {
		t.Fatalf("Select does not publish its physical cases:\n%s", body)
	}
	suspend := strings.Index(body[park:], "call i8 @llvm.coro.suspend")
	resume := strings.Index(body[park:], "runtime.CoroChanSelectResume")
	if suspend < 0 || resume < 0 || suspend >= resume {
		t.Fatalf("Select does not publish all cases before suspend and clean them after resume:\n%s", body)
	}
}

func assertCoroTrySelectBody(t *testing.T, body string) {
	t.Helper()
	if got := strings.Count(body, "runtime.CoroChanSelectTry"); got != 1 {
		t.Fatalf("TrySelectThenRecv select-try calls = %d, want 1:\n%s", got, body)
	}
	for _, forbidden := range []string{"runtime.CoroChanSelectPark", "runtime.CoroChanSelectResume"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("TrySelectThenRecv nonblocking select uses %q:\n%s", forbidden, body)
		}
	}
	if got := strings.Count(body, "call i8 @llvm.coro.suspend"); got != 3 {
		t.Fatalf("TrySelectThenRecv coro.suspend calls = %d, want initial + trailing receive + final:\n%s", got, body)
	}
}

func assertCoroChannelBody(t *testing.T, name, body, parkHook string, statuses []uint64) {
	t.Helper()
	if got := strings.Count(body, "call i8 @llvm.coro.suspend"); got != 3 {
		t.Fatalf("%s coro.suspend calls = %d, want initial + channel + final:\n%s", name, got, body)
	}
	for _, symbol := range []string{parkHook, coroChanResumeHookV1} {
		if got := strings.Count(body, "@"+symbol); got != 1 {
			t.Fatalf("%s references to %q = %d, want 1:\n%s", name, symbol, got, body)
		}
	}
	if !strings.Contains(body, "switch i32") {
		t.Fatalf("%s has no exact typed resume-status dispatch:\n%s", name, body)
	}
	dispatch := regexp.MustCompile(
		`(?s)call i32 @` + regexp.QuoteMeta(coroChanResumeHookV1) + `\([^\n]+\)\n\s+switch i32 [^\[]+\[(.*?)\]`,
	).FindStringSubmatch(body)
	if len(dispatch) != 2 {
		t.Fatalf("%s has no isolated channel resume switch:\n%s", name, body)
	}
	for _, status := range statuses {
		if !regexp.MustCompile(`(?m)^\s+i32 ` + strconv.FormatUint(status, 10) + `, label `).MatchString(dispatch[1]) {
			t.Fatalf("%s channel resume switch lacks status %d:\n%s", name, status, dispatch[0])
		}
	}
	hook := strings.Index(body, "call void @"+parkHook)
	if hook < 0 {
		t.Fatalf("%s does not publish its physical park:\n%s", name, body)
	}
	suspend := strings.Index(body[hook:], "call i8 @llvm.coro.suspend")
	resume := strings.Index(body[hook:], "call i32 @"+coroChanResumeHookV1)
	if suspend < 0 || resume < 0 || suspend >= resume {
		t.Fatalf("%s does not publish park before suspend and dispatch after resume:\n%s", name, body)
	}
}

func compileCoroChannelFixture(t *testing.T, target *llssa.Target) (
	llssa.Program, llssa.Package, *coro.SSAPlan, []*ssa.Function,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroChannelTestSource)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := PrepareEmissionUniverseWithOptions(
		prog,
		nil,
		[]EmissionPackage{{SSA: ssaPkg, Files: files}},
		EmissionUniverseOptions{EnableCoroChannel: true},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	functions := []*ssa.Function{
		ssaPkg.Func("Send"), ssaPkg.Func("Recv"), ssaPkg.Func("RecvOK"),
		ssaPkg.Func("Select"), ssaPkg.Func("TrySelectThenRecv"), ssaPkg.Func("EmptySelect"),
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelABIV0
	functionIDs.ArchiveReady = true
	roots := make(coro.Roots, 0, len(functions))
	for _, fn := range functions {
		roots = append(roots, coro.Root{Function: fn, Demand: coro.AsyncDemand})
	}
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, roots, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{
		CoroPlan:                      plan,
		EmissionUniverse:              universe,
		EnableCoroEntryResolution:     true,
		EnableCoroPhysicalABI:         true,
		EnableCoroChildAwait:          true,
		EnableCoroProgramBootstrapRun: true,
		EnableCoroChannel:             true,
		CoroABI:                       coro.PhysicalABIV1,
		SchedulerABI:                  coro.SchedulerProgramBootstrapChannelABIV0,
		PanicABI:                      coro.PanicLegacyABIV0,
		FuncRepABI:                    coro.FuncRepABIV0,
	}
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

func TestCoroChannelCompilationCapabilityFailsClosed(t *testing.T) {
	compilation := &Compilation{EnableCoroChannel: true}
	if err := compilation.preflightCoroPlan(); err == nil || !strings.Contains(err.Error(), "requires runnable PhysicalABIV1") {
		t.Fatalf("channel capability dependency error = %v", err)
	}
	compilation = &Compilation{
		EnableCoroEntryResolution:     true,
		EnableCoroPhysicalABI:         true,
		EnableCoroChildAwait:          true,
		EnableCoroProgramBootstrapRun: true,
		EnableCoroChannel:             true,
		CoroABI:                       coro.PhysicalABIV1,
		SchedulerABI:                  coro.SchedulerProgramBootstrapABIV2,
		PanicABI:                      coro.PanicLegacyABIV0,
		FuncRepABI:                    coro.FuncRepABIV0,
	}
	if err := compilation.validateCoroABIIdentity(false); err == nil || !strings.Contains(err.Error(), "scheduler ABI") {
		t.Fatalf("channel scheduler identity error = %v", err)
	}
}

func TestCoroChannelPhysicalABIRejectsNilSelectChannel(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog, pkg, plan, functions := compileCoroChannelFixture(t, nil)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	var selectFn *ssa.Function
	for _, fn := range functions {
		if fn.Name() == "Select" {
			selectFn = fn
			break
		}
	}
	if selectFn == nil {
		t.Fatal("Select function not found")
	}
	var instruction *ssa.Select
	for _, block := range selectFn.Blocks {
		for _, candidate := range block.Instrs {
			if candidate, ok := candidate.(*ssa.Select); ok {
				instruction = candidate
				break
			}
		}
	}
	if instruction == nil || len(instruction.States) == 0 || instruction.States[0] == nil {
		t.Fatal("Select instruction has no concrete channel case")
	}
	instruction.States[0].Chan = nil
	functionPlan, ok := plan.FunctionPlan(selectFn)
	if !ok {
		t.Fatal("Select function plan not found")
	}
	err := validateCoroPhysicalABIWithUniverseCapabilitiesFrameRetentionAndChannel(
		selectFn, functionPlan, plan, nil, true, true, false, false, "", true,
	)
	if err == nil || !strings.Contains(err.Error(), "channel select case 0 channel is nil") {
		t.Fatalf("nil select channel validation error = %v", err)
	}
}
