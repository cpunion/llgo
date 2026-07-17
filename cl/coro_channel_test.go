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
			for _, forbidden := range []string{"runtime.ChanSend\"", "runtime.ChanRecv\"", "Future", "Promise", "Task"} {
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
			} {
				if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(symbol)) {
					t.Fatalf("post-CoroSplit channel object lost ABI symbol %q", symbol)
				}
			}
		})
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
	suspend := strings.Index(body[hook:], "call i8 @llvm.coro.suspend")
	resume := strings.Index(body[hook:], "call i32 @"+coroChanResumeHookV1)
	if hook < 0 || suspend < 0 || resume < 0 || suspend >= resume {
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
	functions := []*ssa.Function{ssaPkg.Func("Send"), ssaPkg.Func("Recv"), ssaPkg.Func("RecvOK")}
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
