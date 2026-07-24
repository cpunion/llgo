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
	"go/types"
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestAnalyzeSSAFunctionValueFlow(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "flow.go", `package coroid

var channel chan int
var sink func()

func localTarget() {}
func localCoroTarget() { <-channel }
func mixedPlain() {}
func mixedCoro() { <-channel }
func staticTarget() {}
func boxedTarget() {}
func goTarget() {}
func deferTarget() {}
func openKnownTarget() { <-channel }

func local(flag bool) {
	var fn func()
	if flag { fn = localTarget }
	if fn != nil { fn() }
}

func localCoro(flag bool) {
	var fn func()
	if flag { fn = localCoroTarget }
	if fn != nil { fn() }
}

func mixed(flag bool) {
	fn := mixedPlain
	if flag { fn = mixedCoro }
	fn()
}

func staticEscape() {
	sink = staticTarget
	staticTarget()
}

func box() any { return boxedTarget }
func throughParam(fn func()) { fn() }
func openKnown(fn func(), flag bool) {
	if flag { fn = openKnownTarget }
	fn()
}
func nilCall() {
	var fn func()
	fn()
}
func capturedCoro(flag bool) int {
	var fn func() int
	if flag {
		value := 1
		fn = func() int {
			<-channel
			return value
		}
	}
	return fn()
}
func dynamicGo(flag bool) {
	var fn func()
	if flag { fn = goTarget }
	if fn != nil { go fn() }
}
func dynamicDefer(flag bool) {
	var fn func()
	if flag { fn = deferTarget }
	if fn != nil { defer fn() }
}
`)
	local := packageFunction(t, pkg, "local")
	localCoro := packageFunction(t, pkg, "localCoro")
	mixed := packageFunction(t, pkg, "mixed")
	staticEscape := packageFunction(t, pkg, "staticEscape")
	throughParam := packageFunction(t, pkg, "throughParam")
	openKnown := packageFunction(t, pkg, "openKnown")
	nilCall := packageFunction(t, pkg, "nilCall")
	capturedCoro := packageFunction(t, pkg, "capturedCoro")
	dynamicGo := packageFunction(t, pkg, "dynamicGo")
	dynamicDefer := packageFunction(t, pkg, "dynamicDefer")

	plan, err := AnalyzeSSA(prog, Roots{
		{Function: local, Demand: AsyncDemand},
		{Function: localCoro, Demand: AsyncDemand},
		{Function: mixed, Demand: AsyncDemand},
		{Function: staticEscape, Demand: AsyncDemand},
		{Function: packageFunction(t, pkg, "box"), Demand: AsyncDemand},
		{Function: throughParam, Demand: AsyncDemand},
		{Function: openKnown, Demand: AsyncDemand},
		{Function: nilCall, Demand: AsyncDemand},
		{Function: capturedCoro, Demand: AsyncDemand},
		{Function: dynamicGo, Demand: AsyncDemand},
		{Function: dynamicDefer, Demand: AsyncDemand},
	}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}

	localCall := onlyNonBuiltinCall(t, local)
	assertCallRep(t, plan, localCall, DirectPlain, false, "localTarget")
	localCallPlan, _ := plan.CallPlan(localCall)
	if !localCallPlan.MayBeNil {
		t.Fatal("nil plus singleton call lost its required nil check")
	}
	localValue, ok := plan.ValuePlan(localCall.Common().Value)
	if !ok || len(localValue.Funcs) != 1 || localValue.Funcs[0].Rep != DirectPlain || !localValue.Funcs[0].MayBeNil {
		t.Fatalf("local value plan = %+v, %v", localValue, ok)
	}
	localValue.Funcs[0].Rep = Dispatch
	localValue.Funcs[0].Targets[0] = "mutated"
	localValueAgain, _ := plan.ValuePlan(localCall.Common().Value)
	if localValueAgain.Funcs[0].Rep != DirectPlain || localValueAgain.Funcs[0].Targets[0] == "mutated" {
		t.Fatal("ValuePlan did not return a defensive copy")
	}

	assertCallRep(t, plan, onlyNonBuiltinCall(t, localCoro), DirectCoro, false, "localCoroTarget")
	if got := functionPlanFor(t, plan, localCoro); got.Effect.IsOpaque() || !got.Effect.Contains(MayPark) {
		t.Fatalf("closed local coroutine call did not feed the effect graph: %+v", got)
	}
	assertCallRep(t, plan, onlyNonBuiltinCall(t, mixed), Dispatch, false, "mixedCoro", "mixedPlain")
	assertCallRep(t, plan, onlyNonBuiltinCall(t, throughParam), Dispatch, true)
	throughParamPlan, _ := plan.CallPlan(onlyNonBuiltinCall(t, throughParam))
	if !throughParamPlan.MayBeNil {
		t.Fatal("open function parameter call lost its required nil check")
	}
	assertCallRep(t, plan, onlyNonBuiltinCall(t, openKnown), Dispatch, true, "openKnownTarget")
	if got := functionPlanFor(t, plan, packageFunction(t, pkg, "openKnownTarget")); got.Demand != AsyncDemand {
		t.Fatalf("known subset of open flow did not receive graph demand: %+v", got)
	}
	nilCallInstruction := onlyNonBuiltinCall(t, nilCall)
	assertCallRep(t, plan, nilCallInstruction, Dispatch, false)
	nilCallPlan, _ := plan.CallPlan(nilCallInstruction)
	if !nilCallPlan.MayBeNil {
		t.Fatal("closed nil-only call lost its required nil check")
	}
	if got := functionPlanFor(t, plan, nilCall); got.Effect.IsOpaque() || got.Effect.MaySuspend() {
		t.Fatalf("closed nil-only call polluted the effect graph: %+v", got)
	}
	capturedCall := onlyNonBuiltinCall(t, capturedCoro)
	capturedCallPlan, ok := plan.CallPlan(capturedCall)
	if !ok || capturedCallPlan.Rep != Dispatch || capturedCallPlan.Open ||
		!capturedCallPlan.MayBeNil || len(capturedCallPlan.Targets) != 1 {
		t.Fatalf("captured Phi call plan = %+v, present=%t; want closed nullable singleton Dispatch", capturedCallPlan, ok)
	}
	capturedValuePlan, ok := plan.ValuePlan(capturedCall.Common().Value)
	if !ok || len(capturedValuePlan.Funcs) != 1 ||
		capturedValuePlan.Funcs[0].Rep != Dispatch ||
		!capturedValuePlan.Funcs[0].MayBeNil ||
		len(capturedValuePlan.Funcs[0].Targets) != 1 {
		t.Fatalf("captured Phi value plan = %+v, present=%t; want nullable singleton Dispatch", capturedValuePlan, ok)
	}
	if len(capturedCoro.AnonFuncs) != 1 {
		t.Fatalf("capturedCoro closures = %d, want 1", len(capturedCoro.AnonFuncs))
	}
	if got := functionPlanFor(t, plan, capturedCoro.AnonFuncs[0]); got.FuncRep != Dispatch ||
		got.Emission != EmitCoroutine || !got.Effect.Contains(MayPark) {
		t.Fatalf("captured Phi target plan = %+v, want descriptor-backed coroutine", got)
	}

	chaPlan, err := AnalyzeSSA(prog, Roots{{Function: local, Demand: AsyncDemand}}, SSAConfig{DynamicResolution: DynamicCHAOpen})
	if err != nil {
		t.Fatal(err)
	}
	assertCallRep(t, chaPlan, localCall, DirectPlain, false, "localTarget")
	goCall := onlyNonBuiltinCall(t, dynamicGo)
	deferCall := onlyNonBuiltinCall(t, dynamicDefer)
	assertCallRep(t, plan, goCall, Dispatch, false, "goTarget")
	assertCallRep(t, plan, deferCall, Dispatch, false, "deferTarget")
	for _, call := range []ssa.CallInstruction{goCall, deferCall} {
		if got, _ := plan.CallPlan(call); !got.MayBeNil {
			t.Fatalf("dynamic %T call lost its required nil check: %+v", call, got)
		}
	}
	for _, name := range []string{"goTarget", "deferTarget"} {
		if got := functionPlanFor(t, plan, packageFunction(t, pkg, name)); got.FuncRep != Dispatch || got.Primary != PrimaryPlain {
			t.Fatalf("%s plan = %+v, want one plain primary plus descriptor", name, got)
		}
	}

	staticTarget := packageFunction(t, pkg, "staticTarget")
	if got := functionPlanFor(t, plan, staticTarget); got.FuncRep != Dispatch || got.Primary != PrimaryPlain {
		t.Fatalf("escaping static target plan = %+v", got)
	}
	if got, ok := plan.ValuePlan(staticTarget); !ok || len(got.Funcs) != 1 || got.Funcs[0].Rep != Dispatch {
		t.Fatalf("first-class use of static target lost its value plan: %+v, %v", got, ok)
	}
	assertCallRep(t, plan, onlyNonBuiltinCall(t, staticEscape), DirectPlain, false, "staticTarget")
	if got := functionPlanFor(t, plan, packageFunction(t, pkg, "boxedTarget")); got.FuncRep != Dispatch || got.Primary != PrimaryPlain {
		t.Fatalf("boxed target plan = %+v", got)
	}
	for _, name := range []string{"mixedPlain", "mixedCoro"} {
		if got := functionPlanFor(t, plan, packageFunction(t, pkg, name)); got.FuncRep != Dispatch {
			t.Fatalf("mixed target %s plan = %+v", name, got)
		}
	}
}

