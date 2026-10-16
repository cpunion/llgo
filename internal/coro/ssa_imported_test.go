//go:build !llgo

package coro

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func importedGraphTestFacts() []LibraryEffectFunction {
	return []LibraryEffectFunction{
		{ID: FunctionID(FunctionIDSchema + ":" + strings.Repeat("a", 64)), ABIHash: strings.Repeat("1", 64),
			Effect: MayPark, Exec: MayUnwind, FuncRep: DirectCoro, Primary: PrimaryCoroutine,
			ManagedEntry: ManagedEntryCoroutine, PrimarySymbol: "archive.privateDrop$coro"},
		{ID: FunctionID(FunctionIDSchema + ":" + strings.Repeat("b", 64)), ABIHash: strings.Repeat("2", 64),
			FuncRep: DirectPlain, Primary: PrimaryPlain, ManagedEntry: ManagedEntryPlain,
			PrimarySymbol: "archive.unusedInitialize"},
	}
}

func TestAnalyzeSSAImportedHelpersShareGraphWithoutSourceFunctions(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "imported_helpers.go", `package coroid
func owner() {}
func root() { owner() }
`)
	owner, root := packageFunction(t, pkg, "owner"), packageFunction(t, pkg, "root")
	facts := importedGraphTestFacts()
	config := planDigestSSAConfig()
	config.MaxPlainInstructions = -1
	config.ImportedFunctions = facts
	config.ClassifyLoweredCalls = func(fn *ssa.Function) ([]SSALoweredCall, error) {
		if fn == owner {
			return []SSALoweredCall{{LogicalName: "delivery.drop", ImportedTarget: facts[0].ID}}, nil
		}
		return nil, nil
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []*ssa.Function{owner, root} {
		got := functionPlanFor(t, plan, fn)
		if !got.Effect.Contains(MayPark|AwaitStructured) || !got.Exec.Contains(MayUnwind) || got.Emission != EmitCoroutine {
			t.Fatalf("%s did not inherit archive helper effects: %+v", fn.Name(), got)
		}
	}
	imports := plan.ImportedFunctions()
	if len(imports) != 2 || imports[0].Fact != facts[0] || imports[1].Fact != facts[1] {
		t.Fatalf("archive inventory = %+v", imports)
	}
	if imports[0].Plan.External != ExternalKnown || imports[0].Plan.ManagedDemand != AsyncDemand ||
		imports[0].Plan.Emission == EmitNone || imports[1].Plan.Emission != EmitNone || imports[1].Plan.Demand != NoDemand {
		t.Fatalf("demand escaped its exact imported edge: %+v", imports)
	}
	if len(plan.BasePlan().Functions()) != len(plan.Functions())+len(imports) {
		t.Fatal("archive leaf is outside the single base plan")
	}
	for _, fn := range plan.Functions() {
		if fn.Function == nil {
			t.Fatal("archive leaf fabricated a nil SSA pair")
		}
	}
	if _, ok := plan.Function(facts[0].ID); ok {
		t.Fatal("archive helper pretended to own source SSA")
	}
	if _, ok := plan.ResolveLoweredCall(owner, "delivery.drop"); ok {
		t.Fatal("source-only resolver returned an imported helper")
	}
	if got, ok := plan.ResolveLoweredCallRecord(owner, "delivery.drop"); !ok || got.Target != nil || got.ImportedTarget != facts[0].ID {
		t.Fatalf("exact imported occurrence lost: %+v, %t", got, ok)
	}
	before, err := plan.CoroPlanDigest(validPlanDigestMetadata())
	if err != nil {
		t.Fatal(err)
	}
	// Both frontend input and public output are value copies, never authority
	// aliases. Caller mutations must not change the frozen inventory or digest.
	facts[0].ABIHash = strings.Repeat("3", 64)
	imports[0].Fact.PrimarySymbol = "mutated"
	imports[0].Plan.ManagedDemand = NoDemand
	after, err := plan.CoroPlanDigest(validPlanDigestMetadata())
	if err != nil || before != after {
		t.Fatalf("caller mutated frozen archive plan: %s / %s: %v", before, after, err)
	}
	for name, mutate := range map[string]func(*LibraryEffectFunction){
		"abi":     func(f *LibraryEffectFunction) { f.ABIHash = strings.Repeat("4", 64) },
		"symbol":  func(f *LibraryEffectFunction) { f.PrimarySymbol = "archive.other$coro" },
		"service": func(f *LibraryEffectFunction) { f.ProgramCapabilities = ExecutionDomainProgramCapability() },
		"false-predicate": func(f *LibraryEffectFunction) {
			f.ProgramPredicates = ProgramPredicateRequirements{Mask: ExecutionDomainProgramCapability()}
		},
	} {
		t.Run(name, func(t *testing.T) {
			saved := plan.importedFunctions[0].Fact
			defer func() { plan.importedFunctions[0].Fact = saved }()
			mutate(&plan.importedFunctions[0].Fact)
			got, err := plan.CoroPlanDigest(validPlanDigestMetadata())
			if err != nil || got == before {
				t.Fatalf("archive %s is absent from canonical digest: %v", name, err)
			}
		})
	}
	// Input order cannot affect the graph, archive inventory or canonical digest.
	config.ImportedFunctions = importedGraphTestFacts()
	config.ImportedFunctions[0], config.ImportedFunctions[1] = config.ImportedFunctions[1], config.ImportedFunctions[0]
	facts = importedGraphTestFacts()
	permuted, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := permuted.CoroPlanDigest(validPlanDigestMetadata()); err != nil || got != before {
		t.Fatalf("archive input order changed digest: %s / %s: %v", before, got, err)
	}
}

