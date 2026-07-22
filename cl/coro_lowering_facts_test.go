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
	"encoding/hex"
	"go/ast"
	"go/importer"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroLoweringFactsCallerSource = `package loweringfacts

func AllocatePair(flag bool) (*int, *int) {
	first := new(int)
	if flag {
		*first = 1
	}
	second := new(int)
	return first, second
}
`

func TestCoroLoweringFactsReportIsStableSparseAndPreservesHelperSites(t *testing.T) {
	plain, plainOwnerID, plainDebugRefs := buildCoroLoweringFactsTestReport(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	debug, debugOwnerID, debugRefs := buildCoroLoweringFactsTestReport(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics|ssa.GlobalDebug)
	if plainDebugRefs != 0 || debugRefs == 0 {
		t.Fatalf("debug refs: plain=%d debug=%d", plainDebugRefs, debugRefs)
	}
	if plainOwnerID != debugOwnerID {
		t.Fatalf("DebugRef changed owner FunctionID: %q != %q", plainOwnerID, debugOwnerID)
	}
	plainJSON, err := plain.Facts.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	debugJSON, err := debug.Facts.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plainJSON, debugJSON) || plain.Digest != debug.Digest {
		t.Fatalf("DebugRef changed lowering facts:\nplain %s %s\ndebug %s %s", plain.Digest, plainJSON, debug.Digest, debugJSON)
	}
	if len(plain.Digest) != 64 {
		t.Fatalf("facts digest length = %d", len(plain.Digest))
	}
	if _, err := hex.DecodeString(plain.Digest); err != nil {
		t.Fatalf("facts digest is not canonical hexadecimal: %v", err)
	}

	ownerFacts := loweringFactsFunctionByID(t, plain.Facts, plainOwnerID)
	if ownerFacts.Instance.Owner != "caller-variant" {
		t.Fatalf("owner identity = %q", ownerFacts.Instance.Owner)
	}
	if len(ownerFacts.Instance.Context) != 64 {
		t.Fatalf("owner context = %q, want SHA-256 identity", ownerFacts.Instance.Context)
	}
	if _, err := hex.DecodeString(ownerFacts.Instance.Context); err != nil {
		t.Fatalf("owner context is not hexadecimal: %v", err)
	}
	helperSites := 0
	var helperTarget coro.FunctionID
	seenSites := make(map[coro.EmissionSiteID]bool)
	for _, fact := range ownerFacts.Sites {
		if seenSites[fact.Site] {
			t.Fatalf("duplicate fact site %+v", fact.Site)
		}
		seenSites[fact.Site] = true
		if fact.Site.Source.Kind != coro.SourceInstruction || fact.Site.Source.Function != plainOwnerID {
			t.Fatalf("non-instruction or wrong-function fact site %+v", fact.Site)
		}
		for _, helper := range fact.Helpers {
			if helper.LogicalName != "AllocZ" {
				continue
			}
			helperSites++
			if helper.Order != 0 || helper.Ordinal != 0 || helper.Role != coro.RoleHelper {
				t.Fatalf("AllocZ helper subsite = %+v", helper)
			}
			if helperTarget == "" {
				helperTarget = helper.Target
			} else if helper.Target != helperTarget {
				t.Fatalf("AllocZ sites resolved different targets: %q and %q", helperTarget, helper.Target)
			}
		}
	}
	if helperSites != 2 {
		t.Fatalf("AllocZ helper sites = %d, want two exact source occurrences; facts=%+v", helperSites, ownerFacts.Sites)
	}
	if len(ownerFacts.Sites) >= loweringFactsSemanticInstructionCount(t, coroLoweringFactsCallerSource) {
		t.Fatalf("facts are not sparse: sites=%d", len(ownerFacts.Sites))
	}
}

func TestCompilationBuildCoroLoweringFactsReportIsDeterministic(t *testing.T) {
	report, _, _, compilation := buildCoroLoweringFactsTestFixture(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	first, err := compilation.BuildCoroLoweringFactsReport()
	if err != nil {
		t.Fatal(err)
	}
	second, err := compilation.BuildCoroLoweringFactsReport()
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := first.Facts.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := second.Facts.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != report.Digest || first.Digest != second.Digest || !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("repeated report changed: initial=%q first=%q second=%q", report.Digest, first.Digest, second.Digest)
	}
	if err := first.Facts.Verify(); err != nil {
		t.Fatalf("reported facts do not verify: %v", err)
	}
}

