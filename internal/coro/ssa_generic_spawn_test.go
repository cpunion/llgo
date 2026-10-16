//go:build !llgo

package coro

import (
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestSSAPlanClosedStaticGenericSpawn(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "generic_spawn.go", `package coroid
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
`)
	plan, err := AnalyzeSSA(prog, Roots{{Function: pkg.Func("Root"), Demand: AsyncDemand}}, SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyFunction: func(*ssa.Function) (SSAFunctionPolicy, error) {
			return SSAFunctionPolicy{Effect: YieldOnly}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	spawns := 0
	for _, owner := range plan.Functions() {
		for _, block := range owner.Function.Blocks {
			for _, instruction := range block.Instrs {
				spawn, ok := instruction.(*ssa.Go)
				if !ok {
					continue
				}
				spawns++
				target, physical, err := plan.ResolveClosedStaticSpawn(spawn)
				if err != nil {
					t.Errorf("%s: %v", owner.Function, err)
					continue
				}
				if target != spawn.Common().StaticCallee() || len(target.FreeVars) != 0 ||
					physical.Primary != PrimaryCoroutine || physical.Emission != EmitCoroutine {
					t.Fatalf("spawn does not retain exact context-free coroutine primary: %v %+v", target, physical)
				}
				if !MaterializedGenericInstance(target) || MaterializedGenericInstance(target.Origin()) {
					t.Fatalf("instance/origin identity not distinguished: %s", target)
				}
				originalSynthetic := target.Synthetic
				target.Synthetic = "unproven synthetic body"
				if _, _, err := plan.ResolveClosedStaticSpawn(spawn); err == nil {
					t.Fatal("unproven synthetic body acquired direct spawn ABI")
				}
				target.Synthetic = originalSynthetic
				target.FreeVars = []*ssa.FreeVar{nil}
				if _, _, err := plan.ResolveClosedStaticSpawn(spawn); err == nil {
					t.Fatal("captured body acquired context-free spawn ABI")
				}
				target.FreeVars = nil
				originalSignature := target.Signature
				target.Signature = types.NewSignatureType(nil, nil, nil, types.NewTuple(), types.NewTuple(), false)
				if MaterializedGenericInstance(target) {
					t.Fatal("signature/SSA parameter mismatch was accepted")
				}
				target.Signature = originalSignature
				if parent := target.Parent(); parent != nil {
					originalChildren := parent.AnonFuncs
					parent.AnonFuncs = nil
					if MaterializedGenericInstance(target) {
						t.Fatal("child absent from its parent acquired generic identity")
					}
					parent.AnonFuncs = append(append([]*ssa.Function(nil), originalChildren...), target)
					if MaterializedGenericInstance(target) {
						t.Fatal("duplicated child acquired generic identity")
					}
					parent.AnonFuncs = originalChildren
				}
			}
		}
	}
	if spawns != 3 {
		t.Fatalf("examined %d concrete spawns, want 3", spawns)
	}
}
