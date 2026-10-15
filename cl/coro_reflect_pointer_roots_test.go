//go:build !llgo

package cl

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroFrameReflectPointerImmediateReconstruction(t *testing.T) {
	for _, test := range []struct {
		name, declarations, body string
		want                     bool
	}{
		{"direct", "", "return unsafe.Pointer(value.Pointer())", true},
		{"method-expression", "", "return unsafe.Pointer(reflect.Value.Pointer(value))", true},
		{"unsafeaddr", "", "return unsafe.Pointer(value.UnsafeAddr())", true},
		{"bound", "", "fn := value.Pointer; return unsafe.Pointer(fn())", true},
		{"interface", "type Pointerer interface { Pointer() uintptr }", "var object Pointerer = value; return unsafe.Pointer(object.Pointer())", true},
		{"returned-bound", "func Bound(v reflect.Value) func() uintptr { return v.Pointer }", "return unsafe.Pointer(Bound(value)())", true},
		{"returned-interface", "type Pointerer interface { Pointer() uintptr }; func Interface(v reflect.Value) Pointerer { return v }", "return unsafe.Pointer(Interface(value).Pointer())", true},
		{"pointer-across-await", "var ready chan struct{}", "pointer := unsafe.Pointer(value.Pointer()); <-ready; return pointer", true},
		{"arithmetic", "", "return unsafe.Pointer(value.Pointer() + 1)", false},
		{"intervening-call", "func Other() {}", "word := value.Pointer(); Other(); return unsafe.Pointer(word)", false},
		{"intervening-await", "var ready chan struct{}", "word := value.Pointer(); <-ready; return unsafe.Pointer(word)", false},
		{"lookalike", "type Value struct{}; func (Value) Pointer() uintptr { return 123 }", "return unsafe.Pointer((Value{}).Pointer())", false},
		{"unknown-function", "var unknown func() uintptr", "return unsafe.Pointer(unknown())", false},
		{"mixed-dynamic", "func Other() uintptr { return 123 }; var condition bool", "fn := value.Pointer; if condition { fn = Other }; return unsafe.Pointer(fn())", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "package foo\nimport (\"reflect\"; \"unsafe\")\n" + test.declarations +
				"\nfunc Root(value reflect.Value) unsafe.Pointer { " + test.body + " }\n"
			prog, ssaPkg, universe, root, _, _ := prepareCoroFrameRootAudit(t, source, "Root", EmissionUniverseOptions{})
			defer prog.Dispose()
			ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse: ssaUniverse, FunctionIDs: universe.FunctionIDConfig(), DynamicResolution: coro.DynamicCHAClosed,
				MaxPlainInstructions: -1,
			})
			if err != nil {
				t.Fatal(err)
			}
			audit, err := newCoroPhysicalPureSSAAudit(universe, plan, root, CoroFrameRetentionParkABIV2)
			if err != nil {
				t.Fatal(err)
			}
			proof := audit.currentFrameRetentionProof()
			conversion := onlyReflectHeaderReconstruction(t, root)
			if got := proof.provesTraceableUintptr(conversion.X); got != test.want {
				var dump bytes.Buffer
				ssa.WriteFunction(&dump, root)
				t.Fatalf("traceable reflect result = %t, want %t\n%s", got, test.want, dump.String())
			}
			if test.want {
				roots := proof.uintptrValues[conversion.X].roots
				if len(roots) != 1 || proof.exactRoots[roots[0]].kind == coroFrameRetentionRootInvalid {
					t.Fatalf("reflection address result lacks its exact typed owner: %v", roots)
				}
			}
		})
	}
}

