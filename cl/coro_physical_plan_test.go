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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"golang.org/x/tools/go/ssa"
)

func TestCoroPhysicalPlanRuntimeHelperElisionIsRecipeOwned(t *testing.T) {
	tests := []struct {
		name   string
		plan   coroPhysicalInstructionPlan
		helper string
		want   bool
	}{
		{name: "ordinary keeps helper", helper: "NewSlice2"},
		{name: "slice two index", plan: coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionSlice}, helper: "NewSlice2", want: true},
		{name: "slice three index", plan: coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionSlice}, helper: "NewSlice3Bounds", want: true},
		{name: "slice keeps unrelated", plan: coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionSlice}, helper: "AllocU"},
		{name: "index range", plan: coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionIndex}, helper: "CheckIndexRange", want: true},
		{name: "deref nil", plan: coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionDeref}, helper: "AssertNilDeref", want: true},
		{name: "slice conversion", plan: coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionSliceToArrayPointer}, helper: "PanicSliceConvert", want: true},
		{name: "wrapper nil", plan: coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionBuiltinNilGuard}, helper: "PanicWrapNilPointer", want: true},
		{name: "checked interface pointer nil", plan: coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionInterfaceFromCheckedPtr}, helper: "AssertNilDeref", want: true},
		{name: "checked interface pointer keeps allocation", plan: coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionInterfaceFromCheckedPtr}, helper: "AllocU"},
		{name: "unsafe slice", plan: coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionUnsafeSlice}, helper: "AssertRuntimeError", want: true},
		{name: "interface nil", plan: coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionInterfaceNilCompare}, helper: "EfaceEqual", want: true},
		{name: "frame allocation", plan: coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionFrameAllocation}, helper: "AllocZ", want: true},
		{name: "frame allocation keeps unrelated", plan: coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionFrameAllocation}, helper: "AllocU"},
		{name: "panic outcome", plan: coroPhysicalInstructionPlan{outcome: coroPhysicalOutcomePanic}, helper: "Panic", want: true},
		{name: "recover outcome", plan: coroPhysicalInstructionPlan{outcome: coroPhysicalOutcomeRecover}, helper: "Recover", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.plan.elidesRuntimeHelper(test.helper); got != test.want {
				t.Fatalf("elidesRuntimeHelper(%q) = %t, want %t", test.helper, got, test.want)
			}
		})
	}
}

