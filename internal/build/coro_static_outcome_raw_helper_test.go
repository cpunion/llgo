//go:build !llgo

package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// Use the production runtime and whole-program proof: a synthetic helper with
// a no-suspend policy would conceal the independent raw/managed representations
// that originally prevented an otherwise synchronous aggregate comparison from
// acquiring its outcome entry.
func TestCoroStaticOutcomeRawAggregateAvoidsFrame(t *testing.T) {
	t.Setenv(llgoBuildCache, "off")
	source := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(source, []byte(`package main
var value [32]byte
var enabled bool
var sink bool
func compare(left, right [32]byte, flag *bool) bool {
	return *flag && left == right
}
func main() { sink = compare(value, [32]byte{}, &enabled) }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	var compare coro.FunctionPlan
	var found, rawHelper bool
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.CoroPlanObserver = func(_ *ssa.Package, plan *coro.SSAPlan) {
		for _, function := range plan.Functions() {
			if function.Function.Name() != "compare" || function.Function.Pkg == nil || function.Function.Pkg.Pkg.Name() != "main" {
				continue
			}
			compare, found = function.Plan, true
			for _, call := range plan.LoweredCalls(function.Function) {
				if call.LogicalName == "memequal" {
					rawHelper = call.RawPlain && plan.HasRawPlainVariant(call.Target)
				}
			}
		}
	}
	pkgs, err := Do([]string{source}, conf)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].LPkg == nil {
		t.Fatalf("generated packages = %+v", pkgs)
	}
	defer pkgs[0].LPkg.Prog.Dispose()
	if !found || !rawHelper || !compare.StaticOutcome || compare.Emission != coro.EmitOutcomePlain ||
		compare.AtomicCostProof != coro.AtomicCostUnproven {
		t.Fatalf("aggregate comparison plan = %+v (found=%t exact raw helper=%t)", compare, found, rawHelper)
	}
	module := pkgs[0].LPkg.Module()
	for _, name := range []string{"main.compare$coro", "main.compare$coro.resume", "main.compare$coro.destroy"} {
		if !module.NamedFunction(name).IsNil() {
			t.Fatalf("synchronous aggregate comparison retained %s", name)
		}
	}
	fn := module.NamedFunction("main.compare$outcome")
	if fn.IsNil() || fn.IsDeclaration() {
		t.Fatal("comparison has no emitted outcome definition")
	}
	ir := fn.String()
	for _, unwanted := range []string{"llvm.coro.", "__llgo_coro_frame_alloc", "__llgo_coro_await_"} {
		if strings.Contains(ir, unwanted) {
			t.Fatalf("outcome comparison retained %s:\n%s", unwanted, ir)
		}
	}
	if !strings.Contains(ir, "runtime.memequal") {
		t.Fatalf("comparison did not exercise the actual aggregate helper:\n%s", ir)
	}
}

func TestCoroStaticOutcomeCloseRetainsTaskBoundBody(t *testing.T) {
	t.Setenv(llgoBuildCache, "off")
	source := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(source, []byte(`package main
import "runtime"
type receiver struct { done chan<- struct{} }
func makePair() {
	r := &receiver{make(chan struct{})}
	runtime.SetFinalizer(r, func(r *receiver) { close(r.done) })
}
func main() { makePair(); runtime.GC() }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	var closePlan coro.FunctionPlan
	found := false
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.CoroPlanObserver = func(_ *ssa.Package, plan *coro.SSAPlan) {
		for _, function := range plan.Functions() {
			if function.Function.Name() == "makePair$1" && function.Function.Pkg != nil && function.Function.Pkg.Pkg.Name() == "main" {
				closePlan, found = function.Plan, true
			}
		}
	}
	pkgs, err := Do([]string{source}, conf)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].LPkg == nil {
		t.Fatalf("generated packages = %+v", pkgs)
	}
	defer pkgs[0].LPkg.Prog.Dispose()
	if !found || closePlan.StaticOutcome || closePlan.Emission != coro.EmitCoroutine {
		t.Fatalf("close wrapper plan = %+v (found=%t); want full task-bound body", closePlan, found)
	}
	module := pkgs[0].LPkg.Module()
	if body := module.NamedFunction("main.makePair$1$coro"); body.IsNil() || body.IsDeclaration() {
		t.Fatal("close wrapper has no full coroutine definition")
	}
	if !module.NamedFunction("main.makePair$1$outcome").IsNil() {
		t.Fatal("close wrapper retained an unsupported outcome-only definition")
	}
}
