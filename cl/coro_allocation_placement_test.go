//go:build !llgo

package cl

import (
	"fmt"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/ssa"
	gossa "golang.org/x/tools/go/ssa"
)

func allocationPlacementPlan(t *testing.T, source string) (*coroPhysicalFunctionPlan, *gossa.Alloc, *gossa.Store, *gossa.Call) {
	t.Helper()
	pkg, _, _ := buildGoSSAPkg(t, source)
	fn := pkg.Func("Root")
	plan := &coroPhysicalFunctionPlan{
		function: fn, needsPreempt: true,
		preempt:         &coroPhysicalPreemptPlan{beforeInstructions: make(map[gossa.Instruction]struct{})},
		reachableBlocks: make(map[*gossa.BasicBlock]bool),
		instructions:    make(map[gossa.Instruction]coroPhysicalInstructionPlan),
	}
	var allocation *gossa.Alloc
	var store *gossa.Store
	var call *gossa.Call
	for _, block := range fn.Blocks {
		plan.reachableBlocks[block] = true
		for _, instruction := range block.Instrs {
			site := coroPhysicalInstructionPlan{semantic: coroSemanticInstructionPlan{evaluated: true}}
			switch instruction := instruction.(type) {
			case *gossa.Alloc:
				if allocation == nil {
					allocation = instruction
				}
			case *gossa.Store:
				if instruction.Addr == allocation {
					store = instruction
				}
			case *gossa.Call:
				if target := instruction.Common().StaticCallee(); target != nil && target.Name() == "Seed" {
					call = instruction
					site.control = coroPhysicalControlDirectAwait
					site.controlTarget = target
					site.controlTargetID = "test.Seed"
				}
			}
			plan.instructions[instruction] = site
		}
	}
	if allocation == nil || call == nil {
		t.Fatalf("incomplete allocation fixture: allocation=%v store=%v call=%v", allocation, store, call)
	}
	return plan, allocation, store, call
}

func TestCoroAllocationPlacementSourceBarriers(t *testing.T) {
	for _, test := range []struct{ name, source string }{
		{"read before store", `var saved *int; func Root() **int { var cell *int; saved = cell; cell = Seed(); return &cell }`},
		{"address escape", `var saved **int; func Root() **int { var cell *int; saved = &cell; cell = Seed(); return &cell }`},
		{"opaque call", `func opaque(); func Root() **int { var cell *int; opaque(); cell = Seed(); return &cell }`},
		{"closure", `var saved func() *int; func Root() **int { var cell *int; saved = func() *int { return cell }; cell = Seed(); return &cell }`},
		{"go", `func f() {}; func Root() **int { var cell *int; go f(); cell = Seed(); return &cell }`},
		{"defer", `func f() {}; func Root() **int { var cell *int; defer f(); cell = Seed(); return &cell }`},
		{"recover", `func Root() **int { var cell *int; defer func(){ recover() }(); cell = Seed(); return &cell }`},
		{"cross block", `func Root(b bool) **int { var cell *int; if b { cell = Seed() }; return &cell }`},
		{"loop", `func Root(n int) **int { var out **int; for i:=0; i<n; i++ { var cell *int; cell = Seed(); out = &cell }; return out }`},
		{"integer cell", `func Root() *int { var cell int; cell = *Seed(); return &cell }`},
		{"aggregate cell", `func Root() *[1]*int { var cell [1]*int; cell = [1]*int{Seed()}; return &cell }`},
		{"zero sized cell", `func Root() *struct{} { var cell struct{}; Seed(); cell = struct{}{}; return &cell }`},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, _, _, _ := allocationPlacementPlan(t, "package foo\nfunc Seed() *int { return new(int) }\n"+test.source)
			plan.freezeUnpublishedPointerAllocations()
			for instruction, site := range plan.instructions {
				if site.deferHeapAllocation || site.materializeHeapAllocation != nil {
					t.Fatalf("barrier moved allocation at %s", instruction)
				}
			}
		})
	}
}

func TestCoroAllocationPlacementMultipleResults(t *testing.T) {
	plan, _, _, _ := allocationPlacementPlan(t, `package foo
func Seed() (*int,*int) { return new(int),new(int) }
func Root() (**int,**int) { a,b := Seed(); return &a,&b }
`)
	plan.freezeUnpublishedPointerAllocations()
	deferred, materialized := 0, 0
	for instruction, site := range plan.instructions {
		if site.deferHeapAllocation {
			deferred++
		}
		if site.materializeHeapAllocation != nil {
			materialized++
			store, ok := instruction.(*gossa.Store)
			if !ok || store.Addr != site.materializeHeapAllocation || !plan.instructions[site.materializeHeapAllocation].deferHeapAllocation {
				t.Fatal("unpaired allocation placement")
			}
		}
	}
	if deferred != 2 || materialized != 2 {
		t.Fatalf("deferred=%d materialized=%d want 2/2", deferred, materialized)
	}
}

