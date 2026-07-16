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
