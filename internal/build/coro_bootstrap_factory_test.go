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

package build

import (
	"bytes"
	"go/types"
	"regexp"
	"strings"
	"testing"

	llssa "github.com/goplus/llgo/ssa"
	llvm "github.com/xgo-dev/llvm"
)

func TestCoroProgramBootstrapFactoryV2MixedNativeAndWasm(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	tests := []struct {
		name      string
		target    *llssa.Target
		uintptrIR string
	}{
		{name: "native", uintptrIR: "i64"},
		{name: "wasm", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, uintptrIR: "i32"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := llssa.NewProgram(test.target)
			defer prog.Dispose()
			pkg := prog.NewPackage("entry", "entry")
			defer pkg.Module().Dispose()

			bootstrap, targets, tableSteps, finalHash := newCoroProgramBootstrapFactoryFixtureV2(pkg)
			factory := emitCoroProgramBootstrapFactoryV2(pkg, bootstrap, targets, finalHash, false)
			pkg.NewCoroProgramBootstrap("__llgo_test_program_bootstrap_v2", llssa.CoroProgramBootstrapOptions{
				Version: coroProgramBootstrapVersionV2,
				ABIHash: finalHash,
				Steps:   tableSteps,
				Factory: factory.Expr,
			})

			mod := pkg.Module()
			mod.SetDataLayout(prog.DataLayout())
			mod.SetTarget(prog.TargetSpec().Triple)
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify mixed v2 bootstrap factory before CoroSplit: %v\n%s", err, mod.String())
			}
			pre := mod.String()
			assertCoroProgramBootstrapFactoryPresplitV2(t, pre, test.uintptrIR)

			options := llvm.NewPassBuilderOptions()
			options.SetVerifyEach(true)
			if err := mod.RunPasses("coro-early,cgscc(coro-split),coro-cleanup", prog.TargetMachine(), options); err != nil {
				options.Dispose()
				t.Fatalf("CoroSplit mixed v2 bootstrap factory: %v\n%s", err, mod.String())
			}
			options.Dispose()
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify mixed v2 bootstrap factory after CoroSplit: %v\n%s", err, mod.String())
			}
			post := mod.String()
			for _, suffix := range []string{".resume", ".destroy"} {
				if mod.NamedFunction(coroProgramBootstrapFactorySymbolV2 + suffix).IsNil() {
					t.Fatalf("CoroSplit did not create mixed v2 bootstrap factory%s:\n%s", suffix, post)
				}
			}
			assertCoroProgramRunDecisionResumeOnly(t, mod, coroProgramBootstrapFactorySymbolV2, 3)
			runCoroProgramPostSplitSimplifyCFG(t, prog, mod)
			assertCoroProgramRunDecisionDeadClonesEliminated(t, mod, coroProgramBootstrapFactorySymbolV2)
			post = mod.String()
			for _, intrinsic := range []string{"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend"} {
				if regexp.MustCompile(`call [^\n]*@` + regexp.QuoteMeta(intrinsic) + `\b`).MatchString(post) {
					t.Fatalf("post-split mixed v2 bootstrap still calls %s:\n%s", intrinsic, post)
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(mod, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit mixed v2 bootstrap factory object: %v\n%s", err, post)
			}
			if !bytes.Contains(object.Bytes(), []byte(coroRunDecisionTakeSymbolV1)) {
				object.Dispose()
				t.Fatalf("mixed v2 bootstrap object lost unresolved run-decision ABI symbol %q", coroRunDecisionTakeSymbolV1)
			}
			object.Dispose()
		})
	}
}