func TestCoroPhysicalPlanStageIsAtomicExactAndSingleCommit(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func Root(value int) int { return value + 1 }
`)
	root := ssaPkg.Func("Root")
	owner := &preparedEmissionPackage{identity: "foo"}
	physical := &coroPhysicalFunctionPlan{
		function:     root,
		owner:        owner,
		instructions: make(map[ssa.Instruction]coroPhysicalInstructionPlan),
	}
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			physical.instructions[instruction] = coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionOrdinary}
		}
	}
	key := emissionFunctionOwnerKey{function: root, owner: owner}
	expected := map[emissionFunctionOwnerKey]none{key: {}}

	stage := newCoroPhysicalPlanStage()
	if err := stage.freezePhysicalFunctionPlan(physical); err != nil {
		t.Fatal(err)
	}
	if err := stage.freezePhysicalFunctionPlan(physical); err == nil || !strings.Contains(err.Error(), "frozen more than once") {
		t.Fatalf("duplicate physical freeze = %v", err)
	}

	missing := newCoroProgramIR()
	missing.callsFrozen = true
	if err := missing.commitPhysicalFunctionPlans(newCoroPhysicalPlanStage(), expected); err == nil ||
		!strings.Contains(err.Error(), "has 0 function owners, want 1") {
		t.Fatalf("incomplete physical commit = %v", err)
	}
	if missing.physicalPlansSealed || len(missing.physicalPlans) != 0 {
		t.Fatal("failed physical commit mutated ProgramIR")
	}

	ir := newCoroProgramIR()
	ir.callsFrozen = true
	if err := ir.commitPhysicalFunctionPlans(stage, expected); err != nil {
		t.Fatal(err)
	}
	if loaded, err := ir.physicalFunctionPlan(root, owner); err != nil || loaded != physical {
		t.Fatalf("frozen physical lookup = %p, %v; want %p", loaded, err, physical)
	}
	if err := ir.commitPhysicalFunctionPlans(stage, expected); err == nil || !strings.Contains(err.Error(), "committed more than once") {
		t.Fatalf("second physical commit = %v", err)
	}
}

func TestCoroPhysicalPlanOwnsManagedToRawPlainCall(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
func Critical() {}
func Root() {
	Critical()
	for {}
}
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(
		prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}},
	)
	if err != nil {
		t.Fatal(err)
	}
	root, critical := ssaPkg.Func("Root"), ssaPkg.Func("Critical")
	var rawCall *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common().StaticCallee() == critical {
				rawCall = call
			}
		}
	}
	if rawCall == nil {
		t.Fatal("Root has no static Critical call")
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	whole, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{{Function: root, ManagedDemand: coro.AsyncDemand}},
		coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == critical {
					return coro.SSAFunctionPolicy{
						TrustedNoPreempt: true,
						TrustedNoUnwind:  true,
					}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
			ClassifyRawPlainCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.SSARawPlainCallCertificate, bool, error) {
				if call == rawCall {
					return coro.SSARawPlainCallCertificate{ID: "test.raw-critical.v0"}, true, nil
				}
				return coro.SSARawPlainCallCertificate{}, false, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rootPlan, found := whole.FunctionPlan(root)
	if !found || rootPlan.Emission != coro.EmitCoroutine {
		t.Fatalf("Root plan = %+v, present=%t; want physical coroutine", rootPlan, found)
	}
	criticalPlan, found := whole.FunctionPlan(critical)
	if !found || criticalPlan.Emission != coro.EmitRawPlain || !criticalPlan.RawPlainOnly {
		t.Fatalf("Critical plan = %+v, present=%t; want private raw/plain body", criticalPlan, found)
	}
	audit, err := newCoroPhysicalPureSSAAudit(universe, whole, root, "")
	if err != nil {
		t.Fatal(err)
	}
	instructionPlan := coroPhysicalInstructionPlan{}
	planCoroPhysicalControlInstruction(
		audit, whole, rawCall,
		coroPhysicalLoweringCapabilities{childAwait: true},
		&instructionPlan,
	)
	if instructionPlan.controlFailure != "" || instructionPlan.control != coroPhysicalControlRawPlainCall ||
		instructionPlan.controlTarget != critical || instructionPlan.controlTargetID != criticalPlan.ID {
		t.Fatalf("raw/plain physical control plan = %+v", instructionPlan)
	}
}

func TestCoroPhysicalPlanKeepsClosedNilOnlyDispatchInCurrentFrame(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
var callback func() (bool, error)
func Root() {
	if callback != nil {
		callback()
	}
}
`)
	root := ssaPkg.Func("Root")
	var dynamicCall *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common().StaticCallee() == nil {
				dynamicCall = call
			}
		}
	}
	if dynamicCall == nil {
		t.Fatal("Root has no dynamic callback call")
	}

	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(
		prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}},
	)
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	whole, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{{Function: root, ManagedDemand: coro.AsyncDemand}},
		coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
			OutcomeMode:          coro.OutcomeExplicitStatus,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == root {
					return coro.SSAFunctionPolicy{
						Effect: coro.OutcomeStructured,
						Exec:   coro.MayUnwind,
					}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
			ClassifyClosedDynamicCall: func(
				_ *ssa.Function, call ssa.CallInstruction,
			) (coro.SSAClosedDynamicCallCertificate, bool, error) {
				if call == dynamicCall {
					return coro.SSAClosedDynamicCallCertificate{MayBeNil: true}, true, nil
				}
				return coro.SSAClosedDynamicCallCertificate{}, false, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rootPlan, found := whole.FunctionPlan(root)
	if !found || rootPlan.Emission != coro.EmitCoroutine ||
		rootPlan.Effect != coro.OutcomeStructured ||
		rootPlan.LocalEffect.Contains(coro.AwaitStructured) {
		t.Fatalf("Root plan = %+v, present=%t; want outcome-only physical coroutine", rootPlan, found)
	}
	callPlan, found := whole.CallPlan(dynamicCall)
	if !found || callPlan.Open || !callPlan.MayBeNil || len(callPlan.Targets) != 0 ||
		callPlan.Rep != coro.Dispatch {
		t.Fatalf("nil-only CallPlan = %+v, present=%t", callPlan, found)
	}

	audit, err := newCoroPhysicalPureSSAAudit(universe, whole, root, "")
	if err != nil {
		t.Fatal(err)
	}
	instructionPlan := coroPhysicalInstructionPlan{}
	planCoroPhysicalControlInstruction(
		audit, whole, dynamicCall,
		coroPhysicalLoweringCapabilities{
			childAwait:      true,
			managedDispatch: true,
			explicitPanic:   true,
		},
		&instructionPlan,
	)
	if instructionPlan.controlFailure != "" ||
		instructionPlan.control != coroPhysicalControlNilDispatchFault {
		t.Fatalf("nil-only physical control plan = %+v; want current-frame fault", instructionPlan)
	}
	if err := validateCoroPhysicalABIWithUniverseCapabilitiesFrameRetentionAndChannel(
		root, rootPlan, whole, universe,
		true, true, false, true, "", false, true, false,
	); err != nil {
		t.Fatalf("validate nil-only outcome coroutine: %v", err)
	}

	compilation := &Compilation{CoroPlan: whole, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	physical := requireCoroPhysicalFunction(t, module, "foo.Root").String()
	if strings.Contains(physical, "coro.dispatch.plain") {
		t.Fatalf("nil-only physical call emitted an ordinary descriptor dispatch:\n%s", physical)
	}
}

func TestCoroPhysicalEmissionGeneratedWrapperBorrowsSharedFrozenOwner(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func Root(value int) int { return value + 1 }
`)
	wrapper := ssaPkg.Func("Root")
	wrapper.Pkg = nil
	wrapper.Synthetic = "wrapper for test"
	instruction := wrapper.Blocks[0].Instrs[0]
	declaring := &preparedEmissionPackage{identity: "declaring"}
	consumer := &preparedEmissionPackage{identity: "consumer"}
	physical := &coroPhysicalFunctionPlan{
		function: wrapper,
		owner:    declaring,
	}
	key := emissionFunctionOwnerKey{function: wrapper, owner: declaring}
	ir := newCoroProgramIR()
	ir.physicalPlans = map[emissionFunctionOwnerKey]*coroPhysicalFunctionPlan{key: physical}
	ir.physicalPlansSealed = true
	ir.siteOwners[key] = none{}
	ir.sitePlans[key] = map[ssa.Instruction]coroEmissionSitePlan{
		instruction: {},
	}
	universe := &EmissionUniverse{
		aliases:       make(map[*ssa.Function]*ssa.Function),
		physicalNames: map[emissionFunctionOwnerKey]string{key: "shared.wrapper"},
		useOwners: map[*ssa.Function]map[*preparedEmissionPackage]none{
			wrapper: {declaring: {}},
		},
		coroProgramIR: ir,
	}

	loaded, err := (emissionCanonicalIndex{universe: universe}).physicalFunctionPlanForEmission(wrapper, consumer)
	if err != nil || loaded != physical {
		t.Fatalf("cross-owner wrapper physical plan = %p, %v; want %p", loaded, err, physical)
	}
	ctx := &context{
		emissionOwner: consumer,
		coroEmission: &coroPhysicalEmissionSession{
			phase: coroPhysicalEmissionPrologue,
			plan:  loaded,
		},
	}
	if _, err := ir.sitePlan(ctx, instruction); err != nil {
		t.Fatalf("physical-session SitePlan lookup = %v", err)
	}

	ordinary := *wrapper
	ordinary.Synthetic = ""
	if _, err := (emissionCanonicalIndex{universe: universe}).physicalFunctionPlanForEmission(&ordinary, consumer); err == nil ||
		!strings.Contains(err.Error(), "has no frozen physical plan") {
		t.Fatalf("ordinary cross-owner lookup = %v", err)
	}
}

func TestCoroPhysicalFieldAddrRecipeCoversConstantUnreachableCodegen(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
type Box struct { Value int }
func Address(box *Box) *int { return &box.Value }
`)
	function := ssaPkg.Func("Address")
	var field *ssa.FieldAddr
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.FieldAddr); ok {
				field = candidate
			}
		}
	}
	if field == nil {
		t.Fatal("fixture has no FieldAddr")
	}
	owner := &preparedEmissionPackage{identity: "foo"}
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	ir := newCoroProgramIR()
	ir.siteOwners[key] = none{}
	ir.sitePlans[key] = map[ssa.Instruction]coroEmissionSitePlan{
		field: {
			managedRuntimeHelpers: []coroPlannedRuntimeHelper{{
				name:      "AssertNilDeref",
				placement: coroRuntimeHelperAtSource,
			}},
		},
	}
	audit := &coroPhysicalPureSSAAudit{
		universe:        &EmissionUniverse{coroProgramIR: ir},
		ctx:             &context{emissionOwner: owner},
		fn:              function,
		reachableBlocks: map[*ssa.BasicBlock]bool{field.Block(): false},
	}
	guard, reason := audit.fieldAddrRequiresImplicitNilFault(field)
	if reason != "" || !guard {
		t.Fatalf("constant-unreachable FieldAddr guard = %t, %q; want true", guard, reason)
	}
}