func TestCoroFrameGlobalReflectHeaderImmediateReconstruction(t *testing.T) {
	for _, test := range []struct {
		name, body string
		want       bool
	}{
		{"slice", "h := (*reflect.SliceHeader)(unsafe.Pointer(&values)); return unsafe.Pointer(h.Data)", true},
		{"string", "h := (*reflect.StringHeader)(unsafe.Pointer(&text)); return unsafe.Pointer(h.Data)", true},
		{"pointer-across-await", "h := (*reflect.SliceHeader)(unsafe.Pointer(&values)); pointer := unsafe.Pointer(h.Data); <-ready; return pointer", true},
		{"copied-header", "h := *(*reflect.SliceHeader)(unsafe.Pointer(&values)); return unsafe.Pointer(h.Data)", false},
		{"mutated-data", "h := (*reflect.SliceHeader)(unsafe.Pointer(&values)); h.Data = raw; return unsafe.Pointer(h.Data)", false},
		{"escaped-header", "h := (*reflect.SliceHeader)(unsafe.Pointer(&values)); escaped = h; return unsafe.Pointer(h.Data)", false},
		{"intervening-call", "h := (*reflect.SliceHeader)(unsafe.Pointer(&values)); word := h.Data; Other(); return unsafe.Pointer(word)", false},
		{"intervening-await", "h := (*reflect.SliceHeader)(unsafe.Pointer(&values)); word := h.Data; <-ready; return unsafe.Pointer(word)", false},
		{"mutated-source", "h := (*reflect.SliceHeader)(unsafe.Pointer(&values)); word := h.Data; values = nil; return unsafe.Pointer(word)", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "package foo\nimport (\"reflect\"; \"unsafe\")\nvar values []byte; var text string; var ready chan struct{}; var escaped *reflect.SliceHeader\nfunc Other() {}\nfunc Root(raw uintptr) unsafe.Pointer { " + test.body + " }"
			prog, _, _, root, _, proof := prepareCoroFrameRootAudit(t, source, "Root", EmissionUniverseOptions{})
			defer prog.Dispose()
			conversion := onlyReflectHeaderReconstruction(t, root)
			if got := proof.provesTraceableUintptr(conversion.X); got != test.want {
				t.Fatalf("traceable global header = %t, want %t", got, test.want)
			}
			if test.want && len(proof.uintptrValues[conversion.X].roots) != 0 {
				t.Fatal("mutable global header address is not a backing-array keeper")
			}
		})
	}
}

func TestCoroFrameGlobalReflectHeaderWordAcrossInsertedPoll(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name, word string
		target     *llssa.Target
	}{
		{"native64", "i64", nil},
		{"wasm32", "i32", &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) { testCoroGlobalHeaderInsertedPoll(t, target.target, target.word) })
	}
}

func testCoroGlobalHeaderInsertedPoll(t *testing.T, target *llssa.Target, word string) {
	t.Helper()
	// Sixty padding instructions followed by four address/header operations
	// place the conversion exactly at the instruction-budget preemption cut.
	// The source adjacency proof must not pretend that this is a no-poll zone.
	source := "package foo\nimport (\"reflect\"; \"unsafe\")\nvar values []byte; var sink uintptr\nfunc Root(value uintptr) unsafe.Pointer {\n" +
		strings.Repeat("value++\n", coroPreemptInstructionBudget-5) +
		"sink = value\nh := (*reflect.SliceHeader)(unsafe.Pointer(&values)); return unsafe.Pointer(h.Data)\n}"
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	ids := universe.FunctionIDConfig()
	ids.CoroABI = coro.PhysicalABIV1
	ids.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	ids.ArchiveReady = true
	root := ssaPkg.Func("Root")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse: ssaUniverse, FunctionIDs: ids, MaxPlainInstructions: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversion := onlyReflectHeaderReconstruction(t, root)
	audit, err := newCoroPhysicalPureSSAAudit(universe, plan, root, CoroFrameRetentionParkABIV2)
	if err != nil {
		t.Fatal(err)
	}
	preempt, err := planCoroPhysicalPreemption(audit, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !preempt.pollsBefore(conversion) {
		var dump bytes.Buffer
		ssa.WriteFunction(&dump, root)
		t.Fatalf("fixture did not place the conversion after an inserted poll\n%s", dump.String())
	}
	proof := audit.currentFrameRetentionProof()
	if !proof.provesTraceableUintptr(conversion.X) || len(proof.uintptrValues[conversion.X].roots) != 0 ||
		proof.exactRootCapabilityProfile() != coroFrameRetentionExactRootProfileV2 {
		t.Fatal("global observation must use the existing conservative word profile, never the global cell as a keeper")
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation})
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	runCoroABITestPipeline(t, prog, module)
	resume := module.NamedFunction("foo.Root$coro.resume")
	if resume.IsNil() {
		t.Fatal("missing split resume entry")
	}
	ir := resume.String()
	conversionOperand := regexp.MustCompile(`inttoptr ` + word + ` (%[^ ]+) to ptr`).FindStringSubmatch(ir)
	if len(conversionOperand) != 2 || !strings.Contains(conversionOperand[1], ".reload") ||
		!strings.Contains(ir, conversionOperand[1]+" = load "+word+", ptr ") {
		t.Fatalf("split resume must reconstruct the spilled address word\n%s", ir)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
}
