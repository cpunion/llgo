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
	"go/types"
	"regexp"
	"strings"
	"testing"

	llssa "github.com/goplus/llgo/ssa"
	llvm "github.com/xgo-dev/llvm"
)

func TestCoroProgramBootstrapFactoryV1NativeAndWasm(t *testing.T) {
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

			bootstrap, targets, finalHash := newCoroProgramBootstrapFactoryFixtureV1(pkg)
			factory := emitCoroProgramBootstrapFactoryV1(pkg, bootstrap, targets, finalHash)
			pkg.NewCoroProgramBootstrap("__llgo_test_program_bootstrap_v1", llssa.CoroProgramBootstrapOptions{
				Version: coroProgramBootstrapVersionV1,
				ABIHash: finalHash,
				Steps: []llssa.CoroProgramStep{
					{Kind: llssa.CoroProgramStepDirectPlain, Flags: llssa.CoroProgramStepInit, Target: targets[0].Expr},
					{Kind: llssa.CoroProgramStepDirectPlain, Flags: llssa.CoroProgramStepMain, Target: targets[1].Expr},
				},
				Factory: factory.Expr,
			})

			mod := pkg.Module()
			mod.SetDataLayout(prog.DataLayout())
			mod.SetTarget(prog.TargetSpec().Triple)
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify bootstrap factory before CoroSplit: %v\n%s", err, mod.String())
			}
			pre := mod.String()
			assertCoroProgramBootstrapFactoryPresplitV1(t, pre, test.uintptrIR)

			options := llvm.NewPassBuilderOptions()
			options.SetVerifyEach(true)
			if err := mod.RunPasses("coro-early,cgscc(coro-split),coro-cleanup", prog.TargetMachine(), options); err != nil {
				options.Dispose()
				t.Fatalf("CoroSplit bootstrap factory: %v\n%s", err, mod.String())
			}
			options.Dispose()
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify bootstrap factory after CoroSplit: %v\n%s", err, mod.String())
			}
			post := mod.String()
			for _, suffix := range []string{".resume", ".destroy"} {
				if mod.NamedFunction(coroProgramBootstrapFactorySymbolV1 + suffix).IsNil() {
					t.Fatalf("CoroSplit did not create bootstrap factory%s:\n%s", suffix, post)
				}
			}
			for _, intrinsic := range []string{"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend"} {
				if regexp.MustCompile(`call [^\n]*@` + regexp.QuoteMeta(intrinsic) + `\b`).MatchString(post) {
					t.Fatalf("post-split bootstrap still calls %s:\n%s", intrinsic, post)
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(mod, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit bootstrap factory object: %v\n%s", err, post)
			}
			object.Dispose()
		})
	}
}

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
			factory := emitCoroProgramBootstrapFactoryV2(pkg, bootstrap, targets, finalHash)
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
			for _, intrinsic := range []string{"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend"} {
				if regexp.MustCompile(`call [^\n]*@` + regexp.QuoteMeta(intrinsic) + `\b`).MatchString(post) {
					t.Fatalf("post-split mixed v2 bootstrap still calls %s:\n%s", intrinsic, post)
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(mod, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit mixed v2 bootstrap factory object: %v\n%s", err, post)
			}
			object.Dispose()
		})
	}
}

func TestCoroProgramBootstrapFactoryV1RejectsNonCanonicalInputs(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	tests := []struct {
		name   string
		mutate func(*coroProgramBootstrapV1, *[2]llssa.Function, llssa.Package)
		want   string
	}{
		{
			name: "missing step",
			mutate: func(bootstrap *coroProgramBootstrapV1, _ *[2]llssa.Function, _ llssa.Package) {
				bootstrap.Steps = bootstrap.Steps[:1]
			},
			want: "exactly two validated steps",
		},
		{
			name: "coroutine root",
			mutate: func(bootstrap *coroProgramBootstrapV1, _ *[2]llssa.Function, _ llssa.Package) {
				bootstrap.Steps[0].Kind = coroProgramStepCoroRootV1
			},
			want: "not canonical DirectPlain",
		},
		{
			name: "swapped role",
			mutate: func(bootstrap *coroProgramBootstrapV1, _ *[2]llssa.Function, _ llssa.Package) {
				bootstrap.Steps[0].Role = coroProgramStepRoleMainV1
			},
			want: "not canonical DirectPlain",
		},
		{
			name: "wrong target",
			mutate: func(_ *coroProgramBootstrapV1, targets *[2]llssa.Function, pkg llssa.Package) {
				targets[0] = declareNoArgFunc(pkg, "example.com/program.other")
			},
			want: "target does not match",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := llssa.NewProgram(nil)
			defer prog.Dispose()
			pkg := prog.NewPackage("entry", "entry")
			defer pkg.Module().Dispose()
			bootstrap, targets, _ := newCoroProgramBootstrapFactoryFixtureV1(pkg)
			test.mutate(bootstrap, &targets, pkg)
			defer func() {
				recovered := recover()
				if recovered == nil || !strings.Contains(recovered.(string), test.want) {
					t.Fatalf("panic = %v, want substring %q", recovered, test.want)
				}
			}()
			emitCoroProgramBootstrapFactoryV1(pkg, bootstrap, targets, [16]byte{})
		})
	}
}