func TestAnalyzeSSAFunctionValueStorageBoundaries(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "storage.go", `package coroid

var table map[int]func()
var queue chan func()
var functionSink func()

func mapTarget() {}
func sendTarget() {}
func selectTarget() {}
func memoryTarget() {}

func mapEscape() { table[0] = mapTarget }
func sendEscape() { queue <- sendTarget }
func selectEscape() {
	select {
	case queue <- selectTarget:
	default:
	}
}
func memoryEscape() {
	slot := new(func())
	*slot = memoryTarget
}
func storeNil() { functionSink = nil }
`)
	plan, err := AnalyzeSSA(prog, Roots{
		{Function: packageFunction(t, pkg, "mapEscape"), Demand: AsyncDemand},
		{Function: packageFunction(t, pkg, "sendEscape"), Demand: AsyncDemand},
		{Function: packageFunction(t, pkg, "selectEscape"), Demand: AsyncDemand},
		{Function: packageFunction(t, pkg, "memoryEscape"), Demand: AsyncDemand},
		{Function: packageFunction(t, pkg, "storeNil"), Demand: AsyncDemand},
	}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mapTarget", "sendTarget", "selectTarget", "memoryTarget"} {
		got := functionPlanFor(t, plan, packageFunction(t, pkg, name))
		if got.FuncRep != Dispatch || got.Primary != PrimaryPlain {
			t.Fatalf("%s plan = %+v, want one plain primary plus descriptor", name, got)
		}
	}
	storeNil := packageFunction(t, pkg, "storeNil")
	store, ok := storeNil.Blocks[0].Instrs[0].(*ssa.Store)
	if !ok {
		t.Fatalf("storeNil first instruction = %T, want *ssa.Store", storeNil.Blocks[0].Instrs[0])
	}
	nilPlan, ok := plan.ValuePlan(store.Val)
	if !ok || len(nilPlan.Funcs) != 1 || !nilPlan.Funcs[0].MayBeNil || nilPlan.Funcs[0].Rep != Dispatch {
		t.Fatalf("stored nil plan = %+v, %v", nilPlan, ok)
	}
}

