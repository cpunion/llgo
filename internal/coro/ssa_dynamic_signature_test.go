//go:build !llgo

package coro

import (
	"errors"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestRestrictedCHASourceSignatureFailureRejectsPartialPlan(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
type V int
func target(V) {}
func root(callback func(V), value V) { callback(value) }
var published = target
`)
	root := packageFunction(t, pkg, "root")
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{
		root, packageFunction(t, pkg, "target"), packageFunction(t, pkg, "init"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"target", "root"} {
		for _, nilResult := range []bool{false, true} {
			project := func(fn *ssa.Function, signature *types.Signature) (*types.Signature, error) {
				if fn.Name() == owner {
					if nilResult {
						return nil, nil
					}
					return nil, errors.New("source signature fixture failure")
				}
				return signature, nil
			}
			plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, SSAConfig{
				EmissionUniverse: universe, DynamicResolution: DynamicCHAClosed,
				DynamicSignature: project,
			})
			if plan != nil || err == nil || !strings.Contains(err.Error(), "restricted CHA source signature") || !strings.Contains(err.Error(), owner) {
				t.Fatalf("owner=%s nil=%t: plan=%v err=%v; want contextual error and no partial plan", owner, nilResult, plan, err)
			}
		}
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, SSAConfig{
		DynamicSignature: func(_ *ssa.Function, signature *types.Signature) (*types.Signature, error) {
			return signature, nil
		},
	})
	if plan != nil || err == nil || !strings.Contains(err.Error(), "requires an SSA emission universe") {
		t.Fatalf("unfrozen source signature relation: plan=%v error=%v", plan, err)
	}
}

func TestManagedSpawnConsumesFrozenSourceSignature(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
type Original int
type Replacement int
type Unrelated int
func target(Original) {}
func wrong(Unrelated) {}
func launch(callback func(Replacement), value Replacement) { go callback(value) }
var published = target
var unrelated = wrong
`)
	launch, target, wrong := packageFunction(t, pkg, "launch"), packageFunction(t, pkg, "target"), packageFunction(t, pkg, "wrong")
	var spawn *ssa.Go
	for _, block := range launch.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.Go); ok {
				spawn = candidate
			}
		}
	}
	if spawn == nil || types.Identical(target.Signature, spawn.Common().Signature()) {
		t.Fatal("fixture lost the distinct raw source identities")
	}
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{launch, target, wrong, packageFunction(t, pkg, "init")})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: launch, Demand: AsyncDemand}, {Function: wrong, Demand: AsyncDemand}}, SSAConfig{
		EmissionUniverse: universe, DynamicResolution: DynamicCHAClosed,
		DynamicSignature: func(owner *ssa.Function, signature *types.Signature) (*types.Signature, error) {
			if owner == target {
				return spawn.Common().Signature(), nil
			}
			return signature, nil
		},
		ClassifyFunction: func(*ssa.Function) (SSAFunctionPolicy, error) {
			return SSAFunctionPolicy{Effect: YieldOnly}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	callPlan, err := plan.ResolveManagedDispatchSpawn(spawn)
	if err != nil || callPlan.Open || len(callPlan.Targets) != 1 {
		t.Fatalf("projected spawn = %+v, %v; want one exact closed target", callPlan, err)
	}
	// Same machine shape is not a source identity certificate. Deliberately
	// corrupt the target set to prove the consumer independently rejects it.
	wrongPlan, _ := plan.FunctionPlan(wrong)
	callPlan.Targets = []FunctionID{wrongPlan.ID}
	plan.callPlans[spawn] = callPlan
	if _, err := plan.ResolveManagedDispatchSpawn(spawn); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("nominally incompatible spawn target accepted: %v", err)
	}
}
