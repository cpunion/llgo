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

package coro

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

const planDigestTestSource = `package coroid

type holder struct { callback func() }

func plain() {}
func alternate() {}
func consume(value holder) {
	if value.callback != nil { value.callback() }
}
func root(flag bool) {
	callback := plain
	if flag { callback = alternate }
	callback()
	consume(holder{callback: callback})
}
`

func TestCoroPlanDigestDeterministicCompleteAndDomainSeparated(t *testing.T) {
	plainPlan, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	debugPlan, debugPackage := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics|ssa.GlobalDebug)
	metadata := validPlanDigestMetadata()

	debugRefs := 0
	for _, function := range debugPlan.functions {
		for _, block := range function.Function.Blocks {
			for _, instruction := range block.Instrs {
				if _, debug := instruction.(*ssa.DebugRef); debug {
					debugRefs++
				}
			}
		}
	}
	if debugRefs == 0 {
		t.Fatalf("GlobalDebug package %q has no DebugRef instructions", debugPackage.Pkg.Path())
	}

	plainDigest, err := plainPlan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	debugDigest, err := debugPlan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if plainDigest != debugDigest {
		t.Fatalf("DebugRef instructions changed CoroPlanDigest:\nplain %s\ndebug %s", plainDigest, debugDigest)
	}
	if len(plainDigest) != sha256.Size*2 {
		t.Fatalf("digest length = %d, want %d", len(plainDigest), sha256.Size*2)
	}
	if _, err := hex.DecodeString(plainDigest); err != nil {
		t.Fatalf("digest is not lowercase hexadecimal: %v", err)
	}
	if plainPlan.functionIDs.CoroABI != metadata.CoroABI || plainPlan.functionIDs.SchedulerABI != metadata.SchedulerABI || !plainPlan.functionIDs.ArchiveReady {
		t.Fatalf("SSAPlan lost normalized FunctionID configuration: %+v", plainPlan.functionIDs)
	}

	document, err := plainPlan.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if document.Schema != PlanDigestSchema || document.FunctionIDSchema != FunctionIDSchema {
		t.Fatalf("digest schemas = %q, %q", document.Schema, document.FunctionIDSchema)
	}
	if len(document.Functions) != len(plainPlan.functions) {
		t.Fatalf("function records = %d, want %d", len(document.Functions), len(plainPlan.functions))
	}
	for index, function := range document.Functions {
		if function.Emission != uint8(plainPlan.functions[index].Plan.Emission) {
			t.Fatalf("function %q digest emission = %d, want %s", function.ID, function.Emission, plainPlan.functions[index].Plan.Emission)
		}
	}
	if len(document.Roots) != len(plainPlan.roots) || len(document.Roots) == 0 {
		t.Fatalf("root records = %d, plan roots = %d", len(document.Roots), len(plainPlan.roots))
	}
	if len(document.Calls) != len(plainPlan.callPlans) || len(document.Calls) == 0 {
		t.Fatalf("call records = %d, map plans = %d", len(document.Calls), len(plainPlan.callPlans))
	}
	if len(document.Values) < len(plainPlan.valuePlans) || len(document.Values) == 0 {
		t.Fatalf("value projections = %d, map plans = %d", len(document.Values), len(plainPlan.valuePlans))
	}
	foundOperand := false
	foundAggregatePath := false
	for _, value := range document.Values {
		foundOperand = foundOperand || value.Site.Kind == "operand"
		for _, leaf := range value.Funcs {
			foundAggregatePath = foundAggregatePath || len(leaf.Path) != 0
			if leaf.Targets == nil {
				t.Fatal("canonical target list is nil")
			}
		}
	}
	if !foundOperand {
		t.Fatal("digest did not project a definition-less value by operand occurrence")
	}
	if !foundAggregatePath {
		t.Fatal("digest did not cover an aggregate function-value path")
	}

	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	raw := sha256.Sum256(payload)
	if plainDigest == hex.EncodeToString(raw[:]) {
		t.Fatal("CoroPlanDigest omitted domain separation")
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(PlanDigestSchema))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	if want := hex.EncodeToString(hash.Sum(nil)); plainDigest != want {
		t.Fatalf("digest = %s, want domain-separated %s", plainDigest, want)
	}
}