func TestAnalyzeSSATrustedDirectPlainCallArgumentIsExactAndFailClosed(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "direct_plain_argument.go", `package coroid

type CCallback func()

var stored CCallback

func sink(CCallback) {}
func ordinary(CCallback) {}

func exactTarget() {}
func exact() { sink(CCallback(exactTarget)) }

func storedTarget() {}
func storedUse() {
	callback := CCallback(storedTarget)
	sink(callback)
	stored = callback
}

func boxedTarget() {}
func boxedUse() {
	callback := CCallback(boxedTarget)
	sink(callback)
	_ = any(callback)
}

func ordinaryTarget() {}
func ordinaryUse() {
	callback := CCallback(ordinaryTarget)
	sink(callback)
	ordinary(callback)
}

func firstTarget() {}
func secondTarget() {}
func multiUse(flag bool) {
	callback := CCallback(firstTarget)
	if flag { callback = CCallback(secondTarget) }
	sink(callback)
}

func openUse(callback CCallback) { sink(callback) }
`)
	sink := packageFunction(t, pkg, "sink")
	analyze := func(owner string) (*SSAPlan, error) {
		root := packageFunction(t, pkg, owner)
		return AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, SSAConfig{
			ClassifyDirectPlainCallArgument: func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
				return caller == root && call.Common().StaticCallee() == sink && argument == 0, nil
			},
		})
	}

	plan, err := analyze("exact")
	if err != nil {
		t.Fatal(err)
	}
	exactTarget := packageFunction(t, pkg, "exactTarget")
	if got := functionPlanFor(t, plan, exactTarget); got.FuncRep != DirectPlain || got.Effect != NoSuspend || got.Emission != EmitPlain {
		t.Fatalf("exact target plan = %+v, want one direct plain body", got)
	}
	exactCall := onlyNonBuiltinCall(t, packageFunction(t, pkg, "exact"))
	valuePlan, ok := plan.ValuePlan(exactCall.Common().Args[0])
	if !ok || len(valuePlan.Funcs) != 1 || valuePlan.Funcs[0].Rep != DirectPlain || len(valuePlan.Funcs[0].Targets) != 1 {
		t.Fatalf("exact trusted argument plan = %+v, present=%t", valuePlan, ok)
	}

	for _, test := range []struct {
		owner string
		want  string
	}{
		{owner: "storedUse", want: "another canonical boundary"},
		{owner: "boxedUse", want: "another canonical boundary"},
		{owner: "ordinaryUse", want: "another canonical boundary"},
		{owner: "multiUse", want: "targets=2"},
		{owner: "openUse", want: "unknown=true"},
	} {
		t.Run(test.owner, func(t *testing.T) {
			if _, err := analyze(test.owner); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AnalyzeSSA error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestAnalyzeSSARawDirectPlainCallArgumentHasIndependentDemandProvenance(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "raw_direct_plain_argument.go", `package coroid

type CCallback func()

var channel chan int

func sink(CCallback) {}
func rawTarget() { <-channel }
func rawOwner() { sink(rawTarget) }
func managedOwner() { rawTarget() }

func boxedTarget() {}
func boxedOwner() {
	callback := CCallback(boxedTarget)
	sink(callback)
	_ = any(callback)
}

func openOwner(callback CCallback) { sink(callback) }
`)
	sink := packageFunction(t, pkg, "sink")
	rawOwner := packageFunction(t, pkg, "rawOwner")
	rawTarget := packageFunction(t, pkg, "rawTarget")
	rawCall := onlyNonBuiltinCall(t, rawOwner)
	classifyRaw := func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
		return caller == rawOwner && call.Common().StaticCallee() == sink && argument == 0, nil
	}
	classifyRawType := func(typ types.Type) (bool, error) {
		named, ok := types.Unalias(typ).(*types.Named)
		return ok && named.Obj() != nil && named.Obj().Name() == "CCallback", nil
	}

	plan, err := AnalyzeSSA(prog, Roots{{Function: rawOwner, Demand: AsyncDemand}}, SSAConfig{
		ClassifyRawDirectPlainCallArgument: classifyRaw,
		ClassifyRawCFunctionType:           classifyRawType,
		MaxPlainInstructions:               -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, rawTarget); got.ManagedDemand != NoDemand || !got.RawPlainDemand ||
		!got.RawPlainOnly || got.Emission != EmitRawPlain || got.Primary != PrimaryPlain || got.FuncRep != DirectPlain {
		t.Fatalf("raw-only callback plan = %+v, want one raw plain body and no managed entry", got)
	}
	if !plan.HasRawPlainVariant(rawTarget) {
		t.Fatal("raw-only callback has no exact raw plain variant")
	}
	valuePlan, ok := plan.ValuePlan(rawCall.Common().Args[0])
	if !ok || len(valuePlan.Funcs) != 1 || valuePlan.Funcs[0].Rep != DirectPlain ||
		valuePlan.Funcs[0].Transport != RawCCodePointer ||
		valuePlan.Funcs[0].MayBeNil || len(valuePlan.Funcs[0].Targets) != 1 {
		t.Fatalf("raw callback value plan = %+v, present=%t", valuePlan, ok)
	}

	managedOwner := packageFunction(t, pkg, "managedOwner")
	plan, err = AnalyzeSSA(prog, Roots{
		{Function: rawOwner, Demand: AsyncDemand},
		{Function: managedOwner, Demand: AsyncDemand},
	}, SSAConfig{
		ClassifyRawDirectPlainCallArgument: classifyRaw,
		ClassifyRawCFunctionType:           classifyRawType,
		MaxPlainInstructions:               -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, rawTarget); got.ManagedDemand == NoDemand || !got.RawPlainDemand ||
		got.RawPlainOnly || got.Emission != EmitCoroutine || got.Primary != PrimaryCoroutine || got.FuncRep != DirectCoro {
		t.Fatalf("mixed managed/raw callback plan = %+v, want managed coroutine primary plus raw variant", got)
	}
	if !plan.HasRawPlainVariant(rawTarget) {
		t.Fatal("mixed managed/raw callback lost its raw plain variant")
	}

	if _, err := AnalyzeSSA(prog, Roots{{Function: rawOwner, Demand: AsyncDemand}}, SSAConfig{
		ClassifyDirectPlainCallArgument:    classifyRaw,
		ClassifyRawDirectPlainCallArgument: classifyRaw,
		ClassifyRawCFunctionType:           classifyRawType,
		MaxPlainInstructions:               -1,
	}); err == nil || !strings.Contains(err.Error(), "both managed and raw direct-plain") {
		t.Fatalf("dual-classification error = %v", err)
	}

	boxedOwner := packageFunction(t, pkg, "boxedOwner")
	boxedPlan, err := AnalyzeSSA(prog, Roots{{Function: boxedOwner, Demand: AsyncDemand}}, SSAConfig{
		ClassifyRawDirectPlainCallArgument: func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
			return caller == boxedOwner && call.Common().StaticCallee() == sink && argument == 0, nil
		},
		ClassifyRawCFunctionType: classifyRawType,
		MaxPlainInstructions:     -1,
	})
	if err != nil {
		t.Fatalf("boxed raw callback: %v", err)
	}
	if got := functionPlanFor(t, boxedPlan, packageFunction(t, pkg, "boxedTarget")); got.ManagedDemand != NoDemand || !got.RawPlainDemand || !got.RawPlainOnly {
		t.Fatalf("boxed raw callback target = %+v, want raw-only demand", got)
	}

	for _, test := range []struct {
		owner string
		want  string
	}{
		{owner: "openOwner", want: "unknown=true"},
	} {
		t.Run(test.owner, func(t *testing.T) {
			owner := packageFunction(t, pkg, test.owner)
			_, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: AsyncDemand}}, SSAConfig{
				ClassifyRawDirectPlainCallArgument: func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
					return caller == owner && call.Common().StaticCallee() == sink && argument == 0, nil
				},
				ClassifyRawCFunctionType: classifyRawType,
				MaxPlainInstructions:     -1,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AnalyzeSSA error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestAnalyzeSSADynamicForeignCallPlan(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "foreign_dynamic.go", `package coroid
var channel chan int
func chaCandidate() {}
func knownCoro() { <-channel }
func foreign(fn func()) { fn() }
func foreignGo(fn func()) { go fn() }
func foreignDefer(fn func()) { defer fn() }
func mixed(fn func(), flag bool) {
	if flag { fn = knownCoro }
	fn()
}
func seed(flag bool) {
	var fn func()
	if flag { fn = chaCandidate }
	if fn != nil { fn() }
}
`)
	foreign := packageFunction(t, pkg, "foreign")
	foreignGo := packageFunction(t, pkg, "foreignGo")
	foreignDefer := packageFunction(t, pkg, "foreignDefer")
	mixed := packageFunction(t, pkg, "mixed")
	call := onlyNonBuiltinCall(t, foreign)
	plan, err := AnalyzeSSA(prog, Roots{
		{Function: foreign, Demand: AsyncDemand},
		{Function: foreignGo, Demand: AsyncDemand},
		{Function: foreignDefer, Demand: AsyncDemand},
		{Function: mixed, Demand: AsyncDemand},
	}, SSAConfig{
		DynamicResolution: DynamicCHAClosed,
		ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (UnknownTarget, error) {
			return UnknownForeign, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCallRep(t, plan, call, Dispatch, true)
	got, _ := plan.CallPlan(call)
	if got.Kind != CallForeign || got.Unresolved != UnknownForeign || !got.MayBeNil {
		t.Fatalf("foreign dynamic call plan = %+v", got)
	}
	value, ok := plan.ValuePlan(call.Common().Value)
	if !ok || len(value.Funcs) != 1 || value.Funcs[0].Rep != got.Rep {
		t.Fatalf("foreign dynamic operand/call representations disagree: value=%+v call=%+v", value, got)
	}
	if candidate := functionPlanFor(t, plan, packageFunction(t, pkg, "chaCandidate")); candidate.FuncRep != DirectPlain {
		t.Fatalf("managed CHA candidate leaked into foreign domain: %+v", candidate)
	}
	for fn, wantKind := range map[*ssa.Function]CallKind{
		foreignGo:    CallSpawn,
		foreignDefer: CallDefer,
	} {
		foreignCall := onlyNonBuiltinCall(t, fn)
		assertCallRep(t, plan, foreignCall, Dispatch, true)
		if got, _ := plan.CallPlan(foreignCall); got.Kind != wantKind || got.Unresolved != UnknownForeign {
			t.Fatalf("foreign %s call plan = %+v, want kind=%v unresolved foreign", fn.Name(), got, wantKind)
		}
	}
	mixedCall := onlyNonBuiltinCall(t, mixed)
	assertCallRep(t, plan, mixedCall, Dispatch, true, "knownCoro")
	mixedPlan, _ := plan.CallPlan(mixedCall)
	if mixedPlan.Kind != CallDirect || mixedPlan.Unresolved != UnknownForeign {
		t.Fatalf("mixed managed/foreign call plan = %+v", mixedPlan)
	}
	if got := functionPlanFor(t, plan, mixed); !got.Effect.Contains(MayPark|WaitForeign) || got.Effect.IsOpaque() {
		t.Fatalf("mixed managed/foreign graph effect = %+v", got)
	}
	if got := functionPlanFor(t, plan, packageFunction(t, pkg, "knownCoro")); got.Demand != AsyncDemand || got.FuncRep != Dispatch {
		t.Fatalf("known managed subset plan = %+v", got)
	}
}

func TestAnalyzeSSARawCFunctionTransportIsTypeDirected(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "raw_c_transport.go", `package coroid

type CFunc func(int) int

type Mixed struct {
	Raw CFunc
	Managed func(int) int
}

func invokeRaw(fn CFunc) int { return fn(1) }
func invokeManaged(fn func(int) int) int { return fn(1) }
func carryMixed(value Mixed) Mixed { return value }
`)
	invokeRaw := packageFunction(t, pkg, "invokeRaw")
	invokeManaged := packageFunction(t, pkg, "invokeManaged")
	carryMixed := packageFunction(t, pkg, "carryMixed")

	plan, err := AnalyzeSSA(prog, Roots{
		{Function: invokeRaw, Demand: AsyncDemand},
		{Function: invokeManaged, Demand: AsyncDemand},
		{Function: carryMixed, Demand: AsyncDemand},
	}, SSAConfig{
		ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (UnknownTarget, error) {
			return UnknownForeign, nil
		},
		ClassifyRawCFunctionType: func(typ types.Type) (bool, error) {
			named, ok := types.Unalias(typ).(*types.Named)
			return ok && named.Obj() != nil && named.Obj().Pkg() != nil &&
				named.Obj().Pkg().Path() == "example.test/coroid" && named.Obj().Name() == "CFunc", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	rawCall := onlyNonBuiltinCall(t, invokeRaw)
	rawCallPlan, ok := plan.CallPlan(rawCall)
	if !ok || rawCallPlan.Kind != CallForeign || rawCallPlan.Rep != DirectPlain ||
		rawCallPlan.Transport != RawCCodePointer || !rawCallPlan.Open ||
		rawCallPlan.Unresolved != UnknownForeign || !rawCallPlan.MayBeNil {
		t.Fatalf("raw C dynamic CallPlan = %+v, present=%t", rawCallPlan, ok)
	}
	rawValuePlan, ok := plan.ValuePlan(rawCall.Common().Value)
	if !ok || len(rawValuePlan.Funcs) != 1 || rawValuePlan.Funcs[0].Rep != DirectPlain ||
		rawValuePlan.Funcs[0].Transport != RawCCodePointer || !rawValuePlan.Funcs[0].MayBeNil {
		t.Fatalf("raw C callee ValuePlan = %+v, present=%t", rawValuePlan, ok)
	}

	managedCall := onlyNonBuiltinCall(t, invokeManaged)
	managedCallPlan, ok := plan.CallPlan(managedCall)
	if !ok || managedCallPlan.Kind != CallForeign || managedCallPlan.Rep != Dispatch ||
		managedCallPlan.Transport != ManagedTransport || !managedCallPlan.Open ||
		managedCallPlan.Unresolved != UnknownForeign || !managedCallPlan.MayBeNil {
		t.Fatalf("managed dynamic CallPlan = %+v, present=%t", managedCallPlan, ok)
	}
	managedValuePlan, ok := plan.ValuePlan(managedCall.Common().Value)
	if !ok || len(managedValuePlan.Funcs) != 1 || managedValuePlan.Funcs[0].Rep != Dispatch ||
		managedValuePlan.Funcs[0].Transport != ManagedTransport || !managedValuePlan.Funcs[0].MayBeNil {
		t.Fatalf("managed callee ValuePlan = %+v, present=%t", managedValuePlan, ok)
	}

	mixedPlan, ok := plan.ValuePlan(carryMixed.Params[0])
	if !ok || len(mixedPlan.Funcs) != 2 {
		t.Fatalf("mixed aggregate ValuePlan = %+v, present=%t", mixedPlan, ok)
	}
	wantMixed := []FuncRepLeaf{
		{Path: []FuncPathStep{{Kind: FuncPathStructField, Index: 0}}, Rep: DirectPlain, Transport: RawCCodePointer, MayBeNil: true},
		{Path: []FuncPathStep{{Kind: FuncPathStructField, Index: 1}}, Rep: Dispatch, Transport: ManagedTransport, MayBeNil: true},
	}
	if !reflect.DeepEqual(mixedPlan.Funcs, FuncRepMap(wantMixed)) {
		t.Fatalf("mixed aggregate leaves = %+v, want %+v", mixedPlan.Funcs, wantMixed)
	}
}

func TestAnalyzeSSACHAClosedFunctionCallPlan(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "cha_closed.go", `package coroid
func target(int, int, int) int { return 0 }
func invoke(fn func(int, int, int) int) { _ = fn(1, 2, 3) }
func seed() { invoke(target) }
`)
	invoke := packageFunction(t, pkg, "invoke")
	call := onlyNonBuiltinCall(t, invoke)
	plan, err := AnalyzeSSA(prog, Roots{{Function: invoke, Demand: AsyncDemand}}, SSAConfig{
		DynamicResolution: DynamicCHAClosed,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCallRep(t, plan, call, Dispatch, false, "target")
	if got, _ := plan.CallPlan(call); !got.MayBeNil {
		t.Fatalf("CHA-closed function call lost its required nil check: %+v", got)
	}
}

func TestAnalyzeSSAStaticExternalCallPlans(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "external.go", `package coroid
func external()
func caller() { external() }
`)
	external := packageFunction(t, pkg, "external")
	caller := packageFunction(t, pkg, "caller")
	call := onlyNonBuiltinCall(t, caller)

	unknown, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: AsyncDemand}}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}
	assertCallRep(t, unknown, call, Dispatch, false, "external")

	known, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: AsyncDemand}}, SSAConfig{
		ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == external {
				return SSAFunctionPolicy{Effect: WaitPlatform, External: ExternalKnown, OverrideExternal: true}, nil
			}
			return SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCallRep(t, known, call, DirectCoro, false, "external")

	foreign, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: AsyncDemand}}, SSAConfig{
		ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == external {
				return SSAFunctionPolicy{External: ExternalUnknownForeign, OverrideExternal: true}, nil
			}
			return SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCallRep(t, foreign, call, DirectPlain, false, "external")
	if got, _ := foreign.CallPlan(call); got.Kind != CallForeign {
		t.Fatalf("foreign call kind = %v, want CallForeign", got.Kind)
	}
}

func TestAnalyzeSSAAggregateFuncRepMap(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "aggregate.go", `package coroid

type Bundle struct {
	Plain int
	Callback func()
	Nested [2]func()
}

func bundle() Bundle { return Bundle{} }
`)
	bundle := packageFunction(t, pkg, "bundle")
	plan, err := AnalyzeSSA(prog, Roots{{Function: bundle, Demand: AsyncDemand}}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}

	wantPaths := [][]FuncPathStep{
		{{Kind: FuncPathStructField, Index: 1}},
		{{Kind: FuncPathStructField, Index: 2}, {Kind: FuncPathArrayElement, Index: -1}},
	}
	found := false
	for _, block := range bundle.Blocks {
		for _, instruction := range block.Instrs {
			values := make([]ssa.Value, 0, 1)
			if value, ok := instruction.(ssa.Value); ok {
				values = append(values, value)
			}
			if ret, ok := instruction.(*ssa.Return); ok {
				values = append(values, ret.Results...)
			}
			for _, value := range values {
				if isScalarFuncType(value.Type()) {
					continue
				}
				valuePlan, ok := plan.ValuePlan(value)
				if !ok || len(valuePlan.Funcs) != len(wantPaths) {
					continue
				}
				found = true
				for i, leaf := range valuePlan.Funcs {
					if leaf.Rep != Dispatch || !reflect.DeepEqual(leaf.Path, wantPaths[i]) {
						t.Fatalf("aggregate leaf %d = %+v, want path %+v dispatch", i, leaf, wantPaths[i])
					}
				}
				valuePlan.Funcs[0].Path[0].Index = 99
				again, _ := plan.ValuePlan(value)
				if again.Funcs[0].Path[0].Index == 99 {
					t.Fatal("aggregate ValuePlan path was not defensively copied")
				}
			}
		}
	}
	if !found {
		t.Fatal("no aggregate SSA value received a FuncRepMap")
	}
}

func TestFuncLeafPathsRecursiveAndContainers(t *testing.T) {
	signature := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	nodeName := types.NewTypeName(0, nil, "Node", nil)
	node := types.NewNamed(nodeName, nil, nil)
	node.SetUnderlying(types.NewStruct([]*types.Var{
		types.NewVar(0, nil, "Next", types.NewPointer(node)),
		types.NewVar(0, nil, "Callback", signature),
		types.NewVar(0, nil, "Handlers", types.NewMap(types.Typ[types.Int], types.NewChan(types.SendRecv, signature))),
	}, nil))

	want := [][]FuncPathStep{
		{{Kind: FuncPathStructField, Index: 1}},
		{{Kind: FuncPathStructField, Index: 2}, {Kind: FuncPathMapValue, Index: -1}, {Kind: FuncPathChanElement, Index: -1}},
	}
	if got := funcLeafPaths(node); !reflect.DeepEqual(got, want) {
		t.Fatalf("funcLeafPaths = %+v, want %+v", got, want)
	}
}

func onlyNonBuiltinCall(t *testing.T, fn *ssa.Function) ssa.CallInstruction {
	t.Helper()
	var calls []ssa.CallInstruction
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
				continue
			}
			calls = append(calls, call)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("%s has %d non-builtin calls, want 1", fn.Name(), len(calls))
	}
	return calls[0]
}

