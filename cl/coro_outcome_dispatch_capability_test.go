//go:build !llgo

package cl

import (
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroOutcomePlainDirectBodyCanCallDescriptor(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, generic := range []bool{false, true} {
		for _, target := range []struct {
			name  string
			type_ *llssa.Target
		}{
			{name: "native"},
			{name: "wasm32", type_: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
		} {
			name, typeParams, number, instance := "plain/", "", "int", ""
			if generic {
				name, typeParams, number, instance = "generic/", "[T ~int]", "T", "[int]"
			}
			t.Run(name+target.name, func(t *testing.T) {
				source := `package foo
func Abs` + typeParams + `(value ` + number + `) ` + number + ` {
	if value < 0 { return -value }
	return value
}
func Difference` + typeParams + `(a, b ` + number + `, abs func(` + number + `) ` + number + `) ` + number + ` {
	return abs(a-b)
}
func Root(a, b int) int { return Difference(a, b, Abs` + instance + `) }
`
				prog, pkg, plan, _ := compileCoroOutcomePlainSourceWithResolution(
					t, target.type_, source, "Root", 64, coro.DynamicCHAClosed,
				)
				defer prog.Dispose()
				module := pkg.Module()
				defer module.Dispose()
				functionName := "Difference"
				if generic {
					functionName += "[int]"
				}
				found := false
				for _, function := range plan.Functions() {
					if function.Function.Name() != functionName {
						continue
					}
					found = true
					if function.Plan.FuncRep != coro.DirectCoro ||
						function.Plan.Emission != coro.EmitOutcomePlain || !function.Plan.StaticOutcome {
						t.Fatalf("Difference plan = %+v; want a non-descriptor outcome body", function.Plan)
					}
				}
				if !found {
					t.Fatal("Difference function is absent from the frozen plan")
				}
				body := module.NamedFunction("foo." + functionName + coroOutcomePlainPrimarySuffix)
				if body.IsNil() {
					t.Fatalf("outcome-only Difference body is absent:\n%s", module.String())
				}
				for _, forbidden := range []string{"llvm.coro.", coroFrameAllocHookV1, coroAwaitPrepareInlineHookV4} {
					if strings.Contains(body.String(), forbidden) {
						t.Fatalf("outcome-only descriptor call retained %q:\n%s", forbidden, body.String())
					}
				}
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatalf("verify outcome-only descriptor module: %v\n%s", err, module.String())
				}
			})
		}
	}
}

func TestCoroOutcomeDescriptorWithParkingTargetRemainsCoroutine(t *testing.T) {
	// Enabling the descriptor lowering capability must not let a dynamically
	// selected coroutine run on the caller's synchronous outcome stack.
	const source = `package foo
var input chan int
func Abs(value int) int { return <-input + value }
func Difference(a, b int, abs func(int) int) int { return abs(a-b) }
func Root(a, b int) int { return Difference(a, b, Abs) }
`
	prog, pkg, plan, ssaPkg := compileCoroOutcomePlainSourceWithResolution(
		t, nil, source, "Root", -1, coro.DynamicCHAClosed,
	)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()
	abs, _ := plan.FunctionPlan(ssaPkg.Func("Abs"))
	caller, _ := plan.FunctionPlan(ssaPkg.Func("Difference"))
	if abs.HasStaticOutcome() || abs.ManagedEntry != coro.ManagedEntryCoroutine {
		t.Fatalf("Abs plan = %+v; want a parking coroutine", abs)
	}
	if caller.StaticOutcome || caller.Emission != coro.EmitCoroutine ||
		!module.NamedFunction("foo.Difference"+coroOutcomePlainPrimarySuffix).IsNil() {
		t.Fatalf("Difference incorrectly consumed a parking descriptor on its native stack: %+v", caller)
	}
}

func TestCoroStaticOutcomeDescriptorStillRequiresExactNativeProof(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func Apply(callback func(int) int) int { return callback(1) }
`)
	fn := ssaPkg.Func("Apply")
	var call *ssa.Call
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.Call); ok {
				call = candidate
			}
		}
	}
	if call == nil {
		t.Fatal("fixture has no dynamic call")
	}
	for _, test := range []struct {
		name      string
		published bool
		native    bool
		want      bool
	}{
		{"all-outcome-native", true, true, true},
		{"open-mixed-or-coroutine-target", false, true, false},
		{"oversized-result", true, false, false},
		{"neither-proof", false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			physical := &coroPhysicalFunctionPlan{
				function: fn,
				instructions: map[ssa.Instruction]coroPhysicalInstructionPlan{
					call: {
						semantic:              coroSemanticInstructionPlan{evaluated: true, recipe: "cl.ssa.call.v1"},
						control:               coroPhysicalControlDispatchAwait,
						structuredOutcomeOnly: test.published, directOutcomeNativeResult: test.native,
					},
				},
			}
			err := validateStaticOutcomeFrozenPlan(physical, coro.FunctionPlan{StaticOutcome: true})
			if (err == nil) != test.want {
				t.Fatalf("static outcome proof error = %v, want success %t", err, test.want)
			}
		})
	}
}
