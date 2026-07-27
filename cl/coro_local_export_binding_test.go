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
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestEmissionUniverseRedirectsExactLocalCExportManagedCall(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/localexport", `package localexport

//go:linkname Call C.local_export_v1
func Call(uintptr) uint32

//go:linkname PlainCall C.local_export_plain_v1
func PlainCall(uintptr) uint32

//export local_export_v1
func local_export_v1(value uintptr) uint32 {
	if value == 0 {
		return <-make(chan uint32)
	}
	return uint32(value)
}

//export local_export_plain_v1
func local_export_plain_v1(value uintptr) uint32 { return uint32(value) }

func Root(value uintptr) uint32 { return Call(value) }
func PlainRoot(ready <-chan struct{}, value uintptr) uint32 {
	<-ready
	return PlainCall(value)
}
func Deferred(value uintptr) { defer Call(value) }
func Spawned(value uintptr) { go Call(value) }
`)
	testProg.ssa.Build()
	program := llssa.NewProgram(nil)
	defer program.Dispose()
	universe, err := prepareStacklessEmissionUniverse(program, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "local-export-owner",
	}})
	if err != nil {
		t.Fatal(err)
	}

	declaration := pkg.ssa.Func("Call")
	target := pkg.ssa.Func("local_export_v1")
	if resolved, ok := universe.Resolve(declaration); !ok || resolved != declaration {
		t.Fatalf("Resolve(local C declaration) = %v, %t; want original declaration", resolved, ok)
	}
	if !universe.Contains(declaration) || !universe.Contains(target) {
		t.Fatalf(
			"local export membership = declaration %t target %t; want both retained",
			universe.Contains(declaration), universe.Contains(target),
		)
	}
	call := onlyLocalExportStaticCall(t, pkg.ssa.Func("Root"), declaration)
	site, frozen, err := universe.CoroCallSitePlan(call)
	if err != nil || !frozen || site.ManagedStaticTarget != target ||
		site.ManagedStaticTargetCertificate == "" {
		t.Fatalf("local export call SitePlan = %+v, %t, %v", site, frozen, err)
	}
	bindingID := site.ManagedStaticTargetCertificate
	for _, owner := range []*ssa.Function{
		pkg.ssa.Func("Deferred"),
		pkg.ssa.Func("Spawned"),
	} {
		invocation := onlyLocalExportStaticCall(t, owner, declaration)
		site, frozen, err := universe.CoroCallSitePlan(invocation)
		if err != nil || !frozen || site.ManagedStaticTarget != target ||
			site.ManagedStaticTargetCertificate != bindingID {
			t.Fatalf(
				"local export %T SitePlan in %q = %+v, %t, %v",
				invocation, owner.Name(), site, frozen, err,
			)
		}
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(pkg.ssa.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(pkg.ssa.Prog, coro.Roots{
		{Function: pkg.ssa.Func("Root"), Demand: coro.SyncDemand},
		{Function: pkg.ssa.Func("PlainRoot"), Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse: ssaUniverse,
		ResolveFunction: func(function *ssa.Function) (*ssa.Function, bool, error) {
			resolved, ok := universe.Resolve(function)
			return resolved, ok, nil
		},
		ClassifyStaticCallTarget: func(caller *ssa.Function, direct ssa.CallInstruction) (*ssa.Function, bool, error) {
			site, frozen, err := universe.CoroCallSitePlan(direct)
			if err != nil || !frozen {
				return nil, false, err
			}
			return site.ManagedStaticTarget, site.ManagedStaticTarget != nil, nil
		},
		FunctionIDs: universe.FunctionIDConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}
	callPlan, planned := plan.CallPlan(call)
	targetID, targetPlanned := plan.FunctionID(target)
	if !planned || !targetPlanned || callPlan.Kind != coro.CallDirect ||
		callPlan.Open || len(callPlan.Targets) != 1 ||
		callPlan.Targets[0] != targetID {
		t.Fatalf("local export managed call plan = %+v, %t; target=%q, %t", callPlan, planned, targetID, targetPlanned)
	}
	rootPlan, rootPlanned := plan.FunctionPlan(pkg.ssa.Func("Root"))
	if !rootPlanned || !rootPlan.Effect.Contains(coro.MayPark) ||
		rootPlan.LocalExec.Contains(coro.BlockForeign) {
		t.Fatalf("local export automatic effect propagation = %+v, %t", rootPlan, rootPlanned)
	}
	plainTarget := pkg.ssa.Func("local_export_plain_v1")
	plainCall := onlyLocalExportStaticCall(t, pkg.ssa.Func("PlainRoot"), pkg.ssa.Func("PlainCall"))
	plainCallPlan, plainCallPlanned := plan.CallPlan(plainCall)
	plainTargetPlan, plainTargetPlanned := plan.FunctionPlan(plainTarget)
	if !plainCallPlanned || !plainTargetPlanned ||
		plainTargetPlan.Emission != coro.EmitPlain ||
		len(plainCallPlan.Targets) != 1 ||
		plainCallPlan.Targets[0] != plainTargetPlan.ID {
		t.Fatalf(
			"plain local export plan = call %+v, %t target %+v, %t",
			plainCallPlan, plainCallPlanned, plainTargetPlan, plainTargetPlanned,
		)
	}
	if err := validateCoroPhysicalConsumersCapabilities(plan, universe, true, true, true); err != nil {
		t.Fatalf("managed local export source operand escaped physical validation: %v", err)
	}
}

func TestEmissionUniverseLocalCExportManagedCallFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
	}{
		{
			name: "signature mismatch",
			source: `package localexport
//go:linkname Call C.local_export_v1
func Call(uintptr) uint32
//export local_export_v1
func local_export_v1(value uint32) uint32 { return value }
func Root(value uintptr) uint32 { return Call(value) }
`,
		},
		{
			name: "explicit legacy policy",
			source: `package localexport
//llgo:coro noblock
//go:linkname Call C.local_export_v1
func Call(uintptr) uint32
//export local_export_v1
func local_export_v1(value uintptr) uint32 { return uint32(value) }
func Root(value uintptr) uint32 { return Call(value) }
`,
		},
		{
			name: "competing export ABI directive",
			source: `package localexport
//go:linkname Call C.local_export_v1
func Call(uintptr) uint32
//export local_export_v1
//go:wasmexport local_export_v1
func local_export_v1(value uintptr) uint32 { return uint32(value) }
func Root(value uintptr) uint32 { return Call(value) }
`,
		},
		{
			name: "duplicate export publication",
			source: `package localexport
//go:linkname Call C.local_export_v1
func Call(uintptr) uint32
//export local_export_v1
//export local_export_v1
func local_export_v1(value uintptr) uint32 { return uint32(value) }
func Root(value uintptr) uint32 { return Call(value) }
`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			pkg := testProg.addPackage(t, "example.com/emission/localexport/"+test.name, test.source)
			testProg.ssa.Build()
			program := llssa.NewProgram(nil)
			defer program.Dispose()
			universe, err := prepareStacklessEmissionUniverse(program, nil, []EmissionPackage{{
				SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "local-export-owner",
			}})
			if err != nil {
				t.Fatal(err)
			}
			declaration := pkg.ssa.Func("Call")
			call := onlyLocalExportStaticCall(t, pkg.ssa.Func("Root"), declaration)
			site, frozen, siteErr := universe.CoroCallSitePlan(call)
			resolved, ok := universe.Resolve(declaration)
			if siteErr != nil || !frozen || site.ManagedStaticTarget != nil ||
				site.ManagedStaticTargetCertificate != "" ||
				!ok || resolved != declaration || !universe.Contains(declaration) {
				t.Fatalf(
					"fail-closed local export = site %+v, %t, %v declaration %v, %t, contained=%t",
					site, frozen, siteErr, resolved, ok, universe.Contains(declaration),
				)
			}
		})
	}
}

func TestEmissionUniverseLocalCExportKeepsRawAddressIdentity(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/localexport/raw-address", `package localexport
//go:linkname Call C.local_export_v1
func Call(uintptr) uint32
//export local_export_v1
func local_export_v1(value uintptr) uint32 { return uint32(value) }
var Address = Call
func Root(value uintptr) uint32 { return Call(value) }
`)
	testProg.ssa.Build()
	program := llssa.NewProgram(nil)
	defer program.Dispose()
	universe, err := prepareStacklessEmissionUniverse(program, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "local-export-owner",
	}})
	if err != nil {
		t.Fatal(err)
	}
	declaration := pkg.ssa.Func("Call")
	target := pkg.ssa.Func("local_export_v1")
	call := onlyLocalExportStaticCall(t, pkg.ssa.Func("Root"), declaration)
	site, frozen, err := universe.CoroCallSitePlan(call)
	resolved, contained := universe.Resolve(declaration)
	if err != nil || !frozen || site.ManagedStaticTarget != target ||
		resolved != declaration || !contained || !universe.Contains(declaration) {
		t.Fatalf(
			"mixed managed/raw local export = site %+v, %t, %v declaration %v, %t",
			site, frozen, err, resolved, contained,
		)
	}
}

func TestEmissionUniverseLocalCExportPreservesRawPlainIngress(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/localexport/raw-plain", `package localexport
//go:linkname Call C.local_export_v1
func Call(uintptr) uint32
//export local_export_v1
func local_export_v1(value uintptr) uint32 { return uint32(value) }
func Root(value uintptr) uint32 { return Call(value) }
`)
	testProg.ssa.Build()
	program := llssa.NewProgram(nil)
	defer program.Dispose()
	universe, err := prepareStacklessEmissionUniverse(program, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file}, Identity: "local-export-owner",
	}})
	if err != nil {
		t.Fatal(err)
	}
	declaration := pkg.ssa.Func("Call")
	root := pkg.ssa.Func("Root")
	plan := analyzeCoroRawPlainValidationPlan(
		t,
		universe,
		pkg.ssa,
		root,
		coro.SSAConfig{
			ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
				switch function {
				case declaration:
					return coro.SSAFunctionPolicy{
						IgnoreBody:       true,
						External:         coro.ExternalUnknownForeign,
						OverrideExternal: true,
					}, nil
				case root:
					return coro.SSAFunctionPolicy{RawPlainEntry: true}, nil
				default:
					return coro.SSAFunctionPolicy{}, nil
				}
			},
			ClassifyStaticCallTarget: func(
				_ *ssa.Function,
				call ssa.CallInstruction,
			) (*ssa.Function, bool, error) {
				site, frozen, err := universe.CoroCallSitePlan(call)
				if err != nil || !frozen {
					return nil, false, err
				}
				return site.ManagedStaticTarget, site.ManagedStaticTarget != nil, nil
			},
		},
	)
	target := pkg.ssa.Func("local_export_v1")
	targetPlan, planned := plan.FunctionPlan(target)
	if !planned || !targetPlan.RawPlainDemand || !plan.HasRawPlainVariant(target) {
		t.Fatalf(
			"local export raw target = %+v, %t, variant=%t",
			targetPlan, planned, plan.HasRawPlainVariant(target),
		)
	}
	if err := rawPlainValidationCompilation(plan, universe, false).preflightCoroPlan(); err != nil {
		t.Fatalf("certified local export raw/plain ingress was rejected: %v", err)
	}
}

func onlyLocalExportStaticCall(
	t *testing.T,
	owner, target *ssa.Function,
) ssa.CallInstruction {
	t.Helper()
	var found ssa.CallInstruction
	for _, block := range owner.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok || call.Common() == nil || call.Common().StaticCallee() != target {
				continue
			}
			if found != nil {
				t.Fatalf("function %q has more than one static call to %q", owner.Name(), target.Name())
			}
			found = call
		}
	}
	if found == nil {
		t.Fatalf("function %q has no static call to %q", owner.Name(), target.Name())
	}
	return found
}
