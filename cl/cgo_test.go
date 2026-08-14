//go:build !llgo
// +build !llgo

package cl

import (
	"go/ast"
	"go/build"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/goplus/gogen/packages"
	llpackages "github.com/goplus/llgo/internal/packages"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	gossa "golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func init() {
	// Keep these tests self-contained (other packages may not call Initialize).
	llssa.Initialize(llssa.InitAll | llssa.InitNative)
}

func buildGoSSAPkg(t *testing.T, src string) (*gossa.Package, *token.FileSet, []*ast.File) {
	t.Helper()
	return buildGoSSAPkgWithMode(t, src, gossa.SanityCheckFunctions|gossa.InstantiateGenerics)
}

func buildGoSSAPkgWithMode(t *testing.T, src string, mode gossa.BuilderMode) (*gossa.Package, *token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "foo.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{f}
	pkg := types.NewPackage(f.Name.Name, f.Name.Name)
	imp := packages.NewImporter(fset)
	ssaPkg, _, err := ssautil.BuildPackage(&types.Config{Importer: imp}, fset, pkg, files, mode)
	if err != nil {
		t.Fatal(err)
	}
	return ssaPkg, fset, files
}

func newLLSSAProg(t *testing.T) llssa.Program {
	t.Helper()
	prog := llssa.NewProgram(nil)
	prog.SetRuntime(func() *types.Package {
		rt, err := importer.For("source", nil).Import(llssa.PkgRuntime)
		if err != nil {
			t.Fatal("load runtime failed:", err)
		}
		return rt
	})
	prog.TypeSizes(types.SizesFor("gc", runtime.GOARCH))
	return prog
}

func mustCompileLLPkgFromSrc(t *testing.T, src string) (llssa.Package, llvm.Module) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, src)
	prog := newLLSSAProg(t)
	pkg, err := NewPackage(prog, ssaPkg, files)
	if err != nil {
		t.Fatal(err)
	}
	return pkg, pkg.Module()
}

func mustNamedFunction(t *testing.T, m llvm.Module, name string) llvm.Value {
	t.Helper()
	fn := m.NamedFunction(name)
	if fn.IsNil() {
		t.Fatalf("missing function %q in module", name)
	}
	return fn
}

// -----------------------------------------------------------------------------
// Instruction-level "normal path" tests (Cfunc/Cmacro/C2func)

func TestCgoInstr_Cfunc(t *testing.T) {
	_, m := mustCompileLLPkgFromSrc(t, `
package foo

import "unsafe"

var _cgo_add unsafe.Pointer
func _cgo_runtime_cgocall(fn unsafe.Pointer, arg unsafe.Pointer) int

func _Cfunc_add(a int32, b int32) int32 {
	_cgo_runtime_cgocall(_cgo_add, nil)
	return 0
}
`)

	fn := mustNamedFunction(t, m, "foo._Cfunc_add")
	ir := fn.String()
	if !strings.Contains(ir, "foo._cgo_add") {
		t.Fatalf("expected load from foo._cgo_add, got:\n%s", ir)
	}
	if !strings.Contains(ir, "call") {
		t.Fatalf("expected indirect call in Cfunc wrapper, got:\n%s", ir)
	}
	if strings.Contains(ir, "cliteErrno") {
		t.Fatalf("unexpected cliteErrno in Cfunc wrapper, got:\n%s", ir)
	}
}

func TestIsCgoVar(t *testing.T) {
	for _, name := range []string{
		"_cgo_96608f8de8c8_Cfunc__Cmalloc",
		"__cgo_callback",
	} {
		if !isCgoVar(name) {
			t.Fatalf("isCgoVar(%q) = false, want true", name)
		}
	}
	if isCgoVar("_Cfunc_malloc") {
		t.Fatal("isCgoVar should not match cgo wrapper functions")
	}
	if isCgoFuncPtrVar("_cgo_96608f8de8c8_Cfunc__Cmalloc") {
		t.Fatal("isCgoFuncPtrVar should not match package cgo globals")
	}
	if !isCgoFuncPtrVar("__cgo_callback") {
		t.Fatal("isCgoFuncPtrVar should match __cgo function pointer vars")
	}
}

