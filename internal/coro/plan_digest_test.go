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
	prog, pkg := buildCoroTestSSA(t, "empty.go", `package coroid; func root() {}`)
	root := packageFunction(t, pkg, "root")
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, planDigestSSAConfig())
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
	for _, field := range []string{`"calls":[]`, `"values":[]`} {
		if !strings.Contains(text, field) {
			t.Fatalf("canonical document %s does not contain %s", text, field)
		}
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