func TestCoroPlanDigestRecordsIgnoredPhysicalBodySemantics(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "ignored_digest.go", `package coroid
func external() {}
func root() { external() }
`)
	external := packageFunction(t, pkg, "external")
	root := packageFunction(t, pkg, "root")
	build := func(ignore bool) *SSAPlan {
		t.Helper()
		config := planDigestSSAConfig()
		config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == external {
				return SSAFunctionPolicy{
					IgnoreBody:       ignore,
					Exec:             MayUnwind,
					External:         ExternalUnknownForeign,
					OverrideExternal: true,
				}, nil
			}
			return SSAFunctionPolicy{}, nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	ordinary := build(false)
	ignored := build(true)
	ordinaryPlan := functionPlanFor(t, ordinary, external)
	ignoredPlan := functionPlanFor(t, ignored, external)
	if ordinaryPlan != ignoredPlan {
		t.Fatalf("fixture must isolate ignored-body identity:\nordinary %+v\nignored  %+v", ordinaryPlan, ignoredPlan)
	}
	metadata := validPlanDigestMetadata()
	ordinaryDigest, err := ordinary.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	ignoredDigest, err := ignored.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if ordinaryDigest == ignoredDigest {
		t.Fatal("ignored and physically emitted SSA bodies have the same plan digest")
	}
	document, err := ignored.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	externalID, _ := ignored.FunctionID(external)
	found := false
	for _, function := range document.Functions {
		if function.ID == externalID {
			found = true
			if !function.IgnoredBody {
				t.Fatal("ignored external function record lost ignored_body=true")
			}
		}
	}
	if !found {
		t.Fatal("ignored external function is absent from digest")
	}
}

func TestCoroPlanDigestCanonicalTargetsAndPlanMutations(t *testing.T) {
	plan, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	metadata := validPlanDigestMetadata()
	baseline, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}

	var multiTargetCall ssa.CallInstruction
	var originalCall SSACallPlan
	for call, callPlan := range plan.callPlans {
		if len(callPlan.Targets) >= 2 {
			multiTargetCall = call
			originalCall = callPlan
			originalCall.Targets = append([]FunctionID(nil), callPlan.Targets...)
			break
		}
	}
	if multiTargetCall == nil {
		t.Fatal("test plan has no multi-target CallPlan")
	}
	reordered := originalCall
	reordered.Targets = []FunctionID{originalCall.Targets[1], originalCall.Targets[0], originalCall.Targets[0]}
	plan.callPlans[multiTargetCall] = reordered
	canonical, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != baseline {
		t.Fatalf("target order/duplicates changed canonical digest: %s != %s", canonical, baseline)
	}
	reordered.Open = !reordered.Open
	plan.callPlans[multiTargetCall] = reordered
	mutated, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if mutated == baseline {
		t.Fatal("CallPlan mutation did not change digest")
	}
	plan.callPlans[multiTargetCall] = originalCall

	originalRoots := append([]SSARootPlan(nil), plan.roots...)
	var addedRoot SSARootPlan
	for _, function := range plan.functions {
		isRoot := false
		for _, root := range originalRoots {
			isRoot = isRoot || root.ID == function.Plan.ID
		}
		if !isRoot && function.Plan.Demand != NoDemand {
			addedRoot = SSARootPlan{Function: function.Function, ID: function.Plan.ID, Demand: function.Plan.Demand}
			break
		}
	}
	if addedRoot.Function == nil {
		t.Fatal("test plan has no propagated non-root demand")
	}
	changedRoots := make([]SSARootPlan, 0, len(originalRoots)+1)
	inserted := false
	for _, root := range originalRoots {
		if !inserted && addedRoot.ID < root.ID {
			changedRoots = append(changedRoots, addedRoot)
			inserted = true
		}
		changedRoots = append(changedRoots, root)
	}
	if !inserted {
		changedRoots = append(changedRoots, addedRoot)
	}
	plan.roots = changedRoots
	mutated, err = plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if mutated == baseline {
		t.Fatal("explicit root mutation did not change digest")
	}
	plan.roots = originalRoots

	var value ssa.Value
	var originalValue SSAValuePlan
	for candidate, valuePlan := range plan.valuePlans {
		if len(valuePlan.Funcs) != 0 {
			value = candidate
			originalValue = cloneSSAValuePlan(valuePlan)
			break
		}
	}
	if value == nil {
		t.Fatal("test plan has no SSAValuePlan")
	}
	changedValue := cloneSSAValuePlan(originalValue)
	changedValue.Funcs[0].MayBeNil = !changedValue.Funcs[0].MayBeNil
	plan.valuePlans[value] = changedValue
	mutated, err = plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if mutated == baseline {
		t.Fatal("SSAValuePlan mutation did not change digest")
	}
	plan.valuePlans[value] = originalValue

	originalFunction := plan.functions[0].Plan
	changedFunction := originalFunction
	changedFunction.Recursive = !changedFunction.Recursive
	plan.functions[0].Plan = changedFunction
	plan.plan.functions[0] = changedFunction
	mutated, err = plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if mutated == baseline {
		t.Fatal("FunctionPlan mutation did not change digest")
	}
	plan.functions[0].Plan = originalFunction
	plan.plan.functions[0] = originalFunction
	if restored, err := plan.CoroPlanDigest(metadata); err != nil || restored != baseline {
		t.Fatalf("restored digest = %q, %v; want %q", restored, err, baseline)
	}
}