func TestCoroLoweringFactsRecordsConditionalManagedStoreDecision(t *testing.T) {
	for _, test := range []struct {
		name       string
		liveTarget bool
		wantRecipe coro.RecipeID
	}{
		{"dormant target", false, "cl.ssa.conditional-managed-store.elide.v0"},
		{"live target", true, "cl.ssa.conditional-managed-store.publish.v0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProgram := newCoroLoweringFactsEmissionTestProgram(ssa.SanityCheckFunctions | ssa.InstantiateGenerics)
			runtimePackage := testProgram.addPackage(t, llssa.PkgRuntime, `package runtime
func AllocZ(size uintptr) uintptr { return 0 }
`)
			callerPackage := testProgram.addPackage(t, "example.com/emission/conditional-store", `package conditionalstore
var slot func()
func Target() {}
func Publish() { slot = Target }
func Live() { Target() }
`)
			testProgram.ssa.Build()
			publish := callerPackage.ssa.Func("Publish")
			target := callerPackage.ssa.Func("Target")
			var publication *ssa.Store
			for _, block := range publish.Blocks {
				for _, instruction := range block.Instrs {
					if store, ok := instruction.(*ssa.Store); ok && store.Val == target {
						publication = store
					}
				}
			}
			if publication == nil {
				t.Fatal("Publish has no exact Target Store")
			}
			prog := newLLSSAProg(t)
			t.Cleanup(prog.Dispose)
			universe, err := prepareStacklessEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
				{SSA: runtimePackage.ssa, Files: []*ast.File{runtimePackage.file}, Identity: "runtime-variant"},
				{SSA: callerPackage.ssa, Files: []*ast.File{callerPackage.file}, Identity: "caller-variant"},
			}, EmissionUniverseOptions{CompleteRuntimeABI: true})
			if err != nil {
				t.Fatal(err)
			}
			ssaUniverse, err := coro.NewSSAEmissionUniverse(testProgram.ssa, universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			functionIDs := universe.FunctionIDConfig()
			functionIDs.CoroABI = coro.PhysicalABIV1
			functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
			functionIDs.ArchiveReady = true
			roots := coro.Roots{{Function: publish, Demand: coro.AsyncDemand}}
			if test.liveTarget {
				roots = append(roots, coro.Root{Function: callerPackage.ssa.Func("Live"), Demand: coro.AsyncDemand})
			}
			plan, err := coro.AnalyzeSSA(testProgram.ssa, roots, coro.SSAConfig{
				FunctionIDs: functionIDs, EmissionUniverse: ssaUniverse, MaxPlainInstructions: -1,
				ResolveFunction: func(function *ssa.Function) (*ssa.Function, bool, error) {
					resolved, ok := universe.Resolve(function)
					return resolved, ok, nil
				},
				ClassifyConditionalManagedStoreReference: func(owner *ssa.Function, store *ssa.Store) (*ssa.Function, bool, error) {
					if owner == publish && store == publication {
						return target, true, nil
					}
					return nil, false, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			report, err := (&Compilation{CoroPlan: plan, EmissionUniverse: universe}).BuildCoroLoweringFactsReport()
			if err != nil {
				t.Fatal(err)
			}
			publishID, _ := plan.FunctionID(publish)
			targetID, _ := plan.FunctionID(target)
			facts := loweringFactsFunctionByID(t, report.Facts, publishID)
			var matched []coro.LoweringFact
			for _, fact := range facts.Sites {
				if fact.Contract == "llgo.coro.conditional-managed-publication.v0" {
					matched = append(matched, fact)
				}
			}
			if len(matched) != 1 || matched[0].Recipe != test.wantRecipe || len(matched[0].FunctionUses) != 1 ||
				len(matched[0].FunctionUses[0].Targets) != 1 || matched[0].FunctionUses[0].Targets[0] != targetID {
				t.Fatalf("conditional Store lowering facts = %+v", matched)
			}
		})
	}
}

func TestCoroLoweringFactsReportCriticalRegionContract(t *testing.T) {
	testProgram := newCoroLoweringFactsEmissionTestProgram(ssa.SanityCheckFunctions | ssa.InstantiateGenerics)
	testProgram.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePackage := testProgram.addPackage(t, llssa.PkgRuntime, `package runtime`)
	callerPackage := testProgram.addPackage(t, "example.com/emission/loweringfacts-critical", `package critical
import _ "unsafe"
//go:linkname enter llgo.coroCriticalEnter
func enter()
//go:linkname exit llgo.coroCriticalExit
func exit()
var cell uint32
func Root(value uint32) uint32 {
	enter()
	cell = value
	value = cell
	exit()
	return value
}`)
	testProgram.ssa.Build()
	prog := newLLSSAProg(t)
	t.Cleanup(prog.Dispose)
	universe, err := prepareStacklessEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePackage.ssa, Files: []*ast.File{runtimePackage.file}, Identity: "runtime-critical"},
		{SSA: callerPackage.ssa, Files: []*ast.File{callerPackage.file}, Identity: "caller-critical"},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		t.Fatal(err)
	}
	root := callerPackage.ssa.Func("Root")
	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProgram.ssa, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(testProgram.ssa, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		FunctionIDs:          functionIDs,
		EmissionUniverse:     ssaUniverse,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == root {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, Exec: coro.NeedsPreempt}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			if callee != nil && callee.Pkg != nil && callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init" {
				return true, nil
			}
			semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
			return intrinsic && semantics.ElidesManagedCall(), err
		},
		ClassifyLoweredCalls: universe.CoroLoweredCalls,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := (&Compilation{CoroPlan: plan, EmissionUniverse: universe}).BuildCoroLoweringFactsReport()
	if err != nil {
		t.Fatal(err)
	}
	rootID, ok := plan.FunctionID(root)
	if !ok {
		t.Fatal("critical lowering-facts Root has no FunctionID")
	}
	facts := loweringFactsFunctionByID(t, report.Facts, rootID)
	found := map[coro.SiteRole]coro.LoweringFact{}
	for _, fact := range facts.Sites {
		if fact.Site.Source.Role == coro.RoleRegionBegin || fact.Site.Source.Role == coro.RoleRegionEnd {
			found[fact.Site.Source.Role] = fact
		}
	}
	begin, beginOK := found[coro.RoleRegionBegin]
	end, endOK := found[coro.RoleRegionEnd]
	if !beginOK || begin.Recipe != "cl.intrinsic.coro-critical-enter.v1" || begin.Effect != coro.NoSuspend ||
		begin.Contract != "llgo.coro.critical-depth.v1" || !begin.Footprint.Contains(coro.FootprintBarrier) || begin.Footprint.Contains(coro.FootprintSuspend) {
		t.Fatalf("critical begin fact = %+v, present=%t", begin, beginOK)
	}
	if !endOK || end.Recipe != "cl.intrinsic.coro-critical-exit.v1" || end.Effect != coro.YieldOnly ||
		end.Contract != "llgo.coro.critical-depth.v1" ||
		!end.Footprint.Contains(coro.FootprintBarrier|coro.FootprintSuspend) {
		t.Fatalf("critical end fact = %+v, present=%t", end, endOK)
	}
}

