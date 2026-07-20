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
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroSysctlCPUFixture = `package cpu
import _ "unsafe"

func sysctlbynameInt32(name []byte) (int32, int32)
func sysctlbynameBytes(name, out []byte) int32

//go:linkname sysctlEnabled
func sysctlEnabled(name []byte) bool {
	return len(name) != 0
}
`

const coroSysctlRuntimeFixture = `package runtimebridge
import "unsafe"

//llgo:coro sync
//go:linkname cSysctlbyname C.sysctlbyname
func cSysctlbyname(name *byte, oldp unsafe.Pointer, oldlenp *uintptr, newp unsafe.Pointer, newlen uintptr) int32

//go:linkname internalCPUSysctlbynameInt32 internal/cpu.sysctlbynameInt32
func internalCPUSysctlbynameInt32(name []byte) (int32, int32) {
	return cSysctlbyname(nil, nil, nil, nil, 0), 0
}

//go:linkname internalCPUSysctlbynameBytes internal/cpu.sysctlbynameBytes
func internalCPUSysctlbynameBytes(name, out []byte) int32 {
	return cSysctlbyname(nil, nil, nil, nil, 0)
}
`

const coroSysctlConsumerFixture = `package consumer
//go:linkname linkedSysctlEnabled internal/cpu.sysctlEnabled
func linkedSysctlEnabled(name []byte) bool
func Root(name []byte) bool { return linkedSysctlEnabled(name) }
`

func TestCoroGoLinknameVisibilitySysctlBridgeNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	cpuPkg := testProg.addPackage(t, "internal/cpu", coroSysctlCPUFixture)
	runtimePkg := testProg.addPackage(t, "example.com/runtimebridge", coroSysctlRuntimeFixture)
	consumerPkg := testProg.addPackage(t, "example.com/consumer", coroSysctlConsumerFixture)
	testProg.ssa.Build()

	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var prog llssa.Program
			if test.target == nil {
				prog = newLLSSAProg(t)
			} else {
				prog = newLLSSAProgForTarget(t, test.target)
			}
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
				{SSA: cpuPkg.ssa, Files: []*ast.File{cpuPkg.file}},
				{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
				{SSA: consumerPkg.ssa, Files: []*ast.File{consumerPkg.file}},
			})
			if err != nil {
				t.Fatal(err)
			}

			sysctlEnabled := cpuPkg.ssa.Func("sysctlEnabled")
			declInt32 := cpuPkg.ssa.Func("sysctlbynameInt32")
			defInt32 := runtimePkg.ssa.Func("internalCPUSysctlbynameInt32")
			linked := consumerPkg.ssa.Func("linkedSysctlEnabled")
			root := consumerPkg.ssa.Func("Root")
			cSysctl := runtimePkg.ssa.Func("cSysctlbyname")
			if resolved, ok := universe.Resolve(declInt32); !ok || resolved != defInt32 {
				t.Fatalf("bodyless sysctl bridge resolution = %v, %t; want %v", resolved, ok, defInt32)
			}
			if resolved, ok := universe.Resolve(linked); !ok || resolved != sysctlEnabled {
				t.Fatalf("bodyless visibility consumer resolution = %v, %t; want %v", resolved, ok, sysctlEnabled)
			}
			visibility, certified, err := universe.coroGoLinknameVisibilityCertificate(sysctlEnabled)
			if err != nil || !certified || visibility.ID == "" || visibility.PhysicalSymbol != "internal/cpu.sysctlEnabled" || visibility.ABISignature == "" {
				t.Fatalf("sysctl visibility certificate = %+v, %t, %v", visibility, certified, err)
			}
			syncCertificate, syncCertified, err := universe.CoroForeignSyncCertificate(cSysctl)
			if err != nil || !syncCertified || syncCertificate.ID == "" {
				t.Fatalf("sysctl C sync certificate = %+v, %t, %v", syncCertificate, syncCertified, err)
			}

			ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			functionIDs := universe.FunctionIDConfig()
			functionIDs.CoroABI = coro.PhysicalABIV1
			functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
			functionIDs.ArchiveReady = true
			plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse:     ssaUniverse,
				FunctionIDs:          functionIDs,
				MaxPlainInstructions: -1,
				ResolveFunction: func(fn *ssa.Function) (*ssa.Function, bool, error) {
					canonical, ok := universe.Resolve(fn)
					return canonical, ok, nil
				},
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					if fn == sysctlEnabled {
						return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
					}
					certificate, ok, err := universe.CoroForeignSyncCertificate(fn)
					if err != nil {
						return coro.SSAFunctionPolicy{}, err
					}
					if ok {
						return coro.SSAFunctionPolicy{
							Effect: coro.NoSuspend, Exec: coro.IRQUnsafe,
							External: coro.ExternalKnown, OverrideExternal: true, IgnoreBody: true,
							ForeignSyncCertificate: certificate.ID,
						}, nil
					}
					return coro.SSAFunctionPolicy{}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := universe.ValidatePlanCoverage(plan); err != nil {
				t.Fatal(err)
			}
			sysctlPlan, ok := plan.FunctionPlan(sysctlEnabled)
			if !ok || sysctlPlan.Emission != coro.EmitCoroutine || sysctlPlan.RawPlainEntry || plan.HasRawPlainVariant(sysctlEnabled) {
				t.Fatalf("sysctl plan = %+v, present=%t raw-variant=%t; want one managed coroutine body", sysctlPlan, ok, plan.HasRawPlainVariant(sysctlEnabled))
			}
			if err := validateCoroPhysicalABIWithUniverse(sysctlEnabled, sysctlPlan, plan, universe, true, true); err != nil {
				t.Fatalf("visibility-only physical ABI: %v", err)
			}

			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroChildAwaitCompilation(compilation)
			for name, fixture := range map[string]emissionTestPackage{"cpu": cpuPkg, "consumer": consumerPkg} {
				compiled, _, err := NewPackageExWithEmbedOptions(
					prog, nil, nil, nil, fixture.ssa, []*ast.File{fixture.file}, goembed.VarMap{},
					PackageOptions{Compilation: compilation},
				)
				if err != nil {
					t.Fatalf("compile %s: %v", name, err)
				}
				module := compiled.Module()
				defer module.Dispose()
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatalf("verify %s: %v\n%s", name, err, module.String())
				}
				if name == "cpu" {
					if raw := module.NamedFunction("internal/cpu.sysctlEnabled"); !raw.IsNil() {
						t.Fatalf("visibility-only function emitted a raw body:\n%s", raw.String())
					}
					if managed := module.NamedFunction("internal/cpu.sysctlEnabled" + coroPrimarySuffix); managed.IsNil() {
						t.Fatalf("visibility-only coroutine body is absent:\n%s", module.String())
					}
				} else {
					rootIR := module.NamedFunction("example.com/consumer.Root" + coroPrimarySuffix).String()
					if !strings.Contains(rootIR, "internal/cpu.sysctlEnabled$coro") || strings.Contains(rootIR, "internal/cpu.sysctlEnabled\"(") {
						t.Fatalf("paired consumer did not select managed sysctl entry:\n%s", rootIR)
					}
				}
				runCoroABITestPipeline(t, prog, module)
				object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
				if err != nil {
					t.Fatalf("emit %s object: %v", name, err)
				}
				if len(object.Bytes()) == 0 {
					object.Dispose()
					t.Fatalf("%s object is empty", name)
				}
				object.Dispose()
			}
		})
	}
}