func TestCoroPlanDigestRecordsFrontendElidedCalls(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "elided_digest.go", `package coroid
func target() {}
func root() { target() }
func other() { target() }
`)
	target := packageFunction(t, pkg, "target")
	root := packageFunction(t, pkg, "root")
	other := packageFunction(t, pkg, "other")
	rootCall := onlyNonBuiltinCall(t, root)
	otherCall := onlyNonBuiltinCall(t, other)
	includeWithoutTarget := func(fn *ssa.Function) (bool, error) { return fn != target, nil }
	config := planDigestSSAConfig()
	config.Include = includeWithoutTarget
	conservative, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	config.ClassifyElidedCall = func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
		return call == rootCall, nil
	}
	elided, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	metadata := validPlanDigestMetadata()
	first, err := elided.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	again, err := elided.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatalf("elided-call digest is unstable: %s != %s", first, again)
	}
	ordinary, err := conservative.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if first == ordinary {
		t.Fatal("frontend-elided policy did not change the canonical digest")
	}
	document, err := elided.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	rootPlan, ok := elided.FunctionPlan(root)
	if !ok {
		t.Fatal("elided root has no FunctionPlan")
	}
	if len(document.ElidedCalls) != 1 || !document.ElidedCalls[0].Elided || document.ElidedCalls[0].Function != rootPlan.ID ||
		document.ElidedCalls[0].Block < 0 || document.ElidedCalls[0].Instruction < 0 {
		t.Fatalf("canonical elided-call record = %+v", document.ElidedCalls)
	}
	if len(document.Calls) != len(elided.callPlans) {
		t.Fatalf("elided call was disguised as a CallPlan: calls=%d plans=%d", len(document.Calls), len(elided.callPlans))
	}
	otherConfig := planDigestSSAConfig()
	otherConfig.Include = includeWithoutTarget
	otherConfig.ClassifyElidedCall = func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
		return call == otherCall, nil
	}
	otherElided, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, otherConfig)
	if err != nil {
		t.Fatal(err)
	}
	otherDigest, err := otherElided.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if otherDigest == first {
		t.Fatal("moving the exact elided identity to another SSA call site did not change the digest")
	}
	otherDocument, err := otherElided.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	otherPlan, ok := otherElided.FunctionPlan(other)
	if !ok || len(otherDocument.ElidedCalls) != 1 || otherDocument.ElidedCalls[0].Function != otherPlan.ID {
		t.Fatalf("other exact elided-call record = %+v (plan=%+v, ok=%t)", otherDocument.ElidedCalls, otherPlan, ok)
	}

	delete(elided.elidedCalls, rootCall)
	if _, err := elided.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "missing CallPlan") {
		t.Fatalf("missing elided identity digest error = %v", err)
	}
	elided.elidedCalls[rootCall] = struct{}{}
	elided.elidedCalls[otherCall] = struct{}{}
	if _, err := elided.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "both elided and assigned a CallPlan") {
		t.Fatalf("overlapping elided/CallPlan digest error = %v", err)
	}
}

