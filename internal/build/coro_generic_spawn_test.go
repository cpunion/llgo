//go:build !llgo

package build

import (
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroPlanInputClosedStaticGenericSpawn(t *testing.T) {
	pkg, _ := buildCoroPlanTestPackage(t, "example.com/genericspawn", `package genericspawn
func Send[T any](ch chan T, value T) { ch <- value }
func Launch[T any](ch chan T, value T) {
	go func(ch chan T, value T) { ch <- value }(ch, value)
}
type Sender[T any] struct{}
func (Sender[T]) Launch(ch chan T, value T) {
	go func(ch chan T, value T) { ch <- value }(ch, value)
}
func Root() {
	ch := make(chan int, 3)
	go Send[int](ch, 1)
	Launch[int](ch, 2)
	Sender[int]{}.Launch(ch, 3)
}
`, nil)
	input := CoroPlanInput{Program: pkg.Prog}
	plan, err := input.Analyze(coro.Roots{{Function: pkg.Func("Root"), Demand: coro.AsyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCoroSpawnPlan(plan); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, owner := range plan.Functions() {
		for _, call := range coroPlanTestCalls(owner.Function) {
			spawn, ok := call.(*ssa.Go)
			if !ok {
				continue
			}
			count++
			target, physical, err := plan.ResolveClosedStaticSpawn(spawn)
			if err != nil {
				t.Fatal(err)
			}
			if !coro.MaterializedGenericInstance(target) ||
				physical.Primary != coro.PrimaryCoroutine || physical.Emission != coro.EmitCoroutine ||
				!physical.Effect.Contains(coro.YieldOnly) {
				t.Fatalf("generic spawn lost its single preemptible primary: %v %+v", target, physical)
			}
		}
	}
	if count != 3 {
		t.Fatalf("checked %d concrete spawns, want 3", count)
	}
}