const allocationPlacementSource = `package foo
func Seed() *int { return new(int) }
func Root() **int { var cell *int; cell = Seed(); return &cell }
`

func TestCoroAllocationPlacementFrozenBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		want   bool
		mutate func(*coroPhysicalFunctionPlan, *gossa.Alloc, *gossa.Store, *gossa.Call)
	}{
		{name: "one interval", want: true},
		{name: "frozen no instruction polls", want: true, mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, _ *gossa.Store, _ *gossa.Call) {
			p.needsPreempt = false
			p.preempt = nil
		}},
		{name: "original poll retained", want: true, mutate: func(p *coroPhysicalFunctionPlan, a *gossa.Alloc, _ *gossa.Store, _ *gossa.Call) {
			p.preempt.beforeInstructions[a] = struct{}{}
		}},
		{name: "intervening poll", mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, _ *gossa.Store, c *gossa.Call) {
			p.preempt.beforeInstructions[c] = struct{}{}
		}},
		{name: "destination poll", mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, s *gossa.Store, _ *gossa.Call) {
			p.preempt.beforeInstructions[s] = struct{}{}
		}},
		{name: "no preempt proof", mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, _ *gossa.Store, _ *gossa.Call) { p.preempt = nil }},
		{name: "nonpreemptible", mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, _ *gossa.Store, _ *gossa.Call) {
			p.needsPreempt = false
		}},
		{name: "critical", mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, _ *gossa.Store, _ *gossa.Call) {
			p.critical = &coroCriticalProof{}
		}},
		{name: "static outcome", mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, _ *gossa.Store, _ *gossa.Call) {
			p.staticOutcome = true
		}},
		{name: "atomic proof", mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, _ *gossa.Store, _ *gossa.Call) {
			p.atomicCostProof = coro.AtomicCostDAG
		}},
		{name: "atomic certificate", mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, _ *gossa.Store, _ *gossa.Call) {
			p.atomicCertificate = "existing"
		}},
		{name: "stack allocation", mutate: func(_ *coroPhysicalFunctionPlan, a *gossa.Alloc, _ *gossa.Store, _ *gossa.Call) { a.Heap = false }},
		{name: "borrowed allocation", mutate: func(p *coroPhysicalFunctionPlan, a *gossa.Alloc, _ *gossa.Store, _ *gossa.Call) {
			site := p.instructions[a]
			site.recipe = coroPhysicalInstructionBorrowedAllocation
			p.instructions[a] = site
		}},
		{name: "unevaluated", mutate: func(p *coroPhysicalFunctionPlan, a *gossa.Alloc, _ *gossa.Store, _ *gossa.Call) {
			site := p.instructions[a]
			site.semantic.evaluated = false
			p.instructions[a] = site
		}},
		{name: "unreachable", mutate: func(p *coroPhysicalFunctionPlan, a *gossa.Alloc, _ *gossa.Store, _ *gossa.Call) {
			p.reachableBlocks[a.Block()] = false
		}},
		{name: "raw call", mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, _ *gossa.Store, c *gossa.Call) {
			site := p.instructions[c]
			site.control = coroPhysicalControlRawPlainCall
			p.instructions[c] = site
		}},
		{name: "dynamic call", mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, _ *gossa.Store, c *gossa.Call) {
			site := p.instructions[c]
			site.control = coroPhysicalControlDispatchAwait
			p.instructions[c] = site
		}},
		{name: "host operation", mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, _ *gossa.Store, c *gossa.Call) {
			site := p.instructions[c]
			site.operation = coroPhysicalOperationHostCall
			p.instructions[c] = site
		}},
		{name: "missing target identity", mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, _ *gossa.Store, c *gossa.Call) {
			site := p.instructions[c]
			site.controlTargetID = ""
			p.instructions[c] = site
		}},
		{name: "faulting store", mutate: func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, s *gossa.Store, _ *gossa.Call) {
			site := p.instructions[s]
			site.nilGuard = true
			p.instructions[s] = site
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, allocation, store, call := allocationPlacementPlan(t, allocationPlacementSource)
			if test.mutate != nil {
				test.mutate(plan, allocation, store, call)
			}
			polls := 0
			if plan.preempt != nil {
				polls = len(plan.preempt.beforeInstructions)
			}
			plan.freezeUnpublishedPointerAllocations()
			if got := plan.instructions[allocation].deferHeapAllocation; got != test.want {
				t.Fatalf("deferred=%t want %t", got, test.want)
			}
			if got := plan.instructions[store].materializeHeapAllocation; (got == allocation) != test.want {
				t.Fatalf("materializer=%v want allocation=%t", got, test.want)
			}
			if plan.preempt != nil && len(plan.preempt.beforeInstructions) != polls {
				t.Fatal("placement changed frozen preemption sites")
			}
		})
	}
}