func TestCoroProgramBootstrapFactoryV2MainReturnIsOnlyOnCoroMainContinuation(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("entry", "entry")
	defer pkg.Module().Dispose()

	bootstrap, targets, _, finalHash := newCoroProgramBootstrapFactoryFixtureV2(pkg)
	const anchor = "__llgo_coro_root_package_v1.0123456789abcdef0123456789abcdef"
	bootstrap.Steps[4] = coroProgramBootstrapStepV1{
		Kind: coroProgramStepCoroRootV1, Role: coroProgramStepRoleMainV2,
		FunctionID: "main-coro-id", Target: "example.com/program.main$coro",
		Owner: "example.com/program", CatalogTarget: anchor, Aux: 1,
	}
	targets[4] = coroProgramBootstrapFactoryTargetV2{Anchor: targets[3].Anchor}
	factory := emitCoroProgramBootstrapFactoryV2(pkg, bootstrap, targets, finalHash, true)
	body := pkg.Module().NamedFunction(factory.Name()).String()
	if got := strings.Count(body, "call void @"+coroProgramAwaitPrepareHookV2); got != 3 {
		t.Fatalf("coroutine-main await calls = %d, want 3:\n%s", got, body)
	}
	if got := strings.Count(body, "call i32 @"+coroProgramAwaitConsumeHookV1); got != 3 {
		t.Fatalf("coroutine-main completion consumes = %d, want 3:\n%s", got, body)
	}
	if got := strings.Count(body, "i32 6, label"); got != 3 {
		t.Fatalf("coroutine-main Goexit routes = %d, want 3:\n%s", got, body)
	}
	if got := strings.Count(body, "call void @"+coroProgramMainReturnSymbolV1); got != 1 {
		t.Fatalf("coroutine-main return calls = %d, want 1:\n%s", got, body)
	}
	lastAwait := strings.LastIndex(body, "call void @"+coroProgramAwaitPrepareHookV2)
	lastDecision := strings.LastIndex(body, "call void @"+coroRunDecisionTakeSymbolV1)
	mainReturn := strings.Index(body, "call void @"+coroProgramMainReturnSymbolV1)
	if lastAwait < 0 || lastDecision < 0 || mainReturn < 0 ||
		!(lastAwait < lastDecision && lastDecision < mainReturn) {
		t.Fatalf("main-return cancellation is not on the normal post-await continuation:\n%s", body)
	}
	if got := strings.Count(body, "call void @"+coroProgramCompletePrepareHookV2); got != 1 {
		t.Fatalf("bootstrap terminal completion calls = %d, want 1:\n%s", got, body)
	}
	assertCoroProgramZeroRunDecisionCalls(t, body, 4)
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify coroutine-main return factory: %v\n%s", err, pkg.Module().String())
	}
}

func TestCoroProgramBootstrapFactoryV2MainReturnFollowsPlainMain(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	pkg := prog.NewPackage("entry", "entry")
	defer pkg.Module().Dispose()

	bootstrap, targets, _, finalHash := newCoroProgramBootstrapFactoryFixtureV2(pkg)
	factory := emitCoroProgramBootstrapFactoryV2(pkg, bootstrap, targets, finalHash, true)
	body := pkg.Module().NamedFunction(factory.Name()).String()
	if got := strings.Count(body, "call void @"+coroProgramMainReturnSymbolV1); got != 1 {
		t.Fatalf("plain-main return calls = %d, want 1:\n%s", got, body)
	}
	plainMain := strings.Index(body, "call void @\"example.com/program.main\"()")
	mainReturn := strings.Index(body, "call void @"+coroProgramMainReturnSymbolV1)
	if plainMain < 0 || mainReturn < 0 || plainMain >= mainReturn {
		t.Fatalf("main-return cancellation is not on the normal post-plain-main continuation:\n%s", body)
	}
	if got := strings.Count(body, "call void @"+coroProgramCompletePrepareHookV2); got != 1 {
		t.Fatalf("bootstrap terminal completion calls = %d, want 1:\n%s", got, body)
	}
	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify plain-main return factory: %v\n%s", err, pkg.Module().String())
	}
}

