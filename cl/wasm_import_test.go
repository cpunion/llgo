//go:build !llgo

package cl

import (
	"go/ast"
	"strings"
	"testing"

	llssa "github.com/goplus/llgo/ssa"
)

func TestAttachedWasmImportSource(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		present bool
		wantErr string
	}{
		{
			name: "valid",
			source: `package p
import (
	"structs"
	"unsafe"
)
type record struct {
	_ structs.HostLayout
	value [2]uint16
}
//go:noescape
//go:wasmimport wasi_snapshot_preview1 fd_read
func host(bool, uint32, uint64, float32, float64, uintptr, unsafe.Pointer, *record, string) uintptr`,
			present: true,
		},
		{
			name: "missing name",
			source: `package p
//go:wasmimport wasi_snapshot_preview1
func host()`,
			wantErr: "requires exactly one module and import name",
		},
		{
			name: "duplicate",
			source: `package p
//go:wasmimport env first
//go:wasmimport env second
func host()`,
			wantErr: "duplicate //go:wasmimport",
		},
		{
			name: "bodyful",
			source: `package p
//go:wasmimport env host
func host() {}`,
			wantErr: "requires an exact bodyless",
		},
		{
			name: "competing ABI",
			source: `package p
//go:linkname host C.host
//go:wasmimport env host
func host()`,
			wantErr: "conflicts with physical ABI directive",
		},
		{
			name: "unsupported parameter",
			source: `package p
//go:wasmimport env host
func host(int)`,
			wantErr: "unsupported parameter 0 type int",
		},
		{
			name: "pointer layout without marker",
			source: `package p
//go:wasmimport env host
func host(*struct{ value uint32 })`,
			wantErr: "unsupported parameter 0 type",
		},
		{
			name: "string result",
			source: `package p
//go:wasmimport env host
func host() string`,
			wantErr: "unsupported result type string",
		},
		{
			name: "multiple results",
			source: `package p
//go:wasmimport env host
func host() (uint32, uint32)`,
			wantErr: "too many return values",
		},
		{
			name: "gojs private ABI",
			source: `package p
//go:wasmimport gojs host
func host(uint32) uint32`,
			wantErr: "unsupported Go stack-pointer ABI",
		},
		{
			name:   "absent",
			source: "package p\nfunc host()",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pkg, _, _ := buildGoSSAPkg(t, test.source)
			spec, present, err := attachedWasmImportSource(pkg.Func("host"))
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if present != test.present {
				t.Fatalf("present = %t, want %t", present, test.present)
			}
			if present && (spec.module != "wasi_snapshot_preview1" || spec.name != "fd_read") {
				t.Fatalf("spec = %+v", spec)
			}
		})
	}
}

func TestEmissionUniverseFreezesWasmImportBeforeLowering(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/wasmimport", `package wasmimport
//go:wasmimport env schedule
func schedule(uint32)
func Use() { schedule(1) }`)
	testProg.ssa.Build()

	prog := llssa.NewProgram(&llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file},
	}})
	if err != nil {
		t.Fatal(err)
	}
	function := pkg.ssa.Func("schedule")
	spec, present, err := universe.coroProgramIR.wasmImport(function)
	if err != nil || !present || spec != (wasmImportSpec{module: "env", name: "schedule"}) {
		t.Fatalf("frozen wasm import = %+v, %t, %v", spec, present, err)
	}

	decl, _ := function.Syntax().(*ast.FuncDecl)
	decl.Doc.List[0].Text = "//go:wasmimport changed changed"
	after, present, err := universe.coroProgramIR.wasmImport(function)
	if err != nil || !present || after != spec {
		t.Fatalf("post-mutation wasm import = %+v, %t, %v; want frozen %+v", after, present, err, spec)
	}
}
