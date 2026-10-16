//go:build !llgo

package cl

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/typepatch"
	"github.com/xgo-dev/llgo/ssa/abi"
	"golang.org/x/tools/go/ssa"
)

func TestCoroDynamicSignatureClosesReplacedSourceTargets(t *testing.T) {
	for _, test := range []struct {
		name, publications string
		want               []string
	}{
		{"plain", "Plain", []string{"Plain"}},
		{"coroutine", "Async", []string{"Async"}},
		{"mixed", "Plain, Async", []string{"Plain", "Async"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			original := testProg.addPackage(t, "example.com/emission/callback", `package callback
type Value struct { Fn func(int) int }
type Other struct { Fn func(int) int }
var channel chan int
func Plain(v *Value) *Value { return v }
func Async(v *Value) *Value { <-channel; return v }
func Wrong(v *Other) *Other { return v }
var Published = []func(*Value) *Value{`+test.publications+`}
var Unrelated = Wrong
`)
			alt := testProg.addPackage(t, abi.PatchPathPrefix+"example.com/emission/callback", `package callback
type Value struct { Fn func(int) int }
type Other struct { Fn func(int) int }
func Invoke(callback func(*Value) *Value, value *Value) *Value { return callback(value) }
// This physically present callback is the old wrong closed-world subset.
var channel chan int
func Local(v *Value) *Value { <-channel; return v }
var PublishedLocal = Local
`)
			testProg.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := prepareStacklessEmissionUniverse(prog, Patches{
				"example.com/emission/callback": {Alt: alt.ssa, Types: typepatch.Clone(alt.types)},
			}, []EmissionPackage{{SSA: original.ssa, Files: []*ast.File{original.file, alt.file}}})
			if err != nil {
				t.Fatal(err)
			}
			invoke := alt.ssa.Func("Invoke")
			var call *ssa.Call
			for _, block := range invoke.Blocks {
				for _, instruction := range block.Instrs {
					if candidate, ok := instruction.(*ssa.Call); ok {
						call = candidate
					}
				}
			}
			if call == nil || call.Common().StaticCallee() != nil {
				t.Fatal("fixture lost its dynamic call")
			}
			plain := original.ssa.Func("Plain")
			if types.Identical(plain.Signature, call.Common().Signature()) {
				t.Fatal("fixture raw source/replacement signatures unexpectedly agree")
			}
			projected, err := universe.CoroPlanningMetadata().CallableSignature(plain, plain.Signature)
			if err != nil {
				t.Fatal(err)
			}
			projectedCall, err := universe.CoroPlanningMetadata().CallableSignature(invoke, call.Common().Signature())
			if err != nil || !types.Identical(projected, projectedCall) {
				t.Fatalf("source projection mismatch: %s / %s, %v", projected, projectedCall, err)
			}
			value := projected.Params().At(0).Type().(*types.Pointer).Elem().Underlying().(*types.Struct)
			if _, ok := value.Field(0).Type().Underlying().(*types.Signature); !ok {
				t.Fatalf("source projection physically lowered the function field: %s", value.Field(0).Type())
			}
			ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			config := coro.SSAConfig{
				EmissionUniverse: ssaUniverse, DynamicResolution: coro.DynamicCHAClosed,
				FunctionIDs: universe.FunctionIDConfig(), OutcomeMode: coro.OutcomeExplicitStatus,
				ClassifyLocalBody: universe.CoroLocalBodyFacts,
			}
			roots := coro.Roots{{Function: invoke, ManagedDemand: coro.AsyncDemand}}
			raw, err := coro.AnalyzeSSA(testProg.ssa, roots, config)
			if err != nil {
				t.Fatal(err)
			}
			rawCall, found := raw.CallPlan(call)
			if !found || rawCall.Open || len(rawCall.Targets) != 1 {
				t.Fatalf("fixture did not reproduce the old incorrectly closed subset: %+v", rawCall)
			}
			if !coroManagedDispatchPublishedCoroutineOnly(raw, rawCall) {
				t.Fatal("unprojected target subset did not reproduce the old wrong coroutine-only dispatch")
			}
			config.DynamicSignature = universe.CoroPlanningMetadata().CallableSignature
			plan, err := coro.AnalyzeSSA(testProg.ssa, roots, config)
			if err != nil {
				t.Fatal(err)
			}
			actual, found := plan.CallPlan(call)
			if !found || actual.Open || len(actual.Targets) != len(test.want)+1 {
				t.Fatalf("projected call plan = %+v; want exact source publications plus Local", actual)
			}
			want := map[string]bool{"Local": true}
			for _, name := range test.want {
				want[name] = true
			}
			for _, id := range actual.Targets {
				target, _ := plan.Function(id)
				if target == nil || !want[target.Name()] {
					t.Fatalf("unexpected target %v; unrelated nominal Other must stay excluded", target)
				}
				delete(want, target.Name())
			}
			if len(want) != 0 || coroManagedDispatchPublishedCoroutineOnly(plan, actual) != (test.name == "coroutine") {
				t.Fatalf("mixed/Plain target family lost exact dispatch: missing=%v plan=%+v", want, actual)
			}
			if err := validateCoroManagedDispatchCall(plan, invoke, call, actual, universe); err != nil {
				t.Fatalf("managed consumer rejected canonical source target identities: %v", err)
			}
			if test.name == "plain" {
				config.ClassifyClosedDynamicCall = func(owner *ssa.Function, occurrence ssa.CallInstruction) (coro.SSAClosedDynamicCallCertificate, bool, error) {
					if owner == invoke && occurrence == call {
						return coro.SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{plain}, SyncDispatch: true}, true, nil
					}
					return coro.SSAClosedDynamicCallCertificate{}, false, nil
				}
				syncPlan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{{Function: invoke, ManagedDemand: coro.SyncDemand}}, config)
				if err != nil {
					t.Fatal(err)
				}
				syncCall, _ := syncPlan.CallPlan(call)
				if err := validateCoroPlainDispatchCall(syncPlan, invoke, call, syncCall, universe); err != nil {
					t.Fatalf("plain consumer rejected canonical source target identities: %v", err)
				}
			}
			if _, err := universe.CoroPlanningMetadata().CallableSignature(nil, plain.Signature); err == nil || !strings.Contains(err.Error(), "requires") {
				t.Fatalf("missing owner error = %v", err)
			}
		})
	}
}