func TestCoroLoweringFactsReportFailsClosedWithoutFrozenInputs(t *testing.T) {
	var nilCompilation *Compilation
	if _, err := nilCompilation.BuildCoroLoweringFactsReport(); err == nil || !strings.Contains(err.Error(), "compilation") {
		t.Fatalf("nil Compilation error = %v", err)
	}
	if _, err := (&Compilation{}).BuildCoroLoweringFactsReport(); err == nil || !strings.Contains(err.Error(), "CoroPlan") {
		t.Fatalf("missing plan error = %v", err)
	}

	_, _, _, complete := buildCoroLoweringFactsTestFixture(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	testProgram := newCoroLoweringFactsEmissionTestProgram(ssa.SanityCheckFunctions | ssa.InstantiateGenerics)
	caller := testProgram.addPackage(t, "example.com/emission/loweringfacts-incomplete", coroLoweringFactsCallerSource)
	testProgram.ssa.Build()
	prog := newLLSSAProg(t)
	t.Cleanup(prog.Dispose)
	incomplete, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: caller.ssa, Files: []*ast.File{caller.file}, Identity: "incomplete-caller",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := incomplete.BuildCoroLoweringFactsReport(complete.CoroPlan); err == nil || !strings.Contains(err.Error(), "validate plan coverage") {
		t.Fatalf("incomplete universe error = %v", err)
	}
}

func buildCoroLoweringFactsTestReport(t *testing.T, mode ssa.BuilderMode) (CoroLoweringFactsReport, coro.FunctionID, int) {
	t.Helper()
	report, ownerID, debugRefs, _ := buildCoroLoweringFactsTestFixture(t, mode)
	return report, ownerID, debugRefs
}

func buildCoroLoweringFactsTestFixture(t *testing.T, mode ssa.BuilderMode) (CoroLoweringFactsReport, coro.FunctionID, int, *Compilation) {
	t.Helper()
	testProgram := newCoroLoweringFactsEmissionTestProgram(mode)
	runtimePackage := testProgram.addPackage(t, llssa.PkgRuntime, `package runtime
func AllocZ(size uintptr) uintptr { return 0 }
`)
	callerPackage := testProgram.addPackage(t, "example.com/emission/loweringfacts", coroLoweringFactsCallerSource)
	testProgram.ssa.Build()
	prog := newLLSSAProg(t)
	t.Cleanup(prog.Dispose)
	universe, err := prepareStacklessEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePackage.ssa, Files: []*ast.File{runtimePackage.file}, Identity: "runtime-variant"},
		{SSA: callerPackage.ssa, Files: []*ast.File{callerPackage.file}, Identity: "caller-variant"},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		t.Fatal(err)
	}
	owner := callerPackage.ssa.Func("AllocatePair")
	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProgram.ssa, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(testProgram.ssa, coro.Roots{{Function: owner, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		FunctionIDs:       functionIDs,
		EmissionUniverse:  ssaUniverse,
		ClassifyLocalBody: universe.CoroLocalBodyFacts,
		ResolveFunction: func(function *ssa.Function) (*ssa.Function, bool, error) {
			resolved, ok := universe.Resolve(function)
			return resolved, ok, nil
		},
		MaxPlainInstructions: -1,
		ClassifyLoweredCalls: universe.CoroLoweredCalls,
	})
	if err != nil {
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	report, err := compilation.BuildCoroLoweringFactsReport()
	if err != nil {
		t.Fatal(err)
	}
	ownerID, ok := plan.FunctionID(owner)
	if !ok {
		t.Fatal("owner has no FunctionID")
	}
	debugRefs := 0
	for _, block := range owner.Blocks {
		for _, instruction := range block.Instrs {
			if _, debug := instruction.(*ssa.DebugRef); debug {
				debugRefs++
			}
		}
	}
	return report, ownerID, debugRefs, compilation
}

func newCoroLoweringFactsEmissionTestProgram(mode ssa.BuilderMode) *emissionTestProgram {
	fset := token.NewFileSet()
	return &emissionTestProgram{
		fset: fset,
		ssa:  ssa.NewProgram(fset, mode),
		importer: &emissionTestImporter{
			packages: make(map[string]*types.Package),
			fallback: importer.Default(),
		},
	}
}

func loweringFactsFunctionByID(t *testing.T, facts coro.LoweringFacts, id coro.FunctionID) coro.FunctionLoweringFacts {
	t.Helper()
	var matches []coro.FunctionLoweringFacts
	for _, function := range facts.Functions {
		if function.Instance.Function == id {
			matches = append(matches, function)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("facts for function %q = %d, want one owner instance", id, len(matches))
	}
	return matches[0]
}

func loweringFactsSemanticInstructionCount(t *testing.T, source string) int {
	t.Helper()
	program := newCoroLoweringFactsEmissionTestProgram(ssa.SanityCheckFunctions | ssa.InstantiateGenerics)
	pkg := program.addPackage(t, "example.com/emission/loweringfacts-count", source)
	program.ssa.Build()
	count := 0
	for _, block := range pkg.ssa.Func("AllocatePair").Blocks {
		for _, instruction := range block.Instrs {
			if _, debug := instruction.(*ssa.DebugRef); !debug {
				count++
			}
		}
	}
	return count
}
