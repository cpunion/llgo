//go:build !llgo
// +build !llgo

package cl

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/gogen/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func buildSSAPackageWithPath(t *testing.T, pkgPath, pkgName, src string) *ssa.Package {
	t.Helper()
	ssapkg, _ := buildSSAPackageWithPathAndFiles(t, pkgPath, pkgName, src)
	return ssapkg
}

func buildSSAPackageWithPathAndFiles(t *testing.T, pkgPath, pkgName, src string) (*ssa.Package, []*ast.File) {
	t.Helper()
	return buildSSAPackageWithPathAndFilesMode(t, pkgPath, pkgName, src, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
}

func buildSSAPackageWithPathAndFilesMode(t *testing.T, pkgPath, pkgName, src string, mode ssa.BuilderMode) (*ssa.Package, []*ast.File) {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{f}
	pkg := types.NewPackage(pkgPath, pkgName)
	imp := packages.NewImporter(fset)
	ssapkg, _, err := ssautil.BuildPackage(&types.Config{Importer: imp}, fset, pkg, files, mode)
	if err != nil {
		t.Fatal(err)
	}
	return ssapkg, files
}

func TestConservativeGCPointerTypeAnalysis(t *testing.T) {
	if hasConservativeGCPointers(nil, map[types.Type]bool{}) {
		t.Fatal("nil type should not report conservative pointers")
	}
	if hasConservativeGCPointers(types.Typ[types.Int], map[types.Type]bool{}) {
		t.Fatal("int should not report conservative pointers")
	}
	if hasConservativeGCPointers(types.Typ[types.String], map[types.Type]bool{types.Typ[types.String]: true}) {
		t.Fatal("seen type should short-circuit")
	}
	for _, typ := range []types.Type{
		types.Typ[types.String],
		types.Typ[types.UnsafePointer],
		types.NewPointer(types.Typ[types.Int]),
		types.NewSlice(types.Typ[types.Int]),
		types.NewMap(types.Typ[types.String], types.Typ[types.Int]),
		types.NewChan(types.SendRecv, types.Typ[types.Int]),
		types.NewSignatureType(nil, nil, nil, nil, nil, false),
		types.NewInterfaceType(nil, nil),
		types.NewArray(types.NewPointer(types.Typ[types.Int]), 2),
		types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "p", types.NewPointer(types.Typ[types.Int]), false)}, nil),
	} {
		if !hasConservativeGCPointers(typ, map[types.Type]bool{}) {
			t.Fatalf("%v should report conservative pointers", typ)
		}
	}
	if hasConservativeGCPointers(types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "i", types.Typ[types.Int], false),
	}, nil), map[types.Type]bool{}) {
		t.Fatal("struct without pointer fields should not report conservative pointers")
	}
	if hasConservativeGCPointers(types.NewArray(types.Typ[types.Int], 2), map[types.Type]bool{}) {
		t.Fatal("array without pointer elements should not report conservative pointers")
	}
	if !hasConservativeGCPointers(types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "i", types.Typ[types.Int], false),
		types.NewField(token.NoPos, nil, "p", types.NewPointer(types.Typ[types.Int]), false),
	}, nil), map[types.Type]bool{}) {
		t.Fatal("struct with later pointer field should report conservative pointers")
	}
}

func TestShouldClearAlloc(t *testing.T) {
	ssapkg := buildSSAPackageWithPath(t, "example.com/live", "live", `package live

type Box struct{ p *int }

var Sink any

func allocs(p *int) {
	var box Box
	var i int
	box.p = p
	Sink = &box
	Sink = &i
}
	`)
	fn := ssapkg.Func("allocs")
	ctx := &context{}
	if ctx.shouldClearAlloc(nil) {
		t.Fatal("nil alloc should not be cleared")
	}

	var boxAlloc, intAlloc *ssa.Alloc
	for _, local := range functionAllocs(fn) {
		ptr := local.Type().Underlying().(*types.Pointer)
		if _, ok := ptr.Elem().Underlying().(*types.Struct); ok {
			boxAlloc = local
		}
		if ptr.Elem() == types.Typ[types.Int] {
			intAlloc = local
		}
	}
	if boxAlloc == nil || intAlloc == nil {
		var dump strings.Builder
		fn.WriteTo(&dump)
		t.Fatalf("missing expected allocs: %v\n%s", functionAllocs(fn), dump.String())
	}
	if !boxAlloc.Heap {
		t.Fatal("address-taken box should be marked as a heap allocation")
	}
	if ctx.shouldClearAlloc(boxAlloc) {
		t.Fatal("heap allocation must not be cleared")
	}
	if ctx.shouldClearAlloc(intAlloc) {
		t.Fatal("int alloc should not be cleared")
	}

	boxAlloc.Heap = false
	if !ctx.shouldClearAlloc(boxAlloc) {
		t.Fatal("non-heap stack slot containing a pointer should be cleared")
	}
	boxAlloc.Comment = "varargs"
	if ctx.shouldClearAlloc(boxAlloc) {
		t.Fatal("varargs alloc should not be cleared")
	}
	boxAlloc.Comment = "makeslice"
	if ctx.shouldClearAlloc(boxAlloc) {
		t.Fatal("synthetic makeslice alloc should not be cleared")
	}
}