func TestCoroPlanDigestFailsClosedOnCallAndValueCoverage(t *testing.T) {
	plan, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	metadata := validPlanDigestMetadata()

	var call ssa.CallInstruction
	var callPlan SSACallPlan
	for candidate, candidatePlan := range plan.callPlans {
		call, callPlan = candidate, candidatePlan
		break
	}
	delete(plan.callPlans, call)
	if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "missing CallPlan") {
		t.Fatalf("missing CallPlan error = %v", err)
	}
	plan.callPlans[call] = callPlan
	other, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	var foreignCall ssa.CallInstruction
	var foreignCallPlan SSACallPlan
	for candidate, candidatePlan := range other.callPlans {
		foreignCall, foreignCallPlan = candidate, candidatePlan
		break
	}
	plan.callPlans[foreignCall] = foreignCallPlan
	if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "coverage mismatch") {
		t.Fatalf("unreachable CallPlan error = %v", err)
	}
	delete(plan.callPlans, foreignCall)

	var value ssa.Value
	var valuePlan SSAValuePlan
	for candidate, candidatePlan := range plan.valuePlans {
		value, valuePlan = candidate, cloneSSAValuePlan(candidatePlan)
		break
	}
	delete(plan.valuePlans, value)
	if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "missing SSAValuePlan") {
		t.Fatalf("missing SSAValuePlan error = %v", err)
	}
	plan.valuePlans[value] = valuePlan

	var foreignValue ssa.Value
	var foreignPlan SSAValuePlan
	for candidate, candidatePlan := range other.valuePlans {
		foreignValue, foreignPlan = candidate, cloneSSAValuePlan(candidatePlan)
		break
	}
	plan.valuePlans[foreignValue] = foreignPlan
	if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "coverage mismatch") {
		t.Fatalf("unreachable SSAValuePlan error = %v", err)
	}
	delete(plan.valuePlans, foreignValue)

	originalRoots := append([]SSARootPlan(nil), plan.roots...)
	rootMutations := []struct {
		name   string
		want   string
		mutate func()
	}{
		{"nil function", "nil function", func() { plan.roots[0].Function = nil }},
		{"no demand", "has no demand", func() { plan.roots[0].Demand = NoDemand }},
		{"invalid demand", "unknown demand bits", func() { plan.roots[0].Demand = Demand(1 << 7) }},
		{"duplicate", "not in strict FunctionID order", func() { plan.roots = append(plan.roots, plan.roots[0]) }},
		{"foreign function", "missing forward root mapping", func() { plan.roots[0].Function = other.roots[0].Function }},
	}
	for _, test := range rootMutations {
		t.Run("root/"+test.name, func(t *testing.T) {
			plan.roots = append([]SSARootPlan(nil), originalRoots...)
			test.mutate()
			if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("root mutation error = %v, want %q", err, test.want)
			}
		})
	}
	plan.roots = originalRoots

	originalFunction := plan.functions[0].Plan
	invalidFunction := originalFunction
	invalidFunction.Emission = BodyEmission(255)
	plan.functions[0].Plan = invalidFunction
	if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "invalid body emission") {
		t.Fatalf("invalid emission error = %v", err)
	}
	invalidFunction.Emission = EmitNone
	if originalFunction.Emission == EmitNone {
		invalidFunction.Emission = EmitPlain
	}
	plan.functions[0].Plan = invalidFunction
	if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), "emission") || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched emission error = %v", err)
	}
	plan.functions[0].Plan = originalFunction
}