func newCoroProgramBootstrapFactoryFixtureV2(
	pkg llssa.Package,
) (*coroProgramBootstrapV1, []coroProgramBootstrapFactoryTargetV2, []llssa.CoroProgramStep, [16]byte) {
	prog := pkg.Prog
	pointer := types.Typ[types.UnsafePointer]
	rootFactorySig := newSignature(
		[]types.Type{pointer, pointer, pointer},
		[]types.Type{pointer},
	)
	rootFactories := [2]llssa.Function{
		pkg.NewFunc("example.com/runtime.init$coro.factory", rootFactorySig, llssa.InC),
		pkg.NewFunc("example.com/program.init$coro.factory", rootFactorySig, llssa.InC),
	}
	emptyPayload := prog.Struct()
	descriptors := [2]llssa.Expr{
		pkg.NewCoroRootFactoryDescriptor("example.com/runtime.init$coro.descriptor", llssa.CoroRootFactoryDescriptorOptions{
			Version: coroProgramPhysicalABIVersionV1,
			Factory: rootFactories[0].Expr,
			Startup: emptyPayload,
			Result:  emptyPayload,
		}),
		pkg.NewCoroRootFactoryDescriptor("example.com/program.init$coro.descriptor", llssa.CoroRootFactoryDescriptorOptions{
			Version: coroProgramPhysicalABIVersionV1,
			Factory: rootFactories[1].Expr,
			Startup: emptyPayload,
			Result:  emptyPayload,
		}),
	}
	const anchorName = "__llgo_coro_root_package_v1.0123456789abcdef0123456789abcdef"
	anchor := pkg.NewCoroRootPackageAnchor(anchorName, llssa.CoroRootPackageAnchorOptions{
		Version:     coroProgramPhysicalABIVersionV1,
		Descriptors: descriptors[:],
	})
	plains := [3]llssa.Function{
		declareNoArgFunc(pkg, "init$abitypes"),
		declareNoArgFunc(pkg, "runtime.init"),
		declareNoArgFunc(pkg, "example.com/program.main"),
	}
	steps := []coroProgramBootstrapStepV1{
		{
			Kind: coroProgramStepCoroRootV1, Role: coroProgramStepRoleRuntimeInitV2,
			FunctionID: "runtime-init-id", Target: "example.com/runtime.init$coro",
			Owner: "example.com/runtime", CatalogTarget: anchorName, Aux: 0,
		},
		{
			Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleABIInitV2,
			FunctionID: "abi-init-id", Target: plains[0].Name(),
		},
		{
			Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRolePublicRuntimeInitV2,
			FunctionID: "public-runtime-init-id", Target: plains[1].Name(),
		},
		{
			Kind: coroProgramStepCoroRootV1, Role: coroProgramStepRolePackageInitV2,
			FunctionID: "package-init-id", Target: "example.com/program.init$coro",
			Owner: "example.com/program", CatalogTarget: anchorName, Aux: 1,
		},
		{
			Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleMainV2,
			FunctionID: "main-id", Target: plains[2].Name(),
		},
	}
	bootstrap := &coroProgramBootstrapV1{Version: coroProgramBootstrapVersionV2, Steps: steps}
	targets := []coroProgramBootstrapFactoryTargetV2{
		{Anchor: anchor},
		{Plain: plains[0]},
		{Plain: plains[1]},
		{Anchor: anchor},
		{Plain: plains[2]},
	}
	tableSteps := []llssa.CoroProgramStep{
		{Kind: llssa.CoroProgramStepCoroRoot, Flags: steps[0].Role, Target: anchor, Aux: steps[0].Aux},
		{Kind: llssa.CoroProgramStepDirectPlain, Flags: steps[1].Role, Target: plains[0].Expr},
		{Kind: llssa.CoroProgramStepDirectPlain, Flags: steps[2].Role, Target: plains[1].Expr},
		{Kind: llssa.CoroProgramStepCoroRoot, Flags: steps[3].Role, Target: anchor, Aux: steps[3].Aux},
		{Kind: llssa.CoroProgramStepDirectPlain, Flags: steps[4].Role, Target: plains[2].Expr},
	}
	var finalHash [16]byte
	for index := range finalHash {
		finalHash[index] = byte(index + 1)
	}
	return bootstrap, targets, tableSteps, finalHash
}

func assertCoroProgramBootstrapFactoryPresplitV2(t *testing.T, ir, uintptrIR string) {
	t.Helper()
	descriptorLine := irLineWithPrefix(ir, "@"+coroProgramBootstrapFrameDescriptorPrefixV2)
	if descriptorLine == "" {
		t.Fatalf("mixed v2 bootstrap frame descriptor is missing:\n%s", ir)
	}
	for _, want := range []string{
		"i32 1, i32 1",
		"i64 72623859790382856, i64 651345242494996240",
		uintptrIR + " 0, " + uintptrIR + " 1",
	} {
		if !strings.Contains(descriptorLine, want) {
			t.Fatalf("mixed v2 bootstrap frame descriptor missing %q: %s", want, descriptorLine)
		}
	}
	bootstrapLine := irLineWithPrefix(ir, "@__llgo_test_program_bootstrap_v2 =")
	if bootstrapLine == "" || !strings.Contains(bootstrapLine, "i32 2") ||
		!strings.Contains(bootstrapLine, "ptr @"+coroProgramBootstrapFactorySymbolV2) {
		t.Fatalf("mixed v2 bootstrap descriptor does not publish version/factory: %s\n%s", bootstrapLine, ir)
	}

	body := llvmFunctionIRV1(ir, coroProgramBootstrapFactorySymbolV2)
	if body == "" {
		t.Fatalf("mixed v2 bootstrap factory body is missing:\n%s", ir)
	}
	if got := strings.Count(body, "call void @"+coroProgramAwaitPrepareHookV2); got != 2 {
		t.Fatalf("mixed v2 bootstrap await calls = %d, want 2:\n%s", got, body)
	}
	if got := strings.Count(body, "call i32 @"+coroProgramAwaitConsumeHookV1); got != 2 {
		t.Fatalf("mixed v2 bootstrap completion consumes = %d, want 2:\n%s", got, body)
	}
	if got := strings.Count(body, "i32 6, label"); got != 2 {
		t.Fatalf("mixed v2 bootstrap Goexit routes = %d, want 2:\n%s", got, body)
	}
	if got := strings.Count(body, "call ptr %"); got != 2 {
		t.Fatalf("mixed v2 bootstrap indirect child factory calls = %d, want 2:\n%s", got, body)
	}
	if strings.Contains(body, coroProgramMainReturnSymbolV1) {
		t.Fatalf("V2 factory without closed-static spawn emitted main-return cancellation:\n%s", body)
	}
	assertInOrder(t, body,
		"call void @"+coroProgramFramePublishHookV1,
		"call i8 @llvm.coro.suspend",
		"call void @"+coroRunDecisionTakeSymbolV1,
		"store i16 2",
		"call ptr %",
		"store i16 1",
		"store i16 3",
		"call void @"+coroProgramAwaitPrepareHookV2,
		"call i8 @llvm.coro.suspend",
		"call void @"+coroRunDecisionTakeSymbolV1,
		"call i32 @"+coroProgramAwaitConsumeHookV1,
		"call void @\"init$abitypes\"()",
		"call void @runtime.init()",
		"call ptr %",
		"call void @"+coroProgramAwaitPrepareHookV2,
		"call i8 @llvm.coro.suspend",
		"call void @"+coroRunDecisionTakeSymbolV1,
		"call i32 @"+coroProgramAwaitConsumeHookV1,
		"call void @\"example.com/program.main\"()",
	)
	if got := strings.Count(body, "call void @"+coroProgramCompletePrepareHookV2); got != 1 {
		t.Fatalf("mixed v2 bootstrap terminal completion calls = %d, want 1:\n%s", got, body)
	}
	assertCoroProgramZeroRunDecisionCalls(t, body, 3)
}