func functionAllocs(fn *ssa.Function) []*ssa.Alloc {
	seen := make(map[*ssa.Alloc]bool)
	var allocs []*ssa.Alloc
	add := func(alloc *ssa.Alloc) {
		if alloc != nil && !seen[alloc] {
			seen[alloc] = true
			allocs = append(allocs, alloc)
		}
	}
	for _, local := range fn.Locals {
		add(local)
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if alloc, ok := instr.(*ssa.Alloc); ok {
				add(alloc)
			}
		}
	}
	return allocs
}

func TestRuntimeSetFinalizerDetection(t *testing.T) {
	ssapkg := buildSSAPackageWithPath(t, "github.com/goplus/llgo/runtime/livetest", "livetest", `package livetest

import rt "runtime"

func direct(p *int) {
	rt.SetFinalizer(p, func(*int) {})
}

func deferred(p *int) {
	defer rt.SetFinalizer(p, nil)
}

func goroutine(p *int) {
	go rt.SetFinalizer(p, nil)
}

func nested(p *int) {
	func() {
		rt.SetFinalizer(p, nil)
	}()
}

func none(p *int) {}
`)
	ctx := &context{}
	if ctx.enableConservativeLivenessClears(nil) {
		t.Fatal("nil function should not enable conservative clears")
	}
	for _, name := range []string{"direct", "deferred", "goroutine", "nested"} {
		if !ctx.functionUsesRuntimeSetFinalizer(ssapkg.Func(name), map[*ssa.Function]bool{}) {
			t.Fatalf("%s should be detected as SetFinalizer user", name)
		}
	}
	if ctx.functionUsesRuntimeSetFinalizer(nil, map[*ssa.Function]bool{}) {
		t.Fatal("nil function should not use SetFinalizer")
	}
	direct := ssapkg.Func("direct")
	if ctx.functionUsesRuntimeSetFinalizer(direct, map[*ssa.Function]bool{direct: true}) {
		t.Fatal("seen function should short-circuit")
	}
	if ctx.functionUsesRuntimeSetFinalizer(ssapkg.Func("none"), map[*ssa.Function]bool{}) {
		t.Fatal("none should not use SetFinalizer")
	}
	if ctx.packageUsesRuntimeSetFinalizer(&ssa.Package{Members: map[string]ssa.Member{"none": ssapkg.Func("none")}}) {
		t.Fatal("package without SetFinalizer should not report use")
	}
	if !ctx.packageUsesRuntimeSetFinalizer(ssapkg) {
		t.Fatal("package should report SetFinalizer use")
	}
	if !ctx.enableConservativeLivenessClears(direct) {
		t.Error("module package with SetFinalizer should enable conservative clears")
	}
	ssapkg.Pkg = types.NewPackage("command-line-arguments", "main")
	if !ctx.enableConservativeLivenessClears(direct) {
		t.Fatal("command-line-arguments package with SetFinalizer should enable conservative clears")
	}

	methodPkg := buildSSAPackageWithPath(t, "github.com/goplus/llgo/runtime/methodlive", "methodlive", `package methodlive

import rt "runtime"

	type setter struct{}

func (setter) install(p *int) {
	rt.SetFinalizer(p, func(*int) {})
}
`)
	if !ctx.packageUsesRuntimeSetFinalizer(methodPkg) {
		t.Error("method-only SetFinalizer use should be detected")
	}

	genericMethodPkg := buildSSAPackageWithPath(t, "github.com/goplus/llgo/runtime/genericmethodlive", "genericmethodlive", `package genericmethodlive

import rt "runtime"

type setter[T any] struct{}

func (setter[T]) install(p *T) {
	rt.SetFinalizer(p, func(*T) {})
}

func use(p *int) {
	setter[int]{}.install(p)
}
`)
	if !ctx.packageUsesRuntimeSetFinalizer(genericMethodPkg) {
		t.Error("generic method-only SetFinalizer use should be detected")
	}
	var genericMethod *ssa.Function
	for fn := range ssautil.AllFunctions(genericMethodPkg.Prog) {
		if origin := fn.Origin(); origin != nil && origin.Name() == "install" {
			genericMethod = fn
			break
		}
	}
	if genericMethod == nil {
		t.Fatal("missing instantiated generic method")
	}
	if !ctx.enableConservativeLivenessClears(genericMethod) {
		t.Error("instantiated generic method should inherit its package liveness setting")
	}
}