func TestCoroPlanDigestMetadataValidation(t *testing.T) {
	plan, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	valid := validPlanDigestMetadata()
	if _, err := plan.CoroPlanDigest(valid); err != nil {
		t.Fatalf("valid metadata: %v", err)
	}

	tests := []struct {
		name   string
		change func(*PlanDigestMetadata)
		want   string
	}{
		{"empty coro ABI", func(m *PlanDigestMetadata) { m.CoroABI = "" }, "coroutine ABI is empty"},
		{"mismatched coro ABI", func(m *PlanDigestMetadata) { m.CoroABI = EntryResolutionABIV0 }, "does not match FunctionID ABI"},
		{"mismatched scheduler ABI", func(m *PlanDigestMetadata) { m.SchedulerABI = "llgo.coro.scheduler.other.v0" }, "does not match FunctionID ABI"},
		{"empty panic ABI", func(m *PlanDigestMetadata) { m.PanicABI = "" }, "panic ABI is empty"},
		{"empty func rep ABI", func(m *PlanDigestMetadata) { m.FuncRepABI = "" }, "function representation ABI is empty"},
		{"empty triple", func(m *PlanDigestMetadata) { m.TargetTriple = "" }, "target triple is empty"},
		{"invalid CPU UTF-8", func(m *PlanDigestMetadata) { m.TargetCPU = string([]byte{0xff}) }, "target CPU is not valid UTF-8"},
		{"NUL feature", func(m *PlanDigestMetadata) { m.TargetFeatures = "+simd\x00-bad" }, "target features contains NUL"},
		{"NUL target ABI", func(m *PlanDigestMetadata) { m.TargetABI = "default\x00bad" }, "target ABI contains NUL"},
		{"zero pointer", func(m *PlanDigestMetadata) { m.PointerBits = 0 }, "positive multiple of 8"},
		{"unaligned pointer", func(m *PlanDigestMetadata) { m.PointerBits = 31 }, "positive multiple of 8"},
		{"invalid endianness", func(m *PlanDigestMetadata) { m.Endianness = "middle" }, "not little or big"},
		{"empty data layout", func(m *PlanDigestMetadata) { m.DataLayout = "" }, "data layout is empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := valid
			test.change(&metadata)
			if _, err := plan.CoroPlanDigest(metadata); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}

	prog, pkg := buildCoroTestSSA(t, "report.go", `package coroid; func root() {}`)
	reportOnly, err := AnalyzeSSA(prog, Roots{{Function: packageFunction(t, pkg, "root"), Demand: AsyncDemand}}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reportOnly.CoroPlanDigest(valid); err == nil || !strings.Contains(err.Error(), "archive-ready") {
		t.Fatalf("report-only plan digest error = %v", err)
	}
	var nilPlan *SSAPlan
	if _, err := nilPlan.CoroPlanDigest(valid); err == nil || !strings.Contains(err.Error(), "nil SSA plan") {
		t.Fatalf("nil plan digest error = %v", err)
	}
}

func TestCoroPlanDigestMetadataMutationsChangeDigest(t *testing.T) {
	plan, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	valid := validPlanDigestMetadata()
	baseline, err := plan.CoroPlanDigest(valid)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		change func(*PlanDigestMetadata)
	}{
		{"panic ABI", func(m *PlanDigestMetadata) { m.PanicABI += ".changed" }},
		{"func rep ABI", func(m *PlanDigestMetadata) { m.FuncRepABI += ".changed" }},
		{"triple", func(m *PlanDigestMetadata) { m.TargetTriple = "wasm32-unknown-unknown" }},
		{"CPU", func(m *PlanDigestMetadata) { m.TargetCPU = "generic" }},
		{"features", func(m *PlanDigestMetadata) { m.TargetFeatures += ",+atomics" }},
		{"target ABI", func(m *PlanDigestMetadata) { m.TargetABI = "eabi" }},
		{"pointer bits", func(m *PlanDigestMetadata) { m.PointerBits = 32 }},
		{"endianness", func(m *PlanDigestMetadata) { m.Endianness = "big" }},
		{"data layout", func(m *PlanDigestMetadata) { m.DataLayout += "-i128:128" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := valid
			test.change(&metadata)
			digest, err := plan.CoroPlanDigest(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if digest == baseline {
				t.Fatalf("metadata mutation %q did not change digest", test.name)
			}
		})
	}
}

func TestCoroPlanDigestCanonicalEmptyArrays(t *testing.T) {
	prog, _ := buildCoroTestSSA(t, "empty.go", `package coroid; func root() {}`)
	plan, err := AnalyzeSSA(prog, nil, planDigestSSAConfig())
	if err != nil {
		t.Fatal(err)
	}
	document, err := plan.canonicalPlanDigest(validPlanDigestMetadata())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, field := range []string{`"roots":[]`, `"calls":[]`, `"lowered_calls":[]`, `"values":[]`} {
		if !strings.Contains(text, field) {
			t.Fatalf("canonical document %s does not contain %s", text, field)
		}
	}
}

func TestCoroPlanDigestIncludesExactLoweredCallMapping(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "lowered_digest.go", `package coroid
func root() {}
func first() {}
func second() {}
`)
	root := packageFunction(t, pkg, "root")
	first := packageFunction(t, pkg, "first")
	second := packageFunction(t, pkg, "second")
	build := func(calls []SSALoweredCall) *SSAPlan {
		t.Helper()
		config := planDigestSSAConfig()
		config.MaxPlainInstructions = -1
		config.ClassifyLoweredCalls = func(fn *ssa.Function) ([]SSALoweredCall, error) {
			if fn == root {
				return calls, nil
			}
			return nil, nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, config)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	baseline := build([]SSALoweredCall{
		{LogicalName: "runtime.first", Target: first},
		{LogicalName: "runtime.second", Target: second},
	})
	permuted := build([]SSALoweredCall{
		{LogicalName: "runtime.second", Target: second},
		{LogicalName: "runtime.first", Target: first},
	})
	swapped := build([]SSALoweredCall{
		{LogicalName: "runtime.first", Target: second},
		{LogicalName: "runtime.second", Target: first},
	})
	metadata := validPlanDigestMetadata()
	baselineDigest, err := baseline.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	permutedDigest, err := permuted.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if baselineDigest != permutedDigest {
		t.Fatalf("classifier order changed lowered-call digest:\n%s\n%s", baselineDigest, permutedDigest)
	}
	swappedDigest, err := swapped.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if baselineDigest == swappedDigest {
		t.Fatal("retargeting logical lowered-call identities did not change digest")
	}
	document, err := baseline.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.LoweredCalls) != 2 || document.LoweredCalls[0].LogicalName != "runtime.first" || document.LoweredCalls[1].LogicalName != "runtime.second" {
		t.Fatalf("canonical lowered calls = %+v", document.LoweredCalls)
	}
}

func TestCoroPlanDigestDistinguishesExplicitAndPropagatedRoots(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "roots.go", `package coroid
func leaf(ch chan int) { <-ch }
func root(ch chan int) { leaf(ch) }
`)
	root := packageFunction(t, pkg, "root")
	leaf := packageFunction(t, pkg, "leaf")
	config := planDigestSSAConfig()
	propagated, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := AnalyzeSSA(prog, Roots{
		{Function: root, Demand: AsyncDemand},
		{Function: leaf, Demand: AsyncDemand},
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	permuted, err := AnalyzeSSA(prog, Roots{
		{Function: leaf, Demand: AsyncDemand},
		{Function: root, Demand: AsyncDemand},
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	duplicated, err := AnalyzeSSA(prog, Roots{
		{Function: leaf, Demand: AsyncDemand},
		{Function: root, Demand: AsyncDemand},
		{Function: leaf, Demand: AsyncDemand},
		{Function: root, Demand: AsyncDemand},
	}, config)
	if err != nil {
		t.Fatal(err)
	}

	if got := functionPlanFor(t, propagated, leaf).Demand; got != AsyncDemand {
		t.Fatalf("propagated leaf demand = %s, want async", got)
	}
	if got, want := len(propagated.Roots()), 1; got != want {
		t.Fatalf("propagated roots = %d, want %d", got, want)
	}
	if got, want := len(explicit.Roots()), 2; got != want {
		t.Fatalf("explicit roots = %d, want %d", got, want)
	}
	for _, fn := range []*ssa.Function{root, leaf} {
		left, leftOK := propagated.FunctionPlan(fn)
		right, rightOK := explicit.FunctionPlan(fn)
		if !leftOK || !rightOK || left != right {
			t.Fatalf("function plan for %s differs: propagated=%+v,%v explicit=%+v,%v", fn.Name(), left, leftOK, right, rightOK)
		}
	}

	metadata := validPlanDigestMetadata()
	propagatedDocument, err := propagated.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	explicitDocument, err := explicit.canonicalPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	propagatedDocument.Roots = nil
	explicitDocument.Roots = nil
	propagatedPayload, err := json.Marshal(propagatedDocument)
	if err != nil {
		t.Fatal(err)
	}
	explicitPayload, err := json.Marshal(explicitDocument)
	if err != nil {
		t.Fatal(err)
	}
	if string(propagatedPayload) != string(explicitPayload) {
		t.Fatalf("non-root digest plan changed:\npropagated %s\nexplicit %s", propagatedPayload, explicitPayload)
	}
	propagatedDigest, err := propagated.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if explicitDigest == propagatedDigest {
		t.Fatal("explicit Async root and propagated AsyncDemand produced the same digest")
	}
	permutedDigest, err := permuted.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if permutedDigest != explicitDigest {
		t.Fatalf("root input order changed digest: %s != %s", permutedDigest, explicitDigest)
	}
	duplicatedDigest, err := duplicated.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if duplicatedDigest != explicitDigest {
		t.Fatalf("duplicate roots changed digest: %s != %s", duplicatedDigest, explicitDigest)
	}
}

func TestCoroPlanDigestProjectsDefinitionlessValueOccurrences(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "occurrences.go", `package coroid
func target() {}
func take(func()) {}
func root() {
	take(target)
	take(target)
}
`)
	root := packageFunction(t, pkg, "root")
	target := packageFunction(t, pkg, "target")
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, planDigestSSAConfig())
	if err != nil {
		t.Fatal(err)
	}
	document, err := plan.canonicalPlanDigest(validPlanDigestMetadata())
	if err != nil {
		t.Fatal(err)
	}
	rootID, ok := plan.FunctionID(root)
	if !ok {
		t.Fatal("root has no FunctionID")
	}
	targetID, ok := plan.FunctionID(target)
	if !ok {
		t.Fatal("target has no FunctionID")
	}
	var instructions []int
	for _, value := range document.Values {
		if value.Site.Function != rootID || value.Site.Kind != "operand" {
			continue
		}
		for _, leaf := range value.Funcs {
			if len(leaf.Targets) == 1 && leaf.Targets[0] == targetID {
				instructions = append(instructions, value.Site.Instruction)
			}
		}
	}
	if len(instructions) != 2 || instructions[0] == instructions[1] {
		t.Fatalf("definition-less target operand sites = %v, want two distinct stable occurrences", instructions)
	}
}

func buildPlanDigestTestPlan(t *testing.T, mode ssa.BuilderMode) (*SSAPlan, *ssa.Package) {
	t.Helper()
	prog, pkg := buildCoroTestSSAWithMode(t, "digest.go", planDigestTestSource, mode)
	root := packageFunction(t, pkg, "root")
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, planDigestSSAConfig())
	if err != nil {
		t.Fatal(err)
	}
	return plan, pkg
}

func planDigestSSAConfig() SSAConfig {
	return SSAConfig{FunctionIDs: FunctionIDConfig{
		CoroABI:      PhysicalABIV0,
		SchedulerABI: SchedulerNoneABIV0,
		ArchiveReady: true,
		ResolveLinkIdentity: func(fn *ssa.Function) (string, error) {
			if fn == nil || fn.Name() == "" {
				return "", fmt.Errorf("missing test link identity")
			}
			return "example.test/coroid." + fn.Name(), nil
		},
		CanonicalPackageKey: func(pkg *types.Package) (string, error) {
			if pkg == nil || pkg.Path() == "" {
				return "", fmt.Errorf("missing test package key")
			}
			return pkg.Path(), nil
		},
	}}
}

func validPlanDigestMetadata() PlanDigestMetadata {
	return PlanDigestMetadata{
		CoroABI:        PhysicalABIV0,
		SchedulerABI:   SchedulerNoneABIV0,
		PanicABI:       PanicLegacyABIV0,
		FuncRepABI:     FuncRepABIV0,
		TargetTriple:   "x86_64-unknown-linux-gnu",
		TargetCPU:      "",
		TargetFeatures: "+sse2,-avx",
		TargetABI:      "",
		PointerBits:    64,
		Endianness:     "little",
		DataLayout:     "e-m:e-p:64:64-i64:64-n8:16:32:64-S128",
	}
}