func TestCgoInstr_C2func(t *testing.T) {
	_, m := mustCompileLLPkgFromSrc(t, `
package foo

import (
	"syscall"
	"unsafe"
)

var _ = syscall.Errno(0)

var _cgo_sum unsafe.Pointer
func _cgo_runtime_cgocall(fn unsafe.Pointer, arg unsafe.Pointer) int

func _C2func_sum(a int32, b int32) (int32, error) {
	_cgo_runtime_cgocall(_cgo_sum, nil)
	return 0, nil
}
`)

	fn := mustNamedFunction(t, m, "foo._C2func_sum")
	ir := fn.String()
	if !strings.Contains(ir, "foo._cgo_sum") {
		t.Fatalf("expected load from foo._cgo_sum, got:\n%s", ir)
	}
	if !strings.Contains(ir, "cliteErrno") {
		t.Fatalf("expected cliteErrno call in C2func wrapper, got:\n%s", ir)
	}
	if !strings.Contains(ir, "icmp") {
		t.Fatalf("expected errno check in C2func wrapper, got:\n%s", ir)
	}
}

func TestCgoInstr_Cmacro(t *testing.T) {
	_, m := mustCompileLLPkgFromSrc(t, `
package foo

func _cgo_dummy_ptr_int32(p *int32) {}

func _Cmacro_magic() int32 {
	var v int32
	_cgo_dummy_ptr_int32(&v)
	return 0
}
`)

	fn := mustNamedFunction(t, m, "foo._Cmacro_magic")
	ir := fn.String()
	// Implementation may use heap alloc (AllocZ) instead of alloca; the key is
	// that the macro path returns by loading from the chosen address.
	if !strings.Contains(ir, "load i32") || !strings.Contains(ir, "ret i32") {
		t.Fatalf("expected load+ret in Cmacro wrapper, got:\n%s", ir)
	}
	if strings.Contains(ir, "cliteErrno") {
		t.Fatalf("unexpected cliteErrno in Cmacro wrapper, got:\n%s", ir)
	}
}

// -----------------------------------------------------------------------------
// White-box coverage tests for the highlighted branches in cl/instr.go and cl/compile.go

func findStaticCall(t *testing.T, fn *gossa.Function, name string) *gossa.Call {
	t.Helper()
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			c, ok := instr.(*gossa.Call)
			if !ok {
				continue
			}
			if callee := c.Call.StaticCallee(); callee != nil && callee.Name() == name {
				return c
			}
		}
	}
	t.Fatalf("missing call to %s in %s", name, fn.Name())
	return nil
}