func TestConservativeLivenessPlanCollectors(t *testing.T) {
	ssapkg := buildSSAPackageWithPath(t, "example.com/live", "live", `package live

type Box struct{ p *int }

var Sink any

func linear(p *int) {
	var box Box
	box.p = p
	Sink = box.p
	Sink = 1
}

func loop(p *int) {
	var box Box
	box.p = p
	for i := 0; i < 2; i++ {
		Sink = box.p
	}
	Sink = 1
}

func takes(*int) {}

func deferred(p *int) {
	var box Box
	box.p = p
	defer takes(box.p)
	Sink = 1
}

func goroutine(p *int) {
	var box Box
	box.p = p
	go takes(box.p)
	Sink = 1
}

func takesBox(Box) {}

func callLocal(p *int) {
	var box Box
	box.p = p
	takesBox(box)
	Sink = 1
}

func cyclicLocal(p *int, n int) {
	for n > 0 {
		var box Box
		box.p = p
		Sink = box.p
		n--
	}
}

func slicedLocal(p *int) {
	var values [1]*int
	values[0] = p
	slice := values[:]
	Sink = slice[0]
	Sink = 1
}

func phiLocal(p *int, cond bool) {
	var left, right Box
	left.p = p
	right.p = p
	var box *Box
	if cond {
		box = &left
	} else {
		box = &right
	}
	Sink = box.p
	Sink = 1
}
`)
	ctx := &context{}
	linear := ssapkg.Func("linear")
	stackPlans := ctx.collectStackClearPlans(linear)
	if len(stackPlans) == 0 {
		t.Fatal("linear should produce stack clear plans")
	}
	for instr := range stackPlans {
		if isTerminatingInstruction(instr) {
			t.Fatalf("stack clear should not be scheduled after terminator %T", instr)
		}
	}

	for _, name := range []string{"loop", "deferred", "goroutine"} {
		if got := ctx.collectStackClearPlans(ssapkg.Func(name)); len(got) != 0 {
			t.Fatalf("%s should fail closed instead of producing clear plans: %v", name, got)
		}
	}

	callLocal := ssapkg.Func("callLocal")
	callPlans := ctx.collectStackClearPlans(callLocal)
	if len(callPlans) == 0 {
		t.Fatal("callLocal should produce a stack clear plan")
	}
	for instr := range callPlans {
		if _, ok := instr.(*ssa.Call); !ok {
			t.Fatalf("callLocal clear must follow its real final use, got %T", instr)
		}
	}

	cyclicLocal := ssapkg.Func("cyclicLocal")
	var cyclicAlloc *ssa.Alloc
	for _, alloc := range functionAllocs(cyclicLocal) {
		if !alloc.Heap && blockIsCyclic(alloc.Block()) {
			cyclicAlloc = alloc
			break
		}
	}
	if cyclicAlloc == nil {
		var dump strings.Builder
		cyclicLocal.WriteTo(&dump)
		t.Fatalf("cyclicLocal should contain a non-heap allocation in a cyclic block:\n%s", dump.String())
	}
	if got := ctx.collectStackClearPlans(cyclicLocal); len(got) != 0 {
		t.Fatalf("cyclicLocal should fail closed instead of producing clear plans: %v", got)
	}

	slicedLocal := ssapkg.Func("slicedLocal")
	var hasSlice bool
	for _, block := range slicedLocal.Blocks {
		for _, instr := range block.Instrs {
			if _, ok := instr.(*ssa.Slice); ok {
				hasSlice = true
			}
		}
	}
	if !hasSlice {
		t.Fatal("slicedLocal should exercise a slice-derived stack reference")
	}
	var slicedAlloc *ssa.Alloc
	for _, alloc := range functionAllocs(slicedLocal) {
		ptr, ok := alloc.Type().Underlying().(*types.Pointer)
		if ok {
			if _, ok := ptr.Elem().Underlying().(*types.Array); ok {
				slicedAlloc = alloc
				slicedAlloc.Heap = false
				break
			}
		}
	}
	if slicedAlloc == nil {
		var dump strings.Builder
		slicedLocal.WriteTo(&dump)
		t.Fatalf("slicedLocal should contain an array allocation:\n%s", dump.String())
	}
	if got := ctx.collectStackClearPlans(slicedLocal); len(got) == 0 {
		var dump strings.Builder
		slicedLocal.WriteTo(&dump)
		t.Fatalf("slicedLocal should produce a stack clear plan:\n%s", dump.String())
	}

	phiLocal := ssapkg.Func("phiLocal")
	var hasPhi bool
	for _, block := range phiLocal.Blocks {
		for _, instr := range block.Instrs {
			if _, ok := instr.(*ssa.Phi); ok {
				hasPhi = true
			}
		}
	}
	if !hasPhi {
		t.Fatal("phiLocal should exercise a merged stack reference")
	}
	if got := ctx.collectStackClearPlans(phiLocal); len(got) != 0 {
		t.Fatalf("phiLocal should fail closed instead of producing clear plans: %v", got)
	}
}