func TestCoroPhysicalRecipeObserverRejectsMissingAndMismatch(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func Root(value int) int { return value + 1 }
`)
	root := ssaPkg.Func("Root")
	instruction := root.Blocks[0].Instrs[0]
	owner := &preparedEmissionPackage{identity: "foo"}
	physical := &coroPhysicalFunctionPlan{
		function: root,
		owner:    owner,
		instructions: map[ssa.Instruction]coroPhysicalInstructionPlan{
			instruction: {
				semantic: coroSemanticInstructionPlan{recipe: coro.RecipeID("test.semantic.v0")},
				recipe:   coroPhysicalInstructionDeref,
				nilGuard: true,
			},
		},
	}
	ctx := &context{
		compilation:      &Compilation{},
		emissionUniverse: &EmissionUniverse{coroProgramIR: newCoroProgramIR()},
		coroEmission: &coroPhysicalEmissionSession{
			phase: coroPhysicalEmissionPrologue,
			plan:  physical,
		},
	}

	missingSemantic := captureCoroSitePlanPanic(func() { ctx.beginCoroSiteEmission(instruction)() })
	if !strings.Contains(missingSemantic, "omitted frozen semantic recipe test.semantic.v0") {
		t.Fatalf("missing semantic observation = %q", missingSemantic)
	}
	missing := captureCoroSitePlanPanic(func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroSemanticInstruction(instruction)
	})
	if !strings.Contains(missing, "omitted frozen physical recipe deref") {
		t.Fatalf("missing physical observation = %q", missing)
	}
	mismatch := captureCoroSitePlanPanic(func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroSemanticInstruction(instruction)
		ctx.observeCoroPhysicalInstruction(instruction, coroPhysicalInstructionIndex)
	})
	if !strings.Contains(mismatch, "emitted physical recipe index, frozen SitePlan requires deref") {
		t.Fatalf("mismatched physical observation = %q", mismatch)
	}
	missingGuard := captureCoroSitePlanPanic(func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroSemanticInstruction(instruction)
		ctx.observeCoroPhysicalInstruction(instruction, coroPhysicalInstructionDeref)
	})
	if !strings.Contains(missingGuard, "physical nil-guard emission=false, frozen SitePlan requires true") {
		t.Fatalf("missing physical guard observation = %q", missingGuard)
	}
	func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroSemanticInstruction(instruction)
		ctx.observeCoroPhysicalInstruction(instruction, coroPhysicalInstructionDeref)
		ctx.observeCoroPhysicalNilGuard(instruction)
	}()

	physical.instructions[instruction] = coroPhysicalInstructionPlan{
		semantic: coroSemanticInstructionPlan{recipe: coro.RecipeID("test.control.v0")},
		control:  coroPhysicalControlDirectAwait,
	}
	missingControl := captureCoroSitePlanPanic(func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroSemanticInstruction(instruction)
	})
	if !strings.Contains(missingControl, "omitted frozen physical control recipe direct-await") {
		t.Fatalf("missing physical control observation = %q", missingControl)
	}
	mismatchedControl := captureCoroSitePlanPanic(func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroSemanticInstruction(instruction)
		ctx.observeCoroPhysicalControl(instruction, coroPhysicalControlDispatchSpawn)
	})
	if !strings.Contains(mismatchedControl, "emitted physical control recipe dispatch-spawn, frozen SitePlan requires direct-await") {
		t.Fatalf("mismatched physical control observation = %q", mismatchedControl)
	}
	func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroSemanticInstruction(instruction)
		ctx.observeCoroPhysicalControl(instruction, coroPhysicalControlDirectAwait)
	}()

	physical.instructions[instruction] = coroPhysicalInstructionPlan{
		semantic:  coroSemanticInstructionPlan{recipe: coro.RecipeID("test.operation.v0")},
		operation: coroPhysicalOperationChannelSelectPark,
	}
	missingOperation := captureCoroSitePlanPanic(func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroSemanticInstruction(instruction)
	})
	if !strings.Contains(missingOperation, "omitted frozen physical operation recipe channel-select-park") {
		t.Fatalf("missing physical operation observation = %q", missingOperation)
	}
	mismatchedOperation := captureCoroSitePlanPanic(func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroSemanticInstruction(instruction)
		ctx.observeCoroPhysicalOperation(instruction, coroPhysicalOperationChannelSend)
	})
	if !strings.Contains(mismatchedOperation, "emitted physical operation recipe channel-send, frozen SitePlan requires channel-select-park") {
		t.Fatalf("mismatched physical operation observation = %q", mismatchedOperation)
	}
	func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroSemanticInstruction(instruction)
		ctx.observeCoroPhysicalOperation(instruction, coroPhysicalOperationChannelSelectPark)
	}()

	physical.instructions[instruction] = coroPhysicalInstructionPlan{
		semantic: coroSemanticInstructionPlan{recipe: coro.RecipeID("test.outcome.v0")},
		outcome:  coroPhysicalOutcomePanic,
	}
	missingOutcome := captureCoroSitePlanPanic(func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroSemanticInstruction(instruction)
	})
	if !strings.Contains(missingOutcome, "omitted frozen physical outcome recipe panic") {
		t.Fatalf("missing physical outcome observation = %q", missingOutcome)
	}
	mismatchedOutcome := captureCoroSitePlanPanic(func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroSemanticInstruction(instruction)
		ctx.observeCoroPhysicalOutcome(instruction, coroPhysicalOutcomeReturn)
	})
	if !strings.Contains(mismatchedOutcome, "emitted physical outcome recipe return, frozen SitePlan requires panic") {
		t.Fatalf("mismatched physical outcome observation = %q", mismatchedOutcome)
	}
	func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroSemanticInstruction(instruction)
		ctx.observeCoroPhysicalOutcome(instruction, coroPhysicalOutcomePanic)
	}()
}

func TestCoroPhysicalPlanFreezesTypedControlSubkind(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package control
//llgo:link Exit llgo.controlExit
func Exit(int32)
func Root(status int32) { Exit(status) }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: ssaPkg, Files: files, Identity: "control",
	}})
	if err != nil {
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	calls := allocaCStrTestCalls(root)
	if len(calls) != 1 {
		t.Fatalf("Root calls = %d, want one", len(calls))
	}
	call, ok := calls[0].(*ssa.Call)
	if !ok {
		t.Fatalf("Root call = %T, want *ssa.Call", calls[0])
	}
	owner := universe.ownerOf(root)
	audit := &coroPhysicalPureSSAAudit{
		universe: universe,
		ctx:      &context{emissionOwner: owner},
		fn:       root,
	}
	result := coroPhysicalInstructionPlan{}
	planCoroPhysicalOperationInstruction(
		audit, nil, call, coroPhysicalLoweringCapabilities{}, &result,
	)
	if result.operationFailure != "" ||
		result.operation != coroPhysicalOperationControl ||
		result.operationControl != CoroControlProcessExit {
		t.Fatalf("typed-control physical plan = %+v", result)
	}

	ctx := &context{
		coroEmission: &coroPhysicalEmissionSession{plan: &coroPhysicalFunctionPlan{
			function: root,
			owner:    owner,
			instructions: map[ssa.Instruction]coroPhysicalInstructionPlan{
				call: result,
			},
		}},
	}
	ctx.setCoroEmissionSite(&coroSiteEmissionObserver{
		instruction:          call,
		expectedPhysical:     result,
		hasExpectedPhysical:  true,
		seenPhysical:         true,
		seenPhysicalControl:  true,
		seenPhysicalOutcome:  true,
		seenPhysicalNilGuard: false,
	})
	ctx.selectTypedControlOperation(call, CoroControlProcessExit)
	if !ctx.coroEmissionSite().seenPhysicalOperation {
		t.Fatal("typed-control physical selection was not observed")
	}
}

func TestCoroPhysicalCodegenRejectsMissingCommittedPlan(t *testing.T) {
	prog, ssaPkg, files, universe, plan := prepareCoroPhysicalValueTransportABI(t, nil)
	defer prog.Dispose()
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.FuncRepABI = coro.FuncRepABIV1
	if err := compilation.preflightCoroPlan(); err != nil {
		t.Fatal(err)
	}
	for key := range universe.coroProgramIR.physicalPlans {
		delete(universe.coroProgramIR.physicalPlans, key)
	}
	message := captureCoroSitePlanPanic(func() {
		pkg, _, err := NewPackageExWithEmbedOptions(
			prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
			PackageOptions{Compilation: compilation},
		)
		if pkg != nil {
			pkg.Module().Dispose()
		}
		if err != nil {
			panic(err)
		}
	})
	if !strings.Contains(message, "has no frozen physical plan") {
		t.Fatalf("missing physical plan codegen failure = %q", message)
	}
}