func TestCgoGeneratedTouchIntrinsicRequiresExactDeclarationAndGuard(t *testing.T) {
	ssaPkg, fset, files := buildGoSSAPkg(t, `
package foo

import _ "unsafe"

//go:linkname _Cgo_always_false runtime.cgoAlwaysFalse
var _Cgo_always_false bool

//go:linkname _Cgo_use runtime.cgoUse
func _Cgo_use(any)

//go:linkname _Cgo_keepalive runtime.cgoKeepAlive
func _Cgo_keepalive(any)

func Guarded(value any) {
	if _Cgo_always_false {
		_Cgo_use(value)
	}
}

func GuardedKeepAlive(value any) {
	if _Cgo_always_false {
		_Cgo_keepalive(value)
	}
}

func Unguarded(value any) { _Cgo_use(value) }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	if err := ParsePkgSyntax(prog, fset, ssaPkg.Pkg, files); err != nil {
		t.Fatal(err)
	}
	ctx := &context{prog: prog, goTyps: ssaPkg.Pkg}
	for _, test := range []struct {
		declaration string
		wantName    string
		opcode      int
		caller      string
	}{
		{declaration: "_Cgo_use", wantName: "cgoUse", opcode: llgoCgoUse, caller: "Guarded"},
		{declaration: "_Cgo_keepalive", wantName: "cgoKeepAlive", opcode: llgoCgoKeepAlive, caller: "GuardedKeepAlive"},
	} {
		declaration := ssaPkg.Func(test.declaration)
		_, name, kind := ctx.funcName(declaration)
		if name != test.wantName || kind != llgoInstr || llgoInstrs[name] != test.opcode {
			t.Fatalf("%s classification = (%q, %d/%d), want (%q, llgoInstr/%d)",
				test.declaration, name, kind, llgoInstrs[name], test.wantName, test.opcode)
		}
		call := findStaticCall(t, ssaPkg.Func(test.caller), test.declaration)
		if err := verifyCoroGeneratedTouchCall(prog, test.opcode, call); err != nil {
			t.Fatalf("verify %s generated guard: %v", test.declaration, err)
		}
	}
	unguarded := findStaticCall(t, ssaPkg.Func("Unguarded"), "_Cgo_use")
	if err := verifyCoroGeneratedTouchCall(prog, llgoCgoUse, unguarded); err == nil ||
		!strings.Contains(err.Error(), "false-global guard") {
		t.Fatalf("unguarded cgo touch error = %v", err)
	}
}

func TestCgoGeneratedTouchMetadataFromCmdCgo(t *testing.T) {
	if !build.Default.CgoEnabled {
		t.Skip("cmd/cgo metadata requires cgo")
	}
	fset := token.NewFileSet()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	dedup := llpackages.NewDeduper()
	var preloadErr error
	dedup.SetPreload(func(pkg *types.Package, files []*ast.File) {
		if preloadErr != nil {
			return
		}
		preloadErr = ParsePkgSyntax(prog, fset, pkg, files)
	})
	loaded, err := llpackages.LoadEx(dedup, nil, &llpackages.Config{
		Mode: llpackages.NeedName | llpackages.NeedFiles |
			llpackages.NeedCompiledGoFiles | llpackages.NeedImports |
			llpackages.NeedDeps | llpackages.NeedTypes |
			llpackages.NeedTypesSizes | llpackages.NeedSyntax |
			llpackages.NeedTypesInfo,
		Dir:  filepath.Join("_testgo", "cgobasic"),
		Fset: fset,
	}, ".")
	if err != nil {
		t.Fatal(err)
	}
	if preloadErr != nil {
		t.Fatal(preloadErr)
	}
	if len(loaded) != 1 || loaded[0].Types == nil {
		t.Fatalf("loaded packages = %d, want one typed package", len(loaded))
	}
	fullName := llssa.FullName(loaded[0].Types, "_Cgo_use")
	if linkname, ok := prog.Linkname(fullName); !ok || linkname != "runtime.cgoUse" {
		files := make([]string, len(loaded[0].CompiledGoFiles))
		for index, file := range loaded[0].CompiledGoFiles {
			files[index] = filepath.Base(file)
		}
		t.Fatalf("linkname %q = (%q, %v), want runtime.cgoUse; compiled files: %v",
			fullName, linkname, ok, files)
	}
	goProg, ssaPackages := ssautil.AllPackages(loaded,
		gossa.SanityCheckFunctions|gossa.InstantiateGenerics)
	goProg.Build()
	if len(ssaPackages) != 1 || ssaPackages[0] == nil {
		t.Fatalf("SSA packages = %d, want one package", len(ssaPackages))
	}
	declaration := ssaPackages[0].Func("_Cgo_use")
	ctx := &context{prog: prog, goTyps: loaded[0].Types}
	_, name, kind := ctx.funcName(declaration)
	if name != "cgoUse" || kind != llgoInstr {
		t.Fatalf("real cmd/cgo _Cgo_use classification = (%q, %d), want (cgoUse, llgoInstr)",
			name, kind)
	}
	for _, member := range ssaPackages[0].Members {
		function, ok := member.(*gossa.Function)
		if !ok || !strings.HasPrefix(function.Name(), "_Cfunc_") {
			continue
		}
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(*gossa.Call)
				if !ok || call.Call.StaticCallee() != declaration {
					continue
				}
				if err := verifyCoroGeneratedTouchCall(prog, llgoCgoUse, call); err != nil {
					t.Fatalf("verify real cmd/cgo touch in %s: %v", function.Name(), err)
				}
				return
			}
		}
	}
	t.Fatal("real cmd/cgo output contains no _Cgo_use call")
}

func TestCgoCgocall_InitArgsFromParams(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `
package foo

import "unsafe"

func _cgo_runtime_cgocall(fn unsafe.Pointer, arg unsafe.Pointer) int

func _C2func_withparams(a int) (int, error) {
	_cgo_runtime_cgocall(nil, nil)
	return 0, nil
}
`)
	goFn := ssaPkg.Members["_C2func_withparams"].(*gossa.Function)
	call := findStaticCall(t, goFn, "_cgo_runtime_cgocall")

	prog := newLLSSAProg(t)
	pkg := prog.NewPackage("foo", "foo")
	fn := pkg.NewFunc("_C2func_withparams", goFn.Signature, llssa.InGo)
	b := fn.MakeBody(1)

	ctx := &context{prog: prog, pkg: pkg, fn: fn}
	ctx.cgoArgs = nil // force cgoCgocall to synthesize args from params

	_ = ctx.cgoCgocall(b, call.Call.Args)

	if got, want := len(ctx.cgoArgs), goFn.Signature.Params().Len(); got != want {
		t.Fatalf("cgoArgs len mismatch: got %d, want %d", got, want)
	}
	if len(ctx.cgoArgs) == 0 || ctx.cgoArgs[0].IsNil() {
		t.Fatalf("expected cgoArgs[0] initialized from param")
	}
}

func TestCgoCgocall_PanicNoResults(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `
package foo

import "unsafe"

func _cgo_runtime_cgocall(fn unsafe.Pointer, arg unsafe.Pointer) int

func _C2func_void(a int) {
	_cgo_runtime_cgocall(nil, nil)
}
`)
	goFn := ssaPkg.Members["_C2func_void"].(*gossa.Function)
	call := findStaticCall(t, goFn, "_cgo_runtime_cgocall")

	prog := newLLSSAProg(t)
	pkg := prog.NewPackage("foo", "foo")
	fn := pkg.NewFunc("_C2func_void", goFn.Signature, llssa.InGo)
	b := fn.MakeBody(1)

	ctx := &context{prog: prog, pkg: pkg, fn: fn}
	ctx.cgoArgs = nil

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = ctx.cgoCgocall(b, call.Call.Args)
}

func TestCgoErrnoType_SyscallAndFallbackAndCache(t *testing.T) {
	ssaPkg1, _, _ := buildGoSSAPkg(t, `
package foo

import "syscall"

var _ = syscall.Errno(0)
`)
	ctx1 := &context{goProg: ssaPkg1.Prog}
	if got := ctx1.cgoErrnoType().String(); got != "syscall.Errno" {
		t.Fatalf("unexpected syscall.Errno type: %s", got)
	}
	_ = ctx1.cgoErrnoType() // cached path

	ssaPkg2, _, _ := buildGoSSAPkg(t, `
package foo

func f() {}
`)
	ctx2 := &context{goProg: ssaPkg2.Prog}
	if got := ctx2.cgoErrnoType().String(); got != "int32" {
		t.Fatalf("unexpected fallback errno type: %s", got)
	}
	_ = ctx2.cgoErrnoType() // cached path
}

func TestCgoReturn_PanicWrongResultsLen(t *testing.T) {
	prog := newLLSSAProg(t)
	pkg := prog.NewPackage("foo", "foo")

	// _C2func_ means "C2func" in compileBlock, but here we just need a signature
	// with !=2 results to hit the panic branch.
	sig := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewVar(0, nil, "a", types.Typ[types.Int])),
		types.NewTuple(types.NewVar(0, nil, "", types.Typ[types.Int])),
		false)
	fn := pkg.NewFunc("_C2func_bad", sig, llssa.InGo)
	var b llssa.Builder // nil is fine for panic path
	ctx := &context{prog: prog, pkg: pkg, fn: fn}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	ctx.cgoReturn(b, true)
}

func TestCgoC2Return_ErrnoNil(t *testing.T) {
	prog := newLLSSAProg(t)
	pkg := prog.NewPackage("foo", "foo")
	errType := types.Universe.Lookup("error").Type()
	sig := types.NewSignatureType(nil, nil, nil, nil,
		types.NewTuple(
			types.NewVar(0, nil, "", types.Typ[types.Int]),
			types.NewVar(0, nil, "", errType),
		),
		false)
	fn := pkg.NewFunc("main", sig, llssa.InGo)
	b := fn.MakeBody(1)

	ctx := &context{prog: prog, pkg: pkg, fn: fn}
	ctx.cgoErrno = llssa.Nil
	ret := b.Const(constant.MakeInt64(123), ctx.type_(types.Typ[types.Int], llssa.InGo))

	ctx.cgoC2Return(b, ret, errType)
}

func TestCgoC2Return_ErrnoNeedsConvert(t *testing.T) {
	prog := newLLSSAProg(t)
	pkg := prog.NewPackage("foo", "foo")
	errType := types.Universe.Lookup("error").Type()
	sig := types.NewSignatureType(nil, nil, nil, nil,
		types.NewTuple(
			types.NewVar(0, nil, "", types.Typ[types.Int]),
			types.NewVar(0, nil, "", errType),
		),
		false)
	fn := pkg.NewFunc("main", sig, llssa.InGo)
	b := fn.MakeBody(1)

	ctx := &context{prog: prog, pkg: pkg, fn: fn}
	ctx.cgoErrnoTy = types.Typ[types.Int32] // avoid needing goProg for lookup
	ctx.cgoErrno = b.Const(constant.MakeInt64(1), ctx.type_(types.Typ[types.Int64], llssa.InGo))
	ret := b.Const(constant.MakeInt64(7), ctx.type_(types.Typ[types.Int], llssa.InGo))

	ctx.cgoC2Return(b, ret, errType)
}