func TestConservativeLivenessGraphHelpers(t *testing.T) {
	ssapkg := buildSSAPackageWithPath(t, "example.com/live", "live", `package live

var Sink any

func flow(p *int, cond bool) {
	if cond {
		Sink = p
	} else {
		Sink = 0
	}
}

func target(*int) {}

func withCall(p *int) {
	target(p)
}

func loop(p *int) {
	for i := 0; i < 2; i++ {
		Sink = p
	}
}
	`)
	fn := ssapkg.Func("flow")
	if blockCanReach(nil, fn.Blocks[0], map[*ssa.BasicBlock]bool{}) {
		t.Fatal("nil block should not reach anything")
	}
	if !blockCanReach(fn.Blocks[0], fn.Blocks[0], map[*ssa.BasicBlock]bool{}) {
		t.Fatal("block should reach itself")
	}
	loop := ssapkg.Func("loop")
	var cyclic int
	for _, block := range loop.Blocks {
		if blockIsCyclic(block) {
			cyclic++
		}
	}
	if cyclic == 0 {
		t.Fatal("loop should contain at least one cyclic block")
	}
	if instructionUsesValue(nil, fn.Params[0]) {
		t.Fatal("nil instruction should not use values")
	}
	if instructionUsesValue(fn.Blocks[0].Instrs[0], nil) {
		t.Fatal("nil value should not be used")
	}
	if !isTerminatingInstruction(fn.Blocks[0].Instrs[len(fn.Blocks[0].Instrs)-1]) {
		t.Fatal("entry block should end with a terminator")
	}

	ctx := &context{}
	if last, ok := ctx.lastUseInBlock(nil, fn.Blocks[0], map[ssa.Instruction]int{}, map[ssa.Value]bool{}); !ok || last != nil {
		t.Fatalf("lastUseInBlock(nil) = %v, %v", last, ok)
	}

	withCall := ssapkg.Func("withCall")
	var call *ssa.Call
	for _, block := range withCall.Blocks {
		for _, instr := range block.Instrs {
			if callInstr, ok := instr.(*ssa.Call); ok {
				call = callInstr
			}
		}
	}
	if call == nil {
		t.Fatal("withCall should include a call-like instruction")
	}
	block := call.Block()
	order := make(map[ssa.Instruction]int, len(block.Instrs))
	for i, instr := range block.Instrs {
		order[instr] = i
	}
	if last, ok := ctx.lastUseInBlock(withCall.Params[0], block, order, map[ssa.Value]bool{}); !ok || last != call {
		t.Fatalf("lastUseInBlock(call parameter) = %v, %v; want call", last, ok)
	}
}

