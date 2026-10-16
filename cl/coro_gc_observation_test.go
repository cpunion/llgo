//go:build !llgo

package cl

import (
	"go/ast"
	"go/types"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroGCObservationDemandRequiresReachableExactRegistration(t *testing.T) {
	const runtimePath = "github.com/xgo-dev/llgo/runtime/internal/runtime"
	for _, test := range []struct {
		name, path, body string
		want             bool
	}{
		{"registration", runtimePath, "EnableGCObserver()", true},
		{"function-value-registration", runtimePath, "enable := EnableGCObserver; enable()", true},
		{"query-only", runtimePath, "_ = GCObservationEnabled()", false},
		{"observer-only", runtimePath, "Observe()", false},
		{"dead-registration", runtimePath, "", false},
		{"unrelated-name", "example.com/user", "EnableGCObserver()", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			program := newEmissionTestProgram()
			pkg := program.addPackage(t, test.path, `package example
func EnableGCObserver() {}
func GCObservationEnabled() bool { return false }
func Observe() {}
func Dead() { EnableGCObserver() }
func Root() { `+test.body+` }
`)
			program.ssa.Build()
			plan, err := coro.AnalyzeSSA(program.ssa, coro.Roots{{Function: pkg.ssa.Func("Root"), Demand: coro.AsyncDemand}}, coro.SSAConfig{MaxPlainInstructions: -1})
			if err != nil {
				t.Fatal(err)
			}
			values, err := DiscoverCoroProgramPredicates(plan, nil)
			if err != nil || values.GCObservation() != test.want || values.ExecutionDomain() {
				t.Fatalf("discovered predicates = %#x, %v; want GC observation=%v", values, err, test.want)
			}
			capabilities, err := deriveCoroFunctionProgramCapabilities(plan, nil, nil)
			if err != nil || capabilities[pkg.ssa.Func("Root")].GCObservation() != test.want {
				t.Fatalf("final root capabilities = %#x, %v; want GC observation=%v", capabilities[pkg.ssa.Func("Root")], err, test.want)
			}
		})
	}
}

func TestCoroProgramPredicateCodeAddressObservationIsNotInvocation(t *testing.T) {
	for _, feature := range []struct {
		name, path, seed string
		bit              coro.ProgramCapabilities
	}{
		{"GCObservation", "github.com/xgo-dev/llgo/runtime/internal/runtime", "EnableGCObserver", coro.GCObservationProgramCapability()},
		{"ExecutionDomain", "internal/synctest", "Run", coro.ExecutionDomainProgramCapability()},
	} {
		for _, test := range []struct {
			name, body string
			raw, want  bool
		}{
			{"seed-address-only", "_ = address(" + feature.seed + ")", false, false},
			{"wrapper-address-only", "_ = address(wrapper)", false, false},
			{"address-and-call", "_ = address(wrapper); wrapper()", false, true},
			{"raw-callback-publication", "_ = address(wrapper)", true, true},
			{"managed-publication", "published = wrapper", false, true},
			{"spawn", "go wrapper()", false, true},
			{"defer", "defer wrapper()", false, true},
		} {
			t.Run(feature.name+"/"+test.name, func(t *testing.T) {
				program := newEmissionTestProgram()
				pkg := program.addPackage(t, feature.path, `package example
var published func()
func `+feature.seed+`() {}
func wrapper() { `+feature.seed+`() }
func address(any) uintptr { return 0 }
func Root() { `+test.body+` }
`)
				program.ssa.Build()
				classify := func(_ *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
					return call.Common().StaticCallee() == pkg.ssa.Func("address") && argument == 0, nil
				}
				config := coro.SSAConfig{MaxPlainInstructions: -1, ClassifyStaticFunctionReferenceCallArgument: classify}
				if test.raw {
					config.ClassifyStaticFunctionReferenceCallArgument = nil
					config.ClassifyRawFunctionAddressCallArgument = classify
				}
				plan, err := coro.AnalyzeSSA(program.ssa, coro.Roots{{Function: pkg.ssa.Func("Root"), Demand: coro.AsyncDemand}}, config)
				if err != nil {
					t.Fatal(err)
				}
				values, err := DiscoverCoroProgramPredicates(plan, nil)
				if err != nil || (values&feature.bit != 0) != test.want {
					t.Fatalf("discovered predicates = %#x, %v; want enabled=%v", values, err, test.want)
				}
				capabilities, err := deriveCoroFunctionProgramCapabilities(plan, nil, nil)
				if err != nil || projectCoroProgramCapabilities(plan, capabilities)&feature.bit != values&feature.bit {
					t.Fatalf("final predicate projection disagrees with discovery: %+v, %v", capabilities, err)
				}
			})
		}
	}
}

func TestCoroGCObservationLocalExportDoesNotReviveElidedCall(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		program := newEmissionTestProgram()
		program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
		pkg := program.addPackage(t, "example.com/observation", `package observation
import _ "unsafe"
//go:linkname Enabled llgo.coroGCObservationEnabled
func Enabled() bool
//go:linkname Observe C.local_gc_observe
func Observe() bool
//export local_gc_observe
func local_gc_observe() bool {
	if !Enabled() { return true }
	return false
}
func Root() bool {
	if !Enabled() { return false }
	return Observe()
}
`)
		program.ssa.Build()
		prog := newLLSSAProg(t)
		defer prog.Dispose()
		binding := coro.ProgramPredicateRequirements{Mask: coro.ProgramPredicateMask()}
		if enabled {
			binding.Values = coro.GCObservationProgramCapability()
		}
		universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}}, EmissionUniverseOptions{
			CoroProgramPredicates: binding,
		})
		if err != nil {
			t.Fatal(err)
		}
		call := onlyLocalExportStaticCall(t, pkg.ssa.Func("Root"), pkg.ssa.Func("Observe"))
		site, frozen, err := universe.CoroCallSitePlan(call)
		if err != nil || !frozen {
			t.Fatalf("enabled=%v local export call plan: %+v, %v", enabled, site, err)
		}
		if enabled {
			if site.ElidesCall() || site.ManagedStaticTarget != pkg.ssa.Func("local_gc_observe") || site.ManagedStaticTargetCertificate == "" {
				t.Fatalf("enabled observer lost its certified managed redirect: %+v", site)
			}
		} else if site.Elision != CoroCallElidedFrontendUnevaluated || site.ManagedStaticTarget != nil || site.ManagedStaticTargetCertificate != "" {
			t.Fatalf("disabled observer retained a local-export invocation recipe: %+v", site)
		}
	}
}

func TestCoroGCObservationQueryRejectsUnfrozenOrDifferentPredicate(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
func Root() {}
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}}, EmissionUniverseOptions{
		CoroProgramPredicates: coro.ProgramPredicateRequirements{Mask: coro.ExecutionDomainProgramCapability()},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := &Compilation{EmissionUniverse: universe}
	if _, err := c.coroProgramPredicateEmissionEnabled(coro.GCObservationProgramCapability()); err == nil {
		t.Fatal("freezing a different optional service made GC observation known false")
	}
}