func newCoroProgramBootstrapFactoryFixtureV1(
	pkg llssa.Package,
) (*coroProgramBootstrapV1, [2]llssa.Function, [16]byte) {
	targets := [2]llssa.Function{
		declareNoArgFunc(pkg, "example.com/program.init"),
		declareNoArgFunc(pkg, "example.com/program.main"),
	}
	bootstrap := &coroProgramBootstrapV1{Steps: []coroProgramBootstrapStepV1{
		{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleInitV1, FunctionID: "init-id", Target: targets[0].Name()},
		{Kind: coroProgramStepDirectPlainV1, Role: coroProgramStepRoleMainV1, FunctionID: "main-id", Target: targets[1].Name()},
	}}
	var finalHash [16]byte
	for index := range finalHash {
		finalHash[index] = byte(index + 1)
	}
	return bootstrap, targets, finalHash
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

func assertCoroProgramBootstrapFactoryPresplitV1(t *testing.T, ir, uintptrIR string) {
	t.Helper()
	descriptorLine := irLineWithPrefix(ir, "@"+coroProgramBootstrapFrameDescriptorPrefixV1)
	if descriptorLine == "" {
		t.Fatalf("bootstrap frame descriptor is missing:\n%s", ir)
	}
	for _, want := range []string{
		"i32 1, i32 0",
		"i64 72623859790382856, i64 651345242494996240",
		uintptrIR + " 0, " + uintptrIR + " 1",
	} {
		if !strings.Contains(descriptorLine, want) {
			t.Fatalf("bootstrap frame descriptor missing %q: %s", want, descriptorLine)
		}
	}
	bootstrapLine := irLineWithPrefix(ir, "@__llgo_test_program_bootstrap_v1 =")
	if bootstrapLine == "" || !strings.Contains(bootstrapLine, "ptr @"+coroProgramBootstrapFactorySymbolV1) {
		t.Fatalf("bootstrap descriptor does not publish the compiler factory: %s\n%s", bootstrapLine, ir)
	}

	body := llvmFunctionIRV1(ir, coroProgramBootstrapFactorySymbolV1)
	if body == "" {
		t.Fatalf("bootstrap factory body is missing:\n%s", ir)
	}
	if !strings.Contains(body, "{ ptr, ptr, ptr, ptr, ptr, i16, i16, i32, i32 }") {
		t.Fatalf("bootstrap promise does not use the exact HeaderV1 layout:\n%s", body)
	}
	for _, hook := range []string{
		coroProgramFrameAllocHookV1,
		coroProgramFramePublishHookV1,
		coroProgramCompletePrepareHookV1,
		coroProgramFrameFreeHookV1,
	} {
		if !strings.Contains(body, "@"+hook) {
			t.Fatalf("bootstrap factory does not call %s:\n%s", hook, body)
		}
	}
	assertInOrder(t, body,
		"store i16 1",
		"call void @"+coroProgramFramePublishHookV1,
		"call i8 @llvm.coro.suspend",
		"store i16 2",
		"call void @\"example.com/program.init\"()",
		"call void @\"example.com/program.main\"()",
		"store i16 4",
		"call void @"+coroProgramCompletePrepareHookV1,
		"call i8 @llvm.coro.suspend",
	)
	if !strings.Contains(body, "store ptr %1, ptr") {
		t.Fatalf("bootstrap out parameter is not published in HeaderV1.ResultSlot:\n%s", body)
	}
	startupUses := regexp.MustCompile(`(?:^|[^%A-Za-z0-9_.])%2(?:[^0-9]|$)`).FindAllStringIndex(body, -1)
	if len(startupUses) != 1 {
		t.Fatalf("empty startup parameter has %d textual occurrences, want definition only:\n%s", len(startupUses), body)
	}
	if strings.Contains(body, "store ptr %2") || strings.Contains(body, "load ptr, ptr %2") {
		t.Fatalf("empty startup parameter is read or stored:\n%s", body)
	}
}

func assertCoroProgramBootstrapFactoryPresplitV2(t *testing.T, ir, uintptrIR string) {
	t.Helper()
	descriptorLine := irLineWithPrefix(ir, "@"+coroProgramBootstrapFrameDescriptorPrefixV2)
	if descriptorLine == "" {
		t.Fatalf("mixed v2 bootstrap frame descriptor is missing:\n%s", ir)
	}
	for _, want := range []string{
		"i32 1, i32 0",
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
	if got := strings.Count(body, "call void @__llgo_coro_await_prepare_v1"); got != 2 {
		t.Fatalf("mixed v2 bootstrap await calls = %d, want 2:\n%s", got, body)
	}
	if got := strings.Count(body, "call ptr %"); got != 2 {
		t.Fatalf("mixed v2 bootstrap indirect child factory calls = %d, want 2:\n%s", got, body)
	}
	assertInOrder(t, body,
		"call void @"+coroProgramFramePublishHookV1,
		"call i8 @llvm.coro.suspend",
		"store i16 2",
		"call ptr %",
		"store i16 1",
		"store i16 3",
		"call void @__llgo_coro_await_prepare_v1",
		"call i8 @llvm.coro.suspend",
		"call void @\"init$abitypes\"()",
		"call void @runtime.init()",
		"call ptr %",
		"call void @__llgo_coro_await_prepare_v1",
		"call i8 @llvm.coro.suspend",
		"call void @\"example.com/program.main\"()",
		"call void @"+coroProgramCompletePrepareHookV1,
	)
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
