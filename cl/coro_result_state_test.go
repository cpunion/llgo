//go:build !llgo

package cl

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestCoroResultStateRequiresOneOwnedBindingAndExactDrop(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	const fixture = `package foo
import "unsafe"
type Result = struct { R0 int }
type Context struct { Token int }
func initialize(out *Result) { out.R0 = -1 }
func drop(out *Result, _ unsafe.Pointer, token int) {
 context := (*Context)(load(out))
 if context != nil { out.R0 = context.Token }
}
//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*Result), func(*Result,unsafe.Pointer,int)) *Result
//go:linkname bind llgo.coroBindResultState
func bind(*Result,unsafe.Pointer)
//go:linkname load llgo.coroLoadResultState
func load(*Result) unsafe.Pointer
func Acquire(token int) int {
 out := borrow(initialize,drop)
 context := &Context{Token:token}
 bind(out,unsafe.Pointer(context))
 out.R0 = token
 return out.R0
}
func Root() { Acquire(17) }
func Forward(token int) int { return Acquire(token) }
`
	for _, variant := range []struct{ name, old, replacement, failure string }{
		{name: "owned"},
		{name: "output-retained-as-state", old: "bind(out,unsafe.Pointer(context))", replacement: "_ = context; bind(out,unsafe.Pointer(out))", failure: "own escaping managed record"},
		{name: "second-binding", old: "bind(out,unsafe.Pointer(context))", replacement: "bind(out,unsafe.Pointer(context)); bind(out,unsafe.Pointer(context))", failure: "outside its exact owned borrow"},
		{name: "foreign-output", old: "bind(out,unsafe.Pointer(context))", replacement: "bind(new(Result),unsafe.Pointer(context))", failure: "outside its exact owned borrow"},
		{name: "producer-load", old: "out.R0 = token", replacement: "_ = load(out); out.R0 = token", failure: "outside its exact owned borrow"},
		{name: "foreign-drop-output", old: "load(out)", replacement: "load(new(Result))", failure: "outside its exact owned borrow"},
		{name: "incomplete-state", old: "out.R0 = context.Token", replacement: "out.R0 = token", failure: "state-bound drop must consume only"},
		{name: "prebinding-output", old: "bind(out,unsafe.Pointer(context))", replacement: "out.R0=token; bind(out,unsafe.Pointer(context))", failure: "before any use of its borrowed output"},
		{name: "prebinding-call", old: "bind(out,unsafe.Pointer(context))", replacement: "initialize(new(Result)); bind(out,unsafe.Pointer(context))", failure: "before source calls"},
		{name: "nil-state", old: "bind(out,unsafe.Pointer(context))", replacement: "_ = context; bind(out,nil)", failure: "own escaping managed record"},
		{name: "ordinary-drop-call", old: "func Root() { Acquire(17) }", replacement: "func Root() { Acquire(17); drop(new(Result),nil,17) }", failure: "cannot be invoked or published"},
		{name: "published-drop", old: "func Root() { Acquire(17) }", replacement: "var Published = drop; func Root() { Acquire(17); Published(new(Result),nil,17) }", failure: "cannot be invoked or published"},
		{name: "exported-drop", failure: "outside its exact owned borrow or drop helper"},
	} {
		t.Run(variant.name, func(t *testing.T) {
			source := newEmissionTestProgram()
			source.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			runtimePkg := source.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocZ(size uintptr) unsafe.Pointer { return nil }
func AllocU(size uintptr) unsafe.Pointer { return nil }
`)
			text := fixture
			if variant.old != "" {
				text = strings.Replace(text, variant.old, variant.replacement, 1)
			}
			if variant.name == "exported-drop" {
				text = strings.ReplaceAll(text, "drop", "Drop")
			}
			foo := source.addPackage(t, "foo", text)
			source.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			u, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{
				{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
				{SSA: foo.ssa, Files: []*ast.File{foo.file}},
			})
			if variant.failure != "" {
				if err == nil || !strings.Contains(err.Error(), variant.failure) {
					t.Fatalf("unowned state access: %v, want %s", err, variant.failure)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			counts := 0
			for call, frozen := range u.coroProgramIR.callPlans {
				if !frozen.resultState.owned {
					continue
				}
				counts++
				direct := call.(*ssa.Call)
				if !u.coroProgramIR.borrowedResultStateArgument(direct, 0) || u.coroProgramIR.borrowedResultStateArgument(direct, 1) {
					t.Fatal("state access must borrow only its output, never retain the state as a local allocation")
				}
			}
			if counts != 2 {
				t.Fatalf("owned state accesses=%d, want bind and load", counts)
			}
			primary := u.coroProgramIR.deliveryProducers[foo.ssa.Func("Acquire")]
			forward := u.coroProgramIR.deliveryProducers[foo.ssa.Func("Forward")]
			if primary.stateBind == nil || forward.forward == nil || !forward.requiresState() || forward.stateBind != primary.stateBind {
				t.Fatal("exact source forwarding must inherit its deepest complete state binding")
			}
			allocations := 0
			for _, block := range foo.ssa.Func("Acquire").Blocks {
				for _, instruction := range block.Instrs {
					allocation, ok := instruction.(*ssa.Alloc)
					if !ok {
						continue
					}
					allocations++
					if _, borrowed := proveCoroBorrowedAllocation(u, allocation); borrowed {
						t.Fatal("retained invocation state was promoted into a producer-local frame")
					}
				}
			}
			if allocations != 1 {
				t.Fatalf("state allocations=%d, want one real retained context", allocations)
			}
		})
	}
}