func TestCoroAllocationPlacementInstructionBudget(t *testing.T) {
	for _, calls := range []int{coroPreemptInstructionBudget - 3, coroPreemptInstructionBudget - 2, coroPreemptInstructionBudget - 1} {
		source := "package foo\nfunc Seed() *int { return new(int) }\nfunc Root() **int { var cell *int;\n" +
			strings.Repeat("Seed()\n", calls) + "cell = Seed(); return &cell }"
		plan, allocation, store, _ := allocationPlacementPlan(t, source)
		preempt, err := planCoroPhysicalPreemption(&coroPhysicalPureSSAAudit{fn: plan.function}, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		plan.preempt = preempt
		plan.freezeUnpublishedPointerAllocations()
		want := calls == coroPreemptInstructionBudget-3
		if plan.instructions[allocation].deferHeapAllocation != want {
			t.Fatalf("%d calls: deferred=%t want %t", calls, !want, want)
		}
		if calls == coroPreemptInstructionBudget-2 && !preempt.pollsBefore(store) {
			t.Fatal("boundary case did not put the poll at the destination Store")
		}
	}
}

func TestCoroAllocationPlacementPairValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*coroPhysicalFunctionPlan, *gossa.Alloc, *gossa.Store)
	}{
		{"orphan allocation", func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, s *gossa.Store) {
			site := p.instructions[s]
			site.materializeHeapAllocation = nil
			p.instructions[s] = site
		}},
		{"orphan store", func(p *coroPhysicalFunctionPlan, a *gossa.Alloc, _ *gossa.Store) {
			site := p.instructions[a]
			site.deferHeapAllocation = false
			p.instructions[a] = site
		}},
		{"wrong source kind", func(p *coroPhysicalFunctionPlan, _ *gossa.Alloc, s *gossa.Store) {
			site := p.instructions[s]
			site.deferHeapAllocation = true
			p.instructions[s] = site
		}},
		{"duplicate store", func(p *coroPhysicalFunctionPlan, a *gossa.Alloc, s *gossa.Store) {
			for instruction, site := range p.instructions {
				if other, ok := instruction.(*gossa.Store); ok && other != s && other.Addr == a {
					site.materializeHeapAllocation = a
					p.instructions[other] = site
					return
				}
			}
			panic("missing second store")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, allocation, _, _ := allocationPlacementPlan(t, `package foo
func Seed() *int { return new(int) }
func Root() **int { var cell *int; cell = Seed(); cell = nil; return &cell }
`)
			plan.freezeUnpublishedPointerAllocations()
			if err := plan.validateAllocationPlacements(); err != nil {
				t.Fatal(err)
			}
			var store *gossa.Store
			for instruction, site := range plan.instructions {
				if site.materializeHeapAllocation == allocation {
					store = instruction.(*gossa.Store)
				}
			}
			if store == nil {
				t.Fatal("missing valid pair")
			}
			test.mutate(plan, allocation, store)
			if err := plan.validateAllocationPlacements(); err == nil {
				t.Fatal("invalid pair accepted")
			}
		})
	}
}

func TestCoroAwaitPointerResultRetirement(t *testing.T) {
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg := prog.NewPackage("retire", "test/retire")
	module := pkg.Module()
	defer module.Dispose()
	ctx := &context{prog: prog}
	pointer := types.NewPointer(types.Typ[types.Int])
	aggregate := types.NewStruct([]*types.Var{types.NewField(0, nil, "P", pointer, false)}, nil)
	for _, test := range []struct {
		name   string
		fields []types.Type
		clears int
	}{
		{"pointer", []types.Type{pointer}, 1},
		{"unsafe", []types.Type{types.Typ[types.UnsafePointer]}, 1},
		{"tuple", []types.Type{pointer, types.Typ[types.Int], pointer}, 2},
		{"aggregate", []types.Type{aggregate, types.NewArray(pointer, 2), types.NewSlice(pointer), types.Typ[types.String]}, 0},
		{"integer", []types.Type{types.Typ[types.Int]}, 0},
		{"empty", nil, 0},
	} {
		fields := make([]*types.Var, len(test.fields))
		results := make([]*types.Var, len(test.fields))
		for i, typ := range test.fields {
			fields[i] = types.NewField(0, nil, fmt.Sprintf("field%d", i), typ, false)
			results[i] = types.NewVar(0, nil, "", typ)
		}
		fn := pkg.NewFunc(test.name, ssa.NoArgsNoRet, ssa.InGo)
		b := fn.MakeBody(1)
		slot := b.AllocaT(prog.Type(types.NewStruct(fields, nil), ssa.InGo))
		ctx.loadCoroAwaitResult(b, slot, types.NewTuple(results...))
		b.Return()
		b.EndBuild()
		b.Dispose()
		body := module.NamedFunction(test.name).String()
		if got := strings.Count(body, "store volatile ptr null"); got != test.clears {
			t.Fatalf("%s clears=%d want %d:\n%s", test.name, got, test.clears, body)
		}
		if test.clears != 0 && strings.LastIndex(body, "load ") > strings.Index(body, "store volatile") {
			t.Fatalf("%s cleared before all result loads:\n%s", test.name, body)
		}
	}
}
