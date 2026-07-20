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
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroImplicitIndexBoundsNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroImplicitNilFaultFixture(t, target.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify structured Index before CoroSplit: %v\n%s", err, module.String())
			}
			for _, operation := range []struct {
				name       string
				elementGEP string
			}{
				{name: "StringAt", elementGEP: "getelementptr inbounds i8"},
				{name: "ConstantStringAt", elementGEP: "getelementptr inbounds i8"},
				{name: "ArrayAt", elementGEP: "getelementptr inbounds i32"},
			} {
				function := functions[operation.name]
				if !coroFunctionHasSSAIndex(function) {
					t.Fatalf("%s fixture no longer exercises ssa.Index", operation.name)
				}
				functionPlan, ok := plan.FunctionPlan(function)
				if !ok || functionPlan.Emission != coro.EmitCoroutine || !functionPlan.Exec.Contains(coro.MayUnwind) {
					t.Fatalf("%s plan = %+v, present=%t; want may-unwind coroutine", operation.name, functionPlan, ok)
				}
				body := requireCoroPhysicalFunction(t, module, "foo."+operation.name).String()
				if got := strings.Count(body, "call void @"+coroFaultPrepareHookV1); got != 1 {
					t.Fatalf("%s fault prepare calls = %d, want one:\n%s", operation.name, got, body)
				}
				if strings.Contains(body, "CheckIndexRange") || strings.Contains(body, "AssertNilDeref") {
					t.Fatalf("%s retained a native-stack index helper:\n%s", operation.name, body)
				}
				hook := strings.Index(body, "call void @"+coroFaultPrepareHookV1)
				hookLine := body[hook:]
				if end := strings.IndexByte(hookLine, '\n'); end >= 0 {
					hookLine = hookLine[:end]
				}
				if !strings.Contains(hookLine, "i32 2") {
					t.Fatalf("%s did not select the index-bounds fault kind:\n%s", operation.name, body)
				}
				if !strings.Contains(body[hook:], operation.elementGEP) {
					t.Fatalf("%s formed no element address after its terminal bounds edge:\n%s", operation.name, body)
				}
			}

			runCoroABITestPipeline(t, prog, module)
			for _, name := range []string{"StringAt", "ConstantStringAt", "ArrayAt"} {
				resume := module.NamedFunction("foo." + name + "$coro.resume")
				if resume.IsNil() || strings.Count(resume.String(), "call void @"+coroFaultPrepareHookV1) != 1 {
					t.Fatalf("post-split %s resume lost its bounds-fault edge:\n%s", name, module.String())
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit structured Index object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(coroFaultPrepareHookV1)) {
				t.Fatal("post-CoroSplit object lost the bounds-fault hook")
			}
		})
	}
}

func coroFunctionHasSSAIndex(function *ssa.Function) bool {
	if function == nil {
		return false
	}
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			if _, ok := instruction.(*ssa.Index); ok {
				return true
			}
		}
	}
	return false
}