func TestAnalyzeSSAImportedHelpersFailClosed(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "invalid_imported_helpers.go", `package coroid
func root() {}
`)
	root := packageFunction(t, pkg, "root")
	for name, mutate := range map[string]func(*SSAConfig, *SSALoweredCall){
		"missing": func(c *SSAConfig, _ *SSALoweredCall) { c.ImportedFunctions = nil },
		"duplicate": func(c *SSAConfig, _ *SSALoweredCall) {
			c.ImportedFunctions = append(c.ImportedFunctions, c.ImportedFunctions[0])
		},
		"source-conflict": func(c *SSAConfig, _ *SSALoweredCall) {
			id, err := StableFunctionID(root, c.FunctionIDs)
			if err != nil {
				t.Fatal(err)
			}
			c.ImportedFunctions[0].ID = id
		},
		"invalid-id":     func(c *SSAConfig, _ *SSALoweredCall) { c.ImportedFunctions[0].ID = "not-an-archive-function" },
		"wrong-abi":      func(c *SSAConfig, _ *SSALoweredCall) { c.ImportedFunctions[0].ABIHash = "not-an-abi" },
		"missing-symbol": func(c *SSAConfig, _ *SSALoweredCall) { c.ImportedFunctions[0].PrimarySymbol = "" },
		"mixed-source":   func(_ *SSAConfig, call *SSALoweredCall) { call.Target = root },
		"mixed-dispatch": func(_ *SSAConfig, call *SSALoweredCall) { call.ManagedDispatch = true },
		"raw-plain":      func(_ *SSAConfig, call *SSALoweredCall) { call.RawPlain = true },
		"nested-delivery": func(c *SSAConfig, call *SSALoweredCall) {
			summaries, _ := deliveryLibrarySummaries()
			fact := summaries[0].Functions[0]
			fact.ID = call.ImportedTarget
			fact.Delivery.Producer = fact.ID
			c.ImportedFunctions = []LibraryEffectFunction{fact}
		},
	} {
		t.Run(name, func(t *testing.T) {
			config := planDigestSSAConfig()
			config.ImportedFunctions = importedGraphTestFacts()
			call := SSALoweredCall{LogicalName: "delivery.drop", ImportedTarget: config.ImportedFunctions[0].ID}
			mutate(&config, &call)
			config.ClassifyLoweredCalls = func(fn *ssa.Function) ([]SSALoweredCall, error) {
				if fn == root {
					return []SSALoweredCall{call}, nil
				}
				return nil, nil
			}
			if _, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, config); err == nil {
				t.Fatal("accepted malformed or unbound archive helper")
			}
		})
	}
}

func TestAnalyzeSSAImportedHelperOccurrenceContractsRemainOrthogonal(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "imported_occurrences.go", `package coroid
func owner() {}
`)
	owner := packageFunction(t, pkg, "owner")
	for _, test := range []struct {
		name                 string
		noUnwind, unwindOnly bool
	}{
		{name: "ordinary"},
		{name: "no-unwind", noUnwind: true},
		{name: "unwind-only", unwindOnly: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := importedGraphTestFacts()[:1]
			facts[0].Exec |= ThreadAffine
			plan, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, SSAConfig{
				ImportedFunctions: facts, MaxPlainInstructions: -1,
				ClassifyLoweredCalls: func(fn *ssa.Function) ([]SSALoweredCall, error) {
					if fn == owner {
						return []SSALoweredCall{{LogicalName: "delivery.drop", ImportedTarget: facts[0].ID,
							NoUnwind: test.noUnwind, UnwindOnly: test.unwindOnly}}, nil
					}
					return nil, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			got := functionPlanFor(t, plan, owner)
			if test.unwindOnly {
				if got.Effect != NoSuspend || got.Exec.Contains(MayUnwind|ThreadAffine) {
					t.Fatalf("unwind-only polluted owner: %+v", got)
				}
			} else if !got.Effect.Contains(MayPark) || !got.Exec.Contains(ThreadAffine) || got.Exec.Contains(MayUnwind) == test.noUnwind {
				t.Fatalf("occurrence suppressed unrelated archive facts: %+v", got)
			}
			helper := plan.ImportedFunctions()[0].Plan
			if helper.Effect != MayPark || !helper.Exec.Contains(MayUnwind|ThreadAffine) || helper.Emission == EmitNone {
				t.Fatalf("occurrence changed helper's producer facts: %+v", helper)
			}
		})
	}
}
