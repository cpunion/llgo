//go:build !llgo

package cl

import (
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroPanicInterfacePhiUsesEvaluatedEdges(t *testing.T) {
	for _, test := range []struct{ name, body string }{
		{"dead-block", `func Root() {
	err := Stable()
	if Enabled() { err = Opaque() }
	if err != nil { panic(err) }
}`},
		{"live-predecessor-excluded-edge", `func Root() {
	err := Opaque()
	if Enabled() { goto joined }
	err = Stable()
joined:
	if err != nil { panic(err) }
}`},
	} {
		t.Run(test.name, func(t *testing.T) { testCoroPanicInterfacePhi(t, test.body) })
	}
}

func testCoroPanicInterfacePhi(t *testing.T, body string) {
	t.Helper()
	source := `package foo
import _ "unsafe"
//go:linkname Enabled llgo.coroGCObservationEnabled
func Enabled() bool
var Err any
func Stable() any { return Err }
func Opaque() any
` + body
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		for _, enabled := range []bool{false, true} {
			name := target.name + map[bool]string{false: "/dead", true: "/live"}[enabled]
			t.Run(name, func(t *testing.T) {
				pkg, _, files := buildGoSSAPkg(t, source)
				prog := newLLSSAProgForTarget(t, target.arch)
				defer prog.Dispose()
				binding := coro.ProgramPredicateRequirements{Mask: coro.GCObservationProgramCapability()}
				if enabled {
					binding.Values = binding.Mask
				}
				universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}}, EmissionUniverseOptions{
					CoroProgramPredicates: binding,
				})
				if err != nil {
					t.Fatal(err)
				}
				emission, err := coro.NewSSAEmissionUniverse(pkg.Prog, universe.Functions())
				if err != nil {
					t.Fatal(err)
				}
				ids := universe.FunctionIDConfig()
				ids.CoroABI = coro.PhysicalABIV1
				ids.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
				ids.ArchiveReady = true
				root := pkg.Func("Root")
				plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
					EmissionUniverse: emission, FunctionIDs: ids, MaxPlainInstructions: -1,
					OutcomeMode: coro.OutcomeExplicitStatus, ClassifyLocalBody: universe.CoroLocalBodyFacts,
					ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
						if function == root || function == pkg.Func("Stable") {
							return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
						}
						if function == pkg.Func("Opaque") {
							return coro.SSAFunctionPolicy{Effect: coro.NoSuspend, External: coro.ExternalKnown, OverrideExternal: true}, nil
						}
						return coro.SSAFunctionPolicy{}, nil
					},
					ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
						site, _, err := universe.CoroCallSitePlan(call)
						return site.Elision != CoroCallElidedFrontendUnevaluated && site.ElidesCall(), err
					},
					ClassifyUnevaluatedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
						site, _, err := universe.CoroCallSitePlan(call)
						return site.Elision == CoroCallElidedFrontendUnevaluated, err
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				var payload *ssa.Phi
				var panicValue ssa.Value
				for _, block := range root.Blocks {
					for _, instruction := range block.Instrs {
						switch value := instruction.(type) {
						case *ssa.Phi:
							payload = value
						case *ssa.Panic:
							panicValue = value.X
						}
					}
				}
				if payload == nil || len(payload.Edges) != 2 || panicValue == nil {
					t.Fatal("fixture lost its interface phi containing the opaque call result")
				}
				audit, err := newCoroPhysicalPureSSAAudit(universe, plan, root, "")
				if err != nil {
					t.Fatal(err)
				}
				reason := validateCoroExplicitStatusPanicInterfaceValue(audit, panicValue, coroPhysicalLoweringCapabilities{
					childAwait: true, explicitPanic: true,
				}, make(map[ssa.Value]bool))
				if enabled {
					if !strings.Contains(reason, "interface call result") {
						t.Fatalf("live opaque result escaped its lifetime gate: %q", reason)
					}
					return
				}
				if reason != "" {
					t.Fatalf("dead phi input contaminated the managed result: %s", reason)
				}
				compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
				enableCoroChildAwaitCompilation(compilation)
				compilation.PanicABI = coro.PanicExplicitStatusABIV0
				compiled, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, pkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation})
				if err != nil {
					t.Fatal(err)
				}
				module := compiled.Module()
				defer module.Dispose()
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatalf("verify dead-edge panic phi: %v\n%s", err, module.String())
				}
				rootIR := requireCoroPhysicalFunction(t, module, "foo.Root").String()
				if got, want := hasLLVMCall(rootIR, "foo.Opaque"), strings.Contains(body, "err := Opaque()"); got != want {
					t.Fatalf("selected phi edge changed an evaluated call: opaque call=%t, want %t\n%s", got, want, rootIR)
				}
				if strings.Contains(rootIR, "br i1 false,") {
					t.Fatalf("frozen false branch was retained after excluding its phi edge:\n%s", rootIR)
				}
				runCoroABITestPipeline(t, prog, module)
			})
		}
	}
}