func TestCoroGoLinknameVisibilityRejectsNonPlainShapes(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		find   func(*ssa.Package) *ssa.Function
	}{
		{name: "redirecting two argument", source: `package bad
//go:linkname Visible example.com/elsewhere.Visible
func Visible() {}
`, find: func(pkg *ssa.Package) *ssa.Function { return pkg.Func("Visible") }},
		{name: "additional export", source: `package bad
//go:linkname Visible
//export Visible
func Visible() {}
`, find: func(pkg *ssa.Package) *ssa.Function { return pkg.Func("Visible") }},
		{name: "variadic", source: `package bad
//go:linkname Visible
func Visible(...int) {}
`, find: func(pkg *ssa.Package) *ssa.Function { return pkg.Func("Visible") }},
		{name: "generic", source: `package bad
//go:linkname Visible
func Visible[T any](T) {}
`, find: func(pkg *ssa.Package) *ssa.Function { return pkg.Func("Visible") }},
		{name: "method receiver", source: `package bad
type T struct{}
//go:linkname T.Visible
func (T) Visible() {}
func Root() { T{}.Visible() }
`, find: func(pkg *ssa.Package) *ssa.Function {
			named := pkg.Pkg.Scope().Lookup("T").Type()
			selection := pkg.Prog.MethodSets.MethodSet(named).Lookup(pkg.Pkg, "Visible")
			return pkg.Prog.MethodValue(selection)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _, files := buildGoSSAPkg(t, test.source)
			fn := test.find(ssaPkg)
			if fn == nil {
				t.Fatal("fixture function is absent")
			}
			if directive, ok := attachedGoLinknameVisibilityDirective(fn); ok || directive != "" {
				t.Fatalf("visibility source proof = %q, %t; want rejected", directive, ok)
			}
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			if universe.Contains(fn) {
				if certificate, certified, err := universe.coroGoLinknameVisibilityCertificate(fn); err != nil || certified || certificate.ID != "" {
					t.Fatalf("frozen visibility certificate = %+v, %t, %v; want absent", certificate, certified, err)
				}
			}
		})
	}
}