func TestConservativeLivenessHelperFallbacks(t *testing.T) {
	ssapkg := buildSSAPackageWithPath(t, "example.com/live", "live", `package live

var Sink any

func branch(cond bool) {
	if cond {
		Sink = 1
	} else {
		Sink = 2
	}
}

func useOne(p, q *int) {
	Sink = p
}

func neg(i int) int {
	return -i
}

func callDeref(f *func()) {
	(*f)()
}

func derefOnly(p **int) {
	_ = *p
}
	`)
	ctx := &context{}

	branch := ssapkg.Func("branch")
	if len(branch.Blocks) < 2 {
		t.Fatalf("branch should have successors:\n%s", branch.String())
	}
	if blockCanReach(branch.Blocks[0], branch.Blocks[1], map[*ssa.BasicBlock]bool{branch.Blocks[0]: true}) {
		t.Fatal("seen entry block should stop reachability recursion")
	}

	useOne := ssapkg.Func("useOne")
	var useP ssa.Instruction
	for _, block := range useOne.Blocks {
		for _, instr := range block.Instrs {
			if instructionUsesValue(instr, useOne.Params[0]) {
				useP = instr
				break
			}
		}
		if useP != nil {
			break
		}
	}
	if useP == nil {
		t.Fatalf("missing instruction that uses p:\n%s", useOne.String())
	}
	if instructionUsesValue(useP, useOne.Params[1]) {
		t.Fatal("instruction using p should not report use of q")
	}
	global := ssapkg.Members["Sink"].(*ssa.Global)
	if last, ok := ctx.lastUseInBlock(global, useOne.Blocks[0], map[ssa.Instruction]int{}, map[ssa.Value]bool{}); !ok || last != nil {
		t.Fatalf("lastUseInBlock(global) = %v, %v", last, ok)
	}

	neg := ssapkg.Func("neg")
	var negInstr *ssa.UnOp
	for _, block := range neg.Blocks {
		for _, instr := range block.Instrs {
			if unop, ok := instr.(*ssa.UnOp); ok && unop.Op == token.SUB {
				negInstr = unop
				break
			}
		}
		if negInstr != nil {
			break
		}
	}
	if negInstr == nil {
		t.Fatalf("missing unary negation:\n%s", neg.String())
	}
	order := make(map[ssa.Instruction]int, len(negInstr.Block().Instrs))
	for i, instr := range negInstr.Block().Instrs {
		order[instr] = i
	}
	if last, ok := ctx.lastUseInBlock(neg.Params[0], negInstr.Block(), order, map[ssa.Value]bool{}); !ok {
		t.Fatalf("lastUseInBlock(neg param) = %v, %v", last, ok)
	} else if _, ok := last.(*ssa.Return); !ok {
		t.Fatalf("lastUseInBlock(neg param) = %T; want return", last)
	}

	callDeref := ssapkg.Func("callDeref")
	var deref *ssa.UnOp
	for _, block := range callDeref.Blocks {
		for _, instr := range block.Instrs {
			if unop, ok := instr.(*ssa.UnOp); ok && unop.Op == token.MUL {
				deref = unop
				break
			}
		}
		if deref != nil {
			break
		}
	}
	if deref == nil {
		t.Fatalf("missing call dereference:\n%s", callDeref.String())
	}
	order = make(map[ssa.Instruction]int, len(deref.Block().Instrs))
	for i, instr := range deref.Block().Instrs {
		order[instr] = i
	}
	if last, ok := ctx.lastUseInBlock(callDeref.Params[0], deref.Block(), order, map[ssa.Value]bool{}); !ok {
		t.Fatalf("lastUseInBlock(call deref param) = %v, %v", last, ok)
	} else if _, ok := last.(*ssa.Call); !ok {
		t.Fatalf("lastUseInBlock(call deref param) = %T; want call", last)
	}

	derefOnly := ssapkg.Func("derefOnly")
	var loneDeref *ssa.UnOp
	for _, block := range derefOnly.Blocks {
		for _, instr := range block.Instrs {
			if unop, ok := instr.(*ssa.UnOp); ok && unop.Op == token.MUL {
				loneDeref = unop
				break
			}
		}
		if loneDeref != nil {
			break
		}
	}
	if loneDeref == nil {
		t.Fatalf("missing lone dereference:\n%s", derefOnly.String())
	}
	order = make(map[ssa.Instruction]int, len(loneDeref.Block().Instrs))
	for i, instr := range loneDeref.Block().Instrs {
		order[instr] = i
	}
	if last, ok := ctx.lastUseInBlock(derefOnly.Params[0], loneDeref.Block(), order, map[ssa.Value]bool{}); !ok || last != loneDeref {
		t.Fatalf("lastUseInBlock(lone deref param) = %v, %v; want deref", last, ok)
	}
}

