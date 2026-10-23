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
	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"go/ast"
	"go/types"
	"strings"
	"testing"
)

func TestCoroCurrentTaskProjectionShape(t *testing.T) {
	for _, declaration := range []string{"func Project() unsafe.Pointer", "func Project() uintptr", "func Project(value uintptr) unsafe.Pointer"} {
		t.Run(declaration, func(t *testing.T) {
			program := newEmissionTestProgram()
			program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			args := ""
			if strings.Contains(declaration, "value") {
				args = "0"
			}
			pkg := program.addPackage(t, "example.com/projection", `package projection
import "unsafe"
var _ unsafe.Pointer
//go:linkname Project llgo.coroCurrentTaskIfPresent
`+declaration+`
func Use() { _ = Project(`+args+`) }
`)
			program.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			u, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
			if err != nil {
				t.Fatal(err)
			}
			call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
			semantics, intrinsic, err := u.CoroIntrinsicCallSiteSemantics(call)
			if declaration != "func Project() unsafe.Pointer" {
				if err == nil || !intrinsic {
					t.Fatal("malformed projection accepted", semantics, intrinsic, err)
				}
				return
			}
			if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
				t.Fatal(semantics, intrinsic, err)
			}
		})
	}
}
func TestCoroCurrentTaskProjectionPhysicalAndRaw(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []*llssa.Target{nil, {GOOS: "wasip1", GOARCH: "wasm"}} {
		prog, pkg, plan, source := compileCoroOutcomePlainSource(t, target, `package foo
import "unsafe"
//go:linkname project llgo.coroCurrentTaskIfPresent
func project() unsafe.Pointer
func Leaf(fail bool) unsafe.Pointer { if fail { panic(1) }; return project() }
func Root(fail bool) unsafe.Pointer { return Leaf(fail) }
func Raw() unsafe.Pointer { return project() }
`, "Root", 64)
		module := pkg.Module()
		root, found := plan.FunctionPlan(source.Func("Leaf"))
		if !found || root.Emission != coro.EmitOutcomePlain || root.Exec.Contains(coro.NeedsRuntimeContext) {
			t.Fatalf("projection acquired ambient context or lost outcome ABI: %+v", root)
		}
		if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
			t.Fatal(err, module.String())
		}
		physical := module.NamedFunction("foo.Leaf$outcome")
		if physical.IsNil() || !strings.Contains(physical.String(), "store ptr %0,") {
			t.Fatal("managed projection did not return the hidden task", module.String())
		}
		if strings.Contains(module.String(), "call ptr @project") || strings.Contains(module.String(), "llgo.coroCurrentTaskIfPresent") {
			t.Fatal("projection retained a runtime/TLS call", module.String())
		}
		module.Dispose()
		prog.Dispose()
	}
}

func TestCoroCurrentTaskProjectionRawEntryIsEmpty(t *testing.T) {
	source, _, files := buildGoSSAPkg(t, `package foo
import "unsafe"
//go:linkname project llgo.coroCurrentTaskIfPresent
func project() unsafe.Pointer
func Raw() unsafe.Pointer { return project() }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg, err := NewPackage(prog, source, files)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	options := llvm.NewPassBuilderOptions()
	err = module.RunPasses("function(instcombine,simplifycfg)", prog.TargetMachine(), options)
	options.Dispose()
	if err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	raw := module.NamedFunction("foo.Raw")
	if raw.IsNil() || !strings.Contains(raw.String(), "ret ptr null") || strings.Contains(raw.String(), "call ") {
		t.Fatal("raw owner projected a task", module.String())
	}
}
