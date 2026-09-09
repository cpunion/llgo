//go:build !llgo

package cl

import (
	"go/types"
	"strings"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestPruneStableWasmRootAliases(t *testing.T) {
	for _, tc := range []struct {
		name, source    string
		rootBase, prune bool
	}{
		{"parameter", `type item struct { n int }; func classify(p *[4]item) *int { return &p[3].n }`, true, true},
		{"missing base", `func classify(p *[4]int) *int { return &p[3] }`, false, false},
		{"slice", `func classify(p []int) *int { return &p[3] }`, true, false},
		{"loaded pointer", `type item struct { next *item; n int }; func classify(p *item) *int { return &p.next.n }`, true, false},
		{"distinct base", `func classify(p, q *[4]int) *int { return &q[3] }`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := buildGCRootSSAFunction(t, "package p\n"+tc.source)
			var result ssa.Value
			for _, block := range fn.Blocks {
				for _, instr := range block.Instrs {
					if ret, ok := instr.(*ssa.Return); ok {
						result = ret.Results[0]
					}
				}
			}
			roots := map[ssa.Value]struct{}{result: {}}
			if tc.rootBase {
				roots[fn.Params[0]] = struct{}{}
			}
			pruneStableWasmRootAliases(roots)
			if _, remains := roots[result]; remains == tc.prune {
				t.Fatalf("interior root remains=%v, want pruned=%v", remains, tc.prune)
			}
			if tc.rootBase {
				if _, remains := roots[fn.Params[0]]; !remains {
					t.Fatal("removed the independently published base root")
				}
			}
		})
	}
	pruneStableWasmRootAliases(nil)
}

func TestPruneStableWasmRootAllocationGuards(t *testing.T) {
	fn := buildGCRootSSAFunction(t, "package p\nfunc classify() *int { return &new([2]int)[1] }")
	var alloc *ssa.Alloc
	var address ssa.Value
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			switch instr := instr.(type) {
			case *ssa.Alloc:
				alloc = instr
			case *ssa.IndexAddr:
				address = instr
			}
		}
	}
	if alloc == nil || address == nil || !alloc.Heap {
		t.Fatal("missing escaping heap allocation and interior pointer")
	}
	for _, tc := range []struct {
		comment     string
		heap, prune bool
	}{{"new", true, true}, {"varargs", true, false}, {"local", false, false}} {
		alloc.Comment, alloc.Heap = tc.comment, tc.heap
		roots := map[ssa.Value]struct{}{alloc: {}, address: {}}
		pruneStableWasmRootAliases(roots)
		if _, remains := roots[address]; remains == tc.prune {
			t.Fatalf("comment=%s heap=%v pruned=%v, want %v", tc.comment, tc.heap, !remains, tc.prune)
		}
	}
	// Check general cycles, including irreducible graphs, rather than only
	// assuming that a back edge's target dominates its predecessor.
	entry, left, right, exit := new(ssa.BasicBlock), new(ssa.BasicBlock), new(ssa.BasicBlock), new(ssa.BasicBlock)
	entry.Succs = []*ssa.BasicBlock{left, right}
	left.Succs = []*ssa.BasicBlock{right}
	right.Succs = []*ssa.BasicBlock{left, exit}
	if blockCanRepeat(entry) || !blockCanRepeat(left) || !blockCanRepeat(right) || blockCanRepeat(exit) || !blockCanRepeat(nil) {
		t.Fatal("incorrect classification of repeated allocation blocks")
	}
	loop := buildGCRootSSAFunction(t, `package p
func consume(*[2]int, *int)
func classify(n int) { for i := 0; i < n; i++ { p := new([2]int); consume(p, &p[1]) } }`)
	for _, block := range loop.Blocks {
		for _, instr := range block.Instrs {
			if pointer, ok := instr.(*ssa.IndexAddr); ok {
				base := wasmRootAliasBase(pointer)
				roots := map[ssa.Value]struct{}{base: {}, pointer: {}}
				pruneStableWasmRootAliases(roots)
				if len(roots) != 2 {
					t.Fatal("pruned an allocation whose root slot is reused in a loop")
				}
				return
			}
		}
	}
	t.Fatal("missing loop allocation")
}

func TestCompileStableWasmRootAliases(t *testing.T) {
	const source = `package alias
func use(*[4]int, *int, *int, *int, *int)
func keep(p *[4]int) *int {
  a, b, c, d := &p[0], &p[1], &p[2], &p[3]
  use(p, a, b, c, d)
  return a
}`
	for _, target := range []llssa.Target{
		{GOOS: "js", GOARCH: "wasm"},
		{GOOS: "js", GOARCH: "wasm", Target: "emscripten-memory64", LLVMTarget: "wasm64-unknown-emscripten"},
		{GOOS: "linux", GOARCH: "amd64"},
	} {
		t.Run(target.GOARCH+"/"+target.Target, func(t *testing.T) {
			ssaPkg, files := buildCallerFrameSSAPackage(t, "example.com/alias", source)
			prog := newLLSSAProgForTarget(t, &target)
			defer prog.Dispose()
			if target.Target == "emscripten-memory64" {
				prog.TypeSizes(types.SizesFor("gc", "amd64"))
			}
			prog.EnableGCRoots(true)
			pkg, err := NewPackage(prog, ssaPkg, files)
			if err != nil {
				t.Fatal(err)
			}
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			body := pkg.Module().NamedFunction("example.com/alias.keep").String()
			want := "[1 x ptr]"
			if target.GOARCH != "wasm" {
				want = "[5 x ptr]"
			}
			if !strings.Contains(body, want) {
				t.Fatalf("missing %s root frame:\n%s", want, body)
			}
		})
	}
}