func TestConservativeLivenessMalformedReferrersFailClosed(t *testing.T) {
	ssapkg := buildSSAPackageWithPath(t, "example.com/live", "live", `package live

var Sink any

func use(p *int) {
	Sink = p
}
`)
	fn := ssapkg.Func("use")
	param := fn.Params[0]
	ctx := &context{}

	check := func(t *testing.T, ref ssa.Instruction) {
		t.Helper()
		refs := param.Referrers()
		original := append([]ssa.Instruction(nil), (*refs)...)
		*refs = []ssa.Instruction{ref}
		defer func() {
			*refs = original
		}()

		order := make(map[ssa.Instruction]int, len(fn.Blocks[0].Instrs))
		for i, instr := range fn.Blocks[0].Instrs {
			order[instr] = i
		}
		if last, ok := ctx.lastUseInBlock(param, fn.Blocks[0], order, map[ssa.Value]bool{}); ok || last != nil {
			t.Fatalf("lastUseInBlock with malformed referrer = %v, %v; want failure", last, ok)
		}
	}

	t.Run("unary", func(t *testing.T) {
		check(t, &ssa.UnOp{Op: token.SUB, X: param})
	})
	t.Run("dereference", func(t *testing.T) {
		check(t, &ssa.UnOp{Op: token.MUL, X: param})
	})
	t.Run("return", func(t *testing.T) {
		check(t, &ssa.Return{Results: []ssa.Value{param}})
	})
	t.Run("derived-value", func(t *testing.T) {
		var derived ssa.Value
		for _, ref := range *param.Referrers() {
			if value, ok := ref.(*ssa.MakeInterface); ok {
				derived = value
				break
			}
		}
		if derived == nil {
			t.Fatal("missing MakeInterface derived from parameter")
		}
		refs := derived.Referrers()
		original := append([]ssa.Instruction(nil), (*refs)...)
		*refs = []ssa.Instruction{&ssa.Return{Results: []ssa.Value{derived}}}
		defer func() {
			*refs = original
		}()

		order := make(map[ssa.Instruction]int, len(fn.Blocks[0].Instrs))
		for i, instr := range fn.Blocks[0].Instrs {
			order[instr] = i
		}
		if last, ok := ctx.lastUseInBlock(param, fn.Blocks[0], order, map[ssa.Value]bool{}); ok || last != nil {
			t.Fatalf("lastUseInBlock with malformed derived referrer = %v, %v; want failure", last, ok)
		}
	})
}

func TestConservativeLivenessDebugRefs(t *testing.T) {
	ssapkg, _ := buildSSAPackageWithPathAndFilesMode(t, "example.com/live", "live", `package live

var Sink any

func use(p *int) {
	Sink = p
}
	`, ssa.SanityCheckFunctions|ssa.InstantiateGenerics|ssa.GlobalDebug)

	fn := ssapkg.Func("use")
	var debugRefs int
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if _, ok := instr.(*ssa.DebugRef); ok {
				debugRefs++
			}
		}
	}
	if debugRefs == 0 {
		t.Fatalf("debug SSA package did not contain DebugRef instructions:\n%s", fn.String())
	}

	ctx := &context{}
	order := make(map[ssa.Instruction]int, len(fn.Blocks[0].Instrs))
	for i, instr := range fn.Blocks[0].Instrs {
		order[instr] = i
	}
	if last, ok := ctx.lastUseInBlock(fn.Params[0], fn.Blocks[0], order, map[ssa.Value]bool{}); !ok || last == nil {
		t.Fatalf("lastUseInBlock with DebugRef = %v, %v", last, ok)
	}
}

