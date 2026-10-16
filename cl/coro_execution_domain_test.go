//go:build !llgo

package cl

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroExecutionDomainSeedIsExact(t *testing.T) {
	for _, path := range []string{"runtime", "internal/synctest", "example.com/user"} {
		program := newEmissionTestProgram()
		pkg := program.addPackage(t, path, `package example
func synctest_run() {}
func synctest_inBubble() {}
func synctest_isInBubble() {}
func Run() {}
func inBubble() {}
func IsInBubble() {}
`)
		program.ssa.Build()
		for name, member := range pkg.ssa.Members {
			fn, ok := member.(*ssa.Function)
			if !ok {
				continue
			}
			want := path == "runtime" && (name == "synctest_run" || name == "synctest_inBubble") ||
				path == "internal/synctest" && (name == "Run" || name == "inBubble")
			if got := coroExecutionDomainSeed(fn); got != want {
				t.Fatalf("%s.%s seed = %t, want %t", path, name, got, want)
			}
		}
	}
}

func TestCoroExecutionDomainQueryRequiresFinalCapabilities(t *testing.T) {
	for _, compilation := range []*Compilation{
		nil, {},
		{FinalCoroProgramCapabilities: coro.ExecutionDomainProgramCapability()},
		{FinalCoroProgramCapabilitiesFrozen: true, FinalCoroProgramCapabilities: coro.ProgramCapabilities(0x80)},
	} {
		if _, err := compilation.coroExecutionDomainEmissionEnabled(); err == nil {
			t.Fatal("query accepted an absent, unfrozen or invalid capability set")
		}
	}
}

func TestCoroExecutionDomainDemandUsesReachableCalls(t *testing.T) {
	for _, test := range []struct {
		body string
		want bool
	}{
		{body: "_ = synctest_isInBubble()"},
		{body: "synctest_run()", want: true},
		{body: "entry := synctest_inBubble; entry()", want: true},
	} {
		ssaPkg, _, files := buildGoSSAPkg(t, `package runtime
func synctest_run() {}
func synctest_inBubble() {}
func synctest_isInBubble() bool { return false }
func Dead() { synctest_run() }
func Root() { `+test.body+` }
`)
		prog := newLLSSAProg(t)
		universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
		if err != nil {
			t.Fatal(err)
		}
		ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
		if err != nil {
			t.Fatal(err)
		}
		plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: ssaPkg.Func("Root"), Demand: coro.AsyncDemand}}, coro.SSAConfig{
			EmissionUniverse: ssaUniverse, FunctionIDs: universe.FunctionIDConfig(), MaxPlainInstructions: -1,
		})
		if err != nil {
			t.Fatal(err)
		}
		capabilities, err := deriveCoroFunctionProgramCapabilities(plan, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := capabilities[ssaPkg.Func("Root")].ExecutionDomain(); got != test.want {
			t.Fatalf("Root %q execution-domain demand = %t, want %t", test.body, got, test.want)
		}
		prog.Dispose()
	}
}

func TestCoroExecutionDomainIntrinsicIsExactInlineNoSuspend(t *testing.T) {
	testCoroProgramPredicateIntrinsicShape(t, "coroExecutionDomainEnabled")
}

func TestCoroGCObservationIntrinsicIsExactInlineNoSuspend(t *testing.T) {
	testCoroProgramPredicateIntrinsicShape(t, "coroGCObservationEnabled")
}

func testCoroProgramPredicateIntrinsicShape(t *testing.T, intrinsic string) {
	for _, declaration := range []string{"func Enabled() bool", "func Enabled() uintptr"} {
		t.Run(declaration, func(t *testing.T) {
			program := newEmissionTestProgram()
			program.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			pkg := program.addPackage(t, "example.com/domain", `package domain
import _ "unsafe"
//go:linkname Enabled llgo.`+intrinsic+`
`+declaration+`
func Use() { _ = Enabled() }
`)
			program.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
			if err != nil {
				t.Fatal(err)
			}
			call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
			semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
			if declaration == "func Enabled() uintptr" {
				if err == nil || !intrinsic || !strings.Contains(err.Error(), "func() bool") {
					t.Fatalf("wrong shape accepted: %v %t %v", semantics, intrinsic, err)
				}
				return
			}
			if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
				t.Fatalf("query semantics = %v %t %v", semantics, intrinsic, err)
			}
		})
	}
}

// This is a generated-code gate, not merely a source check: ordinary programs
// must retain neither a capability load nor the guarded accounting write.
func TestCoroExecutionDomainDisabledCodeHasNoRuntimeProbe(t *testing.T) {
	testCoroProgramPredicateDisabledCode(t, "coroExecutionDomainEnabled", coro.ExecutionDomainProgramCapability())
}

func TestCoroGCObservationDisabledCodeHasNoRuntimeProbe(t *testing.T) {
	testCoroProgramPredicateDisabledCode(t, "coroGCObservationEnabled", coro.GCObservationProgramCapability())
}

func testCoroProgramPredicateDisabledCode(t *testing.T, intrinsic string, bit coro.ProgramCapabilities) {
	for _, enabled := range []bool{false, true} {
		ssaPkg, _, files := buildGoSSAPkg(t, `package foo
import _ "unsafe"
//go:linkname Enabled llgo.`+intrinsic+`
func Enabled() bool
var Accounting uint32
func Root() bool {
	if Enabled() { Accounting = 42 }
	return Enabled()
}
`)
		prog := newLLSSAProg(t)
		var capabilities coro.ProgramCapabilities
		if enabled {
			capabilities = bit
		}
		universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}}, EmissionUniverseOptions{
			CoroProgramPredicates: coro.ProgramPredicateRequirements{Mask: bit, Values: capabilities},
		})
		if err != nil {
			t.Fatal(err)
		}
		ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
		if err != nil {
			t.Fatal(err)
		}
		plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: ssaPkg.Func("Root"), Demand: coro.AsyncDemand}}, coro.SSAConfig{
			EmissionUniverse: ssaUniverse, FunctionIDs: universe.FunctionIDConfig(), MaxPlainInstructions: -1,
			ClassifyElidedCall: func(_ *ssa.Function, site ssa.CallInstruction) (bool, error) {
				semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(site)
				return intrinsic && semantics.ElidesManagedCall(), err
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{
			Compilation: &Compilation{CoroPlan: plan, EmissionUniverse: universe,
				FinalCoroProgramCapabilitiesFrozen: true, FinalCoroProgramCapabilities: capabilities},
		})
		if err != nil {
			prog.Dispose()
			t.Fatal(err)
		}
		module := pkg.Module()
		options := llvm.NewPassBuilderOptions()
		err = module.RunPasses("function(instcombine,simplifycfg)", prog.TargetMachine(), options)
		options.Dispose()
		if err != nil {
			t.Fatal(err)
		}
		if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
			t.Fatal(err)
		}
		function := module.NamedFunction("foo.Root")
		if function.IsNil() {
			t.Fatalf("Root missing:\n%s", module.String())
		}
		ir := function.String()
		if strings.Contains(ir, "call ") || strings.Contains(ir, "load ") ||
			strings.Contains(ir, "store ") != enabled {
			t.Fatalf("enabled=%t retained unexpected runtime operations:\n%s", enabled, ir)
		}
		want := "ret i1 false"
		if enabled {
			want = "ret i1 true"
		}
		if !strings.Contains(ir, want) {
			t.Fatalf("enabled=%t missing %q:\n%s", enabled, want, ir)
		}
		module.Dispose()
		prog.Dispose()
	}
}
