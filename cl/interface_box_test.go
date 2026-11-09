package cl

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"runtime"
	"strings"
	"testing"

	"github.com/goplus/gogen/packages"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa/ssautil"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const interfaceBoxDecl = `package ifacebox
type F func() int
type Pair struct { A, B uint64 }
type Payload struct { Fn F; Value uint64 }
var saved any
func extract(x any) V { return x.(V) }
func forward(x any) V { return extract(x) }
func keep(x any) { saved = x }
func mixed(x, y any) V { saved = y; return x.(V) }
func unknown(any)
func recursive(x any, n int) V { if n > 0 { return recursive(x, n-1) }; return x.(V) }
`

func interfaceBoxSource(valueType, body string) string {
	if valueType == "Payload" {
		body = strings.ReplaceAll(body, "extract(f)()", "extract(f).Fn()")
	}
	return interfaceBoxDecl + "type V = " + valueType + "\n" + body
}

func TestCompileBorrowedInterfaceBoxes(t *testing.T) {
	const body = `
func direct(f V) V { return any(f).(V) }
func helper(f V) V { return extract(f) }
func forwarded(f V) V { return forward(f) }
func commaOK(f V) (V, bool) { v, ok := any(f).(V); return v, ok }
func nilComparison(f V) bool { return any(f) != nil }
func integer(x uint64) uint64 { return any(x).(uint64) }
func pair(x Pair) Pair { return any(x).(Pair) }
func branch(f V, choose bool) V {
 x := any(f)
 if choose { return x.(V) }
 return extract(x)
}
func phi(f V, other any, choose bool) V {
 x := any(f)
 if choose { x = other }
 return extract(x)
}
func separate(f V, other any) V { return mixed(f, other) }
`
	for _, target := range []llssa.Target{
		{GOOS: "darwin", GOARCH: "arm64"},
		{GOOS: "linux", GOARCH: "amd64"},
		{GOOS: "windows", GOARCH: "386"},
		{GOOS: "wasip1", GOARCH: "wasm"},
	} {
		for _, mode := range []ssa.BuilderMode{0, ssa.GlobalDebug} {
			for _, valueType := range []string{"F", "Payload"} {
				t.Run(fmt.Sprintf("%s/%s/%v/%s", target.GOOS, target.GOARCH, mode, valueType), func(t *testing.T) {
					ir := compileInterfaceBoxIR(t, interfaceBoxSource(valueType, body), mode, &target, false)
					for _, name := range []string{"direct", "helper", "forwarded", "commaOK", "nilComparison", "integer", "pair", "branch", "phi", "separate"} {
						body := llvmFunction(t, ir, "ifacebox."+name)
						// F contributes its existing descriptor pointer directly.
						// Payload, Pair and uint64 still need borrowed box storage.
						wantStack := valueType == "Payload" || name == "integer" || name == "pair"
						if strings.Contains(body, "runtime.Alloc") || strings.Contains(body, "alloca ") != wantStack {
							t.Errorf("%s stack box = %v, want %v, with no heap box:\n%s", name, strings.Contains(body, "alloca "), wantStack, body)
						}
					}
				})
			}
		}
	}
}

func TestCompileInterfaceBoxPlacement(t *testing.T) {
	for _, tc := range []struct{ name, decl string }{
		{"return", `func box(f V) any { return f }`},
		{"global", `func box(f V) { saved = f }`},
		{"helper", `func box(f V) { keep(f) }`},
		{"second argument", `func box(f V) V { x := any(f); return mixed(x, x) }`},
		{"unknown", `func box(f V) { unknown(f) }`},
		{"indirect", `func box(f V, use func(any)) { use(f) }`},
		{"defer", `func box(f V) { defer extract(f) }`},
		{"go", `func box(f V) { go extract(f) }`},
		{"closure", `func box(f V) func() V { x := any(f); return func() V { return x.(V) } }`},
		{"recursive", `func box(f V) V { return recursive(f, 1) }`},
		{"interface assertion", `func box(f V) any { return any(f).(interface{}) }`},
		{"loop", `func box(f V, n int) int { sum := 0; for i:=0;i<n;i++ { sum += extract(f)() }; return sum }`},
		{"recover", `func box(f V) V { defer func() { recover() }(); return extract(f) }`},
		{"large", `func box(x [16385]uint64) [16385]uint64 { return any(x).([16385]uint64) }`},
		{"budget", "func box(f V) { x := any(f);" + strings.Repeat("extract(x);", 1025) + "}"},
	} {
		for _, valueType := range []string{"F", "Payload"} {
			t.Run(tc.name+"/"+valueType, func(t *testing.T) {
				ir := compileInterfaceBoxIR(t, interfaceBoxSource(valueType, tc.decl), ssa.InstantiateGenerics, nil, false)
				body := llvmFunction(t, ir, "ifacebox.box")
				// These non-direct boxes stay within the invocation/iteration.
				stack := tc.name == "defer" || tc.name == "recursive" || tc.name == "loop" || tc.name == "recover" || tc.name == "budget"
				wantHeap := !stack
				if valueType == "F" {
					// A direct interface needs no box. Escaping closure environments,
					// goroutine state and the independent large value still allocate.
					wantHeap = tc.name == "closure" || tc.name == "go" || tc.name == "large"
				}
				if heap := strings.Contains(body, "runtime.Alloc"); heap != wantHeap {
					t.Fatalf("heap allocation = %v, want %v:\n%s", heap, wantHeap, body)
				}
				if valueType == "Payload" && stack && !strings.Contains(body, "alloca ") {
					t.Fatalf("non-direct value lost its stack box:\n%s", body)
				}
			})
		}
	}
}

func compileInterfaceBoxIR(t *testing.T, src string, mode ssa.BuilderMode, target *llssa.Target, roots bool) string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "ifacebox.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{file}
	pkg, _, err := ssautil.BuildPackage(&types.Config{Importer: packages.NewImporter(fset)}, fset, types.NewPackage(file.Name.Name, file.Name.Name), files, mode)
	if err != nil {
		t.Fatal(err)
	}
	// Source import keeps the runtime's private ABI aliases needed for boxing.
	prog := newLLSSAProgForTarget(t, target)
	defer prog.Dispose()
	if target == nil {
		prog.TypeSizes(types.SizesFor("gc", runtime.GOARCH))
	}
	prog.EnableGCRoots(roots)
	compiled, _, err := NewPackageEx(prog, nil, nil, pkg, files)
	if err != nil {
		t.Fatal(err)
	}
	defer compiled.Module().Dispose()
	if err := llvm.VerifyModule(compiled.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	return compiled.String()
}

func TestInterfaceBoxExplicitRoots(t *testing.T) {
	for _, valueType := range []string{"F", "Payload"} {
		t.Run(valueType, func(t *testing.T) {
			src := interfaceBoxSource(valueType, `func box(f V) V { return extract(f) }`)
			ir := compileInterfaceBoxIR(t, src, ssa.InstantiateGenerics, &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, true)
			body := llvmFunction(t, ir, "ifacebox.box")
			if !strings.Contains(body, "llvm_gc_root_chain") {
				t.Fatalf("pointer-bearing value lost explicit GC roots:\n%s", body)
			}
			// F's descriptor is itself rooted; Payload's function field also
			// requires a scanned heap box rather than unscanned stack fields.
			wantHeap := valueType == "Payload"
			if heap := strings.Contains(body, "runtime.AllocU"); heap != wantHeap {
				t.Fatalf("explicit-root heap box = %v, want %v:\n%s", heap, wantHeap, body)
			}
		})
	}
}