func assertCoroProgramZeroRunDecisionCalls(t *testing.T, body string, want int) {
	t.Helper()
	callPrefix := "call void @" + coroRunDecisionTakeSymbolV1
	if got := strings.Count(body, callPrefix); got != want {
		t.Fatalf("bootstrap run-decision calls = %d, want %d:\n%s", got, want, body)
	}
	zeroTicket := regexp.MustCompile(
		`call void @` + regexp.QuoteMeta(coroRunDecisionTakeSymbolV1) +
			`\(ptr [^,]+, i32 0, i32 0, ptr null, ptr null, ptr null, ptr null, ptr null\)`,
	)
	if got := len(zeroTicket.FindAllString(body, -1)); got != want {
		t.Fatalf("bootstrap normal-only zero-ticket run-decision calls = %d, want %d:\n%s", got, want, body)
	}
}

func assertCoroProgramRunDecisionResumeOnly(t *testing.T, module llvm.Module, rampName string, want int) {
	t.Helper()
	ramp := module.NamedFunction(rampName)
	if ramp.IsNil() {
		t.Fatalf("post-CoroSplit module has no ramp %q:\n%s", rampName, module.String())
	}
	if body := ramp.String(); strings.Contains(body, "call void @"+coroRunDecisionTakeSymbolV1) {
		t.Fatalf("run-decision gate escaped the resume entry into ramp %s:\n%s", rampName, body)
	}
	if destroy := module.NamedFunction(rampName + ".destroy"); destroy.IsNil() {
		t.Fatalf("post-CoroSplit module has no destroy function %q:\n%s", rampName+".destroy", module.String())
	}
	resumeName := rampName + ".resume"
	resume := module.NamedFunction(resumeName)
	if resume.IsNil() {
		t.Fatalf("post-CoroSplit module has no function %q:\n%s", resumeName, module.String())
	}
	assertCoroProgramZeroRunDecisionCalls(t, resume.String(), want)
}

func runCoroProgramPostSplitSimplifyCFG(t *testing.T, prog llssa.Program, module llvm.Module) {
	t.Helper()
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	if err := module.RunPasses("function(simplifycfg)", prog.TargetMachine(), options); err != nil {
		t.Fatalf("simplify post-CoroSplit bootstrap CFG: %v\n%s", err, module.String())
	}
}

func assertCoroProgramRunDecisionDeadClonesEliminated(t *testing.T, module llvm.Module, rampName string) {
	t.Helper()
	call := "call void @" + coroRunDecisionTakeSymbolV1
	for _, name := range []string{rampName, rampName + ".destroy"} {
		function := module.NamedFunction(name)
		if function.IsNil() {
			t.Fatalf("canonical post-CoroSplit module has no function %q:\n%s", name, module.String())
		}
		if body := function.String(); strings.Contains(body, call) {
			t.Fatalf("simplifycfg retained a dead run-decision clone in %s:\n%s", name, body)
		}
	}
}

func llvmFunctionIRV1(ir, name string) string {
	quoted := "@" + name + "("
	start := strings.Index(ir, quoted)
	if start < 0 {
		quoted = "@\"" + name + "\"("
		start = strings.Index(ir, quoted)
	}
	if start < 0 {
		return ""
	}
	start = strings.LastIndex(ir[:start], "define ")
	if start < 0 {
		return ""
	}
	end := strings.Index(ir[start:], "\n}")
	if end < 0 {
		return ""
	}
	return ir[start : start+end+2]
}
