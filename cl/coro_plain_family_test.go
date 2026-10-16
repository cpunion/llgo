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

func TestCoroClosedPlainFamilySelectsExistingSynchronousABI(t *testing.T) {
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{{name: "native"}, {name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		for _, shape := range []struct {
			name, source string
			deferred     bool
		}{
			{"nullable_scalar", `package foo
func First(value uint32) uint32 { return value+1 }
func Second(value uint32) uint32 { return value+2 }
func Apply(callback func(uint32) uint32, value uint32) uint32 { return callback(value) }
func Root(value uint32, second, nilValue bool) uint32 {
 callback := First; if second { callback = Second }; if nilValue { callback = nil }
 return Apply(callback, value)
}`, false},
			{"aggregate", `package foo
type Pair struct { Number uint32; Text string }
func First(value Pair) Pair { value.Number++; return value }
func Second(value Pair) Pair { value.Number += 2; return value }
func Apply(callback func(Pair) Pair, value Pair) Pair { return callback(value) }
func Root(value Pair, second bool) Pair {
 callback := First; if second { callback = Second }; return Apply(callback, value)
}`, false},
			{"interface", `package foo
type Pair struct { Number uint32; Text string }
type Value interface { Transform(Pair) Pair }
type First struct{}
type Second struct{}
var first First
var other Second
func (*First) Transform(value Pair) Pair { value.Number++; return value }
func (*Second) Transform(value Pair) Pair { value.Number += 2; return value }
func Apply(value Value, input Pair) Pair { return value.Transform(input) }
func Root(value Pair, second bool) Pair {
 var callback Value = &first; if second { callback = &other }; return Apply(callback, value)
}`, false},
			{"defer", `package foo
var count uint32
func First() { count++ }
func Second() { count += 2 }
func Apply(callback func()) { defer callback() }
func Root(second, nilValue bool) {
 callback := First; if second { callback = Second }; if nilValue { callback = nil }; Apply(callback)
}`, true},
		} {
			t.Run(target.name+"/"+shape.name, func(t *testing.T) {
				prog, compiled, plan, pkg := compileCoroOutcomePlainSourceWithResolution(
					t, target.arch, shape.source, "Root", -1, coro.DynamicCHAClosed,
				)
				defer prog.Dispose()
				module := compiled.Module()
				defer module.Dispose()
				apply := pkg.Func("Apply")
				var call ssa.CallInstruction
				for _, block := range apply.Blocks {
					for _, instruction := range block.Instrs {
						if candidate, ok := instruction.(ssa.CallInstruction); ok {
							if call != nil {
								t.Fatal("fixture has multiple source calls")
							}
							call = candidate
						}
					}
				}
				if call == nil {
					t.Fatal("fixture lost its dynamic call")
				}
				callPlan, found := plan.CallPlan(call)
				if !found || len(callPlan.Targets) != 2 || coroManagedDispatchPublishedEntry(plan, callPlan) != coro.ManagedEntryPlain {
					t.Fatalf("fixture lost its two-target plain publication proof: %+v", callPlan)
				}
				owner, _ := plan.FunctionPlan(apply)
				var body llvm.Value
				if shape.deferred {
					body = requireCoroPhysicalFunction(t, module, "foo.Apply")
				} else {
					if owner.Emission != coro.EmitOutcomePlain || !owner.StaticOutcome {
						t.Fatalf("plain family retained a coroutine owner: %+v", owner)
					}
					body = module.NamedFunction("foo.Apply" + coroOutcomePlainPrimarySuffix)
				}
				if body.IsNil() {
					t.Fatal("missing physical owner")
				}
				for _, forbidden := range []string{coroAwaitPrepareInlineHookV4, "coro.dispatch.version.invalid"} {
					if strings.Contains(body.String(), forbidden) {
						t.Fatalf("plain family retained %q:\n%s", forbidden, body.String())
					}
				}
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatal(err)
				}
				runCoroABITestPipeline(t, prog, module)
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