func assertCallRep(t *testing.T, plan *SSAPlan, call ssa.CallInstruction, rep FuncRep, open bool, targetNames ...string) {
	t.Helper()
	got, ok := plan.CallPlan(call)
	if !ok {
		t.Fatalf("call %s has no plan", call)
	}
	if got.Rep != rep || got.Open != open {
		t.Fatalf("call plan = %+v, want rep=%s open=%v", got, rep, open)
	}
	wantTargets := make([]FunctionID, 0, len(targetNames))
	for _, name := range targetNames {
		var found FunctionID
		for _, function := range plan.Functions() {
			if function.Function.Name() == name {
				found = function.Plan.ID
				break
			}
		}
		if found == "" {
			t.Fatalf("target function %s not found", name)
		}
		wantTargets = append(wantTargets, found)
	}
	sort.Slice(wantTargets, func(i, j int) bool { return wantTargets[i] < wantTargets[j] })
	if len(got.Targets) != len(wantTargets) {
		t.Fatalf("call targets = %v, want %v", got.Targets, wantTargets)
	}
	for i := range wantTargets {
		if got.Targets[i] != wantTargets[i] {
			t.Fatalf("call targets = %v, want %v", got.Targets, wantTargets)
		}
	}
	if len(got.Targets) != 0 {
		got.Targets[0] = "mutated"
		again, _ := plan.CallPlan(call)
		if again.Targets[0] == "mutated" {
			t.Fatal("CallPlan did not return a defensive copy")
		}
	}
}
