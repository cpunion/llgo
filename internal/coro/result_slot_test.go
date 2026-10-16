package coro

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

func resultSlotTestSourcePackage(t *testing.T) *types.Package {
	t.Helper()
	const source = `package adapter
import "unsafe"
type result = struct { R0 int; R1 error }
type distinct struct { R0 int; R1 error }
func acquire(path string) (int, error) { return -1, nil }
func initialize(out *result) { out.R0 = -1; out.R1 = nil }
func drop(out *result, env unsafe.Pointer, path string) { out.R0 = -1 }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "adapter.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	config := types.Config{Importer: importer.Default()}
	pkg, err := config.Check("example/adapter", fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestResultSlotTypeMatchesSourceAlias(t *testing.T) {
	pkg := resultSlotTestSourcePackage(t)
	signature := pkg.Scope().Lookup("acquire").Type().(*types.Signature)
	slot := ResultSlotType(signature.Results())
	if !types.Identical(slot, pkg.Scope().Lookup("result").Type()) {
		t.Fatal("a Go source alias cannot name the compiler result slot")
	}
	initializer := pkg.Scope().Lookup("initialize").Type().(*types.Signature)
	if !types.Identical(types.NewPointer(slot), initializer.Params().At(0).Type()) {
		t.Fatal("source helper does not accept the exact typed result pointer")
	}
	if types.Identical(slot, pkg.Scope().Lookup("distinct").Type()) {
		t.Fatal("an unrelated defined type lost its source identity")
	}
	if ResultSlotType(nil).NumFields() != 0 {
		t.Fatal("a zero-result function acquired result fields")
	}
}
