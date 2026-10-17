//go:build !llgo

package coro

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestAnalyzeSSAImportedManagedMethodReferenceRequiresPublishedEntry(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "imported_method_reference.go", `package coroid
type Receiver struct{}
func (Receiver) Method()
func owner() {}
`)
	owner := packageFunction(t, pkg, "owner")
	receiver := pkg.Pkg.Scope().Lookup("Receiver").Type()
	method := prog.MethodValue(prog.MethodSets.MethodSet(receiver).Lookup(pkg.Pkg, "Method"))
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{owner, method})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []ManagedEntryKind{ManagedEntryNone, ManagedEntryCoroutine} {
		config := planDigestSSAConfig()
		config.EmissionUniverse = universe
		config.ClassifyManagedValueReferences = func(fn *ssa.Function) ([]*ssa.Function, error) {
			if fn == owner {
				return []*ssa.Function{method}, nil
			}
			return nil, nil
		}
		config.ClassifyFunction = func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == method {
				return SSAFunctionPolicy{Effect: AwaitStructured, ManagedEntry: entry, IgnoreBody: true, OverrideExternal: true, External: ExternalKnown}, nil
			}
			return SSAFunctionPolicy{}, nil
		}
		plan, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, config)
		if entry == ManagedEntryNone {
			if err == nil || !strings.Contains(err.Error(), "exact imported managed entry") {
				t.Fatalf("missing published entry: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		got := functionPlanFor(t, plan, method)
		if got.FuncRep != Dispatch || got.Emission != EmitExternal || got.ManagedEntry != ManagedEntryCoroutine || got.Primary != PrimaryExternal {
			t.Fatalf("imported method re-emitted or lost its descriptor: %+v", got)
		}
		if _, err := plan.canonicalPlanDigest(validPlanDigestMetadata()); err != nil {
			t.Fatalf("digest imported method reference: %v", err)
		}
	}
}
