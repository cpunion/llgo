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
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroExportIngressEmitsOneManagedBodyAndThinPublicAdapter(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
//export export_plain_v1
func export_plain_v1(value int32) int32 { return value + 1 }

//export export_suspend_v1
func export_suspend_v1(value int32) int32 { return value + 2 }
`)
	prog := newLLSSAProg(t)
	universe, err := PrepareEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
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
	plain := ssaPkg.Func("export_plain_v1")
	suspending := ssaPkg.Func("export_suspend_v1")
	roots := make(coro.Roots, 0, 2)
	for _, function := range []*ssa.Function{plain, suspending} {
		certificate, certified, certificateErr :=
			universe.CoroPlanningMetadata().ExportIngressCertificate(function)
		if certificateErr != nil || !certified || certificate == "" {
			prog.Dispose()
			t.Fatalf(
				"export ingress certificate for %q = %q, %t, %v",
				function.Name(), certificate, certified, certificateErr,
			)
		}
		roots = append(roots, coro.Root{
			Function: function, ManagedDemand: coro.AsyncDemand,
			IngressEntry: true, IngressCertificate: certificate,
		})
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, roots, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyLocalBody:    universe.CoroLocalBodyFacts,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == suspending {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	if factories := plan.RootFactoryRoots(); len(factories) != 0 {
		prog.Dispose()
		t.Fatalf("export-only ingress emitted %d generic root factories: %+v", len(factories), factories)
	}
	plainPlan, plainPlanned := plan.FunctionPlan(plain)
	suspendPlan, suspendPlanned := plan.FunctionPlan(suspending)
	if !plainPlanned || plainPlan.Emission != coro.EmitPlain ||
		plainPlan.RawPlainDemand || plainPlan.RawPlainEntry ||
		!suspendPlanned || suspendPlan.Emission != coro.EmitCoroutine ||
		suspendPlan.RawPlainDemand || suspendPlan.RawPlainEntry {
		prog.Dispose()
		t.Fatalf(
			"export ingress plans: plain=%+v/%t suspend=%+v/%t",
			plainPlan, plainPlanned, suspendPlan, suspendPlanned,
		)
	}

	compilation := &Compilation{
		CoroPlan: plan, EmissionUniverse: universe,
		CoroTargetCapabilities: CoroNativeTargetCapabilities(),
	}
	enableCoroChildAwaitCompilation(compilation)
	compilation.SchedulerABI = coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0
	endianness := ""
	switch prog.TargetData().ByteOrder() {
	case llvm.LittleEndian:
		endianness = "little"
	case llvm.BigEndian:
		endianness = "big"
	default:
		prog.Dispose()
		t.Fatal("unknown target byte order")
	}
	compilation.CoroPlanDigest = strings.Repeat("0", 64)
	compilation.CoroPlanMetadata = coro.PlanDigestMetadata{
		CoroABI:        compilation.CoroABI,
		SchedulerABI:   compilation.SchedulerABI,
		PanicABI:       compilation.PanicABI,
		FuncRepABI:     compilation.FuncRepABI,
		TargetTriple:   prog.TargetSpec().Triple,
		TargetCPU:      prog.TargetSpec().CPU,
		TargetFeatures: prog.TargetSpec().Features,
		TargetABI:      prog.TargetSpec().TargetABI,
		PointerBits:    prog.PointerSize() * 8,
		Endianness:     endianness,
		DataLayout:     prog.DataLayout(),
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify export ingress module: %v\n%s", err, module.String())
	}
	var plainRamp llvm.Value
	for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
		if !strings.HasPrefix(function.Name(), coroForeignReentryPlainRampPrefixV1) {
			continue
		}
		if !plainRamp.IsNil() {
			t.Fatalf(
				"export ingress emitted multiple logical plain ramps %q and %q:\n%s",
				plainRamp.Name(), function.Name(), module.String(),
			)
		}
		plainRamp = function
	}
	if plainRamp.IsNil() {
		t.Fatalf("plain export ingress omitted its one typed coroutine ramp:\n%s", module.String())
	}

	for _, test := range []struct {
		public  string
		managed string
		plain   bool
	}{
		{public: "export_plain_v1", managed: "export_plain_v1$managed", plain: true},
		{public: "export_suspend_v1", managed: "export_suspend_v1$coro"},
	} {
		adapter := module.NamedFunction(test.public)
		managed := module.NamedFunction(test.managed)
		if adapter.IsNil() || managed.IsNil() {
			t.Fatalf("export ingress %q lacks adapter or managed primary %q:\n%s", test.public, test.managed, module.String())
		}
		if linkage := adapter.Linkage(); linkage != llvm.ExternalLinkage {
			t.Fatalf("public export ingress adapter %q linkage = %v, want external", test.public, linkage)
		}
		body := adapter.String()
		for _, hook := range []string{
			coroForeignReentryAcquireHookV1,
			coroForeignReentryRunHookV1,
			coroForeignReentryFailureHookV1,
		} {
			if count := coroExportIngressCallCount(body, hook); count != 1 {
				t.Fatalf("export ingress adapter %q calls %q %d times, want one:\n%s", test.public, hook, count, body)
			}
		}
		child := test.managed
		if test.plain {
			child = plainRamp.Name()
		}
		if count := coroExportIngressCallCount(body, child); count != 1 {
			t.Fatalf("export ingress adapter %q calls its exact child %q %d times, want one:\n%s", test.public, child, count, body)
		}
		if coroExportIngressHasCoroutineBody(body) {
			t.Fatalf("thin export ingress adapter %q became a coroutine body:\n%s", test.public, body)
		}
	}
	if count := coroExportIngressCallCount(plainRamp.String(), "export_plain_v1$managed"); count != 1 {
		t.Fatalf("plain ingress ramp calls the sole source body %d times, want one:\n%s", count, plainRamp.String())
	}
	ir := module.String()
	if strings.Contains(ir, coroRootFactoryPrefix) ||
		strings.Contains(ir, "export_plain_v1$raw") ||
		strings.Contains(ir, "export_suspend_v1$raw") {
		t.Fatalf("export ingress module retained a root factory or raw twin:\n%s", ir)
	}
	records, err := CoroLibraryEffectSummaryRecords(pkg)
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := coro.ParseLibraryEffectSummaryRecords(records)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || len(summaries[0].Functions) != 2 ||
		len(summaries[0].ExportBindings) != 2 ||
		len(summaries[0].ExportIngresses) != 2 {
		t.Fatalf("export ingress library summary shape = %+v", summaries)
	}
	functions := make(map[coro.FunctionID]coro.LibraryEffectFunction)
	bindings := make(map[string]coro.LibraryEffectExportBinding)
	for _, function := range summaries[0].Functions {
		functions[function.ID] = function
	}
	for _, binding := range summaries[0].ExportBindings {
		bindings[binding.Symbol] = binding
	}
	for _, ingress := range summaries[0].ExportIngresses {
		binding, bound := bindings[ingress.Symbol]
		function, emitted := functions[ingress.Function]
		if !bound || !emitted || !function.ExportIngress ||
			function.RawPlainSymbol != "" ||
			binding.Function != ingress.Function ||
			binding.ABIHash != ingress.ABIHash ||
			binding.ManagedPrimarySymbol != function.PrimarySymbol ||
			ingress.AdapterABI != coro.LibraryEffectExportIngressABIV1 ||
			len(ingress.Certificate) != 64 {
			t.Fatalf(
				"export ingress library capability %q disagrees: ingress=%+v binding=%+v function=%+v",
				ingress.Symbol, ingress, binding, function,
			)
		}
	}

	// CoroSplit may create the backend resume/destroy continuations required by
	// each stackless body, but it must not turn either public adapter into a
	// coroutine or manufacture another logical source body/ramp.
	runCoroABITestPipeline(t, prog, module)
	post := module.String()
	if strings.Contains(post, coroRootFactoryPrefix) ||
		strings.Contains(post, "export_plain_v1$raw") ||
		strings.Contains(post, "export_suspend_v1$raw") {
		t.Fatalf("CoroSplit manufactured a root factory or raw twin:\n%s", post)
	}
	for _, public := range []string{"export_plain_v1", "export_suspend_v1"} {
		adapter := module.NamedFunction(public)
		if adapter.IsNil() || coroExportIngressHasCoroutineBody(adapter.String()) {
			t.Fatalf("CoroSplit lost or transformed thin public adapter %q:\n%s", public, post)
		}
		for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
			if strings.HasPrefix(function.Name(), public+".") {
				t.Fatalf("public adapter %q gained backend continuation %q:\n%s", public, function.Name(), post)
			}
		}
	}
	for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
		name := function.Name()
		switch {
		case strings.HasPrefix(name, "export_plain_v1$") && name != "export_plain_v1$managed":
			t.Fatalf("plain export source body gained an unexpected physical twin %q:\n%s", name, post)
		case strings.HasPrefix(name, "export_suspend_v1$") &&
			name != "export_suspend_v1$coro" &&
			!strings.HasPrefix(name, "export_suspend_v1$coro."):
			t.Fatalf("suspending export source body gained an unexpected physical twin %q:\n%s", name, post)
		case strings.HasPrefix(name, coroForeignReentryPlainRampPrefixV1) &&
			!strings.HasPrefix(name, plainRamp.Name()):
			t.Fatalf("CoroSplit manufactured a second logical plain ramp %q:\n%s", name, post)
		}
	}
}

func TestCoroExportIngressCertificateRequiresUniquePhysicalSymbol(t *testing.T) {
	testProg := newEmissionTestProgram()
	first := testProg.addPackage(t, "example.com/export/first", `package first
//export duplicate_export_v1
func duplicate_export_v1(value int32) int32 { return value + 1 }
`)
	second := testProg.addPackage(t, "example.com/export/second", `package second
//export duplicate_export_v1
func duplicate_export_v1(value int32) int32 { return value + 2 }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{
			{SSA: first.ssa, Files: []*ast.File{first.file}, Identity: "export-first"},
			{SSA: second.ssa, Files: []*ast.File{second.file}, Identity: "export-second"},
		},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []emissionTestPackage{first, second} {
		function := pkg.ssa.Func("duplicate_export_v1")
		certificate, certified, err := universe.CoroPlanningMetadata().ExportIngressCertificate(function)
		if err != nil || certified || certificate != "" {
			t.Fatalf(
				"ambiguous export target %q certificate = %q, %t, %v; want absent",
				function, certificate, certified, err,
			)
		}
	}
}

func coroExportIngressHasCoroutineBody(body string) bool {
	for _, intrinsic := range []string{
		"@llvm.coro.id(",
		"@llvm.coro.begin(",
		"@llvm.coro.save(",
		"@llvm.coro.suspend(",
		"@llvm.coro.end(",
	} {
		if strings.Contains(body, intrinsic) {
			return true
		}
	}
	return false
}

func coroExportIngressCallCount(body, function string) int {
	return strings.Count(body, "@"+function+"(") +
		strings.Count(body, "@\""+function+"\"(")
}