func TestCompileWithoutConservativeLivenessClears(t *testing.T) {
	ssapkg, files := buildSSAPackageWithPathAndFiles(t, "command-line-arguments", "main", `package main

func main() {
	x := 1
	_ = &x
}
`)

	prog := newLLSSAProg(t)
	pkg, err := NewPackage(prog, ssapkg, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, helper := range []string{"llgo_clear_stack_ptr", "llgo_clobber_pointer_regs"} {
		if strings.Contains(pkg.String(), helper) {
			t.Fatalf("package without SetFinalizer should not emit %s:\n%s", helper, pkg.String())
		}
	}
	if strings.Contains(pkg.String(), "store volatile") {
		t.Fatalf("package without SetFinalizer should not emit liveness clears:\n%s", pkg.String())
	}
}

func TestCompileConservativeLivenessClears(t *testing.T) {
	ssapkg, files := buildSSAPackageWithPathAndFiles(t, "github.com/goplus/llgo/runtime/livetest", "main", `package main

import rt "runtime"

type Box struct{ p, q *int }

var Sink any

func clearLocal(p *int) {
	var box Box
	box.p = p
	box.q = p
	Sink = box.p
	Sink = box.q
	Sink = 1
}

func main() {
	x := new(int)
	rt.SetFinalizer(x, func(*int) {})
	clearLocal(x)
}
`)

	prog := newLLSSAProg(t)
	pkg, err := NewPackage(prog, ssapkg, files)
	if err != nil {
		t.Fatal(err)
	}
	ir := pkg.String()
	for _, helper := range []string{"llgo_clear_stack_ptr", "llgo_clobber_pointer_regs"} {
		if strings.Contains(ir, helper) {
			t.Fatalf("compiled liveness module must not use %s:\n%s", helper, ir)
		}
	}
	if !strings.Contains(ir, `%"github.com/goplus/llgo/runtime/livetest.Box" = type { ptr, ptr }`) {
		t.Fatalf("compiled liveness module missing two-pointer aggregate type:\n%s", ir)
	}
	if !strings.Contains(ir, `store volatile %"github.com/goplus/llgo/runtime/livetest.Box" zeroinitializer`) {
		t.Fatalf("compiled liveness module missing volatile whole-aggregate clear:\n%s", ir)
	}
}

func TestCompileConservativeLivenessDoesNotScanWholeStack(t *testing.T) {
	ssapkg, files := buildSSAPackageWithPathAndFiles(t, "github.com/goplus/llgo/runtime/livetest", "main", `package main

import rt "github.com/goplus/llgo/runtime/internal/lib/runtime"
import "unsafe"

type Cell struct{ p *int }
type Ptr *int

var Sink any

func consume(cell Cell) {
	Sink = cell.p
	Sink = 1
}

func consumePtr(p *int) {
	Sink = p
	Sink = 1
}

func branch(cell Cell, cond bool) {
	if cond {
		Sink = cell.p
	} else {
		Sink = 0
	}
	Sink = 1
}

func main() {
	x := 1
	y := 2
	arr := [2]*int{&x, &y}
	cell := Cell{p: &x}
	p := &x
	pp := &p
	ptr := Ptr(&x)
	rt.SetFinalizer(&cell, func(*Cell) {})
	rt.SetFinalizer(&p, func(**int) {})
	rt.SetFinalizer(*pp, nil)
	rt.SetFinalizer(&cell.p, func(**int) {})
	rt.SetFinalizer(&arr[0], func(**int) {})
	rt.SetFinalizer(unsafe.Pointer(&x), nil)
	rt.SetFinalizer(ptr, nil)
	consume(cell)
	consumePtr(p)
	branch(cell, x == y)
}
	`)
	ssapkg.Pkg = types.NewPackage("command-line-arguments", "main")

	prog := newLLSSAProg(t)
	pkg, err := NewPackage(prog, ssapkg, files)
	if err != nil {
		t.Fatal(err)
	}
	ir := pkg.String()
	for _, helper := range []string{"llgo_clear_stack_ptr", "llgo_clobber_pointer_regs"} {
		if strings.Contains(ir, helper) {
			t.Fatalf("compiled liveness module must not use %s:\n%s", helper, ir)
		}
	}
}
